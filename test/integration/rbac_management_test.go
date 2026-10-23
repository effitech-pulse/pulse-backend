package integration

import (
	"context"
	"errors"
	"fmt"
	"github.com/effitech-pulse/pulse-backend/internal/observability"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

type rbacManagement interface {
	PreviewAccessChange(context.Context, int64, domain.AccessChange, domain.AccessPageQuery) (domain.AccessPreview, error)
	ApplyAccessChange(context.Context, int64, domain.AccessApply, *string) (domain.AccessApplyResult, error)
}

func TestRBACDelegatedPrivilegedTarget(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	m, ok := any(r).(rbacManagement)
	if !ok {
		t.Fatal("preview/apply management store is not implemented")
	}
	org := `{"kind":"department","ngdu_id":1,"department_id":3}`
	actor := rbacReadUser(t, p, "leader", org, "users.read", "users.update", "roles.read", "groups.read", "users.roles.assign", "users.groups.assign", "users.scope.assign")
	target := rbacReadUser(t, p, "privileged", org, "access.manage_global")
	if e := r.CreateSession(ctx, target, 1, "privileged-session", time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	a, e := r.Account(ctx, target)
	if e != nil {
		t.Fatal(e)
	}
	for _, mutation := range []string{"password", "active", "name", "roles", "groups", "scope"} {
		t.Run(mutation, func(t *testing.T) {
			c := domain.UserAccessChange{ID: &target, Login: a.Login, Name: a.Name, Active: true, Revision: a.Revision}
			switch mutation {
			case "password":
				c.ResetPassword = true
			case "active":
				c.Active = false
			case "name":
				c.Name = "captured"
			case "scope":
				c.Organization = domain.Field[domain.OrganizationScope]{Present: true, Value: domain.OrganizationScope{Kind: "organization"}}
			}
			_, e = m.PreviewAccessChange(ctx, actor, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
			rbacReadErrorKind(t, e, domain.Missing)
		})
	}
	var events int
	if e = p.QueryRow(ctx, "SELECT count(*) FROM auth_access_event").Scan(&events); e != nil || events != 0 {
		t.Fatal(events, e)
	}
	if _, e := r.SessionAccountEnforced(ctx, "privileged-session"); e != nil {
		t.Fatal(e)
	}
	var hash string
	if e := p.QueryRow(ctx, "SELECT password_hash FROM auth_user WHERE id=$1", target).Scan(&hash); e != nil || hash != "hash" {
		t.Fatal(hash, e)
	}
	current, e := r.Account(ctx, target)
	if e != nil || current.Revision != a.Revision || !current.Active || current.Name != a.Name {
		t.Fatal(current, e)
	}
}

func rbacManager(t *testing.T, p *pgxpool.Pool) int64 {
	t.Helper()
	return rbacReadUser(t, p, "root", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "users.read", "users.create", "users.update", "users.scope.assign", "users.roles.assign", "users.groups.assign", "roles.read", "roles.manage", "groups.read", "groups.manage", "access.manage_global", "wells.read")
}
func rbacChange(t *testing.T, r *postgres.Repository, id int64) domain.UserAccessChange {
	t.Helper()
	a, e := r.Account(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	return domain.UserAccessChange{ID: &id, Login: a.Login, Name: a.Name, Active: a.Active, Revision: a.Revision}
}
func rbacApply(t *testing.T, r *postgres.Repository, actor int64, c domain.AccessChange) domain.AccessApplyResult {
	t.Helper()
	ctx := context.Background()
	preview, e := r.PreviewAccessChange(ctx, actor, c, domain.AccessPageQuery{})
	if e != nil {
		t.Fatal(e)
	}
	out, e := r.ApplyAccessChange(ctx, actor, domain.AccessApply{Change: c, GraphRevision: preview.GraphRevision, ActorEpoch: preview.ActorEpoch, Fingerprint: preview.Fingerprint}, nil)
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func TestRBACDelegatedTransfer(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	root := rbacManager(t, p)
	if _, e := p.Exec(ctx, `INSERT INTO ngdu(id,name)VALUES(1,'one'),(2,'two');INSERT INTO department(id,name)VALUES(3,'three'),(4,'four')`); e != nil {
		t.Fatal(e)
	}
	keys := []string{"users.read", "users.update", "users.scope.assign"}
	head := rbacReadUser(t, p, "ngdu", `{"kind":"ngdu","ngdu_id":1,"department_id":null}`, keys...)
	local := rbacReadUser(t, p, "workshop", `{"kind":"department","ngdu_id":1,"department_id":3}`, keys...)
	target := rbacReadUser(t, p, "employee", `{"kind":"department","ngdu_id":1,"department_id":3}`)
	c := rbacChange(t, r, target)
	ngdu, dep := int64(1), int64(4)
	c.Organization = domain.Field[domain.OrganizationScope]{Present: true, Value: domain.OrganizationScope{Kind: "department", NGDUID: &ngdu, DepartmentID: &dep}}
	_, e := r.PreviewAccessChange(ctx, local, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
	rbacReadErrorKind(t, e, domain.Forbidden)
	preview, e := r.PreviewAccessChange(ctx, head, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
	if e != nil || preview.ChangedUsers != 1 {
		t.Fatal(preview, e)
	}
	rbacApply(t, r, head, domain.AccessChange{User: &c})
	_, e = r.ManagedAccount(ctx, local, target)
	rbacReadErrorKind(t, e, domain.Missing)
	c = rbacChange(t, r, target)
	ngdu = 2
	c.Organization = domain.Field[domain.OrganizationScope]{Present: true, Value: domain.OrganizationScope{Kind: "department", NGDUID: &ngdu, DepartmentID: &dep}}
	_, e = r.PreviewAccessChange(ctx, head, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
	rbacReadErrorKind(t, e, domain.Forbidden)
	rbacApply(t, r, root, domain.AccessChange{User: &c})
}
func TestRBACGrantContainment(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	root := rbacManager(t, p)
	actor := rbacReadUser(t, p, "local", `{"kind":"department","ngdu_id":1,"department_id":3}`, "users.read", "users.update", "roles.read", "groups.read", "users.roles.assign", "users.groups.assign")
	var role int64
	if e := p.QueryRow(ctx, "SELECT role_id FROM auth_user_role WHERE user_id=$1", root).Scan(&role); e != nil {
		t.Fatal(e)
	}
	c := rbacChange(t, r, actor)
	c.RoleIDs = []int64{role}
	_, e := r.PreviewAccessChange(ctx, actor, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
	rbacReadErrorKind(t, e, domain.Forbidden)
	group := rbacApply(t, r, root, domain.AccessChange{Group: &domain.GroupAccessChange{Name: "privileged", RoleIDs: []int64{role}}}).Group
	c = rbacChange(t, r, actor)
	c.GroupIDs = []int64{group.ID}
	_, e = r.PreviewAccessChange(ctx, actor, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
	rbacReadErrorKind(t, e, domain.Forbidden)
	_, e = r.PreviewAccessChange(ctx, actor, domain.AccessChange{Group: &domain.GroupAccessChange{Name: "local definitions"}}, domain.AccessPageQuery{})
	rbacReadErrorKind(t, e, domain.Forbidden)
}
func TestRBACPreviewStaleRole(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	actor := rbacManager(t, p)
	role := rbacApply(t, r, actor, domain.AccessChange{Role: &domain.RoleAccessChange{Name: "read", PermissionKeys: []domain.Permission{"wells.read"}}}).Role
	group := rbacApply(t, r, actor, domain.AccessChange{Group: &domain.GroupAccessChange{Name: "team"}}).Group
	change := domain.AccessChange{Group: &domain.GroupAccessChange{ID: &group.ID, Revision: group.Revision, Name: group.Name, RoleIDs: []int64{role.ID}}}
	preview, e := r.PreviewAccessChange(ctx, actor, change, domain.AccessPageQuery{})
	if e != nil {
		t.Fatal(e)
	}
	var events, revision int64
	if e = p.QueryRow(ctx, "SELECT (SELECT count(*) FROM auth_access_event),revision FROM auth_rbac_state").Scan(&events, &revision); e != nil {
		t.Fatal(e)
	}
	repeat, e := r.PreviewAccessChange(ctx, actor, change, domain.AccessPageQuery{})
	if e != nil || repeat.Fingerprint != preview.Fingerprint {
		t.Fatal(repeat, e)
	}
	var afterEvents, afterRevision int64
	if e = p.QueryRow(ctx, "SELECT (SELECT count(*) FROM auth_access_event),revision FROM auth_rbac_state").Scan(&afterEvents, &afterRevision); e != nil || events != afterEvents || revision != afterRevision {
		t.Fatal("preview wrote", e)
	}
	rbacApply(t, r, actor, domain.AccessChange{Role: &domain.RoleAccessChange{ID: &role.ID, Revision: role.Revision, Name: "changed", PermissionKeys: []domain.Permission{}}})
	_, e = r.ApplyAccessChange(ctx, actor, domain.AccessApply{Change: change, GraphRevision: preview.GraphRevision, ActorEpoch: preview.ActorEpoch, Fingerprint: preview.Fingerprint}, nil)
	rbacReadErrorKind(t, e, domain.Duplicate)
	fresh, e := r.PreviewAccessChange(ctx, actor, change, domain.AccessPageQuery{})
	if e != nil {
		t.Fatal(e)
	}
	change.Group.Name = "tampered"
	_, e = r.ApplyAccessChange(ctx, actor, domain.AccessApply{Change: change, GraphRevision: fresh.GraphRevision, ActorEpoch: fresh.ActorEpoch, Fingerprint: fresh.Fingerprint}, nil)
	rbacReadErrorKind(t, e, domain.Duplicate)
	current, e := r.AccessGroup(ctx, actor, group.ID)
	if e != nil || current.Revision != 1 || len(current.RoleIDs) != 0 {
		t.Fatal(current, e)
	}
}
func TestRBACUnionSources(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	actor := rbacManager(t, p)
	user := rbacReadUser(t, p, "union", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "wells.read")
	var role int64
	if e := p.QueryRow(ctx, "SELECT role_id FROM auth_user_role WHERE user_id=$1", user).Scan(&role); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"first", "second"} {
		rbacApply(t, r, actor, domain.AccessChange{Group: &domain.GroupAccessChange{Name: name, RoleIDs: []int64{role}, MemberIDs: []int64{user}}})
	}
	groups, e := r.ListAccessGroups(ctx, actor, domain.AccessPageQuery{})
	if e != nil {
		t.Fatal(e)
	}
	c := rbacChange(t, r, user)
	for _, g := range groups.Items {
		c.GroupIDs = append(c.GroupIDs, g.ID)
	}
	rbacApply(t, r, actor, domain.AccessChange{User: &c})
	a, e := r.Authorization(ctx, user)
	if e != nil || a.Require("wells.read") != nil || len(a.Sources["wells.read"]) != 2 {
		t.Fatal(a, e)
	}
	c = rbacChange(t, r, user)
	c.GroupIDs = []int64{groups.Items[1].ID}
	rbacApply(t, r, actor, domain.AccessChange{User: &c})
	a, e = r.Authorization(ctx, user)
	if e != nil || a.Require("wells.read") != nil || len(a.Sources["wells.read"]) != 1 {
		t.Fatal(a, e)
	}
}
func TestRBACLastManagerConcurrent(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	a := rbacManager(t, p)
	var role int64
	if e := p.QueryRow(ctx, "SELECT role_id FROM auth_user_role WHERE user_id=$1", a).Scan(&role); e != nil {
		t.Fatal(e)
	}
	b := rbacReadUser(t, p, "other", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	if _, e := p.Exec(ctx, "INSERT INTO auth_user_role VALUES($1,$2)", b, role); e != nil {
		t.Fatal(e)
	}
	changes := []domain.AccessChange{{User: ptr(rbacChange(t, r, b))}, {User: ptr(rbacChange(t, r, a))}}
	for _, c := range changes {
		c.User.Active = false
		c.User.RoleIDs = []int64{role}
	}
	actors := []int64{a, b}
	receipts := []domain.AccessApply{}
	for i, c := range changes {
		v, e := r.PreviewAccessChange(ctx, actors[i], c, domain.AccessPageQuery{})
		if e != nil {
			t.Fatal(e)
		}
		receipts = append(receipts, domain.AccessApply{Change: c, GraphRevision: v.GraphRevision, ActorEpoch: v.ActorEpoch, Fingerprint: v.Fingerprint})
	}
	results := make(chan error, 2)
	for i := range actors {
		go func(i int) { _, e := r.ApplyAccessChange(ctx, actors[i], receipts[i], nil); results <- e }(i)
	}
	success := 0
	for range actors {
		if e := <-results; e == nil {
			success++
		} else {
			var de *domain.Error
			if !errors.As(e, &de) || (de.Kind != domain.Unauthorized && de.Kind != domain.Forbidden && de.Kind != domain.Duplicate) {
				t.Fatal(e)
			}
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
	var survivor int64
	if e := p.QueryRow(ctx, "SELECT id FROM auth_user WHERE active").Scan(&survivor); e != nil {
		t.Fatal(e)
	}
	g := rbacApply(t, r, survivor, domain.AccessChange{Group: &domain.GroupAccessChange{Name: "last", RoleIDs: []int64{role}, MemberIDs: []int64{survivor}}}).Group
	c := rbacChange(t, r, survivor)
	c.GroupIDs = []int64{g.ID}
	rbacApply(t, r, survivor, domain.AccessChange{User: &c})
	_, e := r.PreviewAccessChange(ctx, survivor, domain.AccessChange{Group: &domain.GroupAccessChange{ID: &g.ID, Revision: g.Revision, Delete: true}}, domain.AccessPageQuery{})
	rbacReadErrorKind(t, e, domain.Duplicate)
}
func TestRBACInvalidation(t *testing.T) {
	p := testDB(t)
	ctx := observability.WithRequestID(context.Background(), "rbac_mutation_123456")
	r := postgres.New(p)
	actor := rbacManager(t, p)
	target := rbacReadUser(t, p, "member", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	other := rbacReadUser(t, p, "untouched", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	for _, id := range []int64{target, other} {
		if e := r.CreateSession(ctx, id, 1, fmt.Sprint(id), time.Now().Add(time.Hour)); e != nil {
			t.Fatal(e)
		}
	}
	c := rbacChange(t, r, target)
	c.Name = "renamed"
	preview, e := r.PreviewAccessChange(ctx, actor, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
	if e != nil {
		t.Fatal(e)
	}
	_, e = r.ApplyAccessChange(ctx, actor, domain.AccessApply{Change: domain.AccessChange{User: &c}, GraphRevision: preview.GraphRevision, ActorEpoch: preview.ActorEpoch, Fingerprint: preview.Fingerprint}, nil)
	if e != nil {
		t.Fatal(e)
	}
	a, e := r.Authorization(ctx, target)
	if e != nil || a.Epoch != 2 {
		t.Fatal(a, e)
	}
	a, e = r.Authorization(ctx, other)
	if e != nil || a.Epoch != 1 {
		t.Fatal(a, e)
	}
	_, e = r.SessionAccountEnforced(ctx, fmt.Sprint(target))
	rbacReadErrorKind(t, e, domain.Missing)
	if _, e = r.SessionAccountEnforced(ctx, fmt.Sprint(other)); e != nil {
		t.Fatal(e)
	}
	var raw, request string
	if e = p.QueryRow(ctx, "SELECT actor_roles::text||before_state::text||after_state::text,request_id FROM auth_access_event ORDER BY id DESC LIMIT 1").Scan(&raw, &request); e != nil {
		t.Fatal(e)
	}
	if request != "rbac_mutation_123456" || strings.Contains(raw, "password") || strings.Contains(raw, "token") || strings.Contains(raw, "hash") || !strings.Contains(raw, "role_ids") {
		t.Fatal(raw, request)
	}
}

func TestRBACEmptyAssignmentsAndMetadata(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	actor := rbacManager(t, p)
	user := rbacReadUser(t, p, "empty", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	role := rbacApply(t, r, actor, domain.AccessChange{Role: &domain.RoleAccessChange{Name: "empty role"}}).Role
	group := rbacApply(t, r, actor, domain.AccessChange{Group: &domain.GroupAccessChange{Name: "empty group"}}).Group
	c := rbacChange(t, r, user)
	c.RoleIDs = []int64{role.ID}
	c.GroupIDs = []int64{group.ID}
	preview, e := r.PreviewAccessChange(ctx, actor, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
	if e != nil || preview.AssignedUsers != 1 || preview.ChangedUsers != 0 || len(preview.Items) != 1 {
		t.Fatal(preview, e)
	}
	item := preview.Items[0]
	if len(item.Before.Assignments.DirectRoleIDs) != 0 || len(item.Before.Assignments.Groups) != 0 || len(item.After.Assignments.DirectRoleIDs) != 1 || len(item.After.Assignments.Groups) != 1 || len(item.After.Assignments.Roles) != 1 || len(item.After.Permissions) != 0 {
		t.Fatal(item)
	}
	out := rbacApply(t, r, actor, domain.AccessChange{User: &c})
	if out.AffectedUsers != 1 || out.UserView == nil || out.UserView.Authorization.Epoch != 2 || out.UserView.Account.Revision != 2 || len(out.UserView.Authorization.Assignments.Groups) != 1 || out.UserView.Authorization.Assignments.Groups[0].RoleIDs == nil {
		t.Fatal(out)
	}
	for _, change := range []domain.AccessChange{
		{Role: &domain.RoleAccessChange{ID: &role.ID, Revision: role.Revision, Name: "renamed role"}},
		{Group: &domain.GroupAccessChange{ID: &group.ID, Revision: group.Revision, Name: "renamed group", MemberIDs: []int64{user}}},
	} {
		preview, e = r.PreviewAccessChange(ctx, actor, change, domain.AccessPageQuery{})
		if e != nil || preview.AssignedUsers != 1 || preview.ChangedUsers != 0 || len(preview.Items) != 1 {
			t.Fatal(preview, e)
		}
		out = rbacApply(t, r, actor, change)
		if out.AffectedUsers != 0 {
			t.Fatal(out)
		}
		a, e := r.Authorization(ctx, user)
		if e != nil || a.Epoch != 2 {
			t.Fatal(a, e)
		}
	}
	c = rbacChange(t, r, user)
	c.RoleIDs = []int64{role.ID}
	preview, e = r.PreviewAccessChange(ctx, actor, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
	if e != nil || preview.AssignedUsers != 1 || preview.ChangedUsers != 0 || len(preview.Items[0].Before.Assignments.Groups) != 1 || len(preview.Items[0].After.Assignments.Groups) != 0 {
		t.Fatal(preview, e)
	}
	rbacApply(t, r, actor, domain.AccessChange{User: &c})
}

func TestRBACCanonicalReceipt(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	actor := rbacManager(t, p)
	a := domain.AccessChange{Group: &domain.GroupAccessChange{Name: "empty"}}
	b := domain.AccessChange{Group: &domain.GroupAccessChange{Name: "empty", RoleIDs: []int64{}, MemberIDs: []int64{}}}
	first, e := r.PreviewAccessChange(ctx, actor, a, domain.AccessPageQuery{})
	if e != nil {
		t.Fatal(e)
	}
	second, e := r.PreviewAccessChange(ctx, actor, b, domain.AccessPageQuery{})
	if e != nil || first.Fingerprint != second.Fingerprint {
		t.Fatal("nil and empty set receipts differ", first.Fingerprint, second.Fingerprint, e)
	}
	_, e = r.ApplyAccessChange(ctx, actor, domain.AccessApply{Change: b, GraphRevision: first.GraphRevision, ActorEpoch: first.ActorEpoch, Fingerprint: first.Fingerprint}, nil)
	if e != nil {
		t.Fatal(e)
	}
}
func TestRBACBootstrapFailClosed(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	if _, e := p.Exec(ctx, "DELETE FROM auth_role_permission WHERE role_id=(SELECT id FROM auth_role WHERE builtin_key='admin') AND permission_key='users.update'"); e != nil {
		t.Fatal(e)
	}
	_, e := r.CreateAccount(ctx, domain.Account{Login: "bootstrap", Name: "bootstrap", Role: "admin", Scope: domain.AccessScope{Funds: []string{"operational", "presentation"}}, Active: true}, "hash", true)
	rbacReadErrorKind(t, e, domain.Duplicate)
	var users int
	if e = p.QueryRow(ctx, "SELECT count(*) FROM auth_user").Scan(&users); e != nil || users != 0 {
		t.Fatal(users, e)
	}
	_, e = r.CreateAccount(ctx, domain.Account{Login: "forged", Name: "forged", Role: "viewer", Active: true}, "hash", false)
	rbacReadErrorKind(t, e, domain.Unauthorized)
	// Only an absent builtin may be explicitly restored by the zero-users bootstrap.
	if _, e = p.Exec(ctx, "DELETE FROM auth_role WHERE builtin_key='admin'"); e != nil {
		t.Fatal(e)
	}
	created, e := r.CreateAccount(ctx, domain.Account{Login: "bootstrap", Name: "bootstrap", Role: "admin", Scope: domain.AccessScope{Funds: []string{"operational", "presentation"}}, Active: true}, "hash", true)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := r.Authorization(ctx, created.ID)
	if e != nil || snapshot.Require("users.update", "roles.manage", "groups.manage") != nil {
		t.Fatal(snapshot, e)
	}
}
func TestRBACRoleDependencyCounts(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	actor := rbacManager(t, p)
	user := rbacReadUser(t, p, "dependent", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "wells.read")
	var role int64
	if e := p.QueryRow(ctx, "SELECT role_id FROM auth_user_role WHERE user_id=$1", user).Scan(&role); e != nil {
		t.Fatal(e)
	}
	rbacApply(t, r, actor, domain.AccessChange{Group: &domain.GroupAccessChange{Name: "one", RoleIDs: []int64{role}, MemberIDs: []int64{user}}})
	rbacApply(t, r, actor, domain.AccessChange{Group: &domain.GroupAccessChange{Name: "two", RoleIDs: []int64{role}, MemberIDs: []int64{user}}})
	_, e := r.PreviewAccessChange(ctx, actor, domain.AccessChange{Role: &domain.RoleAccessChange{ID: &role, Revision: 1, Delete: true}}, domain.AccessPageQuery{})
	rbacReadErrorKind(t, e, domain.Duplicate)
	if !strings.Contains(e.Error(), "users=1") || !strings.Contains(e.Error(), "groups=2") {
		t.Fatal("missing distinct dependency counts", e)
	}
}

func TestRBACInvalidationRoleAndDisabled(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	actor := rbacManager(t, p)
	active := rbacReadUser(t, p, "active", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	disabled := rbacReadUser(t, p, "disabled", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	role := rbacApply(t, r, actor, domain.AccessChange{Role: &domain.RoleAccessChange{Name: "channel", PermissionKeys: []domain.Permission{"wells.read"}}}).Role
	group := rbacApply(t, r, actor, domain.AccessChange{Group: &domain.GroupAccessChange{Name: "both", RoleIDs: []int64{role.ID}, MemberIDs: []int64{active, disabled}}}).Group
	if _, e := p.Exec(ctx, "UPDATE auth_user SET active=false WHERE id=$1", disabled); e != nil {
		t.Fatal(e)
	}
	if e := r.CreateSession(ctx, active, 2, "active-session", time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, "INSERT INTO auth_session(token_hash,user_id,expires_at,authz_epoch)VALUES('disabled-session',$1,now()+interval '1 hour',2)", disabled); e != nil {
		t.Fatal(e)
	}
	change := domain.AccessChange{Role: &domain.RoleAccessChange{ID: &role.ID, Revision: role.Revision, Name: role.Name, PermissionKeys: []domain.Permission{}}}
	preview, e := r.PreviewAccessChange(ctx, actor, change, domain.AccessPageQuery{Limit: 1})
	if e != nil || preview.AssignedUsers != 2 || preview.ChangedUsers != 2 || preview.DisabledUsers != 1 || len(preview.Items) != 1 || preview.NextCursor == nil {
		t.Fatal(preview, e)
	}
	next, e := r.PreviewAccessChange(ctx, actor, change, domain.AccessPageQuery{Limit: 1, Cursor: *preview.NextCursor})
	if e != nil || next.Fingerprint != preview.Fingerprint || next.AssignedUsers != 2 || len(next.Items) != 1 || next.Items[0].User.ID == preview.Items[0].User.ID {
		t.Fatal(next, e)
	}
	result := rbacApply(t, r, actor, change)
	if result.AffectedUsers != 2 {
		t.Fatal(result)
	}
	for _, id := range []int64{active, disabled} {
		a, e := r.Authorization(ctx, id)
		if e != nil || a.Epoch != 3 || len(a.Permissions) != 0 {
			t.Fatal(a, e)
		}
		var sessions int
		if e = p.QueryRow(ctx, "SELECT count(*) FROM auth_session WHERE user_id=$1", id).Scan(&sessions); e != nil || sessions != 0 {
			t.Fatal(sessions, e)
		}
	}
	// Group membership change revokes even when an empty role leaves union unchanged.
	c := rbacChange(t, r, active)
	c.GroupIDs = []int64{group.ID}
	rbacApply(t, r, actor, domain.AccessChange{User: &c})
	var actorRole int64
	if e = p.QueryRow(ctx, "SELECT role_id FROM auth_user_role WHERE user_id=$1", actor).Scan(&actorRole); e != nil {
		t.Fatal(e)
	}
	if e = r.CreateSession(ctx, actor, 1, "self-session", time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	self := rbacChange(t, r, actor)
	self.Name = "renamed manager"
	self.RoleIDs = []int64{actorRole}
	out := rbacApply(t, r, actor, domain.AccessChange{User: &self})
	if out.AffectedUsers != 1 {
		t.Fatal(out)
	}
	_, e = r.SessionAccountEnforced(ctx, "self-session")
	rbacReadErrorKind(t, e, domain.Missing)
}
func TestRBACApplyRollback(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	actor := rbacManager(t, p)
	user := rbacReadUser(t, p, "rollback", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	if e := r.CreateSession(ctx, user, 1, "rollback-session", time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	c := rbacChange(t, r, user)
	c.Name = "uncommitted"
	c.ResetPassword = true
	preview, e := r.PreviewAccessChange(ctx, actor, domain.AccessChange{User: &c}, domain.AccessPageQuery{})
	if e != nil {
		t.Fatal(e)
	}
	var events int
	if e = p.QueryRow(ctx, "SELECT count(*) FROM auth_access_event").Scan(&events); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `CREATE FUNCTION reject_access_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'fixture audit failure'; END$$;CREATE TRIGGER reject_access_audit BEFORE INSERT ON auth_access_event FOR EACH ROW EXECUTE FUNCTION reject_access_audit()`); e != nil {
		t.Fatal(e)
	}
	hash := "credential-must-rollback"
	_, e = r.ApplyAccessChange(ctx, actor, domain.AccessApply{Change: domain.AccessChange{User: &c}, GraphRevision: preview.GraphRevision, ActorEpoch: preview.ActorEpoch, Fingerprint: preview.Fingerprint}, &hash)
	if e == nil {
		t.Fatal("audit failure committed")
	}
	a, e := r.Account(ctx, user)
	if e != nil || a.Name != "rollback" || a.Revision != 1 {
		t.Fatal(a, e)
	}
	snapshot, e := r.Authorization(ctx, user)
	if e != nil || snapshot.Epoch != 1 || snapshot.GraphRevision != preview.GraphRevision {
		t.Fatal(snapshot, e)
	}
	var currentEvents int
	var password string
	if e = p.QueryRow(ctx, "SELECT (SELECT count(*) FROM auth_access_event),password_hash FROM auth_user WHERE id=$1", user).Scan(&currentEvents, &password); e != nil || currentEvents != events || password != "hash" {
		t.Fatal(currentEvents, password, e)
	}
	if _, e = r.SessionAccountEnforced(ctx, "rollback-session"); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, "DROP TRIGGER reject_access_audit ON auth_access_event"); e != nil {
		t.Fatal(e)
	}
	rbacApplyResult, e := r.ApplyAccessChange(ctx, actor, domain.AccessApply{Change: domain.AccessChange{User: &c}, GraphRevision: preview.GraphRevision, ActorEpoch: preview.ActorEpoch, Fingerprint: preview.Fingerprint}, &hash)
	if e != nil || rbacApplyResult.UserView.Authorization.Epoch != 2 {
		t.Fatal(rbacApplyResult, e)
	}
	var audit string
	if e = p.QueryRow(ctx, "SELECT actor_roles::text||before_state::text||after_state::text FROM auth_access_event ORDER BY id DESC LIMIT 1").Scan(&audit); e != nil || strings.Contains(audit, hash) || strings.Contains(audit, "password_hash") {
		t.Fatal(audit, e)
	}
}

func TestRBACLegacyScopePermission(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	rbacManager(t, p)
	permissions := []string{}
	for _, d := range domain.PermissionCatalog() {
		if d.Key != "users.scope.assign" {
			permissions = append(permissions, string(d.Key))
		}
	}
	actor := rbacReadUser(t, p, "without-scope", `{"kind":"organization","ngdu_id":null,"department_id":null}`, permissions...)
	target := rbacReadUser(t, p, "legacy-target", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	if _, e := p.Exec(ctx, "INSERT INTO auth_user_role(user_id,role_id)SELECT $1,id FROM auth_role WHERE builtin_key='viewer'", target); e != nil {
		t.Fatal(e)
	}
	account, e := r.Account(ctx, actor)
	if e != nil {
		t.Fatal(e)
	}
	actorCtx := domain.WithPrincipal(ctx, domain.AccountPrincipal(account))
	old, e := r.Account(ctx, target)
	if e != nil {
		t.Fatal(e)
	}
	changed := old
	changed.Scope.NGDUs = ptr([]int64{1})
	_, e = r.UpdateAccount(actorCtx, changed, nil)
	rbacReadErrorKind(t, e, domain.Forbidden)
	current, e := r.Account(ctx, target)
	if e != nil || current.Revision != old.Revision || current.Scope.NGDUs != nil {
		t.Fatal(current, e)
	}
	// Metadata-only legacy editing remains permitted without scope assignment.
	changed = old
	changed.Name = "permitted name"
	current, e = r.UpdateAccount(actorCtx, changed, nil)
	if e != nil || current.Revision != old.Revision+1 {
		t.Fatal(current, e)
	}
}

func TestRBACAuditAffectedOnly(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	actor := rbacManager(t, p)
	foreign := rbacReadUser(t, p, "unrelated-person", `{"kind":"department","ngdu_id":99,"department_id":98}`)
	role := rbacApply(t, r, actor, domain.AccessChange{Role: &domain.RoleAccessChange{Name: "unrelated-role", PermissionKeys: []domain.Permission{"wells.read"}}}).Role
	rbacApply(t, r, actor, domain.AccessChange{Group: &domain.GroupAccessChange{Name: "unrelated-group", RoleIDs: []int64{role.ID}, MemberIDs: []int64{foreign}}})
	target := rbacReadUser(t, p, "affected", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	change := rbacChange(t, r, target)
	change.Name = "saved"
	rbacApply(t, r, actor, domain.AccessChange{User: &change})
	var payload string
	if e := p.QueryRow(ctx, "SELECT before_state::text||after_state::text FROM auth_access_event ORDER BY id DESC LIMIT 1").Scan(&payload); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(payload, "unrelated-role") || strings.Contains(payload, "unrelated-group") || strings.Contains(payload, "unrelated-person") {
		t.Fatal("unrelated global graph persisted in user audit", payload)
	}
	// A definition-only edit still records the actual changed definition without users.
	rbacApply(t, r, actor, domain.AccessChange{Role: &domain.RoleAccessChange{ID: &role.ID, Revision: 1, Name: "changed-definition", PermissionKeys: role.PermissionKeys}})
	if e := p.QueryRow(ctx, "SELECT before_state::text||after_state::text FROM auth_access_event ORDER BY id DESC LIMIT 1").Scan(&payload); e != nil || !strings.Contains(payload, "unrelated-role") || !strings.Contains(payload, "changed-definition") {
		t.Fatal(payload, e)
	}
}

func TestRBACPreviewSearch(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	actor := rbacManager(t, p)
	first := rbacReadUser(t, p, "alpha-member", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	second := rbacReadUser(t, p, "beta-member", `{"kind":"organization","ngdu_id":null,"department_id":null}`)
	if _, e := p.Exec(ctx, "UPDATE auth_user SET name='ALPHA Name' WHERE id=$1", second); e != nil {
		t.Fatal(e)
	}
	role := rbacApply(t, r, actor, domain.AccessChange{Role: &domain.RoleAccessChange{Name: "search role", PermissionKeys: []domain.Permission{"wells.read"}}}).Role
	group := rbacApply(t, r, actor, domain.AccessChange{Group: &domain.GroupAccessChange{Name: "search group", RoleIDs: []int64{role.ID}, MemberIDs: []int64{actor, first, second}}}).Group
	if _, e := p.Exec(ctx, "UPDATE auth_user SET active=false WHERE id=$1", second); e != nil {
		t.Fatal(e)
	}
	change := domain.AccessChange{Group: &domain.GroupAccessChange{ID: &group.ID, Revision: group.Revision, Delete: true}}
	all, e := r.PreviewAccessChange(ctx, actor, change, domain.AccessPageQuery{})
	if e != nil || len(all.Items) != 3 || all.AssignedUsers != 3 || all.ChangedUsers != 2 || all.DisabledUsers != 1 || !all.SelfAffected {
		t.Fatal(all, e)
	}
	assertStable := func(v domain.AccessPreview) {
		t.Helper()
		if v.Fingerprint != all.Fingerprint || v.GraphRevision != all.GraphRevision || v.ActorEpoch != all.ActorEpoch || v.AssignedUsers != 3 || v.ChangedUsers != 2 || v.DisabledUsers != 1 || !v.SelfAffected {
			t.Fatal("search/page changed receipt or impact counts", v)
		}
	}
	page, e := r.PreviewAccessChange(ctx, actor, change, domain.AccessPageQuery{Search: "aLpHa", Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	assertStable(page)
	if len(page.Items) != 1 || page.Items[0].User.ID != first || page.NextCursor == nil {
		t.Fatal("search must filter login/name before pagination", page)
	}
	next, e := r.PreviewAccessChange(ctx, actor, change, domain.AccessPageQuery{Search: "aLpHa", Limit: 1, Cursor: *page.NextCursor})
	if e != nil {
		t.Fatal(e)
	}
	assertStable(next)
	if len(next.Items) != 1 || next.Items[0].User.ID != second || next.NextCursor != nil {
		t.Fatal("second matching name page missing", next)
	}
	empty, e := r.PreviewAccessChange(ctx, actor, change, domain.AccessPageQuery{Search: "no-match", Limit: 1})
	if e != nil {
		t.Fatal(e)
	}
	assertStable(empty)
	if empty.Items == nil || len(empty.Items) != 0 || empty.NextCursor != nil {
		t.Fatal("unmatched search returned impacts", empty)
	}
	_, e = r.PreviewAccessChange(ctx, actor, change, domain.AccessPageQuery{Search: "ALPHA", Limit: 1, Cursor: *page.NextCursor})
	rbacReadErrorKind(t, e, domain.Duplicate)
	if _, e = p.Exec(ctx, "UPDATE auth_rbac_state SET revision=revision+1"); e != nil {
		t.Fatal(e)
	}
	_, e = r.PreviewAccessChange(ctx, actor, change, domain.AccessPageQuery{Search: "aLpHa", Limit: 1, Cursor: *page.NextCursor})
	rbacReadErrorKind(t, e, domain.Duplicate)
	current, e := r.PreviewAccessChange(ctx, actor, change, domain.AccessPageQuery{Search: "aLpHa", Limit: 1})
	if e != nil || current.NextCursor == nil {
		t.Fatal(current, e)
	}
	if _, e = p.Exec(ctx, "UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=$1", actor); e != nil {
		t.Fatal(e)
	}
	_, e = r.PreviewAccessChange(ctx, actor, change, domain.AccessPageQuery{Search: "aLpHa", Limit: 1, Cursor: *current.NextCursor})
	rbacReadErrorKind(t, e, domain.Duplicate)
	receipt, e := r.PreviewAccessChange(ctx, actor, change, domain.AccessPageQuery{Search: "no-match"})
	if e != nil {
		t.Fatal(e)
	}
	result, e := r.ApplyAccessChange(ctx, actor, domain.AccessApply{Change: change, GraphRevision: receipt.GraphRevision, ActorEpoch: receipt.ActorEpoch, Fingerprint: receipt.Fingerprint}, nil)
	if e != nil || result.AffectedUsers != 3 {
		t.Fatal("filtered receipt must apply full change", result, e)
	}
}
