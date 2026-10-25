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
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestAuthorizationRouteCompleteness(t *testing.T) {
	s := &Server{Config: config.Config{Prefix: "/api/v1"}}
	routes := s.routes().Routes()
	seen := map[string]bool{}
	known := map[domain.Permission]bool{}
	for _, d := range domain.PermissionCatalog() {
		known[d.Key] = true
	}
	public := map[string]bool{"GET /health": true, "GET /openapi.json": true, "POST /auth/login": true, "GET /auth/me": true}
	scopes := map[string]bool{"PUBLIC": true, "PUBLIC_LOGIN": true, "ANONYMOUS_OR_SELF": true, "SELF": true, "CAPABILITY": true, "CHANGE": true, "PROFILE_RUNS": true, "PROFILE_WELLS": true, "LJ": true, "CJ": true, "CJ+FULL100": true, "PRIVATE": true, "W": true, "WT": true, "FULL": true, "GLOBAL": true, "M": true, "G": true, "PROJ": true}
	for _, p := range authorizationPolicies {
		if !scopes[p.ScopeRule] {
			t.Errorf("unknown scope policy %s", p.ScopeRule)
		}
		key := p.Method + " " + p.Path
		if seen[key] {
			t.Errorf("duplicate policy %s", key)
		}
		seen[key] = true
		if p.Public != public[key] {
			t.Errorf("incorrect public declaration %s", key)
		}
		for _, k := range p.All {
			if !known[k] {
				t.Errorf("unknown permission %s", k)
			}
		}
		for _, branch := range p.Any {
			for _, k := range branch {
				if !known[k] {
					t.Errorf("unknown permission %s", k)
				}
			}
		}
		if !p.Public && len(p.All) == 0 && len(p.Any) == 0 && p.ScopeRule != "SELF" && p.ScopeRule != "CAPABILITY" && p.ScopeRule != "CHANGE" && p.ScopeRule != "PROFILE_RUNS" {
			t.Errorf("empty policy %s", key)
		}
	}
	for _, r := range routes {
		path := r.Path
		if len(path) >= len(s.Config.Prefix) && path[:len(s.Config.Prefix)] == s.Config.Prefix {
			path = path[len(s.Config.Prefix):]
		}
		key := r.Method + " " + path
		if !seen[key] {
			t.Errorf("missing policy %s", key)
		}
		delete(seen, key)
	}
	for key := range seen {
		t.Errorf("stale policy %s", key)
	}
}

type routeSessionStore struct {
	service.AccessStore
	view domain.AccountAccessView
}

func (s *routeSessionStore) SessionAccountAccessView(context.Context, string, bool) (domain.AccountAccessView, error) {
	return s.view, nil
}
func (s *routeSessionStore) AccountAccessView(context.Context, int64, int64) (domain.AccountAccessView, error) {
	return s.view, nil
}
func (s *routeSessionStore) ListManagedAccountViews(context.Context, int64, domain.AccessPageQuery) (domain.AccountAccessViewPage, error) {
	return domain.AccountAccessViewPage{}, nil
}
func TestRBACRoutePermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &routeSessionStore{view: domain.AccountAccessView{Account: domain.Account{ID: 7, Role: "admin", Active: true}, Authorization: domain.AuthorizationSnapshot{AccountID: 7, Active: true, Epoch: 1, GraphRevision: 1, Permissions: []domain.Permission{}}}}
	access, e := service.NewAccess(store, true)
	if e != nil {
		t.Fatal(e)
	}
	access.WithRBACMode(domain.RBACEnforce)
	s := &Server{Config: config.Config{Prefix: "/custom"}, Access: access}
	router := s.routes()
	called := false
	router.GET("/custom/forgotten", func(c *gin.Context) { called = true; c.Status(200) })
	request := func(method, path string, cookie bool) int {
		r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		if cookie {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "valid"})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w.Code
	}
	if code := request("GET", "/custom/forgotten", true); code != 403 || called {
		t.Fatalf("unclassified admin-labelled route: %d called=%v", code, called)
	}
	if code := request("GET", "/custom/wells", false); code != 401 {
		t.Fatalf("anonymous %d", code)
	}
	if code := request("HEAD", "/custom/wells", true); code != 405 {
		t.Fatalf("HEAD %d", code)
	}
	for _, policy := range authorizationPolicies {
		if policy.Public || len(policy.All) == 0 && len(policy.Any) == 0 {
			continue
		}
		t.Run(policy.Method+policy.Path, func(t *testing.T) {
			missing := map[domain.Permission]bool{}
			for _, k := range policy.All {
				missing[k] = true
			}
			for _, branch := range policy.Any {
				for _, k := range branch {
					missing[k] = true
				}
			}
			for key := range missing {
				keys := []domain.Permission{}
				for _, d := range domain.PermissionCatalog() {
					if d.Key != key {
						keys = append(keys, d.Key)
					}
				}
				// OR policies are denied only when all allowed alternatives are absent.
				if len(policy.Any) > 0 {
					keys = nil
				}
				store.view.Authorization.Permissions = keys
				path := policy.Path
				for _, param := range []string{"well_id", "check_id", "job_id", "alert_id", "user_id", "role_id", "group_id", "incident_id", "ref_id", "dashboard_id", "source_id", "run_id", "dag_id", "unitek_id"} {
					path = strings.ReplaceAll(path, ":"+param, "1")
				}
				if code := request(policy.Method, "/custom"+path, true); code != 403 {
					t.Fatalf("missing %s returned %d", key, code)
				}
			}
		})
	}
}

func TestRBACDecimalWire(t *testing.T) {
	var n decimal
	for _, raw := range []string{`9007199254740993`, `"01"`, `"+1"`, `"9223372036854775808"`, `null`} {
		if e := json.Unmarshal([]byte(raw), &n); e == nil {
			t.Errorf("invalid decimal accepted %s", raw)
		}
	}
	for _, raw := range []string{`"9007199254740993"`, `"0"`, `"-1"`} {
		if e := json.Unmarshal([]byte(raw), &n); e != nil {
			t.Fatal(raw, e)
		}
		b, e := json.Marshal(n)
		if e != nil || string(b) != raw {
			t.Fatal(raw, string(b), e)
		}
	}
}

func TestRBACAccountDecodeStatusCompatibility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{Config: config.Config{Prefix: "/api/v1"}}
	h := s.routes()
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/api/v1/auth/users", `{`, 422},
		{"PUT", "/api/v1/auth/users/1", `{`, 422},
		{"POST", "/api/v1/auth/users", `[]`, 422},
		{"POST", "/api/v1/auth/users", `{"role_ids":[]}`, 400},
		{"PUT", "/api/v1/auth/users/1", `{"access_version":1,"role":"viewer","change":{"user":{}}}`, 400},
		{"POST", "/api/v1/auth/users", `{"access_version":1,"graph_revision":1,"change":{"user":{}}}`, 400},
	} {
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Errorf("%s %s %s returned %d want %d: %s", tc.method, tc.path, tc.body, w.Code, tc.want, w.Body.String())
		}
	}
}

func TestEquipmentPassportRoutePolicies(t *testing.T) {
	s := &Server{Config: config.Config{Prefix: "/api/v1"}}
	count := 0
	for _, r := range s.routes().Routes() {
		if !strings.Contains(r.Path, "equipment-passport") {
			continue
		}
		count++
		p, ok := authorizationPolicy(r.Method, strings.TrimPrefix(r.Path, "/api/v1"))
		if !ok || p.Public || len(p.All) == 0 {
			t.Fatal("missing private operation policy", r)
		}
	}
	if count != 10 {
		t.Fatal("missing equipment routes", count)
	}
	a := rbacRoutePassportSnapshot()
	if a.Require("equipment_passports.import") == nil {
		t.Fatal("recursive prerequisites implicitly granted")
	}
}
func rbacRoutePassportSnapshot() domain.AuthorizationSnapshot {
	return domain.AuthorizationSnapshot{Active: true, Permissions: []domain.Permission{"equipment_passports.import"}}
}
func TestNotificationCompatibilityUsesEffectivePermissions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &routeSessionStore{view: domain.AccountAccessView{Account: domain.Account{ID: 7, Role: "viewer", Active: true}, Authorization: domain.AuthorizationSnapshot{AccountID: 7, Active: true, Epoch: 1, Permissions: []domain.Permission{"wells.read", "risks.read", "alerts.read"}}}}
	access, e := service.NewAccess(store, true)
	if e != nil {
		t.Fatal(e)
	}
	access.WithRBACMode(domain.RBACCompat)
	facts := &notificationHTTPStore{families: []domain.NotificationFamily{domain.NotificationFamilyAlerts}}
	svc, e := service.NewNotifications(facts, nil, service.NotificationsOptions{AuthMode: "local", Mode: "research", Prefix: "/api/v1", UIEnabled: true})
	if e != nil {
		t.Fatal(e)
	}
	s := &Server{Config: config.Config{Prefix: "/api/v1"}, Access: access, Notifications: svc}
	router := s.routes()
	request := func(method, path string, cookie bool) int {
		r := httptest.NewRequest(method, path, nil)
		if cookie {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "valid"})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("private cache header")
		}
		return w.Code
	}
	if got := request("GET", "/api/v1/notifications", true); got != 200 {
		t.Fatalf("non-admin effective rights: %d", got)
	}
	store.view.Account.Role = "admin"
	store.view.Authorization.Permissions = []domain.Permission{}
	facts.families = nil
	if got := request("GET", "/api/v1/notifications", true); got != 403 {
		t.Fatalf("admin bypass: %d", got)
	}
	if got := request("GET", "/api/v1/notifications/capabilities", true); got != 200 {
		t.Fatalf("no family capability: %d", got)
	}
	facts.err = domain.NewError(domain.Missing, "hidden")
	if got := request("PUT", "/api/v1/notifications/nf1_1/read", true); got != 404 {
		t.Fatalf("hidden mark: %d", got)
	}
	if got := request("GET", "/api/v1/notifications/capabilities", false); got != 401 {
		t.Fatalf("anonymous: %d", got)
	}
}
