package postgres

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/observability"
)

// Единственный serializer auth mutations; user locks всегда следуют после него.
func lockAccessGraph(ctx context.Context, tx pgx.Tx) (int64, error) {
	state, e := one[struct {
		Revision int64 `json:"revision"`
	}](ctx, tx, sqlBuilder.Select("revision").From("auth_rbac_state").Where(sq.Eq{"id": true}).Suffix("FOR UPDATE"))
	return state.Revision, e
}
func lockAccessUsers(ctx context.Context, tx pgx.Tx, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, e := list[struct {
		ID int64 `json:"id"`
	}](ctx, tx, sqlBuilder.Select("id").From("auth_user").Where(sq.Eq{"id": ids}).OrderBy("id").Suffix("FOR UPDATE"))
	return e
}
func bumpAccessUsers(ctx context.Context, tx pgx.Tx, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	if _, e := execute(ctx, tx, sqlBuilder.Update("auth_user").Set("authz_epoch", sq.Expr("authz_epoch+1")).Set("revision", sq.Expr("revision+1")).Where(sq.Eq{"id": ids})); e != nil {
		return e
	}
	_, e := execute(ctx, tx, sqlBuilder.Delete("auth_session").Where(sq.Eq{"user_id": ids}))
	return e
}
func bumpAccessGraph(ctx context.Context, tx pgx.Tx) error {
	_, e := execute(ctx, tx, sqlBuilder.Update("auth_rbac_state").Set("revision", sq.Expr("revision+1")).Where(sq.Eq{"id": true}))
	return e
}

type accessAuditUser struct {
	Account       domain.Account               `json:"account"`
	Authorization domain.AuthorizationSnapshot `json:"authorization"`
	RoleIDs       []int64                      `json:"role_ids"`
	GroupIDs      []int64                      `json:"group_ids"`
}
type profileAudit struct {
	AccountID int64 `json:"account_id"`
	domain.ManagedProfile
}
type accessAuditState struct {
	Profile *profileAudit        `json:"profile,omitempty"`
	Users   []accessAuditUser    `json:"users"`
	Roles   []domain.AccessRole  `json:"roles"`
	Groups  []domain.AccessGroup `json:"groups"`
	Members []groupMember        `json:"members"`
}

func auditAccessState(g accessGraph, ids []int64, change domain.AccessChange) accessAuditState {
	out := accessAuditState{Users: []accessAuditUser{}, Roles: []domain.AccessRole{}, Groups: []domain.AccessGroup{}, Members: []groupMember{}}
	roleIDs, groupIDs := map[int64]bool{}, map[int64]bool{}
	for _, id := range ids {
		if i := g.accountIndex(id); i >= 0 {
			a := g.Accounts[i]
			out.Users = append(out.Users, accessAuditUser{a.Account, a.snapshot(), directAccessRoles(a), g.userGroups(id)})
			for _, grant := range a.Grants {
				roleIDs[grant.Role.ID] = true
			}
			for _, gid := range g.userGroups(id) {
				groupIDs[gid] = true
			}
		}
	}
	if change.Role != nil && change.Role.ID != nil {
		roleIDs[*change.Role.ID] = true
	}
	if change.Group != nil && change.Group.ID != nil {
		groupIDs[*change.Group.ID] = true
	}
	for _, group := range g.Groups {
		if groupIDs[group.ID] {
			out.Groups = append(out.Groups, group)
			for _, rid := range group.RoleIDs {
				roleIDs[rid] = true
			}
		}
	}
	for _, role := range g.Roles {
		if roleIDs[role.ID] {
			out.Roles = append(out.Roles, role)
		}
	}
	for _, member := range g.Members {
		changedGroup := change.Group != nil && change.Group.ID != nil && member.GroupID == *change.Group.ID
		if slices.Contains(ids, member.UserID) || changedGroup {
			out.Members = append(out.Members, member)
		}
	}
	return out
}
func writeAccessEvent(ctx context.Context, tx pgx.Tx, actorID int64, before, after accessGraph, ids []int64, change domain.AccessChange, profile ...*domain.ProfileChangeSummary) error {
	roles := []accessRoleGrant{}
	if ai := before.accountIndex(actorID); ai >= 0 {
		roles = before.Accounts[ai].Grants
	} else if ai := after.accountIndex(actorID); ai >= 0 {
		roles = after.Accounts[ai].Grants
	}
	a, e := json.Marshal(roles)
	if e != nil {
		return e
	}
	beforeState, afterState := auditAccessState(before, ids, change), auditAccessState(after, ids, change)
	if len(profile) > 0 && profile[0] != nil {
		p := profile[0]
		if p.Before != nil {
			beforeState.Profile = &profileAudit{ids[0], p.Before.Profile}
		}
		afterState.Profile = &profileAudit{ids[0], p.After.Profile}
	}
	b, e := json.Marshal(beforeState)
	if e != nil {
		return e
	}
	c, e := json.Marshal(afterState)
	if e != nil {
		return e
	}
	_, e = execute(ctx, tx, sqlBuilder.Insert("auth_access_event").Columns("actor_id", "actor_roles", "before_state", "after_state", "request_id").Values(actorID, a, b, c, observability.RequestID(observability.Ensure(ctx))))
	return e
}
func replaceAccessLinks(ctx context.Context, tx pgx.Tx, table, owner string, id int64, column string, values []int64) error {
	if _, e := execute(ctx, tx, sqlBuilder.Delete(table).Where(sq.Eq{owner: id})); e != nil {
		return e
	}
	if len(values) == 0 {
		return nil
	}
	q := sqlBuilder.Insert(table).Columns(owner, column)
	for _, v := range values {
		q = q.Values(id, v)
	}
	_, e := execute(ctx, tx, q)
	return e
}
func (r *Repository) ApplyAccessChange(ctx context.Context, actorID int64, in domain.AccessApply, hash *string) (out domain.AccessApplyResult, e error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	revision, e := lockAccessGraph(ctx, tx)
	if e != nil {
		return out, e
	}
	g, e := readAccessGraph(ctx, tx, actorID, changePermission(in.Change))
	if e != nil {
		return out, e
	}
	if g.Actor.Epoch != in.ActorEpoch {
		return out, domain.NewError(domain.Unauthorized, "Полномочия изменены; войдите снова")
	}
	if revision != in.GraphRevision || accessFingerprint(in.Change, g) != in.Fingerprint {
		return out, accessConflict()
	}
	ev, e := evaluateAccessChange(ctx, tx, g, in.Change)
	if e != nil {
		return out, e
	}
	lockIDs := append([]int64{actorID}, ev.Affected...)
	if u := in.Change.User; u != nil {
		if u.ID != nil {
			lockIDs = append(lockIDs, *u.ID)
		}
		if u.Profile.Present && !u.Profile.Value.ManagerID.Null {
			lockIDs = append(lockIDs, u.Profile.Value.ManagerID.Value)
		}
	}
	if e = lockAccessUsers(ctx, tx, lockIDs); e != nil {
		return out, e
	}
	// Reload after locks: sessions/workers never lock the graph, but hold user rows.
	g, e = readAccessGraph(ctx, tx, actorID, changePermission(in.Change))
	if e != nil {
		return out, e
	}
	ev, e = evaluateAccessChange(ctx, tx, g, in.Change)
	if e != nil {
		return out, e
	}
	if (hash != nil) != (in.Change.User != nil && in.Change.User.ResetPassword) {
		return out, accessInvalid()
	}
	ids := ev.Affected
	out.SessionEffect = "none"
	if ev.MetadataOnly {
		target := *in.Change.User.ID
		if ev.ProfileChanged {
			if e = writeProfile(ctx, tx, target, ev.ProfileChange.After.Profile); e != nil {
				return out, e
			}
			if e = writeAccessEvent(ctx, tx, actorID, g, g, []int64{target}, in.Change, ev.ProfileChange); e != nil {
				return out, e
			}
		}
		view, err := g.accountView(target)
		if err != nil {
			return out, err
		}
		p, err := managedProfile(ctx, tx, target)
		if err != nil {
			return out, err
		}
		view.Profile = &p
		out.UserView = &view
		out.User = &view.Account
		out.GraphRevision = revision
		e = tx.Commit(ctx)
		return
	}
	if u := in.Change.User; u != nil {
		if u.ID == nil {
			proposed := ev.After.Accounts[ev.After.accountIndex(-1)]
			scope, _ := json.Marshal(proposed.Account.Scope)
			org, _ := json.Marshal(proposed.Organization)
			password := ""
			if hash != nil {
				password = *hash
			}
			a, e := mutate[domain.Account](ctx, tx, sqlBuilder.Insert("auth_user").Columns("login", "name", "password_hash", "role", "scope", "active", "organization_scope").Values(u.Login, u.Name, password, "viewer", scope, u.Active, org).Suffix("RETURNING "+accountColumns))
			if e != nil {
				return out, e
			}
			out.User = &a
			ids = []int64{a.ID}
		} else {
			changes := sq.Eq{"login": u.Login, "name": u.Name, "active": u.Active}
			if hash != nil {
				changes["password_hash"] = *hash
			}
			if u.Organization.Present {
				raw, _ := json.Marshal(u.Organization.Value)
				changes["organization_scope"] = raw
			}
			if _, e = execute(ctx, tx, sqlBuilder.Update("auth_user").SetMap(changes).Where(sq.Eq{"id": *u.ID})); e != nil {
				return out, e
			}
		}
		id := ids[0]
		if e = replaceAccessLinks(ctx, tx, "auth_user_role", "user_id", id, "role_id", u.RoleIDs); e != nil {
			return out, e
		}
		if e = replaceAccessLinks(ctx, tx, "auth_group_member", "user_id", id, "group_id", u.GroupIDs); e != nil {
			return out, e
		}
	}
	if role := in.Change.Role; role != nil {
		if role.Delete {
			if _, e = execute(ctx, tx, sqlBuilder.Delete("auth_role").Where(sq.Eq{"id": *role.ID})); e != nil {
				return out, e
			}
		} else {
			var value domain.AccessRole
			if role.ID == nil {
				value, e = mutate[domain.AccessRole](ctx, tx, sqlBuilder.Insert("auth_role").Columns("name", "description").Values(role.Name, role.Description).Suffix("RETURNING id,name,description,builtin_key,revision"))
			} else {
				value, e = mutate[domain.AccessRole](ctx, tx, sqlBuilder.Update("auth_role").SetMap(sq.Eq{"name": role.Name, "description": role.Description, "revision": sq.Expr("revision+1")}).Where(sq.Eq{"id": *role.ID}).Suffix("RETURNING id,name,description,builtin_key,revision"))
			}
			if e != nil {
				return out, e
			}
			if _, e = execute(ctx, tx, sqlBuilder.Delete("auth_role_permission").Where(sq.Eq{"role_id": value.ID})); e != nil {
				return out, e
			}
			for _, k := range role.PermissionKeys {
				if _, e = execute(ctx, tx, sqlBuilder.Insert("auth_role_permission").Columns("role_id", "permission_key").Values(value.ID, k)); e != nil {
					return out, e
				}
			}
			value.PermissionKeys = append([]domain.Permission{}, role.PermissionKeys...)
			out.Role = &value
		}
	}
	if group := in.Change.Group; group != nil {
		if group.Delete {
			if _, e = execute(ctx, tx, sqlBuilder.Delete("auth_group").Where(sq.Eq{"id": *group.ID})); e != nil {
				return out, e
			}
		} else {
			var value domain.AccessGroup
			if group.ID == nil {
				value, e = mutate[domain.AccessGroup](ctx, tx, sqlBuilder.Insert("auth_group").Columns("name", "description").Values(group.Name, group.Description).Suffix("RETURNING id,name,description,revision"))
			} else {
				value, e = mutate[domain.AccessGroup](ctx, tx, sqlBuilder.Update("auth_group").SetMap(sq.Eq{"name": group.Name, "description": group.Description, "revision": sq.Expr("revision+1")}).Where(sq.Eq{"id": *group.ID}).Suffix("RETURNING id,name,description,revision"))
			}
			if e != nil {
				return out, e
			}
			if e = replaceAccessLinks(ctx, tx, "auth_group_role", "group_id", value.ID, "role_id", group.RoleIDs); e != nil {
				return out, e
			}
			if e = replaceAccessLinks(ctx, tx, "auth_group_member", "group_id", value.ID, "user_id", group.MemberIDs); e != nil {
				return out, e
			}
			value.RoleIDs = append([]int64{}, group.RoleIDs...)
			out.Group = &value
		}
	}
	if ev.ProfileChange != nil && (ev.ProfileChanged || in.Change.User.ID == nil) {
		if e = writeProfile(ctx, tx, ids[0], ev.ProfileChange.After.Profile); e != nil {
			return out, e
		}
	}
	// New users begin at epoch/revision1. Existing impacted users increment exactly once.
	existing := ids
	if in.Change.User != nil && in.Change.User.ID == nil {
		existing = nil
	}
	if e = bumpAccessUsers(ctx, tx, existing); e != nil {
		return out, e
	}
	if e = bumpAccessGraph(ctx, tx); e != nil {
		return out, e
	}
	after, e := readAccessGraphUnchecked(ctx, tx, actorID)
	if e != nil {
		return out, e
	}
	auditChange := in.Change
	if out.Role != nil {
		value := *in.Change.Role
		value.ID = &out.Role.ID
		auditChange.Role = &value
	}
	if out.Group != nil {
		value := *in.Change.Group
		value.ID = &out.Group.ID
		auditChange.Group = &value
	}
	if e = writeAccessEvent(ctx, tx, actorID, g, after, ids, auditChange, ev.ProfileChange); e != nil {
		return out, e
	}
	if u := in.Change.User; u != nil {
		a, e := one[domain.Account](ctx, tx, sqlBuilder.Select(accountColumns).From("auth_user").Where(sq.Eq{"id": ids[0]}))
		if e != nil {
			return out, e
		}
		out.User = &a
		view, e := after.accountView(a.ID)
		if e != nil {
			return out, e
		}
		p, err := managedProfile(ctx, tx, a.ID)
		if err != nil {
			return out, err
		}
		view.Profile = &p
		out.UserView = &view
	}
	out.GraphRevision = revision + 1
	out.AffectedUsers = len(ids)
	if len(existing) > 0 {
		out.SessionEffect = "revoked"
	}
	e = tx.Commit(ctx)
	return
}

// Mutation audit must still read a self-disabled actor's resulting graph.
func readAccessGraphUnchecked(ctx context.Context, conn db, actorID int64) (accessGraph, error) {
	accounts, e := list[authorizationRow](ctx, conn, authorizationQuery().OrderBy("u.id"))
	if e != nil {
		return accessGraph{}, e
	}
	g := accessGraph{Accounts: accounts}
	for _, a := range accounts {
		if a.Account.ID == actorID {
			g.Actor = a.snapshot()
			g.Revision = a.GraphRevision
		}
	}
	g.Roles, e = list[domain.AccessRole](ctx, conn, sqlBuilder.Select("r.id,r.name,r.description,r.builtin_key,r.revision", `COALESCE((SELECT jsonb_agg(permission_key ORDER BY permission_key) FROM auth_role_permission WHERE role_id=r.id),'[]'::jsonb) AS permission_keys`).From("auth_role r").OrderBy("r.id"))
	if e != nil {
		return g, e
	}
	g.Groups, e = list[domain.AccessGroup](ctx, conn, sqlBuilder.Select("g.id,g.name,g.description,g.revision", `COALESCE((SELECT jsonb_agg(role_id ORDER BY role_id) FROM auth_group_role WHERE group_id=g.id),'[]'::jsonb) AS role_ids`).From("auth_group g").OrderBy("g.id"))
	if e != nil {
		return g, e
	}
	g.Members, e = list[groupMember](ctx, conn, sqlBuilder.Select("group_id,user_id").From("auth_group_member").OrderBy("group_id,user_id"))
	return g, e
}

func accessActorID(ctx context.Context) (int64, error) {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || !p.Authenticated || p.AccountID == nil {
		return 0, domain.NewError(domain.Unauthorized, "Войдите в систему")
	}
	return *p.AccountID, nil
}
func ensureAccessManager(g accessGraph) error {
	for _, a := range g.Accounts {
		if a.snapshot().Require(managerPermissions...) == nil {
			return nil
		}
	}
	return domain.NewError(domain.Duplicate, "Нельзя удалить последнего управляющего доступом")
}
func legacyOrganization(a domain.Account) *domain.OrganizationScope {
	s := a.Scope
	if a.Role == "admin" {
		return &domain.OrganizationScope{Kind: "organization"}
	}
	if s.NGDUs != nil && len(*s.NGDUs) == 0 || s.Departments != nil && len(*s.Departments) == 0 {
		return &domain.OrganizationScope{Kind: "none"}
	}
	if s.NGDUs == nil && s.Departments == nil {
		return &domain.OrganizationScope{Kind: "organization"}
	}
	if s.NGDUs != nil && len(*s.NGDUs) == 1 {
		n := (*s.NGDUs)[0]
		if s.Departments == nil {
			return &domain.OrganizationScope{Kind: "ngdu", NGDUID: &n}
		}
		if len(*s.Departments) == 1 {
			d := (*s.Departments)[0]
			return &domain.OrganizationScope{Kind: "department", NGDUID: &n, DepartmentID: &d}
		}
	}
	return nil
}
func organizationJSON(s *domain.OrganizationScope) (any, error) {
	if s == nil {
		return nil, nil
	}
	return json.Marshal(s)
}
func restoreBootstrapRole(ctx context.Context, tx pgx.Tx) error {
	roles, e := list[domain.AccessRole](ctx, tx, sqlBuilder.Select("id").From("auth_role").Where(sq.Eq{"builtin_key": "admin"}))
	if e != nil {
		return e
	}
	if len(roles) != 0 {
		return nil
	}
	role, e := mutate[domain.AccessRole](ctx, tx, sqlBuilder.Insert("auth_role").Columns("name", "builtin_key").Values("Администратор", "admin").Suffix("RETURNING id"))
	if e != nil {
		return e
	}
	for _, key := range domain.BuiltinAccessPermissions("admin") {
		if _, e = execute(ctx, tx, sqlBuilder.Insert("auth_role_permission").Columns("role_id", "permission_key").Values(role.ID, key)); e != nil {
			return e
		}
	}
	return nil
}
