package httpapi

import (
	"context"
	"errors"
	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestEventsQueryStrict(t *testing.T) {
	for _, raw := range []string{"limit=0", "limit=101", "limit=1&limit=2", "page=1", "types=well.archived.v1,well.archived.v1", "well_id=9223372036854775808", "from=2026-10-22T12:00:00.1234567Z", "from=2026-10-23T00:00:00Z&to=2026-10-22T00:00:00Z"} {
		q, _ := url.ParseQuery(raw)
		if _, _, err := parseEventQuery(q); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	f, _, err := parseEventQuery(url.Values{})
	if err != nil || f.Limit != 25 || f.Archive != "all" {
		t.Fatalf("default %+v %v", f, err)
	}
	q, _ := url.ParseQuery("from=2026-10-22T15:00:00.123456%2B03:00&well_id=9223372036854775807")
	f, _, err = parseEventQuery(q)
	if err != nil || f.From.Format("2006-01-02T15:04:05.000000Z") != "2026-10-22T12:00:00.123456Z" {
		t.Fatalf("time %+v %v", f, err)
	}
}

type eventHTTPStore struct {
	records []domain.EventRecord
	err     error
	record  domain.EventRecord
}

func (f eventHTTPStore) ReadEventPage(context.Context, domain.EventReadQuery) ([]domain.EventRecord, error) {
	return f.records, f.err
}
func (f eventHTTPStore) ReadEvent(context.Context, domain.EventReadScope, domain.EventLocator) (domain.EventRecord, error) {
	return f.record, f.err
}
func TestEventsHeadersAndSafeErrors(t *testing.T) {
	for _, tc := range []struct {
		name, path, key string
		err             error
		status          int
	}{{"success", "/api/v1/events", "secret", nil, 200}, {"unauthorized", "/api/v1/events", "", nil, 401}, {"invalid", "/api/v1/events?limit=101", "secret", nil, 422}, {"invalid cursor", "/api/v1/events?cursor=bad", "secret", nil, 422}, {"not found", "/api/v1/events/e1.WyJtYW51YWxfY2hlY2siLCJ4Il0", "secret", domain.NewError(domain.Missing, "secret missing"), 404}, {"unavailable", "/api/v1/events", "secret", errors.New("SQL secret"), 503}} {
		t.Run(tc.name, func(t *testing.T) {
			events, e := service.NewEvents(eventHTTPStore{err: tc.err}, nil, service.EventsOptions{Mode: "research", Prefix: "/api/v1"})
			if e != nil {
				t.Fatal(e)
			}
			s := &Server{Config: config.Config{Prefix: "/api/v1", APIKey: "secret"}, Events: events}
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", tc.path, nil)
			r.Header.Set("X-API-Key", tc.key)
			s.Handler().ServeHTTP(w, r)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "SQL secret") || strings.Contains(w.Body.String(), "secret missing") {
				t.Fatalf("%d %s %v", w.Code, w.Body.String(), w.Header())
			}
			if tc.name == "invalid cursor" && !strings.Contains(w.Body.String(), `"detail":"cursor_restart_required"`) {
				t.Fatal(w.Body.String())
			}
		})
	}
}
func TestEventsDetailEncodedBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, size := range []int{16*1024*1024 - 4, 16 * 1024 * 1024} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		e := eventResponse(c, strings.Repeat("x", size))
		if size < 16*1024*1024 {
			if e != nil || w.Code != 200 {
				t.Fatal(e)
			}
		} else if e == nil || w.Body.Len() != 0 {
			t.Fatal("partial oversized response")
		}
	}
}
