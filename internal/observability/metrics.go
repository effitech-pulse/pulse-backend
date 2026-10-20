package observability

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type telemetrySnapshot struct{ TraceQueueSize, LogQueueSize, TraceDroppedTotal, LogDroppedTotal, TraceExportFailedTotal, MetricExportFailedTotal int64 }
type telemetryMetrics struct {
	traceQueue, logQueue atomic.Int64
	logCapacity          atomic.Int64
	drops                [2][6]atomic.Int64
	failures             [2][3]atomic.Int64
	overflow             atomic.Int64
	mu                   sync.Mutex
	sets                 map[string]bool
	duration             metric.Float64Histogram
	completed            metric.Int64Counter
	cardinality          metric.Int64ObservableCounter
	start                time.Time
}

var dropReasons = []string{"queue_full", "closed", "export_failed", "io_failed", "shutdown_timeout", "oversize"}
var failureReasons = []string{"unavailable", "timeout", "internal"}

func newTelemetryMetrics() *telemetryMetrics {
	return &telemetryMetrics{sets: make(map[string]bool), start: time.Now()}
}
func (m *telemetryMetrics) drop(signal, reason string, n int64) {
	i := 0
	if signal == "logs" {
		i = 1
	}
	for j, r := range dropReasons {
		if r == reason {
			m.drops[i][j].Add(n)
			return
		}
	}
}
func (m *telemetryMetrics) exportFailure(signal, reason string) {
	i := 0
	if signal == "metrics" {
		i = 1
	}
	for j, r := range failureReasons {
		if r == reason {
			m.failures[i][j].Add(1)
			return
		}
	}
}
func (m *telemetryMetrics) snapshot() (s telemetrySnapshot) {
	s.TraceQueueSize = m.traceQueue.Load()
	s.LogQueueSize = m.logQueue.Load()
	for j := range dropReasons {
		s.TraceDroppedTotal += m.drops[0][j].Load()
		s.LogDroppedTotal += m.drops[1][j].Load()
	}
	for j := range failureReasons {
		s.TraceExportFailedTotal += m.failures[0][j].Load()
		s.MetricExportFailedTotal += m.failures[1][j].Load()
	}
	return
}

// Only source-controlled span names can become metric dimensions.
var operationLayers = map[string]string{
	"check.execute":                                       "operation",
	"ml.run":                                              "operation",
	"telemetry.collect":                                   "operation",
	"telemetry.save":                                      "db",
	"telemetry.read":                                      "db",
	"telemetry.bounds":                                    "db",
	"check.save":                                          "db",
	"source.json":                                         "source",
	"Data /v1/bounds":                                     "source",
	"Data /v1/telemetry":                                  "source",
	"Data /v1/scenario":                                   "source",
	"MLService/Score":                                     "rpc",
	"MLService/Upload":                                    "rpc",
	"MLService/GetSnapshot":                               "rpc",
	"HTTP /health":                                        "http",
	"HTTP /openapi.json":                                  "http",
	"HTTP /api/v1/ml/upload":                              "http",
	"HTTP /api/v1/integrations/check":                     "http",
	"HTTP /api/v1/integrations":                           "http",
	"HTTP /api/v1/integrations/logs":                      "http",
	"HTTP /api/v1/auth/me":                                "http",
	"HTTP /api/v1/auth/login":                             "http",
	"HTTP /api/v1/auth/logout":                            "http",
	"HTTP /api/v1/auth/users":                             "http",
	"HTTP /api/v1/auth/users/:user_id":                    "http",
	"HTTP /api/v1/wells":                                  "http",
	"HTTP /api/v1/wells/bulk":                             "http",
	"HTTP /api/v1/wells/import":                           "http",
	"HTTP /api/v1/wells/export":                           "http",
	"HTTP /api/v1/wells/:well_id/telemetry":               "http",
	"HTTP /api/v1/wells/:well_id/telemetry/bounds":        "http",
	"HTTP /api/v1/wells/:well_id/telemetry/score":         "http",
	"HTTP /api/v1/wells/:well_id/checks":                  "http",
	"HTTP /api/v1/wells/:well_id/checks/:check_id":        "http",
	"HTTP /api/v1/wells/:well_id/checks/:check_id/review": "http",
	"HTTP /api/v1/ml/alerts/:alert_id/review":             "http",
	"HTTP /api/v1/wells/:well_id/risk":                    "http",
	"HTTP /api/v1/wells/:well_id/risk/history":            "http",
	"HTTP /api/v1/wells/:well_id/archive":                 "http",
	"HTTP /api/v1/wells/:well_id/restore":                 "http",
	"HTTP /api/v1/wells/:well_id/history":                 "http",
	"HTTP /api/v1/wells/:well_id":                         "http",
	"HTTP /api/v1/wells/:well_id/predictions":             "http",
	"HTTP /api/v1/wells/:well_id/predictions/export":      "http",
	"HTTP /api/v1/wells/:well_id/complications":           "http",
	"HTTP /api/v1/wells/:well_id/classification":          "http",
	"HTTP /api/v1/wells/:well_id/incidents":               "http",
	"HTTP /api/v1/ml/wells":                               "http",
	"HTTP /api/v1/ml/wells/refresh":                       "http",
	"HTTP /api/v1/ml/runs/:run_id/snapshot":               "http",
	"HTTP /api/v1/ml/runs":                                "http",
	"HTTP /api/v1/ml/alerts/:alert_id":                    "http",
	"HTTP /api/v1/ml/wells/by-unitek/:unitek_id":          "http",
	"HTTP /api/v1/ml/wells/:well_id/explain":              "http",
	"HTTP /api/v1/ml/wells/:well_id/history":              "http",
	"HTTP /api/v1/ml/health":                              "http",
	"HTTP /api/v1/ml/metrics":                             "http",
	"HTTP /api/v1/presentation/scenario":                  "http",
	"HTTP /api/v1/presentation/summary":                   "http",
	"HTTP /api/v1/telemetry/import":                       "http",
	"HTTP /api/v1/incidents/:incident_id":                 "http",
	"HTTP /api/v1/incidents/stats":                        "http",
	"HTTP /api/v1/ngdu":                                   "http",
}

func metricDimensions(s sdktrace.ReadOnlySpan) (string, string, string) {
	op := s.Name()
	layer, ok := operationLayers[op]
	if !ok {
		op = "other"
		switch {
		case strings.HasPrefix(s.Name(), "HTTP "):
			layer = "http"
		case strings.HasPrefix(s.Name(), "MLService/"):
			layer = "rpc"
		case strings.HasPrefix(s.Name(), "Data ") || strings.HasPrefix(s.Name(), "source."):
			layer = "source"
		case strings.HasPrefix(s.Name(), "telemetry.") || strings.HasPrefix(s.Name(), "check."):
			layer = "operation"
		default:
			layer = "operation"
		}
	}
	outcome := "ok"
	if s.Status().Code == codes.Error {
		outcome = "error"
	}
	for _, a := range s.Attributes() {
		if a.Key == "http.response.status_code" && a.Value.AsInt64() >= 400 || a.Key == "rpc.grpc.status_code" && a.Value.AsInt64() != 0 {
			outcome = "error"
		}
	}
	return layer, op, outcome
}
func (m *telemetryMetrics) dimensions(layer, op, outcome string) []attribute.KeyValue {
	m.mu.Lock()
	if op != "other" {
		key := layer + "|" + op + "|" + outcome
		if !m.sets[key] {
			if len(m.sets) >= 118 {
				op = "other"
				m.overflow.Add(2)
			} else {
				m.sets[key] = true
			}
		}
	}
	m.mu.Unlock()
	return []attribute.KeyValue{attribute.String("service", "pulse-backend"), attribute.String("layer", layer), attribute.String("operation", op), attribute.String("outcome", outcome)}
}
func (m *telemetryMetrics) operation(s sdktrace.ReadOnlySpan) {
	if m.duration == nil {
		return
	}
	layer, op, outcome := metricDimensions(s)
	attrs := m.dimensions(layer, op, outcome)
	ctx := context.Background()
	m.duration.Record(ctx, max(0, s.EndTime().Sub(s.StartTime()).Seconds()), metric.WithAttributes(attrs...))
	m.completed.Add(ctx, 1, metric.WithAttributes(attrs...))
}
func (m *telemetryMetrics) bind(p *sdkmetric.MeterProvider) error {
	meter := p.Meter("pulse-backend")
	var err error
	m.duration, err = meter.Float64Histogram("pulse.operation.duration", metric.WithUnit("s"), metric.WithExplicitBucketBoundaries(.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 15, 60, 300, 1800))
	if err != nil {
		return err
	}
	m.completed, err = meter.Int64Counter("pulse.operation.completed", metric.WithUnit("{operation}"))
	if err != nil {
		return err
	}
	dropped, _ := meter.Int64ObservableCounter("pulse.telemetry.dropped", metric.WithUnit("{record}"))
	failed, _ := meter.Int64ObservableCounter("pulse.telemetry.export.failed", metric.WithUnit("{batch}"))
	qsize, _ := meter.Int64ObservableGauge("pulse.telemetry.queue.size", metric.WithUnit("{record}"))
	qcap, _ := meter.Int64ObservableGauge("pulse.telemetry.queue.capacity", metric.WithUnit("{record}"))
	uptime, _ := meter.Float64ObservableGauge("pulse.process.uptime", metric.WithUnit("s"))
	heap, _ := meter.Int64ObservableGauge("pulse.process.go.heap", metric.WithUnit("By"))
	goroutines, _ := meter.Int64ObservableGauge("pulse.process.go.goroutines", metric.WithUnit("{goroutine}"))
	m.cardinality, _ = meter.Int64ObservableCounter("pulse.telemetry.cardinality.overflow", metric.WithUnit("{measurement}"))
	_, err = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		for i, signal := range []string{"traces", "logs"} {
			for j, reason := range dropReasons {
				o.ObserveInt64(dropped, m.drops[i][j].Load(), metric.WithAttributes(attribute.String("signal", signal), attribute.String("reason", reason)))
			}
			size := m.traceQueue.Load()
			if i == 1 {
				size = m.logQueue.Load()
			}
			o.ObserveInt64(qsize, size, metric.WithAttributes(attribute.String("signal", signal)))
			capacity := int64(256)
			if i == 1 {
				capacity = m.logCapacity.Load()
			}
			o.ObserveInt64(qcap, capacity, metric.WithAttributes(attribute.String("signal", signal)))
		}
		for i, signal := range []string{"traces", "metrics"} {
			for j, reason := range failureReasons {
				o.ObserveInt64(failed, m.failures[i][j].Load(), metric.WithAttributes(attribute.String("signal", signal), attribute.String("reason", reason)))
			}
		}
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		o.ObserveFloat64(uptime, time.Since(m.start).Seconds())
		o.ObserveInt64(heap, int64(mem.HeapAlloc))
		o.ObserveInt64(goroutines, int64(runtime.NumGoroutine()))
		o.ObserveInt64(m.cardinality, m.overflow.Load())
		return nil
	}, dropped, failed, qsize, qcap, uptime, heap, goroutines, m.cardinality)
	return err
}

type countingMetricExporter struct {
	sdkmetric.Exporter
	metrics *telemetryMetrics
}

func (e *countingMetricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) (err error) {
	defer func() {
		if recover() != nil {
			e.metrics.exportFailure("metrics", "internal")
			err = errTelemetry
		}
	}()
	err = e.Exporter.Export(ctx, rm)
	if err != nil {
		reason := "unavailable"
		if ctx.Err() != nil {
			reason = "timeout"
		}
		e.metrics.exportFailure("metrics", reason)
		return errTelemetry
	}
	return nil
}
