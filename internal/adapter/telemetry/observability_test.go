package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/observability"
)

func TestArchiveActualHTTPPropagatesSafeChildContext(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	p := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(p)
	defer otel.SetTracerProvider(old)
	defer p.Shutdown(context.Background())
	var sent http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = r.Header.Clone()
		w.Write([]byte(`{"points":[]}`))
	}))
	defer server.Close()
	ctx, parent := observability.Start(observability.WithRequestID(context.Background(), "request_fixture_1234"), "test.parent", trace.SpanKindServer)
	defer parent.End()
	archive := Archive{BaseURL: server.URL, AllowedHosts: []string{"127.0.0.1"}}
	if _, err := archive.Read(ctx, "PRIVATE_WELL_SENTINEL", time.Now().Add(-time.Hour), time.Now()); err != nil {
		t.Fatal(err)
	}
	if sent.Get("X-Request-ID") != "request_fixture_1234" || sent.Get("traceparent") == "" {
		t.Fatal("source context absent")
	}
	spans := exp.GetSpans()
	if len(spans) != 1 || spans[0].Parent.SpanID() != parent.SpanContext().SpanID() {
		t.Fatal("actual child absent")
	}
	for _, a := range spans[0].Attributes {
		if strings.Contains(a.Value.AsString(), "SENTINEL") {
			t.Fatal("well leaked")
		}
	}
	if _, err := (Archive{}).Read(ctx, "1", time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(exp.GetSpans()) != 1 {
		t.Fatal("unconfigured Data created fake span")
	}
}

func TestCollectionSourceHTTPPropagatesWithoutLoggingSecrets(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	p := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(p)
	defer otel.SetTracerProvider(old)
	defer p.Shutdown(context.Background())
	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { headers = r.Header.Clone(); w.Write([]byte(`[]`)) }))
	defer server.Close()
	ctx, parent := observability.Start(observability.WithRequestID(context.Background(), "request_fixture_1234"), "telemetry.collect", trace.SpanKindInternal)
	defer parent.End()
	connector := Connector{AllowedHosts: []string{"127.0.0.1"}}
	_, err := connector.Fetch(ctx, domain.TelemetrySource{Kind: "json_pull", Endpoint: server.URL + "/PRIVATE_SENTINEL?token=QUERY_SENTINEL"}, "AUTH_SENTINEL")
	if err != nil {
		t.Fatal(err)
	}
	if headers.Get("X-Request-ID") != "request_fixture_1234" || headers.Get("traceparent") == "" || headers.Get("Authorization") != "Bearer AUTH_SENTINEL" {
		t.Fatal("source correlation/auth lost")
	}
	spans := exp.GetSpans()
	if len(spans) != 1 || spans[0].Parent.SpanID() != parent.SpanContext().SpanID() {
		t.Fatal("source client span missing")
	}
	for _, a := range spans[0].Attributes {
		if strings.Contains(a.Value.AsString(), "SENTINEL") {
			t.Fatal("private source attribute")
		}
	}
}
