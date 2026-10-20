package observability

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

var providerInitMu sync.Mutex

// NewProvider не экспортирует/не удерживает завершённые spans; sampler локальный.
// Вызывать до запуска goroutines: SDK 1.38 всегда подмешивает Environment.
// Mutex сериализует наши конструкторы, но не чужие чтения process-global env.
func NewProvider(version string) (provider *sdktrace.TracerProvider, err error) {
	err = withIsolatedEnvironment(func() error { provider = newProvider(version); return nil })
	return
}

// withIsolatedEnvironment is startup-only; it serializes our constructors, not foreign env readers.
func withIsolatedEnvironment(build func() error) (err error) {
	providerInitMu.Lock()
	defer providerInitMu.Unlock()
	type envValue struct {
		key, value string
		present    bool
	}
	var saved []envValue
	defer func() {
		for _, v := range saved {
			var restoreErr error
			if v.present {
				restoreErr = os.Setenv(v.key, v.value)
			} else {
				restoreErr = os.Unsetenv(v.key)
			}
			if restoreErr != nil {
				err = errors.New("observability environment restoration failed")
			}
		}
	}()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "OTEL_RESOURCE_ATTRIBUTES" && key != "OTEL_SERVICE_NAME" && !strings.HasPrefix(key, "OTEL_EXPORTER_OTLP_") && !strings.HasPrefix(key, "OTEL_PYTHON_EXPORTER_OTLP_GRPC_") {
			continue
		}
		value, present := os.LookupEnv(key)
		saved = append(saved, envValue{key, value, present})
		if os.Unsetenv(key) != nil {
			return errors.New("observability environment isolation failed")
		}
	}
	return build()
}
func newProvider(version string) *sdktrace.TracerProvider {
	limits := sdktrace.NewSpanLimits()
	limits.AttributeCountLimit = 32
	limits.EventCountLimit = 16
	limits.LinkCountLimit = 8
	limits.AttributeValueLengthLimit = 256
	return sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()), sdktrace.WithSpanLimits(limits), sdktrace.WithResource(localResource(version)))
}
func localResource(version string) *resource.Resource {
	return resource.NewSchemaless(attribute.String("service.name", "pulse-backend"), attribute.String("service.version", bounded(version, 128)), attribute.String("service.instance.id", instanceID), attribute.String("deployment.environment.name", "local"))
}
func Start(ctx context.Context, name string, kind trace.SpanKind) (context.Context, trace.Span) {
	return otel.Tracer("pulse-backend").Start(Ensure(ctx), name, trace.WithSpanKind(kind))
}

// Detached сохраняет значения/права, но создаёт отдельный корень со ссылкой на инициатора.
func Detached(ctx context.Context, name string) (context.Context, trace.Span) {
	parent := trace.SpanContextFromContext(ctx)
	opts := []trace.SpanStartOption{trace.WithNewRoot()}
	if parent.IsValid() {
		opts = append(opts, trace.WithLinks(trace.Link{SpanContext: parent}))
	}
	return otel.Tracer("pulse-backend").Start(Ensure(context.WithoutCancel(ctx)), name, opts...)
}

// Complete не записывает error.Error() и panic/SQL/содержимое запросов.
func Complete(ctx context.Context, span trace.Span, event string, start time.Time, err error, attrs ...slog.Attr) {
	if err != nil {
		span.SetStatus(codes.Error, "operation_failed")
		attrs = append(attrs, slog.String("error_code", "operation_failed"))
	}
	attrs = append(attrs, slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000))
	slog.LogAttrs(ctx, slog.LevelInfo, event, attrs...)
	span.End()
}

// BackgroundSeed переносит в goroutine только безопасную корреляцию, а не HTTP/Gin.
func BackgroundSeed(ctx context.Context) context.Context {
	return trace.ContextWithSpanContext(WithRequestID(context.Background(), RequestID(ctx)), trace.SpanContextFromContext(ctx))
}
func Database(ctx context.Context, name string) (context.Context, func(error)) {
	start := time.Now()
	ctx, span := Start(ctx, name, trace.SpanKindClient)
	span.SetAttributes(attribute.String("db.system.name", "postgresql"))
	return ctx, func(err error) { Complete(ctx, span, "database completed", start, err) }
}
func Operation(ctx context.Context, name string) (context.Context, func(error)) {
	start := time.Now()
	ctx, span := Start(ctx, name, trace.SpanKindInternal)
	return ctx, func(err error) { Complete(ctx, span, "operation completed", start, err) }
}
