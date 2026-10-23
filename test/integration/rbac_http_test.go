package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestRBACHTTP(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	app := New(testConfig(), p, &fakeML{})
	a, e := service.NewAccess(app.store, true)
	if e != nil {
		t.Fatal(e)
	}
	admin, e := a.Bootstrap(ctx, accountInput("admin", "admin"))
	if e != nil {
		t.Fatal(e)
	}
	adminCtx := domain.WithPrincipal(ctx, domain.AccountPrincipal(admin))
	user, e := a.Create(adminCtx, accountInput("empty", "viewer"))
	if e != nil {
		t.Fatal(e)
	}
	// Exact IDs beyond Number precision, direct zero-permission role and empty group.
	const uid int64 = 9007199254740997
	const rid int64 = 9007199254740993
	const gid int64 = 9007199254740995
	for _, q := range []string{
		fmt.Sprintf(`INSERT INTO auth_user(id,login,name,password_hash,role,scope,organization_scope,revision) OVERRIDING SYSTEM VALUE SELECT %d,'precise','precise',password_hash,'admin',scope,organization_scope,9007199254740999 FROM auth_user WHERE id=%d`, uid, user.ID),
		fmt.Sprintf(`INSERT INTO auth_role(id,name,revision) OVERRIDING SYSTEM VALUE VALUES(%d,'admin',9007199254740999)`, rid),
		fmt.Sprintf(`INSERT INTO auth_group(id,name) OVERRIDING SYSTEM VALUE VALUES(%d,'empty')`, gid),
		fmt.Sprintf(`INSERT INTO auth_user_role VALUES(%d,%d)`, uid, rid),
		fmt.Sprintf(`INSERT INTO auth_group_member VALUES(%d,%d)`, gid, uid),
	} {
		if _, e = p.Exec(ctx, q); e != nil {
			t.Fatal(e)
		}
	}
	a.WithRBACMode(domain.RBACEnforce)
	app.http.Access = a
	h := app.Handler()
	authRequest(t, h, "GET", "/api/v1/wells", "", nil, 401)
	login := authRequest(t, h, "POST", "/api/v1/auth/login", `{"login":"precise","password":"long-local-test-password"}`, nil, 200)
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal(cookies)
	}
	cookie := cookies[0]
	var payload struct{ Principal map[string]json.RawMessage }
	checkView := func(raw []byte) {
		t.Helper()
		if e := json.Unmarshal(raw, &payload); e != nil {
			t.Fatal(e)
		}
		v := payload.Principal
		for key, want := range map[string]string{"access_id": `"9007199254740997"`, "access_revision": `"9007199254740999"`, "account_id": `9007199254740997`, "role": `"viewer"`, "legacy_role": `null`, "permission_keys": `[]`, "role_ids": `["9007199254740993"]`, "group_ids": `["9007199254740995"]`} {
			if string(v[key]) != want {
				t.Fatalf("%s=%s want %s", key, v[key], want)
			}
		}
		if !strings.Contains(string(v["roles"]), `"revision":"9007199254740999"`) || !strings.Contains(string(v["groups"]), `"role_ids":[]`) {
			t.Fatal(string(raw))
		}
	}
	checkView(login.Body.Bytes())
	checkView(authRequest(t, h, "GET", "/api/v1/auth/me", "", cookie, 200).Body.Bytes())
	authRequest(t, h, "GET", "/api/v1/wells", "", cookie, 403)
	authRequest(t, h, "GET", "/api/v1/auth/roles", "", cookie, 403)

	if _, e = p.Exec(ctx, `INSERT INTO auth_role_permission VALUES($1,'unknown.permission')`, rid); e != nil {
		t.Fatal(e)
	}
	own := authRequest(t, h, "GET", "/api/v1/auth/me", "", cookie, 200)
	if !strings.Contains(own.Body.String(), `"unknown.permission"`) {
		t.Fatal("unassignable own definition lost", own.Body.String())
	}
	if _, e = p.Exec(ctx, `DELETE FROM auth_role_permission WHERE role_id=$1`, rid); e != nil {
		t.Fatal(e)
	}
	// Registry visibility does not control the account's own assigned definitions.
	if _, e = p.Exec(ctx, `INSERT INTO auth_role_permission VALUES($1,'wells.read')`, rid); e != nil {
		t.Fatal(e)
	}
	authRequest(t, h, "GET", "/api/v1/wells/1/risk", "", cookie, 403)
	adminCookie := loginCookie(t, h, "admin")
	card := authRequest(t, h, "GET", fmt.Sprintf("/api/v1/auth/users/%d", uid), "", adminCookie, 200)
	var account map[string]json.RawMessage
	if e = json.Unmarshal(card.Body.Bytes(), &account); e != nil {
		t.Fatal(e)
	}
	for key, want := range map[string]string{"access_id": `"9007199254740997"`, "access_revision": `"9007199254740999"`, "id": `9007199254740997`, "revision": `9007199254740999`} {
		if string(account[key]) != want {
			t.Fatalf("card %s=%s", key, account[key])
		}
	}
	page := authRequest(t, h, "GET", "/api/v1/auth/users?access_version=1&search=precise", "", adminCookie, 200)
	if !strings.Contains(page.Body.String(), `"access_id":"9007199254740997"`) || !strings.Contains(page.Body.String(), `"id":9007199254740997`) {
		t.Fatal(page.Body.String())
	}
	// Revoked session epoch is checked on the same statement as returned rights.
	if _, e = p.Exec(ctx, `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=$1`, uid); e != nil {
		t.Fatal(e)
	}
	authRequest(t, h, "GET", "/api/v1/auth/me", "", cookie, 401)
	// Origin/cookie/logout semantics remain unchanged.
	r := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	r.Header.Set("Origin", "https://foreign.example")
	r.AddCookie(adminCookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	authRequest(t, h, "POST", "/api/v1/auth/logout", "", adminCookie, 204)
	authRequest(t, h, "GET", "/api/v1/auth/me", "", adminCookie, 401)
}

func TestRBACLegacyWrite(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	app := New(testConfig(), p, &fakeML{})
	a, e := service.NewAccess(app.store, true)
	if e != nil {
		t.Fatal(e)
	}
	admin, e := a.Bootstrap(ctx, accountInput("admin", "admin"))
	if e != nil {
		t.Fatal(e)
	}
	adminCtx := domain.WithPrincipal(ctx, domain.AccountPrincipal(admin))
	user, e := a.Create(adminCtx, accountInput("target", "viewer"))
	if e != nil {
		t.Fatal(e)
	}
	a.WithRBACMode(domain.RBACEnforce)
	app.http.Access = a
	h := app.Handler()
	cookie := loginCookie(t, h, "admin")
	authRequest(t, h, "POST", "/api/v1/auth/users", `{"login":"newuser","name":"newuser","role":"viewer","scope":{"funds":["operational"]}}`, cookie, 409)
	authRequest(t, h, "POST", "/api/v1/auth/users", `{"access_version":1,"role":"viewer","change":{"user":{}}}`, cookie, 400)
	body := func() string {
		in := accountInput("target", "viewer")
		in.Password = nil
		in.Name = "metadata"
		in.Revision = user.Revision
		b, _ := json.Marshal(in)
		return string(b)
	}
	path := fmt.Sprintf("/api/v1/auth/users/%d", user.ID)
	saved := authRequest(t, h, "PUT", path, body(), cookie, 200)
	if !strings.Contains(saved.Body.String(), `"access_model":"rbac_v1"`) {
		t.Fatal("safe old response lacks coherent additive projection", saved.Body.String())
	}
	user, e = app.store.Account(ctx, user.ID)
	if e != nil || user.Revision != 2 {
		t.Fatal(user, e)
	}
	var role int64
	if e = p.QueryRow(ctx, `SELECT role_id FROM auth_user_role WHERE user_id=$1`, user.ID).Scan(&role); e != nil {
		t.Fatal(e)
	}
	snapshot := func() string {
		var raw string
		e := p.QueryRow(ctx, `SELECT jsonb_build_object('account',row_to_json(u),'roles',(SELECT jsonb_agg(role_id ORDER BY role_id) FROM auth_user_role WHERE user_id=u.id),'groups',(SELECT jsonb_agg(group_id ORDER BY group_id) FROM auth_group_member WHERE user_id=u.id))::text FROM auth_user u WHERE id=$1`, user.ID).Scan(&raw)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	for _, kind := range []string{"multi", "group", "modified_builtin", "complex", "scope_change"} {
		t.Run(kind, func(t *testing.T) {
			var undo func()
			switch kind {
			case "multi":
				var other int64
				if e = p.QueryRow(ctx, `INSERT INTO auth_role(name)VALUES('extra') RETURNING id`).Scan(&other); e != nil {
					t.Fatal(e)
				}
				if _, e = p.Exec(ctx, `INSERT INTO auth_user_role VALUES($1,$2)`, user.ID, other); e != nil {
					t.Fatal(e)
				}
				undo = func() { p.Exec(ctx, `DELETE FROM auth_user_role WHERE user_id=$1 AND role_id=$2`, user.ID, other) }
			case "group":
				var group int64
				if e = p.QueryRow(ctx, `INSERT INTO auth_group(name)VALUES('empty') RETURNING id`).Scan(&group); e != nil {
					t.Fatal(e)
				}
				if _, e = p.Exec(ctx, `INSERT INTO auth_group_member VALUES($1,$2)`, group, user.ID); e != nil {
					t.Fatal(e)
				}
				undo = func() { p.Exec(ctx, `DELETE FROM auth_group_member WHERE group_id=$1`, group) }
			case "modified_builtin":
				if _, e = p.Exec(ctx, `DELETE FROM auth_role_permission WHERE role_id=$1 AND permission_key='wells.export'`, role); e != nil {
					t.Fatal(e)
				}
				undo = func() { p.Exec(ctx, `INSERT INTO auth_role_permission VALUES($1,'wells.export')`, role) }
			case "complex":
				if _, e = p.Exec(ctx, `UPDATE auth_user SET organization_scope=null WHERE id=$1`, user.ID); e != nil {
					t.Fatal(e)
				}
				undo = func() {
					p.Exec(ctx, `UPDATE auth_user SET organization_scope='{"kind":"organization","ngdu_id":null,"department_id":null}' WHERE id=$1`, user.ID)
				}
			default:
				undo = func() {}
			}
			defer undo()
			before := snapshot()
			b := body()
			if kind == "scope_change" {
				b = strings.Replace(b, `"ngdus":null`, `"ngdus":[0]`, 1)
			}
			authRequest(t, h, "PUT", path, b, cookie, 409)
			if after := snapshot(); after != before {
				t.Fatal("rejected legacy write changed graph", before, after)
			}
		})
	}
}

func TestRBACHTTPModern(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	app := New(testConfig(), p, &fakeML{})
	a, e := service.NewAccess(app.store, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.Bootstrap(ctx, accountInput("admin", "admin")); e != nil {
		t.Fatal(e)
	}
	app.http.Access = a
	h := app.Handler()
	cookie := loginCookie(t, h, "admin")
	authRequest(t, h, "POST", "/api/v1/auth/access/preview", `{"access_version":1,"change":{"role":{"name":"empty","permission_keys":[]}}}`, cookie, 503)
	authRequest(t, h, "POST", "/api/v1/auth/roles", `{}`, cookie, 503)
	// Existing old POST remains available in compat and returns a coherent real view.
	old := authRequest(t, h, "POST", "/api/v1/auth/users", `{"login":"compat","name":"compat","role":"viewer","scope":{"funds":["operational"]}}`, cookie, 201)
	if !strings.Contains(old.Body.String(), `"access_model":"legacy"`) || !strings.Contains(old.Body.String(), `"legacy_role":"viewer"`) || !strings.Contains(old.Body.String(), `"permission_keys":[`) {
		t.Fatal(old.Body.String())
	}
	a.WithRBACMode(domain.RBACEnforce)
	h = app.Handler()
	apply := func(method, path, change, password string) *httptest.ResponseRecorder {
		t.Helper()
		preview := authRequest(t, h, "POST", "/api/v1/auth/access/preview", `{"access_version":1,"change":`+change+`}`, cookie, 200)
		var receipt map[string]json.RawMessage
		if e = json.Unmarshal(preview.Body.Bytes(), &receipt); e != nil {
			t.Fatal(e)
		}
		in := map[string]json.RawMessage{"access_version": json.RawMessage(`1`), "change": json.RawMessage(change), "graph_revision": receipt["graph_revision"], "actor_epoch": receipt["actor_epoch"], "fingerprint": receipt["fingerprint"]}
		if password != "" {
			b, _ := json.Marshal(password)
			in["password"] = b
		}
		body, _ := json.Marshal(in)
		status := 200
		if method == "POST" {
			status = 201
		}
		return authRequest(t, h, method, path, string(body), cookie, status)
	}
	role := apply("POST", "/api/v1/auth/roles", `{"role":{"name":"zero permission role","permission_keys":[]}}`, "")
	var roleOut struct {
		Role struct {
			ID       string `json:"id"`
			Revision string `json:"revision"`
		}
	}
	if e = json.Unmarshal(role.Body.Bytes(), &roleOut); e != nil || roleOut.Role.ID == "" || roleOut.Role.Revision != "1" {
		t.Fatal(role.Body.String(), e)
	}
	group := apply("POST", "/api/v1/auth/groups", `{"group":{"name":"empty group","role_ids":[],"member_ids":[]}}`, "")
	var groupOut struct {
		Group struct {
			ID string `json:"id"`
		}
	}
	if e = json.Unmarshal(group.Body.Bytes(), &groupOut); e != nil || groupOut.Group.ID == "" {
		t.Fatal(group.Body.String(), e)
	}
	change := fmt.Sprintf(`{"user":{"login":"modern","name":"Modern","active":true,"role_ids":[%q],"group_ids":[%q],"organization":{"kind":"organization","ngdu_id":null,"department_id":null},"reset_password":true}}`, roleOut.Role.ID, groupOut.Group.ID)
	created := apply("POST", "/api/v1/auth/users", change, "long-local-test-password")
	var userOut struct{ User map[string]json.RawMessage }
	if e = json.Unmarshal(created.Body.Bytes(), &userOut); e != nil {
		t.Fatal(e)
	}
	for key, want := range map[string]string{"role_ids": fmt.Sprintf(`[%q]`, roleOut.Role.ID), "group_ids": fmt.Sprintf(`[%q]`, groupOut.Group.ID), "permission_keys": `[]`, "legacy_role": `null`, "role": `"viewer"`, "authz_epoch": `"1"`} {
		if string(userOut.User[key]) != want {
			t.Fatalf("Apply user %s=%s want %s", key, userOut.User[key], want)
		}
	}

	members := authRequest(t, h, "GET", "/api/v1/auth/groups/"+groupOut.Group.ID+"/members", "", cookie, 200)
	if !strings.Contains(members.Body.String(), `"access_id":`) || !strings.Contains(members.Body.String(), `"role_ids":["`+roleOut.Role.ID+`"]`) || !strings.Contains(members.Body.String(), `"permission_keys":[]`) {
		t.Fatal(members.Body.String())
	}
	logged := loginCookie(t, h, "modern")
	authRequest(t, h, "GET", "/api/v1/auth/me", "", logged, 200)
	authRequest(t, h, "GET", "/api/v1/wells", "", logged, 403)
	var canonicalID, canonicalRevision string
	if e = json.Unmarshal(userOut.User["access_id"], &canonicalID); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(userOut.User["access_revision"], &canonicalRevision); e != nil {
		t.Fatal(e)
	}
	roleless := fmt.Sprintf(`{"user":{"id":%q,"revision":%q,"login":"modern","name":"Modern","active":true,"role_ids":[],"group_ids":[]}}`, canonicalID, canonicalRevision)
	updated := apply("PUT", "/api/v1/auth/users/"+canonicalID, roleless, "")
	if !strings.Contains(updated.Body.String(), `"role_ids":[]`) || !strings.Contains(updated.Body.String(), `"group_ids":[]`) {
		t.Fatal(updated.Body.String())
	}
	authRequest(t, h, "GET", "/api/v1/auth/me", "", logged, 401)
	rolelessCookie := loginCookie(t, h, "modern")
	authRequest(t, h, "GET", "/api/v1/auth/me", "", rolelessCookie, 200)
	authRequest(t, h, "GET", "/api/v1/wells", "", rolelessCookie, 403)

	for _, bad := range []string{
		`{"access_version":1,"change":{"user":{"login":"valid","name":"x","active":true,"role_ids":[1],"group_ids":[],"organization":{"kind":"organization"}}}}`,
		`{"access_version":1,"change":{"user":{"login":"valid","name":"x","active":true,"role_ids":[],"group_ids":[],"organization":{"kind":"unknown"}}}}`,
		`{"access_version":1,"change":{"user":{"login":"valid","name":"x","active":true,"role_ids":[],"group_ids":[],"organization":{"kind":"organization","unexpected":1}}}}`,
		`{"access_version":1,"change":{"role":{"name":"secret","permission_keys":[]}},"password":"no preview secrets"}`,
	} {
		authRequest(t, h, "POST", "/api/v1/auth/access/preview", bad, cookie, 400)
	}
}
