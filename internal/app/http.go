package app

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// APIError сохраняет HTTP-коды и формат ошибок исходного API.
type APIError struct {
	Status        int
	Code, Message string
	Plain         bool
}

func (e *APIError) Error() string { return e.Message }
func validation(s string) error   { return &APIError{422, "validation_error", s, false} }
func notFound(s string) error     { return &APIError{404, "not_found", s, false} }
func conflict(s string) error     { return &APIError{409, "conflict", s, false} }
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, err error) {
	err = dbError(err)
	var api *APIError
	if !errors.As(err, &api) {
		slog.Error("request failed", "error", err)
		api = &APIError{500, "internal_error", "Internal server error", false}
	}
	if api.Plain {
		writeJSON(w, api.Status, map[string]any{"detail": api.Message})
	} else {
		writeJSON(w, api.Status, map[string]any{"code": api.Code, "message": api.Message, "detail": nil})
	}
}
func readJSON(r *http.Request, dest any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 4*1024*1024))
	dec.UseNumber()
	if err := dec.Decode(dest); err != nil {
		return validation("Request validation failed")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return validation("Ожидается один JSON документ")
	}
	return nil
}
func id(r *http.Request, key string) (int64, error) {
	v, err := strconv.ParseInt(r.PathValue(key), 10, 64)
	if err != nil {
		return 0, validation("Некорректный идентификатор")
	}
	return v, nil
}
func intValue(v any) (int64, error) {
	switch x := v.(type) {
	case json.Number:
		if n, e := x.Int64(); e == nil {
			return n, nil
		}
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	case string:
		n, e := strconv.ParseInt(x, 10, 64)
		if e == nil {
			return n, nil
		}
	}
	return 0, validation("Ожидается целое число")
}
func limit(r *http.Request, defaultValue, max int) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultValue, nil
	}
	n, e := strconv.Atoi(raw)
	if e != nil || n < 1 || n > max {
		return 0, validation("Некорректный limit")
	}
	return n, nil
}
func queryBool(r *http.Request, key string, defaultValue bool) (bool, error) {
	raw := r.URL.Query().Get(key)
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

type handler func(http.ResponseWriter, *http.Request) error

// App объединяет HTTP, PostgreSQL и gRPC-клиент без ML-вычислений.
type App struct {
	Config  Config
	Pool    *pgxpool.Pool
	ML      MLAPI
	mu      sync.Mutex
	closing bool
	jobs    sync.WaitGroup
}

func New(config Config, pool *pgxpool.Pool, ml MLAPI) *App {
	return &App{Config: config, Pool: pool, ML: ml}
}
func (a *App) wrap(fn handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			writeError(w, err)
		}
	}
}
func (a *App) routes() *http.ServeMux {
	mux := http.NewServeMux()
	p := a.Config.Prefix
	mux.HandleFunc("GET /health", a.wrap(a.health))
	for _, route := range []struct {
		method, path string
		fn           handler
	}{
		{"GET", "/wells", a.listWells}, {"POST", "/wells", a.createWell}, {"POST", "/wells/bulk", a.bulkWells}, {"POST", "/wells/import", a.importWells}, {"GET", "/wells/export", a.exportWells},
		{"GET", "/wells/{well_id}", a.getWell}, {"PATCH", "/wells/{well_id}", a.patchWell}, {"DELETE", "/wells/{well_id}", a.deleteWell},
		{"GET", "/wells/{well_id}/predictions", a.predictions}, {"GET", "/wells/{well_id}/predictions/export", a.exportPredictions}, {"GET", "/wells/{well_id}/complications", a.complications}, {"GET", "/wells/{well_id}/classification", a.classification},
		{"GET", "/wells/{well_id}/incidents", a.incidents}, {"POST", "/wells/{well_id}/incidents", a.createIncident}, {"DELETE", "/incidents/{incident_id}", a.deleteIncident}, {"GET", "/incidents/stats", a.incidentStats},
		{"GET", "/ngdu", a.listReference}, {"POST", "/ngdu", a.createReference}, {"PUT", "/ngdu/{ref_id}", a.updateReference}, {"DELETE", "/ngdu/{ref_id}", a.deleteReference},
		{"GET", "/departments", a.listReference}, {"POST", "/departments", a.createReference}, {"PUT", "/departments/{ref_id}", a.updateReference}, {"DELETE", "/departments/{ref_id}", a.deleteReference},
		{"GET", "/runs", a.runs}, {"GET", "/runs/export", a.exportRuns}, {"GET", "/runs/{dag_id}/{run_id}", a.runEvents},
		{"GET", "/audit", a.audit}, {"GET", "/parameters", a.parameters}, {"GET", "/analytics/status", a.analyticsStatus}, {"GET", "/analytics/dashboards/{dashboard_id}/embed", a.analyticsEmbed},
		{"GET", "/ml/wells", a.mlWells}, {"POST", "/ml/wells/refresh", a.mlRefresh}, {"GET", "/ml/runs", a.mlRuns}, {"PATCH", "/ml/alerts/{alert_id}", a.mlAlertStatus},
		{"GET", "/ml/wells/{segment}/{action}", a.mlWellAction}, {"GET", "/ml/health", a.mlHealth}, {"GET", "/ml/metrics", a.mlMetrics}, {"POST", "/ml/upload", a.mlUpload},
	} {
		mux.HandleFunc(route.method+" "+p+route.path, a.wrap(route.fn))
	}
	mux.HandleFunc("GET /openapi.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(openAPI)
	})
	return mux
}
func (a *App) Handler() http.Handler { return a.middleware(a.routes()) }

//go:embed openapi.json
var openAPI []byte

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (a *App) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			if err := recover(); err != nil {
				slog.Error("handler panic", "error", err)
				if sw.status == 0 {
					writeError(sw, fmt.Errorf("panic"))
				}
			}
			if strings.HasPrefix(r.URL.Path, a.Config.Prefix+"/") && strings.Contains(" POST PUT PATCH DELETE ", " "+r.Method+" ") {
				a.recordAudit(r, sw.status)
			}
		}()
		origin := r.Header.Get("Origin")
		for _, allowed := range a.Config.CORS {
			if origin == allowed {
				sw.Header().Set("Access-Control-Allow-Origin", origin)
				sw.Header().Set("Vary", "Origin")
				sw.Header().Set("Access-Control-Allow-Credentials", "true")
			}
		}
		if r.Method == "OPTIONS" && origin != "" {
			sw.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			sw.Header().Set("Access-Control-Allow-Headers", "Content-Type,X-API-Key")
			sw.WriteHeader(204)
			return
		}
		if strings.HasPrefix(r.URL.Path, a.Config.Prefix+"/") && a.Config.APIKey != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-API-Key")), []byte(a.Config.APIKey)) != 1 {
			writeError(sw, &APIError{401, "unauthorized", "Invalid or missing X-API-Key", false})
			return
		}
		next.ServeHTTP(sw, r)
	})
}
func (a *App) health(w http.ResponseWriter, r *http.Request) error {
	status := "ok"
	if a.Pool == nil || a.Pool.Ping(r.Context()) != nil {
		status = "error"
	}
	writeJSON(w, 200, map[string]any{"status": "ok", "database": status, "version": "1.0.0"})
	return nil
}
func (a *App) recordAudit(r *http.Request, status int) {
	if a.Pool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	actor, _, _ := net.SplitHostPort(r.RemoteAddr)
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		slog.Warn("audit failed", "error", err)
		return
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, "INSERT INTO action_log(actor,method,path,status_code) VALUES($1,$2,$3,$4)", actor, r.Method, r.URL.Path, status)
	if err == nil {
		_, err = tx.Exec(ctx, "DELETE FROM action_log WHERE ts < now()-interval '7 days'")
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		slog.Warn("audit failed", "error", err)
	}
}
