package observability

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type ExportConfig struct {
	Enabled bool
	LogDir  string
}
type Runtime struct {
	provider  *sdktrace.TracerProvider
	meter     *sdkmetric.MeterProvider
	processor *countingProcessor
	file      *fileSink
	metrics   *telemetryMetrics
	stop      chan struct{}
	once      sync.Once
}

// NewRuntime constructs optional diagnostics before application workers start.
// Invalid optional configuration falls back to the unchanged O1 stdout/provider.
func NewRuntime(ctx context.Context, version string, cfg ExportConfig) (*Runtime, error) {
	r := &Runtime{stop: make(chan struct{})}
	var traceExporter sdktrace.SpanExporter
	var metricExporter sdkmetric.Exporter
	valid := cfg.LogDir == "" || cfg.LogDir == "/var/log/pulse"
	enabled := cfg.Enabled && valid
	if enabled {
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {}))
	}
	err := withIsolatedEnvironment(func() error {
		r.provider = newProvider(version)
		if !enabled {
			return nil
		}
		te, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint("otel-collector:4317"), otlptracegrpc.WithInsecure(), otlptracegrpc.WithHeaders(map[string]string{}), otlptracegrpc.WithTimeout(500*time.Millisecond), otlptracegrpc.WithCompressor(""), otlptracegrpc.WithRetry(otlptracegrpc.RetryConfig{Enabled: false}))
		if err != nil {
			return errors.New("telemetry setup failed")
		}
		traceExporter = te
		me, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpoint("otel-collector:4317"), otlpmetricgrpc.WithInsecure(), otlpmetricgrpc.WithHeaders(map[string]string{}), otlpmetricgrpc.WithTimeout(500*time.Millisecond), otlpmetricgrpc.WithCompressor(""), otlpmetricgrpc.WithRetry(otlpmetricgrpc.RetryConfig{Enabled: false}), otlpmetricgrpc.WithTemporalitySelector(func(sdkmetric.InstrumentKind) metricdata.Temporality { return metricdata.CumulativeTemporality }))
		if err != nil {
			return errors.New("telemetry setup failed")
		}
		metricExporter = me
		r.metrics = newTelemetryMetrics()
		reader := sdkmetric.NewPeriodicReader(&countingMetricExporter{Exporter: metricExporter, metrics: r.metrics}, sdkmetric.WithInterval(5*time.Second), sdkmetric.WithTimeout(500*time.Millisecond))
		r.meter = newMeterProvider(version, reader)
		return r.metrics.bind(r.meter)
	})
	if err != nil { // Constructor failures are static and optional; clients receive bounded cleanup.
		if traceExporter != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			_ = traceExporter.Shutdown(cleanup)
			cancel()
		}
		enabled = false
	}
	if r.provider == nil {
		return nil, errors.New("observability setup failed")
	}
	otel.SetTracerProvider(r.provider)
	if !enabled {
		Install(version)
		if cfg.Enabled {
			slog.Warn("telemetry setup failed")
		}
		return r, nil
	}
	// Собственные trace/file workers запускаются после восстановления окружения.
	r.processor = newCountingProcessor(traceExporter, r.metrics)
	r.provider.RegisterSpanProcessor(r.processor)
	var emit func([]byte) bool
	if cfg.LogDir != "" {
		r.file = newFileSink(cfg.LogDir, r.metrics)
		emit = r.file.tryEmit
	}
	slog.SetDefault(newLogger(os.Stdout, version, emit))
	go r.snapshots()
	return r, nil
}

func newMeterProvider(version string, reader sdkmetric.Reader) *sdkmetric.MeterProvider {
	return sdkmetric.NewMeterProvider(sdkmetric.WithResource(localResource(version)), sdkmetric.WithReader(reader), sdkmetric.WithCardinalityLimit(0), sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter))
}
func (r *Runtime) snapshots() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	var previous int64
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			s := r.metrics.snapshot()
			total := s.TraceDroppedTotal + s.LogDroppedTotal + s.TraceExportFailedTotal + s.MetricExportFailedTotal
			level := slog.LevelInfo
			if total > previous {
				level = slog.LevelWarn
			}
			previous = total
			slog.LogAttrs(context.Background(), level, "telemetry.snapshot", slog.Int64("trace_queue_size", s.TraceQueueSize), slog.Int64("log_queue_size", s.LogQueueSize), slog.Int64("trace_dropped_total", s.TraceDroppedTotal), slog.Int64("log_dropped_total", s.LogDroppedTotal), slog.Int64("trace_export_failed_total", s.TraceExportFailedTotal), slog.Int64("metric_export_failed_total", s.MetricExportFailedTotal))
		}
	}
}
func (r *Runtime) Shutdown(ctx context.Context) (err error) {
	r.once.Do(func() {
		close(r.stop)
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if r.processor != nil {
			err = r.processor.Shutdown(ctx)
		}
		if r.meter != nil {
			if e := r.shutdownMeter(ctx); e != nil {
				err = errTelemetry
			}
		}
		if r.file != nil {
			if e := r.file.Shutdown(ctx); e != nil {
				err = e
			}
		}
		// The processor has already been bounded and made idempotent.
		if e := r.provider.Shutdown(ctx); e != nil {
			err = errTelemetry
		}
	})
	return
}

// shutdownMeter isolates the SDK's synchronous final export/shutdown from callers.
// A noncooperative exporter may retain this one lifecycle goroutine; it is never replaced.
func (r *Runtime) shutdownMeter(ctx context.Context) error {
	done := make(chan error, 1)
	go func() {
		var err error
		defer func() {
			if recover() != nil {
				err = errTelemetry
				if r.metrics != nil {
					r.metrics.exportFailure("metrics", "internal")
				}
			}
			done <- err
		}()
		err = r.meter.Shutdown(ctx)
	}()
	select {
	case err := <-done:
		if err != nil {
			return errTelemetry
		}
		return nil
	case <-ctx.Done():
		return errTelemetry
	}
}
