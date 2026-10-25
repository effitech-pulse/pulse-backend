package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestOrganizationTreeSearchInactive(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	viewer := rbacReadUser(t, p, "reader", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	if _, e := p.Exec(ctx, `INSERT INTO auth_user(login,name,password_hash,role,scope,job_title,workplace_organization,workplace_division,workplace_department,active) VALUES('active','Matched name','hash','viewer','{"funds":[]}', 'Engineer','Org','Same','Department',true),('inactive','Other name','hash','viewer','{"funds":[]}','Literal %_\','Org','Other','Department',false)`); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		q     domain.OrganizationTreeQuery
		count int
	}{{domain.OrganizationTreeQuery{Search: "Org"}, 1}, {domain.OrganizationTreeQuery{Search: "Org", IncludeInactive: true}, 2}, {domain.OrganizationTreeQuery{Search: `%_\`, IncludeInactive: true}, 1}, {domain.OrganizationTreeQuery{Search: "Matched"}, 1}, {domain.OrganizationTreeQuery{Search: "missing"}, 0}} {
		tree, e := r.OrganizationTreePeople(ctx, viewer, tc.q)
		if e != nil || len(tree) != tc.count {
			t.Fatal(tc, tree, e)
		}
		built := service.BuildOrganizationTree(tree)
		if built.EmployeeCount != tc.count {
			t.Fatal(built)
		}
	}
}
func TestOrganizationTreeSafeHTTP(t *testing.T) {
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
	reader, e := a.Create(domain.WithPrincipal(ctx, domain.AccountPrincipal(admin)), accountInput("reader", "viewer"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `DELETE FROM auth_user_role WHERE user_id=$1`, reader.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `INSERT INTO auth_user(id,login,name,password_hash,role,scope,workplace_department) OVERRIDING SYSTEM VALUE VALUES(9007199254740993,'exact','Точный ID','hash','viewer','{"funds":[]}','Отдел')`); e != nil {
		t.Fatal(e)
	}
	app.http.Access = a
	h := app.Handler()
	cookie := loginCookie(t, h, "reader")
	for _, mode := range []domain.RBACMode{domain.RBACCompat, domain.RBACEnforce} {
		a.WithRBACMode(mode)
		h = app.Handler()
		response := authRequest(t, h, "GET", "/api/v1/people/organization/tree", "", cookie, 200)
		if response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), `"id":"9007199254740993"`) {
			t.Fatal(response.Body.String())
		}
		var tree domain.OrganizationTree
		if e = json.Unmarshal(response.Body.Bytes(), &tree); e != nil || !tree.Complete || tree.EmployeeCount != 3 {
			t.Fatal(tree, e)
		}
		for _, key := range []string{"login", "role", "roles", "scope", "manager", "workplace", "revision", "authz_epoch", "password", "last_login", "permissions", "created_at"} {
			if strings.Contains(response.Body.String(), `"`+key+`"`) {
				t.Fatal("unsafe field", key, response.Body.String())
			}
		}
	}
	authRequest(t, h, "GET", "/api/v1/people/organization/tree", "", nil, 401)
	for _, query := range []string{"search=%GG", "search=%FF", "search=a;b", "search=x&search=y", "include_inactive=1", "limit=1", "cursor="} {
		response := authRequest(t, h, "GET", "/api/v1/people/organization/tree?"+query, "", cookie, 400)
		if response.Body.String() != `{"code":"invalid","detail":null,"message":"Некорректный запрос структуры"}` {
			t.Fatal(response.Body.String())
		}
	}
	auth, e := app.store.Authorization(ctx, reader.ID)
	if e != nil {
		t.Fatal(e)
	}
	principal := domain.AccountPrincipal(reader)
	principal.Authorization = &auth
	stale := domain.WithPrincipal(ctx, principal)
	if _, e = p.Exec(ctx, `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=$1`, reader.ID); e != nil {
		t.Fatal(e)
	}
	_, e = a.OrganizationTree(stale, domain.OrganizationTreeQuery{})
	rbacReadErrorKind(t, e, domain.Unauthorized)
	authRequest(t, h, "GET", "/api/v1/people/organization/tree", "", cookie, 401)
	app.http.Access = nil
	h = app.Handler()
	req := httptest.NewRequest("GET", "/api/v1/people/organization/tree", nil)
	req.Header.Set("X-API-Key", app.http.Config.APIKey)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestOrganizationTreeMatchedLimit(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	actor := rbacReadUser(t, p, "reader", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	if _, e := p.Exec(ctx, `INSERT INTO auth_user(login,name,password_hash,role,scope,workplace_organization) SELECT 'bulk-'||i,'Person'||i,'hash','viewer','{"funds":[]}',CASE WHEN i=5001 THEN 'Overflow' ELSE 'Matched' END FROM generate_series(1,5001) AS i`); e != nil {
		t.Fatal(e)
	}
	rows, e := r.OrganizationTreePeople(ctx, actor, domain.OrganizationTreeQuery{Search: "Matched"})
	if e != nil || len(rows) != 5000 {
		t.Fatal(len(rows), e)
	}
	_, e = r.OrganizationTreePeople(ctx, actor, domain.OrganizationTreeQuery{})
	var problem *domain.Error
	if !errors.As(e, &problem) || problem.Kind != domain.Unavailable || problem.Message != "organization_tree_too_large" {
		t.Fatal(e)
	}
}

func TestOrganizationTreeSnapshot(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	schema := p.Config().ConnConfig.RuntimeParams["search_path"]
	if !strings.HasPrefix(schema, "pulse_test_") || schema == "public" {
		t.Fatal("snapshot fixture requires its generated disposable test schema")
	}
	ns := pgx.Identifier{schema}.Sanitize()
	actor := rbacReadUser(t, p, "reader", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	target := rbacReadUser(t, p, "transfer", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	if _, e := p.Exec(ctx, `UPDATE auth_user SET workplace_organization='Old',workplace_division='Division',workplace_department='Department' WHERE id=$1`, target); e != nil {
		t.Fatal(e)
	}
	// The actor SELECT pauses after establishing its RR snapshot, so the
	// concurrent transfer commits before the employee SELECT begins.
	// All fixture DDL is qualified to the unique testDB schema. testDB registers
	// DROP SCHEMA CASCADE; this additional cleanup restores its table first.
	key := time.Now().UnixNano()
	ddl := fmt.Sprintf(`ALTER TABLE %s.auth_user RENAME TO organization_snapshot_users;
 CREATE FUNCTION %s.organization_snapshot_barrier(account bigint) RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$ BEGIN IF account=%d THEN PERFORM pg_catalog.pg_advisory_xact_lock(%d); END IF; RETURN true; END $$;
 CREATE VIEW %s.auth_user AS SELECT * FROM %s.organization_snapshot_users WHERE %s.organization_snapshot_barrier(id)`, ns, ns, actor, key, ns, ns, ns)
	if _, e := p.Exec(ctx, ddl); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_, err := p.Exec(context.Background(), fmt.Sprintf(`DROP VIEW %s.auth_user; ALTER TABLE %s.organization_snapshot_users RENAME TO auth_user; DROP FUNCTION %s.organization_snapshot_barrier(bigint)`, ns, ns, ns))
		if err != nil {
			t.Errorf("restore disposable snapshot fixture: %v", err)
		}
	})
	holder, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer holder.Rollback(ctx)
	var holderPID int
	if e = holder.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); e != nil {
		t.Fatal(e)
	}
	if _, e = holder.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, key); e != nil {
		t.Fatal(e)
	}
	type result struct {
		rows []domain.OrganizationTreePerson
		err  error
	}
	done := make(chan result, 1)
	go func() {
		rows, err := r.OrganizationTreePeople(ctx, actor, domain.OrganizationTreeQuery{Search: "Old"})
		done <- result{rows, err}
	}()
	until := time.Now().Add(3 * time.Second)
	blocked := false
	for time.Now().Before(until) {
		if e = p.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event='advisory' AND $1=ANY(pg_blocking_pids(pid)))`, holderPID).Scan(&blocked); e != nil {
			t.Fatal(e)
		}
		if blocked {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !blocked {
		t.Fatal("snapshot SELECT did not reach synchronized barrier")
	}
	if _, e = holder.Exec(ctx, fmt.Sprintf(`UPDATE %s.organization_snapshot_users SET workplace_organization='New',workplace_division='Other',workplace_department=NULL WHERE id=$1`, ns), target); e != nil {
		t.Fatal(e)
	}
	if e = holder.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	got := <-done
	if got.err != nil || len(got.rows) != 1 || got.rows[0].Workplace.Organization == nil || *got.rows[0].Workplace.Organization != "Old" || *got.rows[0].Workplace.Division != "Division" {
		t.Fatal(got.rows, got.err)
	}
	tree := service.BuildOrganizationTree(got.rows)
	if tree.EmployeeCount != 1 || tree.Organizations[0].EmployeeCount != 1 {
		t.Fatal(tree)
	}
	fresh, e := r.OrganizationTreePeople(ctx, actor, domain.OrganizationTreeQuery{Search: "New"})
	if e != nil || len(fresh) != 1 || *fresh[0].Workplace.Organization != "New" || fresh[0].Workplace.Department != nil {
		t.Fatal(fresh, e)
	}
}

func TestOrganizationTreeDeadline(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	actor := rbacReadUser(t, p, "reader", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	cfg := p.Config()
	cfg.MaxConns = 1
	limited, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer limited.Close()
	held, e := limited.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Release()
	bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, e = postgres.New(limited).OrganizationTreePeople(bounded, actor, domain.OrganizationTreeQuery{})
	var problem *domain.Error
	if !errors.As(e, &problem) || problem.Kind != domain.Unavailable || problem.Message != "organization_tree_timeout" || problem.Cause != nil {
		t.Fatal(e)
	}
	if time.Since(started) > time.Second {
		t.Fatal("connection acquisition escaped caller deadline")
	}
}
