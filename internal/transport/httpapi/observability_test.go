package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/observability"
)

func TestCorrelationHeaderOnAllHTTPOutcomes(t *testing.T) {
	s := &Server{Config: config.Config{Prefix: "/api/v1", APIKey: "secret", CORS: []string{"https://ui.example"}}}
	router := s.routes()
	router.GET("/api/v1/test-panic", func(c *gin.Context) { panic("TOKEN_SENTINEL") })
	safe := regexp.MustCompile(`^[a-f0-9]{32}$`)
	for _, tc := range []struct {
		name, path string
		ids        []string
		key        string
		status     int
	}{
		{"valid", "/health", []string{"request_fixture_1234"}, "", 200},
		{"missing", "/health", nil, "", 200},
		{"duplicate", "/health", []string{"request_fixture_1234", "request_fixture_5678"}, "", 200},
		{"oversize", "/health", []string{strings.Repeat("a", 65)}, "", 200},
		{"injection", "/health", []string{"request_fixture_1234\r\nsecret"}, "", 200},
		{"unauthorized", "/api/v1/wells", nil, "", 401},
		{"panic", "/api/v1/test-panic", nil, "secret", 500},
		{"unmatched", "/private/123?token=secret", nil, "", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header["X-Request-Id"] = tc.ids
			req.Header.Set("X-API-Key", tc.key)
			req.Header.Set("Origin", "https://ui.example")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d", rec.Code)
			}
			id := rec.Header().Get("X-Request-ID")
			if tc.name == "valid" {
				if id != tc.ids[0] {
					t.Fatalf("id=%q", id)
				}
			} else if !safe.MatchString(id) {
				t.Fatalf("unsafe or missing id=%q", id)
			}
			if !strings.Contains(rec.Header().Get("Access-Control-Expose-Headers"), "X-Request-ID") {
				t.Fatal("ID unavailable to browser")
			}
		})
	}
}

func TestHTTPSpanAndJSONExcludePrivatePathsAndPanic(t *testing.T) {
	var logs bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(observability.NewLogger(&logs, "test"))
	defer slog.SetDefault(oldLogger)
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	defer provider.Shutdown(context.Background())
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(old)
	s := &Server{Config: config.Config{Prefix: "/api/v1"}}
	router := s.routes()
	router.GET("/panic/:id", func(c *gin.Context) { panic("PANIC_SENTINEL") })
	for _, path := range []string{"/panic/PRIVATE_SENTINEL?token=QUERY_SENTINEL", "/unmatched/PRIVATE_SENTINEL?token=QUERY_SENTINEL"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
		req.Header.Set("X-Request-ID", "request_fixture_1234")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
	}
	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatal("missing HTTP spans")
	}
	for _, span := range spans {
		if span.SpanContext.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || span.Parent.SpanID().String() != "00f067aa0ba902b7" {
			t.Fatal("remote context lost")
		}
		for _, a := range span.Attributes {
			if strings.Contains(a.Value.AsString(), "SENTINEL") {
				t.Fatal("private span")
			}
		}
	}
	if spans[0].Name != "HTTP /panic/:id" || spans[1].Name != "HTTP unmatched" {
		t.Fatal("raw route used")
	}
	if strings.Contains(logs.String(), "SENTINEL") {
		t.Fatal("private log content")
	}
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var row map[string]any
		if json.Unmarshal([]byte(line), &row) != nil {
			t.Fatal("invalid log")
		}
		if row["http_request_id"] != "request_fixture_1234" || row["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
			t.Fatal("uncorrelated record", row)
		}
	}
}

type failedLogSink struct{}

func (failedLogSink) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestHTTPResultSurvivesLogSinkFailure(t *testing.T) {
	old := slog.Default()
	slog.SetDefault(observability.NewLogger(failedLogSink{}, "test"))
	defer slog.SetDefault(old)
	server := &Server{Config: config.Config{Prefix: "/api/v1"}}
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
	if rec.Code != 200 || !json.Valid(rec.Body.Bytes()) || rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("diagnostic sink changed result")
	}
}
