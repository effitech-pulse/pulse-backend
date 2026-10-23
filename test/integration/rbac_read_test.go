package integration

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

func TestRBACGraphRead(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	r := postgres.New(pool)
	var user, role, group int64
	if e := pool.QueryRow(ctx, `INSERT INTO auth_user(id,login,name,password_hash,role,scope,organization_scope) OVERRIDING SYSTEM VALUE VALUES(9007199254740997,'u','u','hash','viewer','{"funds":["operational"]}','{"kind":"organization","ngdu_id":null,"department_id":null}') RETURNING id`).Scan(&user); e != nil {
		t.Fatal(e)
	}
	a, e := r.Authorization(ctx, user)
	if e != nil || a.Permissions == nil || len(a.Permissions) != 0 {
		t.Fatal(a, e)
	}
	if e = pool.QueryRow(ctx, `INSERT INTO auth_role(id,name,revision) OVERRIDING SYSTEM VALUE VALUES(9007199254740993,'read',9) RETURNING id`).Scan(&role); e != nil {
		t.Fatal(e)
	}
	if e = pool.QueryRow(ctx, `INSERT INTO auth_group(id,name) OVERRIDING SYSTEM VALUE VALUES(9007199254740995,'group') RETURNING id`).Scan(&group); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO auth_role_permission VALUES($1,'wells.read')`, role); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO auth_user_role VALUES($1,$2)`, user, role); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO auth_group_role VALUES($1,$2)`, group, role); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO auth_group_member VALUES($1,$2)`, group, user); e != nil {
		t.Fatal(e)
	}
	a, e = r.Authorization(ctx, user)
	if e != nil || len(a.Permissions) != 1 || a.Permissions[0] != domain.Permission("wells.read") || len(a.Sources["wells.read"]) != 2 {
		t.Fatal(a, e)
	}
	sources := a.Sources["wells.read"]
	if a.AccountID != 9007199254740997 || sources[0].RoleID != 9007199254740993 || sources[0].RoleRevision != 9 || sources[0].GroupID != nil || sources[1].GroupID == nil || *sources[1].GroupID != 9007199254740995 || sources[1].GroupName == nil || *sources[1].GroupName != "group" {
		t.Fatal("typed provenance lost", a)
	}

}

// Graph fixtures bypass future mutation APIs so these tests isolate persisted reads.
func rbacReadUser(t *testing.T, pool *pgxpool.Pool, login, organization string, permissions ...string) int64 {
	t.Helper()
	ctx := context.Background()
	var id, role int64
	var persistedOrganization *string
	if organization != "null" {
		persistedOrganization = &organization
	}
	if e := pool.QueryRow(ctx, `INSERT INTO auth_user(login,name,password_hash,role,scope,organization_scope)VALUES($1,$1,'hash','viewer','{"funds":["operational"]}',$2) RETURNING id`, login, persistedOrganization).Scan(&id); e != nil {
		t.Fatal(e)
	}
	if len(permissions) == 0 {
		return id
	}
	if e := pool.QueryRow(ctx, `INSERT INTO auth_role(name)VALUES($1) RETURNING id`, login).Scan(&role); e != nil {
		t.Fatal(e)
	}
	for _, key := range permissions {
		if _, e := pool.Exec(ctx, `INSERT INTO auth_role_permission VALUES($1,$2)`, role, key); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := pool.Exec(ctx, `INSERT INTO auth_user_role VALUES($1,$2)`, id, role); e != nil {
		t.Fatal(e)
	}
	return id
}
func rbacReadErrorKind(t *testing.T, e error, want domain.Kind) {
	t.Helper()
	var de *domain.Error
	if !errors.As(e, &de) || de.Kind != want {
		t.Fatalf("error %v, want %v", e, want)
	}
}

func TestRBACReadBoundary(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	org := `{"kind":"department","ngdu_id":0,"department_id":-1}`
	actor := rbacReadUser(t, p, "manager", org, "users.read", "users.update", "users.scope.assign", "roles.read", "groups.read", "wells.read")
	snapshot, e := r.Authorization(ctx, actor)
	if e != nil || snapshot.Organization == nil || snapshot.Organization.NGDUID == nil || *snapshot.Organization.NGDUID != 0 || snapshot.Organization.DepartmentID == nil || *snapshot.Organization.DepartmentID != -1 {
		t.Fatal("organization decode changed literal IDs", snapshot, e)
	}
	managed := rbacReadUser(t, p, "managed", org, "wells.read")
	privileged := rbacReadUser(t, p, "privileged", org, "access.manage_global")
	foreign := rbacReadUser(t, p, "foreign", `{"kind":"department","ngdu_id":1,"department_id":-1}`, "wells.read")
	none := rbacReadUser(t, p, "none", `{"kind":"none","ngdu_id":null,"department_id":null}`)
	legacy := rbacReadUser(t, p, "legacy", "null")
	page, e := r.ListManagedAccounts(ctx, actor, domain.AccessPageQuery{})
	if e != nil || page.TotalVisible != 2 || len(page.Items) != 2 || page.Items[0].ID != actor || page.Items[1].ID != managed {
		t.Fatal(page, e)
	}
	for _, id := range []int64{privileged, foreign, none, legacy} {
		_, e = r.ManagedAccount(ctx, actor, id)
		rbacReadErrorKind(t, e, domain.Missing)
	}
	if a, e := r.ManagedAccount(ctx, actor, managed); e != nil || a.ID != managed {
		t.Fatal(a, e)
	}
	page, e = r.ListManagedAccounts(ctx, actor, domain.AccessPageQuery{Search: "managed"})
	if e != nil || page.TotalVisible != 1 || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	// Role/group assignability is checked even when membership spans departments.
	var safeRole, unsafeRole, safeGroup, unsafeGroup int64
	if e = p.QueryRow(ctx, `SELECT role_id FROM auth_user_role WHERE user_id=$1`, managed).Scan(&safeRole); e != nil {
		t.Fatal(e)
	}
	if e = p.QueryRow(ctx, `SELECT role_id FROM auth_user_role WHERE user_id=$1`, privileged).Scan(&unsafeRole); e != nil {
		t.Fatal(e)
	}
	if e = p.QueryRow(ctx, `INSERT INTO auth_group(name)VALUES('safe')RETURNING id`).Scan(&safeGroup); e != nil {
		t.Fatal(e)
	}
	if e = p.QueryRow(ctx, `INSERT INTO auth_group(name)VALUES('unsafe')RETURNING id`).Scan(&unsafeGroup); e != nil {
		t.Fatal(e)
	}
	for _, pair := range [][2]int64{{safeGroup, safeRole}, {unsafeGroup, unsafeRole}} {
		if _, e = p.Exec(ctx, `INSERT INTO auth_group_role VALUES($1,$2)`, pair[0], pair[1]); e != nil {
			t.Fatal(e)
		}
	}
	for _, id := range []int64{managed, privileged, foreign} {
		if _, e = p.Exec(ctx, `INSERT INTO auth_group_member VALUES($1,$2)`, safeGroup, id); e != nil {
			t.Fatal(e)
		}
	}
	roles, e := r.ListAccessRoles(ctx, actor, domain.AccessPageQuery{})
	if e != nil || roles.TotalVisible != 3 {
		t.Fatal(roles, e)
	}
	if _, e = r.AccessRole(ctx, actor, unsafeRole); e == nil {
		t.Fatal("privileged role visible")
	}
	if role, e := r.AccessRole(ctx, actor, safeRole); e != nil || role.ID != safeRole {
		t.Fatal(role, e)
	}
	groups, e := r.ListAccessGroups(ctx, actor, domain.AccessPageQuery{})
	if e != nil || groups.TotalVisible != 1 || len(groups.Items) != 1 || groups.Items[0].ID != safeGroup {
		t.Fatal(groups, e)
	}
	_, e = r.AccessGroup(ctx, actor, unsafeGroup)
	rbacReadErrorKind(t, e, domain.Missing)
	members, e := r.AccessGroupMembers(ctx, actor, safeGroup, domain.AccessPageQuery{})
	if e != nil || members.TotalVisible != 1 || len(members.Items) != 1 || members.Items[0].ID != managed {
		t.Fatal(members, e)
	}
	// No actor permission and inactive actors fail before returning graph metadata.
	_, e = r.ListManagedAccounts(ctx, managed, domain.AccessPageQuery{})
	rbacReadErrorKind(t, e, domain.Forbidden)
	if _, e = p.Exec(ctx, `UPDATE auth_user SET active=false WHERE id=$1`, actor); e != nil {
		t.Fatal(e)
	}
	_, e = r.ListAccessGroups(ctx, actor, domain.AccessPageQuery{})
	rbacReadErrorKind(t, e, domain.Unauthorized)
}

func TestRBACReadPagination(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	actor := rbacReadUser(t, p, "global", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "access.manage_global", "users.read", "roles.read", "groups.read")
	for _, name := range []string{"Alice", "Bob", "Carol"} {
		rbacReadUser(t, p, name, `{"kind":"none","ngdu_id":null,"department_id":null}`)
	}
	first, e := r.ListManagedAccounts(ctx, actor, domain.AccessPageQuery{Limit: 2})
	if e != nil || first.TotalVisible != 4 || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatal(first, e)
	}
	second, e := r.ListManagedAccounts(ctx, actor, domain.AccessPageQuery{Limit: 2, Cursor: *first.NextCursor})
	if e != nil || second.TotalVisible != 4 || len(second.Items) != 2 || second.NextCursor != nil || second.Items[0].Login != "Bob" || second.Items[1].Login != "Carol" {
		t.Fatal(second, e)
	}
	_, e = r.ListManagedAccounts(ctx, actor, domain.AccessPageQuery{Cursor: *first.NextCursor, Search: "Bob"})
	rbacReadErrorKind(t, e, domain.Duplicate)
	_, e = r.ListManagedAccounts(ctx, actor, domain.AccessPageQuery{Limit: 101})
	rbacReadErrorKind(t, e, domain.Invalid)
	_, e = r.ListManagedAccounts(ctx, actor, domain.AccessPageQuery{Cursor: "bad!"})
	rbacReadErrorKind(t, e, domain.Invalid)
	_, e = r.ListAccessRoles(ctx, actor, domain.AccessPageQuery{Cursor: *first.NextCursor})
	rbacReadErrorKind(t, e, domain.Duplicate)
	if _, e = p.Exec(ctx, `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=$1`, actor); e != nil {
		t.Fatal(e)
	}
	_, e = r.ListManagedAccounts(ctx, actor, domain.AccessPageQuery{Cursor: *first.NextCursor})
	rbacReadErrorKind(t, e, domain.Duplicate)
	fresh, e := r.ListAccessRoles(ctx, actor, domain.AccessPageQuery{Limit: 1})
	if e != nil || fresh.NextCursor == nil {
		t.Fatal(fresh, e)
	}
	if _, e = p.Exec(ctx, `UPDATE auth_rbac_state SET revision=revision+1`); e != nil {
		t.Fatal(e)
	}
	_, e = r.ListAccessRoles(ctx, actor, domain.AccessPageQuery{Cursor: *fresh.NextCursor})
	rbacReadErrorKind(t, e, domain.Duplicate)
	_, e = r.ListManagedAccounts(ctx, actor, domain.AccessPageQuery{GraphRevision: &first.GraphRevision})
	rbacReadErrorKind(t, e, domain.Duplicate)
}

func TestRBACOrganizationOptions(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	if _, e := p.Exec(ctx, `INSERT INTO ngdu(id,name)VALUES(0,'zero'),(1,'one'); INSERT INTO department(id,name)VALUES(-1,'negative'),(2,'two'),(3,'three'); INSERT INTO well(ngdu,department)VALUES(0,2),(1,3)`); e != nil {
		t.Fatal(e)
	}
	permissions := []string{"users.read", "users.update", "users.scope.assign"}
	actor := rbacReadUser(t, p, "department", `{"kind":"department","ngdu_id":0,"department_id":-1}`, permissions...)
	out, e := r.OrganizationOptions(ctx, actor)
	if e != nil || len(out.NGDUs) != 1 || out.NGDUs[0].ID != 0 || len(out.Departments) != 1 || out.Departments[0].ID != -1 || len(out.Pairs) != 0 {
		t.Fatal(out, e)
	} // Existing pair without wells remains an option, not an invented observed pair.
	ngdu := rbacReadUser(t, p, "ngdu", `{"kind":"ngdu","ngdu_id":0,"department_id":null}`, permissions...)
	out, e = r.OrganizationOptions(ctx, ngdu)
	if e != nil || len(out.NGDUs) != 1 || len(out.Departments) != 1 || out.Departments[0].ID != 2 || len(out.Pairs) != 1 || out.Pairs[0].NGDUID != 0 {
		t.Fatal(out, e)
	}
	global := rbacReadUser(t, p, "global", `{"kind":"department","ngdu_id":0,"department_id":-1}`, append(permissions, "access.manage_global")...)
	out, e = r.OrganizationOptions(ctx, global)
	if e != nil || len(out.NGDUs) != 2 || len(out.Departments) != 3 || len(out.Pairs) != 2 {
		t.Fatal(out, e)
	}
}

func TestRBACReadSessionEpoch(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	id := rbacReadUser(t, p, "session", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	if e := r.CreateSession(ctx, id, 1, "new", time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	if _, e := r.SessionAccountEnforced(ctx, "new"); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `INSERT INTO auth_session(token_hash,user_id,expires_at)VALUES('old',$1,now()+interval '1 hour')`, id); e != nil {
		t.Fatal(e)
	}
	if _, e := r.SessionAccount(ctx, "old"); e != nil {
		t.Fatal(e)
	}
	_, e := r.SessionAccountEnforced(ctx, "old")
	rbacReadErrorKind(t, e, domain.Missing)
	if _, e = p.Exec(ctx, `UPDATE auth_user SET authz_epoch=2 WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if _, e = r.SessionAccount(ctx, "new"); e != nil {
		t.Fatal("compat session changed", e)
	}
	_, e = r.SessionAccountEnforced(ctx, "new")
	rbacReadErrorKind(t, e, domain.Missing)
}
