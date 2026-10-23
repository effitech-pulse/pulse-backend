package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestCollectionSourceBoundedProtocol(t *testing.T) {
	for _, tc := range []struct {
		name     string
		size     int
		encoding string
		status   int
		valid    bool
	}{{"exact", 2097152, "", 200, true}, {"over", 2097153, "", 200, false}, {"gzip", 2, "gzip", 200, false}, {"redirect", 2, "", 302, false}, {"unavailable", 2, "", 503, false}} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/collection-capture" || r.Header.Get("Accept-Encoding") != "identity" || r.URL.Query().Get("protocol") != "collection-fixture-v1" {
					t.Error("unbounded request protocol")
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.encoding != "" {
					w.Header().Set("Content-Encoding", tc.encoding)
				}
				w.WriteHeader(tc.status)
				_, _ = io.Copy(w, strings.NewReader(strings.Repeat(" ", tc.size)))
			}))
			defer s.Close()
			source, e := NewCollectionSource(s.URL)
			if e != nil {
				t.Fatal(e)
			}
			raw, e := source.FetchCollection(context.Background(), domain.CollectionIntent{FixtureOrigin: s.URL})
			if (e == nil) != tc.valid {
				t.Fatalf("valid=%v got %v", tc.valid, e)
			}
			if tc.valid && len(raw.Body) != tc.size {
				t.Fatal("wrong bytes")
			}
		})
	}
}

func TestCollectionSourceNoRedirectOrProxyAndCancellation(t *testing.T) {
	var forwarded atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forbidden" {
			forwarded.Add(1)
		}
		w.Header().Set("Location", "/forbidden")
		w.WriteHeader(302)
	}))
	defer s.Close()
	source, e := NewCollectionSource(s.URL)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = source.FetchCollection(context.Background(), domain.CollectionIntent{FixtureOrigin: s.URL}); e == nil || forwarded.Load() != 0 {
		t.Fatal("followed redirect", e, forwarded.Load())
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer slow.Close()
	source, e = NewCollectionSource(slow.URL)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	before := time.Now()
	if _, e = source.FetchCollection(ctx, domain.CollectionIntent{FixtureOrigin: slow.URL}); e == nil || time.Since(before) > time.Second {
		t.Fatal("source read not bounded by cancellation", e)
	}
	if _, e = NewCollectionSource("http://user:secret@fixture:8080"); e == nil {
		t.Fatal("credential origin accepted")
	}
	if _, e = NewCollectionSource("http://example.com:8080"); e == nil {
		t.Fatal("external origin accepted")
	}
}

func TestCollectionSourceExactMaxIsValidCapture(t *testing.T) {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	body := `{"protocol":"collection-fixture-v1","fixture_id":"dense","well_id":"-1","start":"2026-10-19T00:00:00Z","end":"2026-10-19T00:01:00Z","data_revision":"a","complete":true,"empty":false,"expected_slots":1,"records":[{"well_id":"-1","timestamp":"2026-10-19T00:00:00Z","values":{"f":0}}]}`
	body += strings.Repeat(" ", 2097152-len(body))
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer s.Close()
	source, e := NewCollectionSource(s.URL)
	if e != nil {
		t.Fatal(e)
	}
	intent := domain.CollectionIntent{FixtureOrigin: s.URL, Start: start, End: start.Add(time.Minute), Binding: domain.CollectionBinding{SourceID: "source", WellID: 1, ExternalID: "-1"}, Fixture: domain.CollectionFixture{ID: "dense", Protocol: "collection-fixture-v1", Horizon: start.Add(time.Hour), RetentionStart: start.Add(-30 * 24 * time.Hour), Channels: []domain.CollectionChannel{{Name: "F_out", Minimum: 0, Maximum: 100}}, Mappings: []domain.Mapping{{Field: "f", Parameter: "F_out", Scale: 1}}}, CanonicalVersion: "collection-canonical-v1", SchemaVersion: "collection-measurement-v1"}
	raw, e := source.FetchCollection(context.Background(), intent)
	if e != nil {
		t.Fatal(e)
	}
	capture, e := domain.PrepareCollectionCapture(intent, raw)
	if e != nil || len(capture.Raw) != 2097152 || capture.UniqueRows != 1 {
		t.Fatal("valid exact-byte max rejected", e)
	}
}
