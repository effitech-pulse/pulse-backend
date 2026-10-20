package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
)

func sampleContext() trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled})
}
func TestDisabledHasNoWorkersFilesOrNetwork(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	r, err := NewRuntime(context.Background(), "test", ExportConfig{LogDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Shutdown(context.Background())
	if r.processor != nil || r.meter != nil || r.file != nil || r.metrics != nil {
		t.Fatal("disabled allocated exporter/metric/file workers")
	}
	if _, err = os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("disabled created files")
	}
}
func TestPoisonExporterEnvRestored(t *testing.T) {
	keys := []string{"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE", "OTEL_EXPORTER_OTLP_METRICS_CLIENT_KEY", "OTEL_PYTHON_EXPORTER_OTLP_GRPC_TRACES_CREDENTIAL_PROVIDER", "OTEL_RESOURCE_ATTRIBUTES", "OTEL_SERVICE_NAME"}
	for _, k := range keys {
		t.Setenv(k, "PRIVATE_POISON")
	}
	r, err := NewRuntime(context.Background(), "test", ExportConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Shutdown(context.Background())
	if r.processor == nil || r.meter == nil {
		t.Fatal("poison caused disabled fallback")
	}
	for _, k := range keys {
		if os.Getenv(k) != "PRIVATE_POISON" {
			t.Fatal("env not restored", k)
		}
	}
}
func TestSafeBytesOnlyFileSink(t *testing.T) {
	dir := t.TempDir()
	m := newTelemetryMetrics()
	s := newFileSink(dir, m)
	var out bytes.Buffer
	logger := newLogger(&out, "test", s.tryEmit)
	logger.Info("request completed", "filename", "PRIVATE", "error", "PRIVATE")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "pulse.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, out.Bytes()) || strings.Contains(string(raw), "PRIVATE") {
		t.Fatal("unsafe file")
	}
	if s.tryEmit([]byte(strings.Repeat("x", 8193))) {
		t.Fatal("oversize accepted")
	}
}
func TestSnapshotAllowlistAndFileFailureKeepsStdout(t *testing.T) {
	var out bytes.Buffer
	logger := newLogger(&out, "test", func([]byte) bool { panic("PRIVATE") })
	logger.LogAttrs(context.Background(), slog.LevelWarn, "telemetry.snapshot", slog.Int64("trace_dropped_total", 3), slog.Int64("log_queue_size", -1), slog.String("filename", "PRIVATE"))
	var record map[string]any
	if err := json.Unmarshal(out.Bytes(), &record); err != nil {
		t.Fatal("stdout skipped", err)
	}
	if record["trace_dropped_total"] != float64(3) || record["log_queue_size"] != nil || strings.Contains(out.String(), "PRIVATE") {
		t.Fatal(record)
	}
}

func TestRuntimeRestoresAbsentEmptyAndPresentExporterEnv(t *testing.T) {
	for _, value := range []string{"absent", "", "PRIVATE"} {
		t.Run(value, func(t *testing.T) {
			key := "OTEL_EXPORTER_OTLP_METRICS_CERTIFICATE"
			t.Setenv(key, value)
			if value == "absent" {
				if err := os.Unsetenv(key); err != nil {
					t.Fatal(err)
				}
			}
			r, err := NewRuntime(context.Background(), "test", ExportConfig{Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			_ = r.Shutdown(context.Background())
			got, present := os.LookupEnv(key)
			if present != (value != "absent") || present && got != value {
				t.Fatal("environment presence/value changed", present, got)
			}
		})
	}
}

func TestPoisonCertificateIsNotReadBeforeExplicitOverrides(t *testing.T) {
	if _, err := os.Stat("/dev/fd"); err != nil {
		t.Skip("requires Unix descriptor filesystem")
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	defer write.Close()
	poison := fmt.Sprintf("/dev/fd/%d", read.Fd())
	for _, key := range []string{"OTEL_EXPORTER_OTLP_CERTIFICATE", "OTEL_EXPORTER_OTLP_TRACES_CERTIFICATE", "OTEL_EXPORTER_OTLP_METRICS_CERTIFICATE", "OTEL_EXPORTER_OTLP_CLIENT_KEY", "OTEL_EXPORTER_OTLP_TRACES_CLIENT_KEY", "OTEL_EXPORTER_OTLP_METRICS_CLIENT_KEY"} {
		t.Setenv(key, poison)
	}
	result := make(chan *Runtime, 1)
	go func() { r, _ := NewRuntime(context.Background(), "test", ExportConfig{Enabled: true}); result <- r }()
	select {
	case r := <-result:
		if r == nil || r.processor == nil || r.meter == nil {
			t.Fatal("poison caused silent fallback")
		}
		_ = r.Shutdown(context.Background())
	case <-time.After(2 * time.Second):
		_ = write.Close()
		r := <-result
		if r != nil {
			_ = r.Shutdown(context.Background())
		}
		t.Fatal("exporter attempted to read poisoned certificate descriptor")
	}
}

func TestSDKConstructorErrorsUseStaticHandler(t *testing.T) {
	t.Setenv("OTEL_GO_X_CARDINALITY_LIMIT", "PRIVATE_INVALID_NUMBER")
	var called atomic.Bool
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) { called.Store(true) }))
	r, err := NewRuntime(context.Background(), "test", ExportConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Shutdown(context.Background())
	if called.Load() {
		t.Fatal("SDK constructor exposed private error to ambient handler")
	}
}

type noncooperativeMetricShutdown struct{ entered, release chan struct{} }

func (*noncooperativeMetricShutdown) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}
func (*noncooperativeMetricShutdown) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}
func (*noncooperativeMetricShutdown) Export(context.Context, *metricdata.ResourceMetrics) error {
	return nil
}
func (*noncooperativeMetricShutdown) ForceFlush(context.Context) error { return nil }
func (e *noncooperativeMetricShutdown) Shutdown(context.Context) error {
	close(e.entered)
	<-e.release
	return nil
}
func TestRuntimeShutdownBoundsNoncooperativeMetricShutdown(t *testing.T) {
	exporter := &noncooperativeMetricShutdown{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(exporter.release)
	provider, err := NewProvider("test")
	if err != nil {
		t.Fatal(err)
	}
	reader := sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(time.Hour))
	r := &Runtime{provider: provider, meter: newMeterProvider("test", reader), stop: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Shutdown(ctx) }()
	<-exporter.entered
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("metric shutdown failure hidden")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("metric exporter shutdown held Runtime caller after deadline")
	}
	if err := r.Shutdown(context.Background()); err != nil {
		t.Fatal("repeat shutdown not idempotent")
	}
}

func TestInvalidOptionalConfigFallsBackWithStaticDiagnostic(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	old := os.Stdout
	os.Stdout = write
	defer func() { os.Stdout = old; Install("test") }()
	r, err := NewRuntime(context.Background(), "test", ExportConfig{Enabled: true, LogDir: "/PRIVATE_INVALID_PATH"})
	if err != nil {
		t.Fatal(err)
	}
	if r.processor != nil || r.meter != nil || r.file != nil {
		t.Fatal("invalid config created optional clients")
	}
	_ = r.Shutdown(context.Background())
	_ = write.Close()
	raw, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err = json.Unmarshal(raw, &record); err != nil {
		t.Fatal("missing static diagnostic", err)
	}
	if record["event_name"] != "diagnostic.suppressed" || strings.Contains(string(raw), "PRIVATE") {
		t.Fatal("invalid fallback diagnostic", string(raw))
	}
}
