package integration

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/telemetry"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestManualCheckTraceUsesRealPGAndLinkedOperation(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	scorer := &checkScorer{version: "test"}
	app.http.Integrations = service.NewIntegrations(app.store, telemetry.Connector{}, telemetry.Secrets{}, scorer)
	app.http.Checks = service.NewChecks(app.store, app.http.Integrations)
	h := app.Handler()
	call(t, h, "POST", "/api/v1/wells", `{"ngdu":0,"department":0}`, 201)
	call(t, h, "POST", "/api/v1/telemetry/import", `{"points":[{"well_id":"1","timestamp":"2025-01-01T00:00:00Z","values":{"Ia":0}}]}`, 200)
	exp := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(old)
	defer provider.Shutdown(context.Background())
	body := `{"request_id":"stable_business_1234","start":"2025-01-01T00:00:00Z","end":"2025-02-02T00:00:00Z"}`
	req := httptest.NewRequest("POST", "/api/v1/wells/1/checks?secret=QUERY_SENTINEL", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "request_fixture_1234")
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var saved map[string]any
	if json.Unmarshal(rec.Body.Bytes(), &saved) != nil || saved["request_id"] != "stable_business_1234" || rec.Header().Get("X-Request-ID") != "request_fixture_1234" {
		t.Fatal("identity mixed")
	}
	spans := exp.GetSpans()
	byName := map[string]tracetest.SpanStub{}
	for _, s := range spans {
		byName[s.Name] = s
		if strings.HasPrefix(s.Name, "Data ") {
			t.Fatal("PG-only check invented Data request")
		}
	}
	check, ok := byName["check.execute"]
	if !ok {
		t.Fatal("manual operation has no span")
	}
	server := byName["HTTP /api/v1/wells/:well_id/checks"]
	if check.Parent.IsValid() || len(check.Links) != 1 || check.Links[0].SpanContext.SpanID() != server.SpanContext.SpanID() {
		t.Fatal("detached check relationship invalid")
	}
	for _, name := range []string{"telemetry.read", "check.save"} {
		s, ok := byName[name]
		if !ok || s.Parent.SpanID() != check.SpanContext.SpanID() {
			t.Fatal("actual PG span missing/parent incorrect", name)
		}
	}
	// Replaying the stable body remains idempotent even with a different HTTP attempt ID.
	again := call(t, h, "POST", "/api/v1/wells/1/checks", body, 200).(map[string]any)
	if again["id"] != saved["id"] || scorer.calls != 1 {
		t.Fatal("correlation changed idempotency")
	}
}
