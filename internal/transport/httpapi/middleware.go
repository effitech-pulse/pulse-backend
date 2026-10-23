package httpapi

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/observability"
)

func (s *Server) auditMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		if !strings.HasPrefix(c.Request.URL.Path, s.Config.Prefix+"/") {
			return
		}
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			s.recordAudit(c, c.Writer.Status())
		}
	}
}

func (s *Server) recoverPanic(c *gin.Context, recovered any) {
	slog.ErrorContext(c.Request.Context(), "handler panic", "error_code", "panic")
	if !c.Writer.Written() {
		writeError(c, fmt.Errorf("handler panic"))
	}
	c.Abort()
}

func (s *Server) corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Expose-Headers", "X-Request-ID")
		origin := c.GetHeader("Origin")
		for _, allowed := range s.Config.CORS {
			if origin == allowed {
				c.Header("Access-Control-Allow-Origin", origin)
				c.Header("Vary", "Origin")
				c.Header("Access-Control-Allow-Credentials", "true")
				break
			}
		}
		if c.Request.Method == http.MethodOptions && origin != "" {
			c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type,X-API-Key,X-Request-ID,traceparent,Idempotency-Key")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func (s *Server) authorizeMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.Access.Enabled() {
			s.personalAuthorization(c)
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, s.Config.Prefix+"/") && s.Config.APIKey != "" &&
			subtle.ConstantTimeCompare([]byte(c.GetHeader("X-API-Key")), []byte(s.Config.APIKey)) != 1 {
			writeError(c, domain.NewError(domain.Unauthorized, "Invalid or missing X-API-Key"))
			c.Abort()
			return
		}
		p := domain.TrustedLegacyPrincipal("Локальный сеанс")
		if s.Config.APIKey != "" {
			p.Name = "Общий API-ключ"
		}
		c.Request = c.Request.WithContext(domain.WithPrincipal(c.Request.Context(), p))
		c.Next()
	}
}

// observationMiddleware охватывает auth/recovery и пишет только шаблон маршрута.
func (s *Server) observationMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		route := c.FullPath()
		name := "HTTP unmatched"
		if route != "" {
			name = "HTTP " + route
		}
		ctx, span := observability.Start(observability.ExtractHTTP(c.Request.Context(), c.Request.Header), name, trace.SpanKindServer)
		c.Request = c.Request.WithContext(ctx)
		c.Header(observability.RequestIDHeader, observability.RequestID(ctx))
		c.Next()
		status := c.Writer.Status()
		if status >= 500 {
			span.SetStatus(codes.Error, "internal_error")
		}
		method := c.Request.Method
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD":
		default:
			method = "OTHER"
		}
		span.SetAttributes(attribute.String("http.request.method", method), attribute.String("http.route", route), attribute.Int("http.response.status_code", status))
		observability.Complete(ctx, span, "request completed", start, nil, slog.String("http.request.method", method), slog.String("http.route", route), slog.Int("http.response.status_code", status))
	}
}
