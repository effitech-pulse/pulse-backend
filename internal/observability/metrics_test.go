package observability

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	metricapi "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	apitrace "go.opentelemetry.io/otel/trace"
)

func TestMetricsRemainCumulativeAndNoPrivateDimensions(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer provider.Shutdown(context.Background())
	m := newTelemetryMetrics()
	if err := m.bind(provider); err != nil {
		t.Fatal(err)
	}
	span := tracetest.SpanStub{Name: "HTTP /PRIVATE_123?token=PRIVATE", SpanContext: sampleContext(), StartTime: time.Now(), EndTime: time.Now().Add(time.Second), Attributes: []attribute.KeyValue{attribute.Int("http.response.status_code", 401), attribute.String("private", "PRIVATE")}}.Snapshot()
	m.operation(span)
	m.drop("traces", "queue_full", 3)
	m.exportFailure("metrics", "timeout")
	for range 2 {
		var data metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &data); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, scope := range data.ScopeMetrics {
			for _, metric := range scope.Metrics {
				switch d := metric.Data.(type) {
				case metricdata.Sum[int64]:
					if metric.Name == "pulse.operation.completed" {
						if d.Temporality != metricdata.CumulativeTemporality || len(d.DataPoints) != 1 || d.DataPoints[0].Value != 1 {
							t.Fatalf("not cumulative %+v", d)
						}
						attrs := d.DataPoints[0].Attributes
						op, _ := attrs.Value("operation")
						outcome, _ := attrs.Value("outcome")
						if op.AsString() != "other" || outcome.AsString() != "error" {
							t.Fatal(attrs)
						}
						found = true
					}
					if strings.Contains(fmt.Sprint(d), "PRIVATE") {
						t.Fatal("private metric")
					}
				}
			}
		}
		if !found {
			t.Fatal("no completion")
		}
	}
}
func TestCardinalityOverflowPreservesFallbackDimensions(t *testing.T) {
	m := newTelemetryMetrics()
	for i := range 118 {
		m.dimensions("http", fmt.Sprint(i), "ok")
	}
	attrs := m.dimensions("db", "check.save", "error")
	set := attribute.NewSet(attrs...)
	op, _ := set.Value("operation")
	layer, _ := set.Value("layer")
	outcome, _ := set.Value("outcome")
	if op.AsString() != "other" || layer.AsString() != "db" || outcome.AsString() != "error" || m.overflow.Load() != 2 {
		t.Fatal(set, m.overflow.Load())
	}
	m.dimensions("db", "other", "error")
	if m.overflow.Load() != 2 {
		t.Fatal("overflow recursively guarded")
	}
}

type privateMetricError struct{}

func (privateMetricError) Error() string { panic("PRIVATE_ERROR_WAS_FORMATTED") }

type failingMetricExporter struct {
	sdkmetric.Exporter
	panics bool
}

func (e failingMetricExporter) Export(context.Context, *metricdata.ResourceMetrics) error {
	if e.panics {
		panic("PRIVATE")
	}
	return privateMetricError{}
}
func TestMetricExportFailuresAreStaticCountedAndDoNotReenterSpans(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(fmt.Sprint(panics), func(t *testing.T) {
			traceExporter := tracetest.NewInMemoryExporter()
			traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(traceExporter))
			old := otel.GetTracerProvider()
			otel.SetTracerProvider(traceProvider)
			defer otel.SetTracerProvider(old)
			defer traceProvider.Shutdown(context.Background())
			m := newTelemetryMetrics()
			e := &countingMetricExporter{Exporter: failingMetricExporter{panics: panics}, metrics: m}
			if err := e.Export(context.Background(), &metricdata.ResourceMetrics{}); err != errTelemetry {
				t.Fatal("private error escaped", err)
			}
			if s := m.snapshot(); s.MetricExportFailedTotal != 1 {
				t.Fatal("failure counted incorrectly", s)
			}
			if len(traceExporter.GetSpans()) != 0 {
				t.Fatal("metric exporter generated spans")
			}
		})
	}
}
func TestCompletionMetricsSurviveTraceQueueLoss(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer provider.Shutdown(context.Background())
	m := newTelemetryMetrics()
	if err := m.bind(provider); err != nil {
		t.Fatal(err)
	}
	e := &barrierExporter{entered: make(chan struct{}), release: make(chan struct{})}
	p := newCountingProcessor(e, m)
	for range 64 {
		p.OnEnd(sampleSpan())
	}
	<-e.entered
	for range 257 {
		p.OnEnd(sampleSpan())
	}
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	var completions int64
	for _, scope := range data.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name == "pulse.operation.completed" {
				for _, point := range metric.Data.(metricdata.Sum[int64]).DataPoints {
					completions += point.Value
				}
			}
		}
	}
	if completions != 321 || m.snapshot().TraceDroppedTotal != 1 {
		t.Fatal("trace loss removed completion metrics", completions, m.snapshot())
	}
	close(e.release)
	_ = p.Shutdown(context.Background())
}

func TestMetricOperationsUseActualStaticAdapterAndGinNames(t *testing.T) {
	for _, tc := range []struct{ name, layer string }{{"Data /v1/bounds", "source"}, {"Data /v1/telemetry", "source"}, {"Data /v1/scenario", "source"}, {"MLService/GetSnapshot", "rpc"}, {"MLService/Upload", "rpc"}, {"HTTP /api/v1/wells/:well_id", "http"}, {"HTTP /api/v1/wells/:well_id/checks", "http"}, {"HTTP /api/v1/ml/upload", "http"}} {
		span := tracetest.SpanStub{Name: tc.name}.Snapshot()
		layer, op, _ := metricDimensions(span)
		if op != tc.name || layer != tc.layer {
			t.Fatal("valid static operation collapsed", tc, layer, op)
		}
	}
	span := tracetest.SpanStub{Name: "MLService/UploadPinned"}.Snapshot()
	layer, op, _ := metricDimensions(span)
	if layer != "rpc" || op != "MLService/Upload" || span.Name() != "MLService/UploadPinned" {
		t.Fatal("pinned metric alias changed operation or span identity", layer, op, span.Name())
	}
	if len(operationLayers) > 63 {
		t.Fatal("named operation allowlist leaves no slot for other")
	}
}

func TestMetricSDKRejectsAmbientExemplarsAndCardinality(t *testing.T) {
	t.Setenv("OTEL_METRICS_EXEMPLAR_FILTER", "always_on")
	t.Setenv("OTEL_GO_X_CARDINALITY_LIMIT", "1")
	reader := sdkmetric.NewManualReader()
	provider := newMeterProvider("test", reader)
	defer provider.Shutdown(context.Background())
	m := newTelemetryMetrics()
	if err := m.bind(provider); err != nil {
		t.Fatal(err)
	}
	ctx := apitrace.ContextWithSpanContext(context.Background(), sampleContext())
	for _, op := range []string{"check.execute", "ml.run"} {
		attrs := m.dimensions("operation", op, "ok")
		m.duration.Record(ctx, 1, metricapi.WithAttributes(attrs...))
		m.completed.Add(ctx, 1, metricapi.WithAttributes(attrs...))
	}
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	for _, scope := range data.ScopeMetrics {
		for _, metric := range scope.Metrics {
			switch d := metric.Data.(type) {
			case metricdata.Histogram[float64]:
				if metric.Name == "pulse.operation.duration" && len(d.DataPoints) != 2 {
					t.Fatal("ambient cardinality changed histogram", d)
				}
				for _, point := range d.DataPoints {
					if len(point.Exemplars) != 0 {
						t.Fatal("metric has trace/span exemplars", point.Exemplars)
					}
				}
			case metricdata.Sum[int64]:
				for _, point := range d.DataPoints {
					if len(point.Exemplars) != 0 {
						t.Fatal("metric has trace/span exemplars", point.Exemplars)
					}
				}
			}
		}
	}
}
