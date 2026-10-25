package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
	"github.com/gin-gonic/gin"
)

func TestTableQueryParamsRejectLegacyMixAndBounds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, suffix := range []string{"&before=abc", "&limit=25", "&page_size=0", "&page_size=101", "&table_query=x"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/?table_query="+url.QueryEscape(`{"version":1,"filters":[],"sort":[]}`)+suffix, nil)
		if _, _, _, err := tableQueryParams(c); err == nil {
			t.Errorf("accepted %s", suffix)
		}
	}
}

type tableRouteStore struct{ service.Store }

func (s tableRouteStore) Audit(context.Context, int) ([]domain.AuditEntry, error) {
	return []domain.AuditEntry{{ID: 7, Ts: "2026-10-24T10:00:00", Method: "GET", Path: "/fixture", StatusCode: 200}}, nil
}
func (s tableRouteStore) AuditTable(context.Context, domain.TableRequest) (domain.TableRows[domain.AuditEntry], error) {
	return domain.TableRows[domain.AuditEntry]{Items: []domain.AuditEntry{{ID: 7, Ts: "2026-10-24T10:00:00", Method: "GET", Path: "/fixture", StatusCode: 200}}}, nil
}
func TestTableRoutePreservesLegacyAndOptInEnvelope(t *testing.T) {
	server := &Server{Config: config.Config{Prefix: "/api/v1"}, Catalog: service.NewCatalog(tableRouteStore{})}
	h := server.Handler()
	for _, query := range []bool{false, true} {
		path := "/api/v1/audit"
		if query {
			path += "?table_query=" + url.QueryEscape(`{"version":1,"filters":[],"sort":[]}`)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 200 {
			t.Fatalf("%d %s", response.Code, response.Body.String())
		}
		var items []domain.AuditEntry
		if query {
			var page domain.TablePage[domain.AuditEntry]
			if json.Unmarshal(response.Body.Bytes(), &page) != nil || page.NextCursor != nil {
				t.Fatal(response.Body.String())
			}
			items = page.Items
		} else if json.Unmarshal(response.Body.Bytes(), &items) != nil {
			t.Fatal("legacy array changed", response.Body.String())
		}
		if len(items) != 1 || items[0].ID != 7 || items[0].Actor != nil {
			t.Fatal(items)
		}
	}
}

func TestTableRouteInvalidQueryHas400MachineReason(t *testing.T) {
	server := &Server{Config: config.Config{Prefix: "/api/v1"}, Catalog: service.NewCatalog(tableRouteStore{})}
	for _, suffix := range []string{"&limit=25", "&cursor=forged"} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/audit?table_query="+url.QueryEscape(`{"version":1,"filters":[],"sort":[]}`)+suffix, nil))
		if response.Code != 400 || !strings.Contains(response.Body.String(), "invalid_table_") {
			t.Fatalf("%d %s", response.Code, response.Body.String())
		}
	}
}

func TestTableMalformedPercentQueryDoesNotFallBackToLegacy(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	server := &Server{Config: config.Config{Prefix: "/api/v1"}, Catalog: service.NewCatalog(tableRouteStore{})}
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/audit?table_query=%zz", nil))
	if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_table_query") {
		t.Fatalf("bad query returned legacy body: %d %s", w.Code, w.Body.String())
	}
}
