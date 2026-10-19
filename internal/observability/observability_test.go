package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	apitrace "go.opentelemetry.io/otel/trace"
)

func TestContextPropagationAndDetachedLink(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	p := trace.NewTracerProvider(trace.WithSyncer(exp))
	defer p.Shutdown(context.Background())
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(p)
	defer otel.SetTracerProvider(old)
	h := http.Header{"X-Request-Id": {"request_fixture_1234"}, "Traceparent": {"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}, "Baggage": {"secret=value"}, "Tracestate": {"secret=value"}}
	ctx, cancel := context.WithCancel(ExtractHTTP(context.Background(), h))
	ctx, parent := Start(ctx, "HTTP /health", apitrace.SpanKindServer)
	if parent.SpanContext().TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatal("remote parent lost")
	}
	detached, child := Detached(ctx, "check.execute")
	cancel()
	if detached.Err() != nil || RequestID(detached) != RequestID(ctx) {
		t.Fatal("detached operation cancelled/lost correlation")
	}
	InjectHTTP(detached, h)
	if h.Get("Baggage") != "" || h.Get("Tracestate") != "" {
		t.Fatal("private context propagated")
	}
	child.End()
	parent.End()
	spans := exp.GetSpans()
	if len(spans) != 2 || spans[0].Parent.IsValid() || len(spans[0].Links) != 1 || spans[0].Links[0].SpanContext.SpanID() != parent.SpanContext().SpanID() {
		t.Fatalf("bad detached relationship: %+v", spans)
	}
	for _, raw := range []string{"", "00-00000000000000000000000000000000-00f067aa0ba902b7-01", strings.Repeat("x", 513), "\r\ninject", "01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-\u0085"} {
		in := http.Header{"Traceparent": {raw}}
		c, s := Start(ExtractHTTP(context.Background(), in), "HTTP unmatched", apitrace.SpanKindServer)
		if !apitrace.SpanContextFromContext(c).IsValid() || apitrace.SpanContextFromContext(c).TraceID().String() == "4bf92f3577b34da6a3ce929d0e0e4736" {
			t.Fatal("invalid input accepted as remote parent or prevented local trace")
		}
		s.End()
	}
}

type brokenSink struct{}

func (brokenSink) Write([]byte) (int, error) { return 0, errors.New("sink sentinel") }
func TestJSONAllowlistAndSinkFailure(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(&buf, "version")
	ctx := WithRequestID(context.Background(), "request_fixture_1234")
	logger.ErrorContext(ctx, "request failed", "error", errors.New("DSN_SENTINEL"), "filename", "FILE_SENTINEL", "http.route", "/health", "http.response.status_code", 500, "duration_ms", 2.5, "message", "TOKEN_SENTINEL")
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"timestamp", "severity_text", "event_name", "service.name", "service.version", "service.instance.id", "deployment.environment.name", "http_request_id"} {
		if rec[key] == nil {
			t.Fatal("missing", key)
		}
	}
	if rec["http.response.status_code"] != float64(500) || rec["duration_ms"] != 2.5 {
		t.Fatal(rec)
	}
	if strings.Contains(buf.String(), "SENTINEL") || len(buf.Bytes()) > 8192 {
		t.Fatal("private/big record", buf.String())
	}
	buf.Reset()
	logger.Info("TOKEN_SENTINEL", "http.route", strings.Repeat("x", 12000), "error_code", "TOKEN_SENTINEL")
	if !json.Valid(bytes.TrimSpace(buf.Bytes())) || strings.Contains(buf.String(), "SENTINEL") {
		t.Fatal("fallback unsafe")
	}
	NewLogger(brokenSink{}, "version").LogAttrs(ctx, slog.LevelError, "request failed", slog.String("error", "SENTINEL"))
}

type unsafeValue struct{ called *bool }

func (v unsafeValue) LogValue() slog.Value { *v.called = true; panic("SECRET_LOG_VALUE") }
func TestLoggerDoesNotEvaluateUntrustedValues(t *testing.T) {
	called := false
	var out bytes.Buffer
	logger := NewLogger(&out, "test")
	logger.Info("request completed", "error", unsafeValue{called: &called}, "metadata", map[string]string{"key": "SECRET_METADATA"}, "operation_digest", "business-key-SECRET")
	if called || strings.Contains(out.String(), "SECRET") {
		t.Fatal("unsafe attribute evaluated or serialized")
	}
}

func TestProductionProviderUsesBoundedLocalSpans(t *testing.T) {
	p, err := NewProvider("test")
	if err != nil {
		t.Fatal(err)
	}
	exp := tracetest.NewInMemoryExporter()
	p.RegisterSpanProcessor(trace.NewSimpleSpanProcessor(exp))
	defer p.Shutdown(context.Background())
	_, span := p.Tracer("test").Start(context.Background(), "bounded")
	for i := range 60 {
		span.SetAttributes(attribute.String(fmt.Sprintf("safe_%d", i), strings.Repeat("x", 500)))
		span.AddEvent("safe")
	}
	span.End()
	spans := exp.GetSpans()
	if len(spans) != 1 || len(spans[0].Attributes) != 32 || len(spans[0].Events) != 16 {
		t.Fatal("SDK limits missing")
	}
	for _, a := range spans[0].Attributes {
		if len(a.Value.AsString()) > 256 {
			t.Fatal("SDK string limit missing")
		}
	}
}

func TestProductionResourceIgnoresAmbientPrivateEnvironment(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "private.token=ENV_SECRET,service.namespace=ENV_SECRET")
	t.Setenv("OTEL_SERVICE_NAME", "ENV_SECRET")
	p, err := NewProvider("test")
	if err != nil {
		t.Fatal(err)
	}
	exp := tracetest.NewInMemoryExporter()
	p.RegisterSpanProcessor(trace.NewSimpleSpanProcessor(exp))
	defer p.Shutdown(context.Background())
	_, span := p.Tracer("test").Start(context.Background(), "bounded")
	span.End()
	resource := exp.GetSpans()[0].Resource
	allowed := map[attribute.Key]bool{"service.name": true, "service.version": true, "service.instance.id": true, "deployment.environment.name": true}
	for _, a := range resource.Attributes() {
		if !allowed[a.Key] || strings.Contains(a.Value.AsString(), "SECRET") {
			t.Fatal("ambient private resource imported")
		}
	}
	if got := os.Getenv("OTEL_SERVICE_NAME"); got != "ENV_SECRET" {
		t.Fatal("provider changed service-name environment")
	}
	if got := os.Getenv("OTEL_RESOURCE_ATTRIBUTES"); got != "private.token=ENV_SECRET,service.namespace=ENV_SECRET" {
		t.Fatal("provider persistently changed process environment")
	}
}

func TestProviderRestoresEnvironmentPresence(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			for _, key := range []string{"OTEL_RESOURCE_ATTRIBUTES", "OTEL_SERVICE_NAME"} {
				t.Setenv(key, "")
				if !present {
					if err := os.Unsetenv(key); err != nil {
						t.Fatal(err)
					}
				}
			}
			p, err := NewProvider("test")
			if err != nil {
				t.Fatal(err)
			}
			defer p.Shutdown(context.Background())
			for _, key := range []string{"OTEL_RESOURCE_ATTRIBUTES", "OTEL_SERVICE_NAME"} {
				value, exists := os.LookupEnv(key)
				if exists != present || value != "" {
					t.Fatal("environment presence changed")
				}
			}
		})
	}
}
