package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func authRequest(t *testing.T, h http.Handler, method, path, body string, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, want, w.Body.String())
	}
	return w
}
func accountInput(login, role string) domain.AccountInput {
	return domain.AccountInput{Login: login, Name: login, Role: role, Active: ptr(true), Scope: domain.AccessScope{Funds: []string{"operational"}}, Password: ptr("long-local-test-password")}
}
func loginCookie(t *testing.T, h http.Handler, login string) *http.Cookie {
	t.Helper()
	w := authRequest(t, h, "POST", "/api/v1/auth/login", `{"login":"`+login+`","password":"long-local-test-password"}`, nil, 200)
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal(cookies)
	}
	return cookies[0]
}
func TestAccountsEnforceRoleAndWellScopeAcrossRoutes(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	app := New(testConfig(), pool, &fakeML{})
	legacy := app.Handler()
	for _, body := range []string{`{"unitek_id":42,"ngdu":0,"department":0}`, `{"unitek_id":43,"ngdu":1,"department":0}`, `{"fund_type":"presentation","ngdu":0,"department":0}`} {
		call(t, legacy, "POST", "/api/v1/wells", body, 201)
	}
	access, err := service.NewAccess(app.store, true)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := access.Bootstrap(ctx, accountInput("admin", "admin"))
	if err != nil {
		t.Fatal(err)
	}
	adminCtx := domain.WithPrincipal(ctx, domain.AccountPrincipal(admin))
	for _, role := range []string{"viewer", "technologist"} {
		in := accountInput(role, role)
		in.Scope.NGDUs = ptr([]int64{0})
		if _, err = access.Create(adminCtx, in); err != nil {
			t.Fatal(err)
		}
	}
	app.http.Access = access
	app.http.Reviews = service.NewReviews(app.store).WithCatalog(app.store)
	app.http.Presentation = service.NewPresentation(app.store)
	h := app.Handler()
	authRequest(t, h, "GET", "/api/v1/wells", "", nil, 401)
	// Общий ключ и произвольные claims не обходят персональную авторизацию.
	r := httptest.NewRequest("GET", "/api/v1/wells", nil)
	r.Header.Set("X-API-Key", "test")
	r.Header.Set("X-User-Role", "admin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	viewer := loginCookie(t, h, "viewer")
	tech := loginCookie(t, h, "technologist")
	result := authRequest(t, h, "GET", "/api/v1/wells", "", viewer, 200)
	var wells []domain.Well
	if err = json.Unmarshal(result.Body.Bytes(), &wells); err != nil || len(wells) != 1 || wells[0].ID != 1 {
		t.Fatal(wells, err)
	}
	for _, cookie := range []*http.Cookie{viewer, tech} {
		for _, path := range []string{"/wells/2", "/wells/2/predictions", "/wells/2/telemetry", "/wells/2/checks", "/wells/3"} {
			authRequest(t, h, "GET", "/api/v1"+path, "", cookie, 404)
		}
		for _, path := range []string{"/telemetry?well_id=43", "/ml/wells/by-unitek/43", "/ml/wells/999/explain", "/ml/wells/999/history"} {
			status := 403
			if strings.Contains(path, "999") {
				status = 404
			}
			authRequest(t, h, "GET", "/api/v1"+path, "", cookie, status)
		}
		for _, path := range []string{"/integrations", "/audit", "/ml/health", "/ml/metrics", "/runs", "/auth/users", "/presentation/scenario"} {
			authRequest(t, h, "GET", "/api/v1"+path, "", cookie, 403)
		}
		authRequest(t, h, "GET", "/api/v1/ml/wells/123/explain", "", cookie, 200)
		authRequest(t, h, "GET", "/api/v1/incidents/stats?fund=presentation", "", cookie, 200)
		summary := authRequest(t, h, "GET", "/api/v1/presentation/summary", "", cookie, 200)
		if !strings.Contains(summary.Body.String(), `"count":0`) {
			t.Fatal(summary.Body.String())
		}
	}
	authRequest(t, h, "POST", "/api/v1/wells/1/checks", `{}`, viewer, 403)
	authRequest(t, h, "POST", "/api/v1/wells/2/checks", `{}`, tech, 404)
	check, err := app.store.SaveManualCheck(ctx, domain.ManualCheck{ID: "scope-check", WellID: 1, PointsCount: 1, RequestID: "scope-check-request", Start: time.Now().Add(-time.Hour), End: time.Now(), CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/wells/1/checks/" + check.ID + "/review"
	authRequest(t, h, "PUT", path, `{"request_id":"viewer-write-denied","revision":0,"status":"new"}`, viewer, 403)
	saved := authRequest(t, h, "PUT", path, `{"request_id":"verified-actor-review","revision":0,"status":"in_progress","owner":"Технолог","comment":"Проверка доступа","actor":{"name":"Подмена"}}`, tech, 200)
	var review domain.Review
	if err = json.Unmarshal(saved.Body.Bytes(), &review); err != nil || len(review.Events) != 1 || !review.Events[0].Actor.Authenticated || review.Events[0].Actor.Name != "technologist" {
		t.Fatal(saved.Body.String(), err)
	}
	// Попытка с чужого Origin отвергается даже с валидным cookie.
	r = httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	r.AddCookie(tech)
	r.Header.Set("Origin", "https://foreign.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	authRequest(t, h, "POST", "/api/v1/auth/logout", "", tech, 204)
	authRequest(t, h, "GET", "/api/v1/wells", "", tech, 401)
	// Экспорт ограничен тем же списком скважин.
	authRequest(t, h, "GET", "/api/v1/wells/export", "", viewer, 200)
	adminCookie := loginCookie(t, h, "admin")
	authRequest(t, h, "PATCH", "/api/v1/wells/1", `{"responsible":"Администратор"}`, adminCookie, 422)
	authRequest(t, h, "PATCH", "/api/v1/wells/1", `{"revision":1,"responsible":"Администратор"}`, adminCookie, 200)
	authRequest(t, h, "POST", "/api/v1/wells/1/archive", `{"revision":2,"reason":"Проверка прав"}`, viewer, 403)
	authRequest(t, h, "POST", "/api/v1/wells/1/archive", `{"revision":2,"reason":"Проверка прав","actor":{"name":"Подмена"}}`, adminCookie, 200)
	authRequest(t, h, "GET", "/api/v1/wells/1?include_archived=true", "", viewer, 200)
	authRequest(t, h, "GET", "/api/v1/wells/2/history", "", viewer, 404)
	eventsResponse := authRequest(t, h, "GET", "/api/v1/wells/1/history", "", viewer, 200)
	var events domain.WellHistory
	if err = json.Unmarshal(eventsResponse.Body.Bytes(), &events); err != nil || len(events.Events) != 2 || events.Events[0].Actor.Name != "admin" || !events.Events[0].Actor.Authenticated {
		t.Fatal(eventsResponse.Body.String(), err)
	}
	authRequest(t, h, "DELETE", "/api/v1/wells/1", "", adminCookie, 422)

}
func TestAccountSessionsRevocationAndFutureSSOIdentity(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	app := New(testConfig(), pool, &fakeML{})
	a, err := service.NewAccess(app.store, true)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := a.Bootstrap(ctx, accountInput("admin", "admin"))
	if err != nil {
		t.Fatal(err)
	}
	adminCtx := domain.WithPrincipal(ctx, domain.AccountPrincipal(admin))
	if _, err = a.Bootstrap(ctx, accountInput("other", "admin")); err == nil {
		t.Fatal("second bootstrap allowed")
	}
	in := accountInput("scoped", "viewer")
	in.Scope.NGDUs = ptr([]int64{})
	user, err := a.Create(adminCtx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.Login(ctx, "scoped", "wrong", "127.0.0.1"); err == nil {
		t.Fatal("wrong password")
	}
	_, token, err := a.Login(ctx, "scoped", *in.Password, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	if err = pool.QueryRow(ctx, "SELECT token_hash FROM auth_session WHERE user_id=$1", user.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(token))
	if hash != hex.EncodeToString(sum[:]) || hash == token {
		t.Fatal("token not hashed")
	}
	p, err := a.Identify(ctx, token)
	if err != nil || p.Allows(domain.Well{NGDU: 0, Department: 0}) {
		t.Fatal(p, err)
	}
	in.Revision = user.Revision
	in.Password = ptr("new-long-test-password")
	updated, err := a.Update(adminCtx, user.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Identify(ctx, token); err == nil {
		t.Fatal("old session survived update")
	}
	if err = app.store.CreateSession(ctx, user.ID, user.Revision, "stale-token", time.Now().Add(time.Hour)); err == nil {
		t.Fatal("stale credential race allowed")
	}
	if _, err = a.Update(adminCtx, user.ID, in); err == nil {
		t.Fatal("stale revision allowed")
	}
	if err = a.BindIdentity(adminCtx, "keycloak-main", "stable-subject", user.ID); err != nil {
		t.Fatal(err)
	}
	accepted, externalToken, err := a.AcceptIdentity(ctx, service.VerifiedIdentity{ProviderID: "keycloak-main", Subject: "stable-subject"})
	if err != nil || accepted.Role != "viewer" || accepted.Revision != updated.Revision {
		t.Fatal(accepted, err)
	}
	if _, err = a.Identify(ctx, externalToken); err != nil {
		t.Fatal(err)
	}
	if _, _, err = a.AcceptIdentity(ctx, service.VerifiedIdentity{ProviderID: "other-provider", Subject: "stable-subject"}); err == nil {
		t.Fatal("issuer confused")
	}
	adminChange := accountInput("admin", "viewer")
	adminChange.Revision = admin.Revision
	if _, err = a.Update(adminCtx, admin.ID, adminChange); err == nil {
		t.Fatal("last admin demoted")
	}
	if _, err = a.Create(ctx, accountInput("anonymous", "admin")); err == nil {
		t.Fatal("anonymous account creation")
	}
	for i := 0; i < 6; i++ {
		_, _, err = a.Login(ctx, "nonexistent", "wrong", fmt.Sprint("192.0.2.1"))
		var de *domain.Error
		if !errors.As(err, &de) {
			t.Fatal(err)
		}
		want := domain.Unauthorized
		if i == 5 {
			want = domain.Limited
		}
		if de.Kind != want {
			t.Fatal(i, de)
		}
	}
}
