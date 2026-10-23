package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// Один statement фиксирует account, union, provenance, epoch и revision одного snapshot.
func authorizationQuery() sq.SelectBuilder {
	return sqlBuilder.Select(
		`(SELECT row_to_json(a) FROM (
    SELECT u.id,u.login,u.name,u.role,u.scope,u.active,u.revision,u.created_at,u.last_login
   ) a) AS account`,
		`COALESCE((SELECT jsonb_agg(jsonb_build_object('id',g.id,'name',g.name,'revision',g.revision,'role_ids',COALESCE((SELECT jsonb_agg(gr.role_id ORDER BY gr.role_id) FROM auth_group_role gr WHERE gr.group_id=g.id),'[]'::jsonb)) ORDER BY g.id) FROM auth_group_member m JOIN auth_group g ON g.id=m.group_id WHERE m.user_id=u.id),'[]'::jsonb) AS groups`,
		"u.organization_scope AS organization", "u.authz_epoch AS epoch", "s.revision AS graph_revision",
		`COALESCE((
   SELECT jsonb_agg(jsonb_build_object(
    'role',jsonb_build_object(
     'id',r.id,'name',r.name,'description',r.description,'builtin_key',r.builtin_key,'revision',r.revision,
     'permission_keys',COALESCE((
      SELECT jsonb_agg(p.permission_key ORDER BY p.permission_key)
      FROM auth_role_permission p WHERE p.role_id=r.id
     ),'[]'::jsonb)),
    'group_id',a.group_id,'group_name',a.group_name
   ) ORDER BY r.id,a.group_id NULLS FIRST)
   FROM (
    SELECT ur.role_id,NULL::bigint AS group_id,NULL::text AS group_name
    FROM auth_user_role ur WHERE ur.user_id=u.id
    UNION ALL
    SELECT gr.role_id,g.id,g.name
    FROM auth_group_member m
    JOIN auth_group g ON g.id=m.group_id
    JOIN auth_group_role gr ON gr.group_id=g.id
    WHERE m.user_id=u.id
   ) a JOIN auth_role r ON r.id=a.role_id
  ),'[]'::jsonb) AS grants`,
	).From("auth_user u").CrossJoin("auth_rbac_state s")
}

type accessRoleGrant struct {
	Role      domain.AccessRole `json:"role"`
	GroupID   *int64            `json:"group_id"`
	GroupName *string           `json:"group_name"`
}

type authorizationRow struct {
	Groups        []domain.AccessGroupMembership `json:"groups"`
	Account       domain.Account                 `json:"account"`
	Organization  *domain.OrganizationScope      `json:"organization"`
	Epoch         int64                          `json:"epoch"`
	GraphRevision int64                          `json:"graph_revision"`
	Grants        []accessRoleGrant              `json:"grants"`
}

func (a authorizationRow) snapshot() domain.AuthorizationSnapshot {
	grants := make([]domain.RolePermissionGrant, 0, len(a.Grants))
	for _, g := range a.Grants {
		grants = append(grants, domain.RolePermissionGrant{Role: g.Role, GroupID: g.GroupID, GroupName: g.GroupName})
	}
	keys, sources := domain.ResolveRolePermissions(grants)
	assignments := domain.AccessAssignments{DirectRoleIDs: []int64{}, Groups: slices.Clone(a.Groups), Roles: []domain.AccessRoleSummary{}}
	if assignments.Groups == nil {
		assignments.Groups = []domain.AccessGroupMembership{}
	}
	seen := map[int64]bool{}
	for _, grant := range a.Grants {
		if grant.GroupID == nil {
			assignments.DirectRoleIDs = append(assignments.DirectRoleIDs, grant.Role.ID)
		}
		if !seen[grant.Role.ID] {
			seen[grant.Role.ID] = true
			var key *string
			if grant.Role.BuiltinKey != nil {
				v := *grant.Role.BuiltinKey
				key = &v
			}
			assignments.Roles = append(assignments.Roles, domain.AccessRoleSummary{ID: grant.Role.ID, Name: grant.Role.Name, Revision: grant.Role.Revision, BuiltinKey: key})
		}
	}
	slices.Sort(assignments.DirectRoleIDs)
	slices.SortFunc(assignments.Roles, func(a, b domain.AccessRoleSummary) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	for i := range assignments.Groups {
		assignments.Groups[i].RoleIDs = slices.Clone(assignments.Groups[i].RoleIDs)
	}
	return domain.AuthorizationSnapshot{Assignments: assignments, AccountID: a.Account.ID, Active: a.Account.Active, Epoch: a.Epoch, GraphRevision: a.GraphRevision, Permissions: keys, Sources: sources, Organization: a.Organization, LegacyScope: a.Account.Scope}
}
func authorization(ctx context.Context, conn db, id int64) (domain.AuthorizationSnapshot, error) {
	a, e := one[authorizationRow](ctx, conn, authorizationQuery().Where(sq.Eq{"u.id": id}))
	if e != nil {
		return domain.AuthorizationSnapshot{}, e
	}
	return a.snapshot(), nil
}
func (r *Repository) Authorization(ctx context.Context, id int64) (domain.AuthorizationSnapshot, error) {
	return authorization(ctx, r.pool, id)
}

type accessGraph struct {
	Actor    domain.AuthorizationSnapshot
	Revision int64
	Accounts []authorizationRow
	Roles    []domain.AccessRole
	Groups   []domain.AccessGroup
	Members  []groupMember
}
type groupMember struct {
	GroupID int64 `json:"group_id"`
	UserID  int64 `json:"user_id"`
}

// Четыре bulk-запроса вместо запроса для каждого пользователя/permission/source.
func readAccessGraph(ctx context.Context, conn db, actorID int64, permission domain.Permission) (accessGraph, error) {
	g := accessGraph{}
	var e error
	g.Accounts, e = list[authorizationRow](ctx, conn, authorizationQuery().OrderBy("u.id"))
	if e != nil {
		return g, e
	}
	found := false
	for _, a := range g.Accounts {
		if a.Account.ID == actorID {
			g.Actor = a.snapshot()
			g.Revision = a.GraphRevision
			found = true
			break
		}
	}
	if !found || !g.Actor.Active {
		return g, domain.NewError(domain.Unauthorized, "Войдите в систему")
	}
	if e = g.Actor.Require(permission); e != nil {
		return g, e
	}
	g.Roles, e = list[domain.AccessRole](ctx, conn, sqlBuilder.Select("r.id,r.name,r.description,r.builtin_key,r.revision", `COALESCE((SELECT jsonb_agg(permission_key ORDER BY permission_key) FROM auth_role_permission WHERE role_id=r.id),'[]'::jsonb) AS permission_keys`).From("auth_role r").OrderBy("r.id"))
	if e != nil {
		return g, e
	}
	g.Groups, e = list[domain.AccessGroup](ctx, conn, sqlBuilder.Select("g.id,g.name,g.description,g.revision", `COALESCE((SELECT jsonb_agg(role_id ORDER BY role_id) FROM auth_group_role WHERE group_id=g.id),'[]'::jsonb) AS role_ids`).From("auth_group g").OrderBy("g.id"))
	if e != nil {
		return g, e
	}
	g.Members, e = list[groupMember](ctx, conn, sqlBuilder.Select("group_id,user_id").From("auth_group_member"))
	return g, e
}
func (r *Repository) withAccessGraph(ctx context.Context, id int64, key domain.Permission, fn func(db, accessGraph) error) error {
	tx, e := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	g, e := readAccessGraph(ctx, tx, id, key)
	if e != nil {
		return e
	}
	if e = fn(tx, g); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (g accessGraph) roleVisible(role domain.AccessRole) bool {
	return g.Actor.Require("access.manage_global") == nil || g.Actor.CanGrant(role.PermissionKeys)
}
func (g accessGraph) groupVisible(group domain.AccessGroup) bool {
	if g.Actor.Require("access.manage_global") == nil {
		return true
	}
	for _, id := range group.RoleIDs {
		found := false
		for _, r := range g.Roles {
			if r.ID == id {
				found = true
				if !g.roleVisible(r) {
					return false
				}
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func (g accessGraph) group(id int64) (domain.AccessGroup, error) {
	for _, a := range g.Groups {
		if a.ID == id && g.groupVisible(a) {
			return a, nil
		}
	}
	return domain.AccessGroup{}, domain.NewError(domain.Missing, "Группа не найдена")
}

// Cursor не является authority: все границы проверяются заново до pagination.
type accessCursor struct {
	Actor    int64  `json:"actor"`
	Epoch    int64  `json:"epoch"`
	Revision int64  `json:"revision"`
	Kind     string `json:"kind"`
	Search   string `json:"search"`
	LastID   int64  `json:"last_id"`
}

func pageBounds(g accessGraph, q domain.AccessPageQuery, kind string) (int, int64, error) {
	limit := q.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return 0, 0, domain.NewError(domain.Invalid, "Размер страницы должен быть от 1 до 100")
	}
	if q.GraphRevision != nil && *q.GraphRevision != g.Revision {
		return 0, 0, domain.NewError(domain.Duplicate, "Граф доступа изменён; обновите список")
	}
	if q.Cursor == "" {
		return limit, 0, nil
	}
	raw, e := base64.RawURLEncoding.DecodeString(q.Cursor)
	var c accessCursor
	if e != nil || json.Unmarshal(raw, &c) != nil {
		return 0, 0, domain.NewError(domain.Invalid, "Некорректный cursor")
	}
	if c.Actor != g.Actor.AccountID || c.Epoch != g.Actor.Epoch || c.Revision != g.Revision || c.Kind != kind || c.Search != q.Search {
		return 0, 0, domain.NewError(domain.Duplicate, "Список изменён; обновите страницу")
	}
	return limit, c.LastID, nil
}
func nextAccessCursor(g accessGraph, q domain.AccessPageQuery, kind string, id int64) *string {
	raw, _ := json.Marshal(accessCursor{g.Actor.AccountID, g.Actor.Epoch, g.Revision, kind, q.Search, id})
	s := base64.RawURLEncoding.EncodeToString(raw)
	return &s
}
func matchesAccessSearch(q domain.AccessPageQuery, values ...string) bool {
	for _, s := range values {
		if strings.Contains(strings.ToLower(s), strings.ToLower(q.Search)) {
			return true
		}
	}
	return false
}
func accountPage(g accessGraph, q domain.AccessPageQuery, kind string, groupID *int64) (domain.AccountPage, error) {
	out := domain.AccountPage{Items: make([]domain.Account, 0), GraphRevision: g.Revision}
	limit, last, e := pageBounds(g, q, kind)
	if e != nil {
		return out, e
	}
	for _, a := range g.Accounts {
		if !g.Actor.CanManageAccount(a.snapshot()) || !matchesAccessSearch(q, a.Account.Login, a.Account.Name) {
			continue
		}
		if groupID != nil {
			member := false
			for _, m := range g.Members {
				if m.GroupID == *groupID && m.UserID == a.Account.ID {
					member = true
					break
				}
			}
			if !member {
				continue
			}
		}
		out.TotalVisible++
		if a.Account.ID <= last {
			continue
		}
		if len(out.Items) < limit {
			out.Items = append(out.Items, a.Account)
		} else if out.NextCursor == nil {
			out.NextCursor = nextAccessCursor(g, q, kind, out.Items[len(out.Items)-1].ID)
		}
	}
	return out, nil
}
func (r *Repository) ListManagedAccounts(ctx context.Context, id int64, q domain.AccessPageQuery) (out domain.AccountPage, e error) {
	e = r.withAccessGraph(ctx, id, "users.read", func(_ db, g accessGraph) error { var e error; out, e = accountPage(g, q, "users", nil); return e })
	return
}
func (r *Repository) ManagedAccount(ctx context.Context, actorID, targetID int64) (out domain.Account, e error) {
	e = r.withAccessGraph(ctx, actorID, "users.read", func(_ db, g accessGraph) error {
		for _, a := range g.Accounts {
			if a.Account.ID == targetID && g.Actor.CanManageAccount(a.snapshot()) {
				out = a.Account
				return nil
			}
		}
		return domain.NewError(domain.Missing, "Пользователь не найден")
	})
	return
}
func (r *Repository) ListAccessRoles(ctx context.Context, id int64, q domain.AccessPageQuery) (out domain.RolePage, e error) {
	e = r.withAccessGraph(ctx, id, "roles.read", func(_ db, g accessGraph) error {
		out = domain.RolePage{Items: make([]domain.AccessRole, 0), GraphRevision: g.Revision}
		limit, last, e := pageBounds(g, q, "roles")
		if e != nil {
			return e
		}
		for _, a := range g.Roles {
			if !g.roleVisible(a) || !matchesAccessSearch(q, a.Name, a.Description) {
				continue
			}
			out.TotalVisible++
			if a.ID <= last {
				continue
			}
			if len(out.Items) < limit {
				out.Items = append(out.Items, a)
			} else if out.NextCursor == nil {
				out.NextCursor = nextAccessCursor(g, q, "roles", out.Items[len(out.Items)-1].ID)
			}
		}
		return nil
	})
	return
}
func (r *Repository) AccessRole(ctx context.Context, actorID, roleID int64) (out domain.AccessRole, e error) {
	e = r.withAccessGraph(ctx, actorID, "roles.read", func(_ db, g accessGraph) error {
		for _, a := range g.Roles {
			if a.ID == roleID && g.roleVisible(a) {
				out = a
				return nil
			}
		}
		return domain.NewError(domain.Missing, "Роль не найдена")
	})
	return
}
func (r *Repository) ListAccessGroups(ctx context.Context, id int64, q domain.AccessPageQuery) (out domain.GroupPage, e error) {
	e = r.withAccessGraph(ctx, id, "groups.read", func(_ db, g accessGraph) error {
		out = domain.GroupPage{Items: make([]domain.AccessGroup, 0), GraphRevision: g.Revision}
		limit, last, e := pageBounds(g, q, "groups")
		if e != nil {
			return e
		}
		for _, a := range g.Groups {
			if !g.groupVisible(a) || !matchesAccessSearch(q, a.Name, a.Description) {
				continue
			}
			out.TotalVisible++
			if a.ID <= last {
				continue
			}
			if len(out.Items) < limit {
				out.Items = append(out.Items, a)
			} else if out.NextCursor == nil {
				out.NextCursor = nextAccessCursor(g, q, "groups", out.Items[len(out.Items)-1].ID)
			}
		}
		return nil
	})
	return
}
func (r *Repository) AccessGroup(ctx context.Context, actorID, groupID int64) (out domain.AccessGroup, e error) {
	e = r.withAccessGraph(ctx, actorID, "groups.read", func(_ db, g accessGraph) error { var e error; out, e = g.group(groupID); return e })
	return
}
func (r *Repository) AccessGroupMembers(ctx context.Context, actorID, groupID int64, q domain.AccessPageQuery) (out domain.AccountPage, e error) {
	e = r.withAccessGraph(ctx, actorID, "groups.read", func(_ db, g accessGraph) error {
		if e := g.Actor.Require("users.read"); e != nil {
			return e
		}
		if _, e := g.group(groupID); e != nil {
			return e
		}
		var e error
		out, e = accountPage(g, q, "members:"+strconv.FormatInt(groupID, 10), &groupID)
		return e
	})
	return
}

func (r *Repository) OrganizationOptions(ctx context.Context, id int64) (out domain.OrganizationOptions, e error) {
	e = r.withAccessGraph(ctx, id, "users.scope.assign", func(conn db, g accessGraph) error {
		out = domain.OrganizationOptions{NGDUs: make([]domain.Reference, 0), Departments: make([]domain.Reference, 0), Pairs: make([]domain.OrganizationPair, 0), ActorScope: g.Actor.Organization}
		global := g.Actor.Require("access.manage_global") == nil
		if !global && (g.Actor.Organization == nil || g.Actor.Organization.Kind == "none") {
			return nil
		}
		nq := sqlBuilder.Select("id,name").From("ngdu").OrderBy("id")
		dq := sqlBuilder.Select("id,name").From("department").OrderBy("id")
		pq := sqlBuilder.Select("DISTINCT ngdu AS ngdu_id,department AS department_id").From("well").OrderBy("ngdu,department")
		if !global && g.Actor.Organization.Kind != "organization" {
			nq = nq.Where(sq.Eq{"id": *g.Actor.Organization.NGDUID})
			pq = pq.Where(sq.Eq{"ngdu": *g.Actor.Organization.NGDUID})
			if g.Actor.Organization.Kind == "department" {
				dq = dq.Where(sq.Eq{"id": *g.Actor.Organization.DepartmentID})
				pq = pq.Where(sq.Eq{"department": *g.Actor.Organization.DepartmentID})
			} else {
				dq = dq.Where(sq.Expr("id IN (?)", sqlBuilder.Select("department").From("well").Where(sq.Eq{"ngdu": *g.Actor.Organization.NGDUID})))
			}
		}
		var e error
		out.NGDUs, e = list[domain.Reference](ctx, conn, nq)
		if e != nil {
			return e
		}
		out.Departments, e = list[domain.Reference](ctx, conn, dq)
		if e != nil {
			return e
		}
		out.Pairs, e = list[domain.OrganizationPair](ctx, conn, pq)
		return e
	})
	return
}

// SessionAccountEnforced используется только после явного cutover вызывающим кодом.
func (r *Repository) SessionAccountEnforced(ctx context.Context, hash string) (domain.Account, error) {
	return one[domain.Account](ctx, r.pool, sqlBuilder.Select("u.id,u.login,u.name,u.role,u.scope,u.active,u.revision,u.created_at,u.last_login").From("auth_session s").Join("auth_user u ON u.id=s.user_id").Where(sq.Eq{"s.token_hash": hash, "u.active": true}).Where("s.expires_at > now() AND s.authz_epoch=u.authz_epoch"))
}

// accountView сохраняет все назначения, включая роли и группы с пустым union.
func (g accessGraph) accountView(id int64) (domain.AccountAccessView, error) {
	i := g.accountIndex(id)
	if i < 0 {
		return domain.AccountAccessView{}, domain.NewError(domain.Missing, "Пользователь не найден")
	}
	a := g.Accounts[i]
	a.Groups = []domain.AccessGroupMembership{}
	a.Grants = []accessRoleGrant{}
	for _, rid := range directAccessRoles(g.Accounts[i]) {
		ri := g.roleIndex(rid)
		if ri < 0 {
			return domain.AccountAccessView{}, accessDenied()
		}
		a.Grants = append(a.Grants, accessRoleGrant{Role: g.Roles[ri]})
	}
	for _, gid := range g.userGroups(id) {
		gi := g.groupIndex(gid)
		if gi < 0 {
			return domain.AccountAccessView{}, accessDenied()
		}
		group := g.Groups[gi]
		roles := slices.Clone(group.RoleIDs)
		if roles == nil {
			roles = []int64{}
		}
		slices.Sort(roles)
		for _, rid := range roles {
			ri := g.roleIndex(rid)
			if ri < 0 {
				return domain.AccountAccessView{}, accessDenied()
			}
			v, name := gid, group.Name
			a.Grants = append(a.Grants, accessRoleGrant{Role: g.Roles[ri], GroupID: &v, GroupName: &name})
		}
		a.Groups = append(a.Groups, domain.AccessGroupMembership{ID: gid, Name: group.Name, Revision: group.Revision, RoleIDs: roles})
	}
	// Typed deep copy keeps mutable slices/pointers independent of current/proposed graphs.
	raw, e := json.Marshal(domain.AccountAccessView{Account: a.Account, Authorization: a.snapshot()})
	if e != nil {
		return domain.AccountAccessView{}, e
	}
	var view domain.AccountAccessView
	e = json.Unmarshal(raw, &view)
	return view, e
}
