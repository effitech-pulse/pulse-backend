package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

type auditEntry struct {
	actor  string
	path   string
	status int
}

type recordingStore struct {
	service.Store
	entries []auditEntry
}

func (s *recordingStore) RecordAudit(ctx context.Context, actor, method, path string, status int) error {
	s.entries = append(s.entries, auditEntry{actor: actor, path: path, status: status})
	return nil
}

func TestGinMiddlewareRecordsFinalStatusAndRecoversPanic(t *testing.T) {
	store := &recordingStore{}
	server := &Server{
		Config:  config.Config{Prefix: "/api/v1", APIKey: "test-key", CORS: []string{"https://ui.example"}},
		Catalog: service.NewCatalog(store),
	}
	router := server.routes()
	router.POST("/api/v1/test-panic", server.wrap(func(c *gin.Context) error {
		panic("internal database details")
	}))

	preflight := httptest.NewRequest(http.MethodOptions, "/api/v1/wells", nil)
	preflight.Header.Set("Origin", "https://ui.example")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, preflight)
	if response.Code != 204 || response.Header().Get("Access-Control-Allow-Origin") != "https://ui.example" {
		t.Fatalf("preflight: %d %v", response.Code, response.Header())
	}
	if len(store.entries) != 0 {
		t.Fatal("preflight was audited as a mutation")
	}

	for _, authenticated := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/test-panic", nil)
		request.RemoteAddr = "192.0.2.7:1234"
		request.Header.Set("X-Forwarded-For", "198.51.100.1")
		if authenticated {
			request.Header.Set("X-API-Key", "test-key")
		}
		response = httptest.NewRecorder()
		router.ServeHTTP(response, request)
		expected := http.StatusUnauthorized
		if authenticated {
			expected = http.StatusInternalServerError
		}
		if response.Code != expected || !json.Valid(response.Body.Bytes()) {
			t.Fatalf("mutation: %d %s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "internal database details") {
			t.Fatal("panic details escaped into HTTP response")
		}
		entry := store.entries[len(store.entries)-1]
		if entry.status != expected || entry.actor != "192.0.2.7" {
			t.Fatalf("audit: %+v", entry)
		}
	}
	if len(store.entries) != 2 {
		t.Fatalf("audit count: %d", len(store.entries))
	}
}

func TestGinKeepsValidationStatusAndPublicOpenAPI(t *testing.T) {
	server := &Server{Config: config.Config{Prefix: "/api/v1"}}
	router := server.routes()
	cases := []struct {
		method string
		path   string
		body   string
	}{
		{"GET", "/api/v1/wells/not-an-id", ""},
		{"GET", "/api/v1/runs?well_id=not-an-id", ""},
		{"GET", "/api/v1/ml/wells/not-an-id/explain", ""},
		{"GET", "/api/v1/ml/wells/by-unitek/not-an-id", ""},
		{"POST", "/api/v1/wells", `{"unitek_id":1} {"unitek_id":2}`},
		{"POST", "/api/v1/wells", `{"unitek_id":"bad"}`},
		{"POST", "/api/v1/wells", "{"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path+" "+tc.body, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != 422 || !json.Valid(response.Body.Bytes()) {
				t.Fatalf("validation: %d %s", response.Code, response.Body.String())
			}
		})
	}
	request := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 200 || response.Body.String() != string(openAPI) {
		t.Fatal("OpenAPI response changed")
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/health", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unsupported method: %d", response.Code)
	}
}
