package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestArchiveUsesExactWellAndPeriod(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("well_id") != "9223372036854775807" {
			t.Error("well ID changed")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/bounds":
			_, _ = w.Write([]byte(`{"start":"2025-01-01T00:00:00Z","end":"2025-01-02T00:00:00Z"}`))
		case "/v1/telemetry":
			if r.URL.Query().Get("start") != "2025-01-01T00:00:00Z" {
				t.Error("period changed")
			}
			_, _ = w.Write([]byte(`{"points":[{"well_id":"9223372036854775807","timestamp":"2025-01-01T00:00:00Z","values":{"F_out":0,"R_izol":null}}],"truncated":false,"resolution":"1h"}`))
		default:
			t.Error(r.URL.Path)
		}
	}))
	defer server.Close()
	archive := Archive{BaseURL: server.URL, AllowedHosts: []string{"127.0.0.1"}}
	bounds, err := archive.Bounds(context.Background(), "9223372036854775807")
	if err != nil || bounds.Start == nil || bounds.End == nil {
		t.Fatal(bounds, err)
	}
	data, err := archive.Read(context.Background(), "9223372036854775807", *bounds.Start, *bounds.End)
	if err != nil || len(data.Points) != 1 || *data.Points[0].Values["F_out"] != 0 || data.Points[0].Values["R_izol"] != nil || data.Resolution != "1h" {
		t.Fatal(data, err)
	}
}
func TestArchiveDoesNotFollowRedirect(t *testing.T) {
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	_, err := (Archive{BaseURL: source.URL, AllowedHosts: []string{"127.0.0.1"}}).Bounds(context.Background(), "1")
	if err == nil || redirected {
		t.Fatal(err, redirected)
	}
	_, err = (Archive{BaseURL: source.URL, AllowedHosts: []string{"other.test"}}).Read(context.Background(), "1", time.Now().Add(-time.Hour), time.Now())
	if err == nil || !strings.Contains(err.Error(), "INTEGRATION_ALLOWED_HOSTS") {
		t.Fatal(err)
	}
}
