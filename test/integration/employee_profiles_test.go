package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func employeeInput(revision int64, title string, manager *int64) domain.Field[domain.ProfileInput] {
	text := func() domain.Field[string] { return domain.Field[string]{Present: true, Null: true} }
	m := domain.Field[int64]{Present: true, Null: manager == nil}
	if manager != nil {
		m.Value = *manager
	}
	return domain.Field[domain.ProfileInput]{Present: true, Value: domain.ProfileInput{Revision: domain.Field[int64]{Present: true, Value: revision}, JobTitle: domain.Field[string]{Present: true, Value: title}, Workplace: domain.Field[domain.WorkplaceInput]{Present: true, Value: domain.WorkplaceInput{Organization: text(), Division: text(), Department: text()}}, ManagerID: m}}
}
func TestEmployeeProfileOnlyPreservesAuthority(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	root := rbacManager(t, p)
	target := rbacReadUser(t, p, "employee", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	if e := r.CreateSession(ctx, target, 1, "employee-session", time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	before, e := r.Authorization(ctx, target)
	if e != nil {
		t.Fatal(e)
	}
	c := rbacChange(t, r, target)
	c.Profile = employeeInput(1, "  Инженер  ", nil)
	preview, e := r.PreviewAccessChange(ctx, root, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
	if e != nil {
		t.Fatal(e)
	}
	if preview.ChangedUsers != 0 || preview.SelfAffected || len(preview.Items) != 0 || preview.SessionEffect != "none" || preview.ProfileChange == nil {
		t.Fatal(preview)
	}
	out := rbacApply(t, r, root, domain.AccessChange{User: &c})
	if out.AffectedUsers != 0 || out.SessionEffect != "none" || out.UserView.Profile == nil || out.UserView.Profile.Revision != 2 || out.User.Revision != 1 {
		t.Fatal(out)
	}
	after, e := r.Authorization(ctx, target)
	if e != nil || after.Epoch != before.Epoch || after.GraphRevision != before.GraphRevision {
		t.Fatal(before, after, e)
	}
	if _, e = r.SessionAccountEnforced(ctx, "employee-session"); e != nil {
		t.Fatal(e)
	}

	noOp := c
	noOp.Profile = employeeInput(2, "Инженер", nil)
	noOpOut := rbacApply(t, r, root, domain.AccessChange{User: &noOp})
	if noOpOut.UserView.Profile.Revision != 2 || noOpOut.GraphRevision != before.GraphRevision || noOpOut.SessionEffect != "none" {
		t.Fatal(noOpOut)
	}
	var events int
	if e := p.QueryRow(ctx, `SELECT count(*) FROM auth_access_event`).Scan(&events); e != nil || events != 1 {
		t.Fatal("no-op wrote event", events, e)
	}
	if _, e = r.PreviewAccessChange(ctx, root, domain.AccessChange{User: &c}, domain.AccessPageQuery{}); e == nil || !strings.Contains(e.Error(), "profile_revision_conflict") {
		t.Fatal(e)
	}
	c.Profile = employeeInput(2, "Начальник", nil)
	c.Name = "Новое имя"
	out = rbacApply(t, r, root, domain.AccessChange{User: &c})
	if out.SessionEffect != "revoked" || out.AffectedUsers != 1 {
		t.Fatal(out)
	}
	if _, e = r.SessionAccountEnforced(ctx, "employee-session"); e == nil {
		t.Fatal("session survived authority mutation")
	}
}
func TestEmployeeDirectorySafeProjection(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	viewer := rbacReadUser(t, p, "private-login", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	target := rbacReadUser(t, p, "colleague", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	if _, e := p.Exec(ctx, `UPDATE auth_user SET active=false,job_title='Инженер' WHERE id=$1`, target); e != nil {
		t.Fatal(e)
	}
	page, e := r.ListPeople(ctx, viewer, domain.PeopleQuery{Limit: 100})
	if e != nil {
		t.Fatal(e)
	}
	for _, a := range page.Items {
		if a.ID == target {
			t.Fatal("inactive leaked")
		}
	}
	if _, e := p.Exec(ctx, `UPDATE auth_user SET manager_account_id=$1 WHERE id=$2`, target, viewer); e != nil {
		t.Fatal(e)
	}
	employee, e := r.EmployeeProfile(ctx, viewer, viewer)
	if e != nil || employee.Manager == nil || employee.Manager.ID != target || employee.Manager.Active {
		t.Fatal(employee, e)
	}
	profile, e := r.EmployeeProfile(ctx, viewer, target)
	if e != nil || profile.Active || profile.JobTitle == nil {
		t.Fatal(profile, e)
	}
	raw, _ := json.Marshal(profile)
	for _, key := range []string{"login", "scope", "revision", "epoch", "role", "created_at", "last_login"} {
		if strings.Contains(string(raw), `"`+key+`"`) {
			t.Fatal(string(raw))
		}
	}
	page, e = r.ListPeople(ctx, viewer, domain.PeopleQuery{Search: "private-login-missing"})
	if e != nil || len(page.Items) != 0 {
		t.Fatal(page, e)
	}
}

func TestEmployeeProfileRevokedActor(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	root := rbacManager(t, p)
	target := rbacReadUser(t, p, "target", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	auth, e := r.Authorization(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	ctx = domain.WithPrincipal(ctx, domain.Principal{AccountID: &root, Authenticated: true, Mechanism: "session", Authorization: &auth})
	if _, e = p.Exec(ctx, `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=$1`, root); e != nil {
		t.Fatal(e)
	}
	c := rbacChange(t, r, target)
	c.Profile = employeeInput(1, "Инженер", nil)
	_, e = r.PreviewAccessChange(ctx, root, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
	rbacReadErrorKind(t, e, domain.Unauthorized)
	_, e = r.ListPeople(ctx, root, domain.PeopleQuery{})
	rbacReadErrorKind(t, e, domain.Unauthorized)
}

func TestEmployeeDirectoryPagination(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	viewer := rbacReadUser(t, p, "viewer", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	for _, name := range []string{"Сотрудник_1%", "Сотрудник_2%", "СотрудникX3"} {
		id := rbacReadUser(t, p, strings.ReplaceAll(name, "%", ""), `{"kind":"organization","ngdu_id":null,"department_id":null}`)
		if _, e := p.Exec(ctx, `UPDATE auth_user SET name=$1 WHERE id=$2`, name, id); e != nil {
			t.Fatal(e)
		}
	}
	page, e := r.ListPeople(ctx, viewer, domain.PeopleQuery{Search: "Сотрудник_", Limit: 1})
	if e != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal(page, e)
	}
	rawCursor, _ := base64.RawURLEncoding.DecodeString(*page.NextCursor)
	malformed := base64.RawURLEncoding.EncodeToString(append(rawCursor, []byte(" trailing")...))
	if _, e := r.ListPeople(ctx, viewer, domain.PeopleQuery{Search: "Сотрудник_", Cursor: malformed}); e == nil {
		t.Fatal("trailing cursor data accepted")
	}
	next, e := r.ListPeople(ctx, viewer, domain.PeopleQuery{Search: "Сотрудник_", Limit: 1, Cursor: *page.NextCursor})
	if e != nil || len(next.Items) != 1 || next.Items[0].ID <= page.Items[0].ID || next.NextCursor != nil {
		t.Fatal(next, e)
	}
	for _, q := range []domain.PeopleQuery{{Limit: 101}, {Limit: -1}, {Cursor: "broken"}, {Search: "changed", Cursor: *page.NextCursor}, {IncludeInactive: true, Search: "Сотрудник_", Cursor: *page.NextCursor}, {Search: strings.Repeat("я", 201)}, {Cursor: strings.Repeat("x", 1025)}} {
		if _, e := r.ListPeople(ctx, viewer, q); e == nil {
			t.Fatal("bad query accepted", q)
		}
	}
	literal, e := r.ListPeople(ctx, viewer, domain.PeopleQuery{Search: "%"})
	if e != nil || len(literal.Items) != 2 {
		t.Fatal(literal, e)
	}
	if _, e := p.Exec(ctx, `UPDATE auth_user SET name='Имя' WHERE id=$1`, viewer); e != nil {
		t.Fatal(e)
	}
	hidden, e := r.ListPeople(ctx, viewer, domain.PeopleQuery{Search: "viewer"})
	if e != nil || len(hidden.Items) != 0 {
		t.Fatal("login searchable", hidden, e)
	}
}
func TestEmployeeManagerConcurrentCycle(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	root := rbacManager(t, p)
	a := rbacReadUser(t, p, "alice", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	b := rbacReadUser(t, p, "boris", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	ca, cb := rbacChange(t, r, a), rbacChange(t, r, b)
	ca.Profile = employeeInput(1, "А", &b)
	cb.Profile = employeeInput(1, "Б", &a)
	pa, e := r.PreviewAccessChange(ctx, root, domain.AccessChange{User: &ca}, domain.AccessPageQuery{})
	if e != nil {
		t.Fatal(e)
	}
	pb, e := r.PreviewAccessChange(ctx, root, domain.AccessChange{User: &cb}, domain.AccessPageQuery{})
	if e != nil {
		t.Fatal(e)
	}
	start := make(chan struct{})
	result := make(chan error, 2)
	for _, v := range []struct {
		c domain.UserAccessChange
		p domain.AccessPreview
	}{{ca, pa}, {cb, pb}} {
		go func() {
			<-start
			_, err := r.ApplyAccessChange(ctx, root, domain.AccessApply{Change: domain.AccessChange{User: &v.c}, GraphRevision: v.p.GraphRevision, ActorEpoch: v.p.ActorEpoch, Fingerprint: v.p.Fingerprint}, nil)
			result <- err
		}()
	}
	close(start)
	first, second := <-result, <-result
	if (first == nil) == (second == nil) {
		t.Fatal("expected exactly one commit", first, second)
	}
	if first != nil {
		rbacReadErrorKind(t, first, domain.Invalid)
	}
	if second != nil {
		rbacReadErrorKind(t, second, domain.Invalid)
	}
}
func TestEmployeeProfileConcurrentRevision(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	root := rbacManager(t, p)
	target := rbacReadUser(t, p, "target", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	c := rbacChange(t, r, target)
	c.Profile = employeeInput(1, "Инженер", nil)
	preview, e := r.PreviewAccessChange(ctx, root, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
	if e != nil {
		t.Fatal(e)
	}
	in := domain.AccessApply{Change: domain.AccessChange{User: &c}, GraphRevision: preview.GraphRevision, ActorEpoch: preview.ActorEpoch, Fingerprint: preview.Fingerprint}
	start := make(chan struct{})
	result := make(chan error, 2)
	for range 2 {
		go func() { <-start; _, err := r.ApplyAccessChange(ctx, root, in, nil); result <- err }()
	}
	close(start)
	a, b := <-result, <-result
	if (a == nil) == (b == nil) {
		t.Fatal(a, b)
	}
	if a != nil {
		rbacReadErrorKind(t, a, domain.Duplicate)
	}
	if b != nil {
		rbacReadErrorKind(t, b, domain.Duplicate)
	}
}
func TestEmployeeCombinedAtomicRollback(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	root := rbacManager(t, p)
	target := rbacReadUser(t, p, "target", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	var role, group int64
	if e := p.QueryRow(ctx, `INSERT INTO auth_role(name) VALUES('rollback zero') RETURNING id`).Scan(&role); e != nil {
		t.Fatal(e)
	}
	if e := p.QueryRow(ctx, `INSERT INTO auth_group(name) VALUES('rollback group') RETURNING id`).Scan(&group); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `CREATE FUNCTION reject_employee_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected audit failure'; END $$; CREATE TRIGGER reject_employee_event BEFORE INSERT ON auth_access_event FOR EACH ROW EXECUTE FUNCTION reject_employee_event()`); e != nil {
		t.Fatal(e)
	}
	for _, create := range []bool{false, true} {
		c := rbacChange(t, r, target)
		c.Profile = employeeInput(1, "Инженер", nil)
		c.Name = "changed"
		c.RoleIDs = []int64{role}
		c.GroupIDs = []int64{group}
		c.ResetPassword = true
		if create {
			c.ID = nil
			c.Login = "created"
			c.Revision = 0
			c.Profile.Value.Revision.Value = 0
			c.Organization = domain.Field[domain.OrganizationScope]{Present: true, Value: domain.OrganizationScope{Kind: "organization"}}
		}
		preview, e := r.PreviewAccessChange(ctx, root, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
		if e != nil {
			t.Fatal(e)
		}
		hash := "new-password-hash"
		_, e = r.ApplyAccessChange(ctx, root, domain.AccessApply{Change: domain.AccessChange{User: &c}, GraphRevision: preview.GraphRevision, ActorEpoch: preview.ActorEpoch, Fingerprint: preview.Fingerprint}, &hash)
		if e == nil {
			t.Fatal("audit failure committed")
		}
	}
	var title *string
	var rev int64
	var hash string
	if e := p.QueryRow(ctx, `SELECT job_title,profile_revision,password_hash FROM auth_user WHERE id=$1`, target).Scan(&title, &rev, &hash); e != nil || title != nil || rev != 1 || hash != "hash" {
		t.Fatal(title, rev, hash, e)
	}
	var links int
	if e := p.QueryRow(ctx, `SELECT (SELECT count(*) FROM auth_user_role WHERE user_id=$1)+(SELECT count(*) FROM auth_group_member WHERE user_id=$1)`, target).Scan(&links); e != nil || links != 0 {
		t.Fatal("partial assignment writes", links, e)
	}
	var created int
	if e := p.QueryRow(ctx, `SELECT count(*) FROM auth_user WHERE login='created'`).Scan(&created); e != nil || created != 0 {
		t.Fatal(created, e)
	}
}

func TestEmployeeProfileLegacyOmission(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	root := rbacManager(t, p)
	target := rbacReadUser(t, p, "target", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	if _, e := p.Exec(ctx, `UPDATE auth_user SET job_title='Сохранить',profile_revision=8 WHERE id=$1`, target); e != nil {
		t.Fatal(e)
	}
	c := rbacChange(t, r, target)
	c.Name = "Изменено"
	out := rbacApply(t, r, root, domain.AccessChange{User: &c})
	if out.SessionEffect != "revoked" || out.UserView.Profile == nil || out.UserView.Profile.Revision != 8 || *out.UserView.Profile.JobTitle != "Сохранить" {
		t.Fatal(out)
	}
}
func TestEmployeeProfileBoundedManagement(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	_ = rbacManager(t, p)
	if _, e := p.Exec(ctx, `INSERT INTO ngdu(id,name)VALUES(1,'one'),(2,'two');INSERT INTO department(id,name)VALUES(3,'three'),(4,'four')`); e != nil {
		t.Fatal(e)
	}
	org := `{"kind":"department","ngdu_id":1,"department_id":3}`
	actor := rbacReadUser(t, p, "leader", org, "users.read", "users.update")
	allowed := rbacReadUser(t, p, "allowed", org)
	protected := rbacReadUser(t, p, "protected", org, "access.manage_global")
	outside := rbacReadUser(t, p, "outside", `{"kind":"department","ngdu_id":2,"department_id":4}`)
	for _, id := range []int64{protected, outside} {
		c := rbacChange(t, r, id)
		c.Profile = employeeInput(1, "Х", nil)
		_, e := r.PreviewAccessChange(ctx, actor, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
		rbacReadErrorKind(t, e, domain.Missing)
	}
	c := rbacChange(t, r, allowed)
	c.Profile = employeeInput(1, "Инженер", &outside)
	out := rbacApply(t, r, actor, domain.AccessChange{User: &c})
	if out.UserView.Profile.ManagerID == nil || *out.UserView.Profile.ManagerID != outside {
		t.Fatal(out)
	}
	for _, manager := range []int64{allowed, 99999999} {
		c.Profile = employeeInput(2, "Х", &manager)
		_, e := r.PreviewAccessChange(ctx, actor, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
		rbacReadErrorKind(t, e, domain.Invalid)
	}
}

func TestEmployeeDirectoryHTTP(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	app := New(testConfig(), p, &fakeML{})
	access, e := service.NewAccess(app.store, true)
	if e != nil {
		t.Fatal(e)
	}
	root, e := access.Bootstrap(ctx, accountInput("admin", "admin"))
	if e != nil {
		t.Fatal(e)
	}
	viewer, e := access.Create(domain.WithPrincipal(ctx, domain.AccountPrincipal(root)), accountInput("reader", "viewer"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `DELETE FROM auth_user_role WHERE user_id=$1;`, viewer.ID); e != nil {
		t.Fatal(e)
	}
	app.http.Access = access
	access.WithRBACMode(domain.RBACEnforce)
	h := app.Handler()
	cookie := loginCookie(t, h, "reader")
	authRequest(t, h, "GET", "/api/v1/people", "", nil, 401)
	safe := authRequest(t, h, "GET", "/api/v1/people", "", cookie, 200)
	if safe.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("directory is cacheable")
	}
	authRequest(t, h, "GET", "/api/v1/auth/users", "", cookie, 403)
	for _, path := range []string{"/api/v1/people?limit=0", "/api/v1/people?limit=", "/api/v1/people?include_inactive=", "/api/v1/people?include_inactive=1", "/api/v1/people?unknown=x", "/api/v1/people?cursor=broken", "/api/v1/people/01", "/api/v1/people/-1"} {
		authRequest(t, h, "GET", path, "", cookie, 400)
	}
	for _, query := range []string{"limit=%GG", "search=%", "cursor=%zz", "include_inactive=%Q0", "%ZZ=value", "limit=1;search=reader", "limit=1&search=reader;other=x"} {
		authRequest(t, h, "GET", "/api/v1/people?"+query, "", cookie, 400)
	}
	if _, e = p.Exec(ctx, `UPDATE auth_user SET job_title='Отдел; %_ +&' WHERE id=$1`, viewer.ID); e != nil {
		t.Fatal(e)
	}
	for _, query := range []string{"search=%D0%9E%D1%82%D0%B4%D0%B5%D0%BB%3B%20%25_%20%2B%26", "search=%3B", "search=%25", "search=%2B", "search=%26"} {
		result := authRequest(t, h, "GET", "/api/v1/people?"+query, "", cookie, 200)
		var page domain.PeoplePage
		if e := json.Unmarshal(result.Body.Bytes(), &page); e != nil || len(page.Items) != 1 || page.Items[0].ID != viewer.ID {
			t.Fatal("encoded literal search lost", query, result.Body.String(), e)
		}
	}
	authRequest(t, h, "GET", "/api/v1/people?search=&cursor=", "", cookie, 200)
	authRequest(t, h, "GET", "/api/v1/people/9999999", "", cookie, 404)
	access.WithRBACMode(domain.RBACCompat)
	h = app.Handler()
	authRequest(t, h, "GET", "/api/v1/people", "", cookie, 200)
	// Shared-key legacy profile cannot open the local employee directory.
	app.http.Access = nil
	h = app.Handler()
	req := httptest.NewRequest("GET", "/api/v1/people", nil)
	req.Header.Set("X-API-Key", app.http.Config.APIKey)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestEmployeeProfileSchemaReadiness(t *testing.T) {
	p := testDB(t)
	r := postgres.New(p)
	ctx := context.Background()
	if e := r.ValidateEmployeeProfiles(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `DELETE FROM backend_schema_migration WHERE version=17`); e != nil {
		t.Fatal(e)
	}
	if e := r.ValidateEmployeeProfiles(ctx); e == nil {
		t.Fatal("schema16 accepted for profile binary")
	}
}

func TestEmployeeDirectoryUnicodeCursorBound(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	viewer := rbacReadUser(t, p, "viewer", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	search := strings.Repeat("😀", 200)
	for _, name := range []string{"first", "second"} {
		id := rbacReadUser(t, p, name, `{"kind":"organization","ngdu_id":null,"department_id":null}`)
		if _, e := p.Exec(ctx, `UPDATE auth_user SET job_title=$1 WHERE id=$2`, search, id); e != nil {
			t.Fatal(e)
		}
	}
	page, e := r.ListPeople(ctx, viewer, domain.PeopleQuery{Search: search, Limit: 1})
	if e != nil || page.NextCursor == nil {
		t.Fatal(page, e)
	}
	if len(*page.NextCursor) > 1024 {
		t.Fatal("server generated unusable cursor", len(*page.NextCursor))
	}
	next, e := r.ListPeople(ctx, viewer, domain.PeopleQuery{Search: search, Limit: 1, Cursor: *page.NextCursor})
	if e != nil || len(next.Items) != 1 {
		t.Fatal(next, e)
	}
}

func TestEmployeeCombinedStaleProfileRollback(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	root := rbacManager(t, p)
	target := rbacReadUser(t, p, "target", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	combined := rbacChange(t, r, target)
	combined.Name = "Must rollback"
	combined.ResetPassword = true
	combined.Profile = employeeInput(1, "Combined", nil)
	preview, e := r.PreviewAccessChange(ctx, root, domain.AccessChange{User: &combined}, domain.AccessPageQuery{})
	if e != nil {
		t.Fatal(e)
	}
	profile := rbacChange(t, r, target)
	profile.Profile = employeeInput(1, "Committed", nil)
	rbacApply(t, r, root, domain.AccessChange{User: &profile})
	hash := "new hash"
	_, e = r.ApplyAccessChange(ctx, root, domain.AccessApply{Change: domain.AccessChange{User: &combined}, GraphRevision: preview.GraphRevision, ActorEpoch: preview.ActorEpoch, Fingerprint: preview.Fingerprint}, &hash)
	rbacReadErrorKind(t, e, domain.Duplicate)
	current, e := r.Account(ctx, target)
	if e != nil || current.Name != "target" || current.Revision != 1 {
		t.Fatal(current, e)
	}
	var title, password string
	if e := p.QueryRow(ctx, `SELECT job_title,password_hash FROM auth_user WHERE id=$1`, target).Scan(&title, &password); e != nil || title != "Committed" || password != "hash" {
		t.Fatal(title, password, e)
	}
}
func TestEmployeeCreateManagerRechecked(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	root := rbacManager(t, p)
	manager := rbacReadUser(t, p, "manager", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	c := domain.UserAccessChange{Login: "created", Name: "Created", Active: true, Organization: domain.Field[domain.OrganizationScope]{Present: true, Value: domain.OrganizationScope{Kind: "organization"}}, Profile: employeeInput(0, "Engineer", &manager)}
	preview, e := r.PreviewAccessChange(ctx, root, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `DELETE FROM auth_user_role WHERE user_id=$1`, manager); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `DELETE FROM auth_user WHERE id=$1`, manager); e != nil {
		t.Fatal(e)
	}
	_, e = r.ApplyAccessChange(ctx, root, domain.AccessApply{Change: domain.AccessChange{User: &c}, GraphRevision: preview.GraphRevision, ActorEpoch: preview.ActorEpoch, Fingerprint: preview.Fingerprint}, nil)
	rbacReadErrorKind(t, e, domain.Invalid)
	var count int
	if e := p.QueryRow(ctx, `SELECT count(*) FROM auth_user WHERE login='created'`).Scan(&count); e != nil || count != 0 {
		t.Fatal(count, e)
	}
}

func TestEmployeeProfileOnlyPreservesWorkerAuthority(t *testing.T) {
	p, ctx, r, checks, v := fileJobSetup(t)
	if _, e := p.Exec(ctx, `UPDATE auth_user SET login='operator',organization_scope='{"kind":"organization","ngdu_id":null,"department_id":null}'; INSERT INTO auth_user_role SELECT 1,id FROM auth_role WHERE builtin_key='technologist'`); e != nil {
		t.Fatal(e)
	}
	auth, e := r.Authorization(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	account, e := r.Account(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	principal := domain.AccountPrincipal(account)
	principal.Authorization = &auth
	ctx = domain.WithRBACMode(domain.WithPrincipal(ctx, principal), domain.RBACEnforce)
	accepted, e := r.AcceptFileJob(ctx, v)
	if e != nil {
		t.Fatal(e)
	}
	var before string
	if e = p.QueryRow(ctx, `SELECT actor_authorization::text FROM file_job WHERE id=$1`, accepted.ID).Scan(&before); e != nil {
		t.Fatal(e)
	}
	root := rbacManager(t, p)
	if _, e := p.Exec(ctx, `INSERT INTO auth_user_role SELECT $1,id FROM auth_role WHERE builtin_key='admin'`, root); e != nil {
		t.Fatal(e)
	}
	c := rbacChange(t, r, 1)
	c.RoleIDs = auth.Assignments.DirectRoleIDs
	c.Profile = employeeInput(1, "Инженер", nil)
	out := rbacApply(t, r, root, domain.AccessChange{User: &c})
	if out.SessionEffect != "none" {
		t.Fatal(out)
	}
	worker, e := service.NewFileJobWorker(r, checks, "11111111111111111111111111111111")
	if e != nil {
		t.Fatal(e)
	}
	if e = worker.HandleDelivery(domain.WithRBACMode(context.Background(), domain.RBACEnforce), fileJobMessage(t, p, accepted.ID)); e != nil {
		t.Fatal(e)
	}
	var state, after string
	var results int
	if e = p.QueryRow(ctx, `SELECT state,actor_authorization::text,(SELECT count(*) FROM file_check_result) FROM file_job WHERE id=$1`, accepted.ID).Scan(&state, &after, &results); e != nil || state != "succeeded" || results != 1 || before != after {
		t.Fatal(state, results, before, after, e)
	}
}
