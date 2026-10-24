// Package httpapi адаптирует HTTP к типизированным сценариям приложения.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"

	"github.com/effitech-pulse/pulse-backend/api"
	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

var openAPI = api.Specification

type Server struct {
	Events         *service.Events
	CollectionJobs *service.CollectionJobs
	CohortJobs     *service.CohortJobs
	FileJobs       *service.FileJobs
	Config         config.Config
	Access         *service.Access
	Catalog        *service.Catalog
	ML             *service.ML
	Parameters     *service.Parameters
	Analytics      service.Analytics
	Integrations   *service.Integrations
	Checks         *service.Checks
	Reviews        *service.Reviews
	Presentation   *service.Presentation
}

func (s *Server) wrap(fn handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := fn(c); err != nil {
			writeError(c, err)
			c.Abort()
		}
	}
}

type handler func(*gin.Context) error

func writeJSON(c *gin.Context, status int, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	c.Data(status, "application/json", append(raw, '\n'))
	return nil
}

func respond[T any](c *gin.Context, status int, value T, err error) error {
	if err != nil {
		return err
	}
	return writeJSON(c, status, value)
}

func writeError(c *gin.Context, err error) {
	var e *domain.Error
	if !errors.As(err, &e) {
		slog.ErrorContext(c.Request.Context(), "request failed", "error_code", "internal_error")
		_ = writeJSON(c, 500, struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Detail  any    `json:"detail"`
		}{Code: "internal_error", Message: "Internal server error"})
		return
	}
	status := 500
	switch e.Kind {
	case domain.Invalid:
		status = 422
		if e.Message == "invalid_table_query" || e.Message == "invalid_table_cursor" {
			status = 400
		}
	case domain.Missing:
		status = 404
	case domain.Duplicate:
		status = 409
	case domain.Unauthorized:
		status = 401
	case domain.Forbidden:
		status = 403
	case domain.Limited:
		status = 429
	case domain.Unavailable:
		status = 503
	case "analytics_disabled":
		status = 502
	}
	if e.Plain {
		_ = writeJSON(c, status, struct {
			Detail string `json:"detail"`
		}{e.Message})
	} else {
		_ = writeJSON(c, status, struct {
			Code    domain.Kind `json:"code"`
			Message string      `json:"message"`
			Detail  any         `json:"detail"`
		}{Code: e.Kind, Message: e.Message})
	}
}

func validation(message string) error {
	return domain.NewError(domain.Invalid, message)
}

func readJSON(c *gin.Context, dest any) error {
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4*1024*1024))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return validation("Request validation failed")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return validation("Ожидается один JSON документ")
	}
	if err := binding.JSON.BindBody(raw, dest); err != nil {
		return validation("Request validation failed")
	}
	return nil
}

func id(c *gin.Context, key string) (int64, error) {
	n, err := strconv.ParseInt(c.Param(key), 10, 64)
	if err != nil {
		return 0, validation("Некорректный идентификатор")
	}
	return n, nil
}

func limit(c *gin.Context, defaultValue, max int) (int, error) {
	raw := c.Query("limit")
	if raw == "" {
		return defaultValue, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > max {
		return 0, validation("Некорректный limit")
	}
	return n, nil
}

func queryBool(c *gin.Context, key string, defaultValue bool) (bool, error) {
	raw := c.Query(key)
	if raw == "" {
		return defaultValue, nil
	}
	switch strings.ToLower(raw) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	}
	return false, validation("Некорректный " + key)
}

func (s *Server) health(c *gin.Context) error {
	status := "ok"
	if s.Catalog == nil || s.Catalog.Ping(c.Request.Context()) != nil {
		status = "error"
	}
	return writeJSON(c, 200, struct {
		Status   string `json:"status"`
		Database string `json:"database"`
		Version  string `json:"version"`
	}{"ok", status, "1.0.0"})
}

func (s *Server) recordAudit(c *gin.Context, status int) {
	if s.Catalog == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	actor := c.RemoteIP()
	if p, ok := domain.CurrentPrincipal(c.Request.Context()); ok && p.AccountID != nil {
		actor = "user:" + strconv.FormatInt(*p.AccountID, 10)
	}
	if err := s.Catalog.RecordAudit(ctx, actor, c.Request.Method, c.Request.URL.Path, status); err != nil {
		slog.WarnContext(c.Request.Context(), "audit failed", "error_code", "operation_failed")
	}
}
