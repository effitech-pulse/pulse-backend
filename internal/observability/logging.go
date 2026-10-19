package observability

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"os"
	"regexp"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
)

var instanceID = RequestID(WithRequestID(context.Background(), ""))
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var safeCode = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
var events = map[string]string{
	"создание администратора не выполнено": "admin.create_failed", "первый администратор создан": "admin.created", "migration failed": "migration.failed", "database schema ready": "migration.ready", "cohort capture failed": "cohort.capture_failed", "cohort captured": "cohort.captured",
	"request completed": "http.completed", "request failed": "http.failed", "handler panic": "http.panic", "audit failed": "audit.failed",
	"rpc completed": "rpc.completed", "source completed": "source.completed", "database completed": "database.completed", "operation completed": "operation.completed",
	"Go API started": "service.started", "API stopped": "service.stopped", "run creation failed": "run.create_failed", "ML run failed": "run.failed", "record run failure": "run.persist_failed", "scheduled ML run failed": "run.schedule_failed", "unlock failed": "run.unlock_failed",
	"telemetry collection failed": "collection.failed", "record collection failure": "collection.persist_failed", "record mapping failure": "collection.mapping_failed",
}
var errorCodes = map[string]bool{"operation_failed": true, "panic": true, "internal_error": true}

func bounded(v string, n int) string {
	if len(v) > n || v == "" {
		return "unknown"
	}
	return v
}

type logSink struct {
	mu  sync.Mutex
	out io.Writer
}
type handler struct {
	sink    *logSink
	version string
	attrs   []slog.Attr
}

func NewLogger(w io.Writer, version string) *slog.Logger {
	return slog.New(&handler{sink: &logSink{out: w}, version: bounded(version, 128)})
}
func Install(version string)                                    { slog.SetDefault(NewLogger(os.Stdout, version)) }
func (h *handler) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelInfo }
func (h *handler) WithAttrs(a []slog.Attr) slog.Handler {
	c := *h
	c.attrs = append(append([]slog.Attr{}, h.attrs...), a...)
	return &c
}
func (h *handler) WithGroup(_ string) slog.Handler { return h }
func (h *handler) Handle(ctx context.Context, r slog.Record) (err error) {
	// Диагностика не должна менять исход бизнес-операции даже при неисправном sink.
	defer func() { _ = recover(); err = nil }()
	event, ok := events[r.Message]
	if !ok {
		event = "diagnostic.suppressed"
	}
	severity := "INFO"
	if r.Level >= slog.LevelError {
		severity = "ERROR"
	} else if r.Level >= slog.LevelWarn {
		severity = "WARN"
	}
	out := map[string]any{"timestamp": r.Time.UTC().Format(time.RFC3339Nano), "severity_text": severity, "event_name": event, "service.name": "pulse-backend", "service.version": h.version, "service.instance.id": instanceID, "deployment.environment.name": "local"}
	if id := RequestID(ctx); id != "" {
		out["http_request_id"] = id
	}
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		out["trace_id"] = sc.TraceID().String()
		out["span_id"] = sc.SpanID().String()
	}
	add := func(a slog.Attr) {
		if !ok {
			return
		}
		// Никогда не вызываем Resolve/String/Error у пользовательского значения.
		switch a.Key {
		case "duration_ms":
			if a.Value.Kind() == slog.KindFloat64 && a.Value.Float64() >= 0 && !math.IsInf(a.Value.Float64(), 0) {
				out[a.Key] = a.Value.Float64()
			}
		case "http.response.status_code", "rpc.grpc.status_code":
			if a.Value.Kind() == slog.KindInt64 {
				out[a.Key] = a.Value.Int64()
			}
		case "operation_digest":
			if a.Value.Kind() == slog.KindString && digestPattern.MatchString(a.Value.String()) {
				out[a.Key] = a.Value.String()
			}
		case "http.request.method", "http.route", "rpc.method":
			if a.Value.Kind() == slog.KindString && len(a.Value.String()) <= 256 {
				out[a.Key] = a.Value.String()
			}
		case "error_code":
			if a.Value.Kind() == slog.KindString && safeCode.MatchString(a.Value.String()) && errorCodes[a.Value.String()] {
				out[a.Key] = a.Value.String()
			}
		}
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool { add(a); return true })
	raw, e := json.Marshal(out)
	if e != nil {
		return nil
	}
	if len(raw) > 8191 {
		for k := range out {
			switch k {
			case "timestamp", "severity_text", "event_name", "service.name", "service.version", "service.instance.id", "deployment.environment.name", "http_request_id", "trace_id", "span_id":
			default:
				delete(out, k)
			}
		}
		raw, _ = json.Marshal(out)
	}
	h.sink.mu.Lock()
	defer h.sink.mu.Unlock()
	_, _ = h.sink.out.Write(append(raw, '\n'))
	return nil
}
