package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestEmployeeProfilesAtomicHTTP(t *testing.T) {
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
	a.WithRBACMode(domain.RBACEnforce)
	app.http.Access = a
	h := app.Handler()
	cookie := loginCookie(t, h, "admin")
	profile := `{"revision":"0","job_title":" Инженер ","workplace":{"organization":"Татнефть","division":null,"department":"Отдел"},"manager_id":null}`
	change := `{"user":{"login":"created","name":"Сотрудник","active":true,"revision":"0","role_ids":[],"group_ids":[],"organization":{"kind":"organization","ngdu_id":null,"department_id":null},"profile":` + profile + `}}`
	apply := func(method, path, change string, want int) map[string]json.RawMessage {
		t.Helper()
		preview := authRequest(t, h, "POST", "/api/v1/auth/access/preview", `{"access_version":1,"change":`+change+`}`, cookie, 200)
		var receipt map[string]json.RawMessage
		if e := json.Unmarshal(preview.Body.Bytes(), &receipt); e != nil {
			t.Fatal(e)
		}
		body := map[string]json.RawMessage{"access_version": json.RawMessage(`1`), "change": json.RawMessage(change), "actor_epoch": receipt["actor_epoch"], "graph_revision": receipt["graph_revision"], "fingerprint": receipt["fingerprint"]}
		raw, _ := json.Marshal(body)
		result := authRequest(t, h, method, path, string(raw), cookie, want)
		var out map[string]json.RawMessage
		if e := json.Unmarshal(result.Body.Bytes(), &out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	out := apply("POST", "/api/v1/auth/users", change, 201)
	if string(out["session_effect"]) != `"none"` {
		t.Fatal(out)
	}
	var user struct {
		ID       string                `json:"access_id"`
		Revision string                `json:"access_revision"`
		Profile  domain.ManagedProfile `json:"profile"`
	}
	if e = json.Unmarshal(out["user"], &user); e != nil || user.Profile.Revision != 1 || user.Profile.JobTitle == nil || *user.Profile.JobTitle != "Инженер" {
		t.Fatal(string(out["user"]), e)
	}
	detail := authRequest(t, h, "GET", "/api/v1/auth/users/"+user.ID, "", cookie, 200)
	if !strings.Contains(detail.Body.String(), `"profile":{"revision":"1"`) {
		t.Fatal(detail.Body.String())
	}
	profile = strings.Replace(profile, `"revision":"0"`, `"revision":"1"`, 1)
	profile = strings.Replace(profile, `" Инженер "`, `"Главный инженер"`, 1)
	change = fmt.Sprintf(`{"user":{"id":%q,"login":"created","name":"Сотрудник","active":true,"revision":%q,"role_ids":[],"group_ids":[],"profile":%s}}`, user.ID, user.Revision, profile)
	out = apply("PUT", "/api/v1/auth/users/"+user.ID, change, 200)
	if string(out["session_effect"]) != `"none"` || string(out["affected_users"]) != "0" {
		t.Fatal(out)
	}
	stale := authRequest(t, h, "POST", "/api/v1/auth/access/preview", `{"access_version":1,"change":`+change+`}`, cookie, 409)
	if !strings.Contains(stale.Body.String(), `"message":"profile_revision_conflict"`) {
		t.Fatal(stale.Body.String())
	}
	for _, bad := range []string{strings.Replace(change, `"revision":"1","job_title"`, `"revision":"2","job_title"`, 1)} {
		bad = strings.Replace(bad, `"job_title":"Главный инженер",`, "", 1)
		authRequest(t, h, "POST", "/api/v1/auth/access/preview", `{"access_version":1,"change":`+bad+`}`, cookie, 422)
	}
	unsafe := strings.Replace(change, `"department":"Отдел"`, `"department":"Отдел","tenant":"other"`, 1)
	authRequest(t, h, "POST", "/api/v1/auth/access/preview", `{"access_version":1,"change":`+unsafe+`}`, cookie, 400)
}
