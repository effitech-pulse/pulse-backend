// Package observability задаёт безопасные границы корреляции и диагностики O1.
package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"go.opentelemetry.io/otel/propagation"
)

const RequestIDHeader = "X-Request-ID"

type requestIDKey struct{}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

func RequestID(ctx context.Context) string { v, _ := ctx.Value(requestIDKey{}).(string); return v }
func WithRequestID(ctx context.Context, id string) context.Context {
	if !requestIDPattern.MatchString(id) {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			panic("secure random unavailable")
		}
		id = hex.EncodeToString(b)
	}
	return context.WithValue(ctx, requestIDKey{}, id)
}
func Ensure(ctx context.Context) context.Context {
	if RequestID(ctx) == "" {
		return WithRequestID(ctx, "")
	}
	return ctx
}

// ExtractHTTP игнорирует baggage/tracestate и неоднозначные заголовки.
func ExtractHTTP(ctx context.Context, h http.Header) context.Context {
	id := ""
	if values := h.Values(RequestIDHeader); len(values) == 1 {
		id = values[0]
	}
	ctx = WithRequestID(ctx, id)
	values := h.Values("traceparent")
	if len(values) != 1 || len(values[0]) > 512 || strings.IndexFunc(values[0], unicode.IsControl) >= 0 {
		return ctx
	}
	return propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": values[0]})
}
func InjectHTTP(ctx context.Context, h http.Header) {
	h.Del("baggage")
	h.Del("tracestate")
	h.Del("traceparent")
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(h))
	h.Del("tracestate")
	h.Set(RequestIDHeader, RequestID(ctx))
}
