package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	sq "github.com/Masterminds/squirrel"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

var managerPermissions = []domain.Permission{"users.read", "users.create", "users.update", "users.roles.assign", "users.groups.assign", "users.scope.assign", "roles.read", "roles.manage", "groups.read", "groups.manage", "access.manage_global"}

type accessEvaluation struct {
	Before, After  accessGraph
	Affected       []int64
	Impacts        []domain.AccessImpact
	Assigned       int
	ProfileChange  *domain.ProfileChangeSummary
	ProfileChanged bool
	MetadataOnly   bool
	Change         domain.AccessChange
}

func accessConflict() error {
	return domain.NewError(domain.Duplicate, "Граф доступа изменён; повторите предварительный просмотр")
}
func accessInvalid() error {
	return domain.NewError(domain.Invalid, "Неизвестное назначение или некорректное изменение доступа")
}
func accessDenied() error {
	return domain.NewError(domain.Forbidden, "Недостаточно прав для назначения")
}

func canonicalAccessChange(c domain.AccessChange) domain.AccessChange {
	// JSON copy isolates set sorting from caller-owned slices and Field presence flags.
	raw, _ := json.Marshal(c)
	var out domain.AccessChange
	_ = json.Unmarshal(raw, &out)
	// Field has a custom unmarshaller for HTTP. Copy it explicitly for canonical structs.
	if c.User != nil {
		u := *c.User
		u.RoleIDs = slices.Clone(c.User.RoleIDs)
		u.GroupIDs = slices.Clone(c.User.GroupIDs)
		out.User = &u
		if u.RoleIDs == nil {
			u.RoleIDs = []int64{}
		}
		if u.GroupIDs == nil {
			u.GroupIDs = []int64{}
		}
		if u.Profile.Present && !u.Profile.Null {
			p := service.NormalizeProfile(u.Profile.Value)
			u.Profile.Value.JobTitle.Value = ""
			normalize := func(f *domain.Field[string], v *string) {
				f.Null = v == nil
				f.Value = ""
				if v != nil {
					f.Value = *v
				}
			}
			normalize(&u.Profile.Value.JobTitle, p.JobTitle)
			normalize(&u.Profile.Value.Workplace.Value.Organization, p.Workplace.Organization)
			normalize(&u.Profile.Value.Workplace.Value.Division, p.Workplace.Division)
			normalize(&u.Profile.Value.Workplace.Value.Department, p.Workplace.Department)
		}
		slices.Sort(u.RoleIDs)
		slices.Sort(u.GroupIDs)
	}
	if out.Role != nil {
		if out.Role.PermissionKeys == nil {
			out.Role.PermissionKeys = []domain.Permission{}
		}
		slices.Sort(out.Role.PermissionKeys)
	}
	if out.Group != nil {
		if out.Group.RoleIDs == nil {
			out.Group.RoleIDs = []int64{}
		}
		if out.Group.MemberIDs == nil {
			out.Group.MemberIDs = []int64{}
		}
		slices.Sort(out.Group.RoleIDs)
		slices.Sort(out.Group.MemberIDs)
	}
	return out
}
func accessFingerprint(c domain.AccessChange, g accessGraph) string {
	raw, _ := json.Marshal(struct {
		Change                 domain.AccessChange
		Actor, Epoch, Revision int64
	}{canonicalAccessChange(c), g.Actor.AccountID, g.Actor.Epoch, g.Revision})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func cloneAccessGraph(g accessGraph) accessGraph {
	out := g
	out.Accounts = slices.Clone(g.Accounts)
	out.Roles = slices.Clone(g.Roles)
	out.Groups = slices.Clone(g.Groups)
	out.Members = slices.Clone(g.Members)
	for i := range out.Accounts {
		out.Accounts[i].Grants = slices.Clone(g.Accounts[i].Grants)
	}
	return out
}
func (g accessGraph) accountIndex(id int64) int {
	for i, a := range g.Accounts {
		if a.Account.ID == id {
			return i
		}
	}
	return -1
}
func (g accessGraph) roleIndex(id int64) int {
	for i, a := range g.Roles {
		if a.ID == id {
			return i
		}
	}
	return -1
}
func (g accessGraph) groupIndex(id int64) int {
	for i, a := range g.Groups {
		if a.ID == id {
			return i
		}
	}
	return -1
}
func directAccessRoles(a authorizationRow) []int64 {
	out := []int64{}
	for _, gr := range a.Grants {
		if gr.GroupID == nil {
			out = append(out, gr.Role.ID)
		}
	}
	slices.Sort(out)
	return out
}
func (g accessGraph) userGroups(id int64) []int64 {
	out := []int64{}
	for _, m := range g.Members {
		if m.UserID == id {
			out = append(out, m.GroupID)
		}
	}
	slices.Sort(out)
	return out
}
func (g *accessGraph) rebuildGrants() {
	for i := range g.Accounts {
		a := &g.Accounts[i]
		ids := directAccessRoles(*a)
		grants := []accessRoleGrant{}
		for _, id := range ids {
			if ri := g.roleIndex(id); ri >= 0 {
				grants = append(grants, accessRoleGrant{Role: g.Roles[ri]})
			}
		}
		for _, gid := range g.userGroups(a.Account.ID) {
			gi := g.groupIndex(gid)
			if gi < 0 {
				continue
			}
			group := g.Groups[gi]
			for _, rid := range group.RoleIDs {
				if ri := g.roleIndex(rid); ri >= 0 {
					id, name := group.ID, group.Name
					grants = append(grants, accessRoleGrant{Role: g.Roles[ri], GroupID: &id, GroupName: &name})
				}
			}
		}
		a.Grants = grants
		a.Groups = []domain.AccessGroupMembership{}
		for _, gid := range g.userGroups(a.Account.ID) {
			gi := g.groupIndex(gid)
			if gi >= 0 {
				v := g.Groups[gi]
				a.Groups = append(a.Groups, domain.AccessGroupMembership{ID: gid, Name: v.Name, Revision: v.Revision, RoleIDs: slices.Clone(v.RoleIDs)})
			}
		}
	}
}
func validateOrganizationReference(ctx context.Context, conn db, s domain.OrganizationScope) error {
	for _, v := range []struct {
		table string
		id    *int64
	}{{"ngdu", s.NGDUID}, {"department", s.DepartmentID}} {
		if v.id == nil {
			continue
		}
		rows, e := list[domain.Reference](ctx, conn, sqlBuilder.Select("id,name").From(v.table).Where(sq.Eq{"id": *v.id}))
		if e != nil {
			return e
		}
		if len(rows) != 1 {
			return accessInvalid()
		}
	}
	return nil
}
func evaluateAccessChange(ctx context.Context, conn db, g accessGraph, c domain.AccessChange) (accessEvaluation, error) {
	ev := accessEvaluation{Before: g, After: cloneAccessGraph(g), Affected: []int64{}, Impacts: []domain.AccessImpact{}, Change: c}
	if e := service.ValidateAccessChange(c); e != nil {
		return ev, e
	}
	after := &ev.After
	affected := map[int64]bool{}
	impacted := map[int64]bool{}
	require := func(keys ...domain.Permission) error { return g.Actor.Require(keys...) }
	checkRoles := func(ids []int64) error {
		for _, id := range ids {
			i := g.roleIndex(id)
			if i < 0 {
				return accessInvalid()
			}
			if !g.Actor.CanGrant(g.Roles[i].PermissionKeys) {
				return accessDenied()
			}
		}
		return nil
	}
	if u := c.User; u != nil {
		creating := u.ID == nil
		var old authorizationRow
		idx := -1
		id := int64(-1)
		if creating {
			if e := require("users.create"); e != nil {
				return ev, e
			}
			old = authorizationRow{Account: domain.Account{ID: id, Scope: domain.AccessScope{Funds: slices.Clone(g.Actor.LegacyScope.Funds)}, Role: "viewer"}, Epoch: 1, GraphRevision: g.Revision}
			if g.Actor.Require("access.manage_global") == nil {
				old.Account.Scope.Funds = []string{"operational", "presentation"}
			}
		} else {
			id = *u.ID
			idx = g.accountIndex(id)
			if idx < 0 || !g.Actor.CanManageAccount(g.Accounts[idx].snapshot()) {
				return ev, domain.NewError(domain.Missing, "Пользователь не найден")
			}
			old = g.Accounts[idx]
			if old.Account.Revision != u.Revision {
				return ev, accessConflict()
			}
			if e := require("users.update"); e != nil {
				return ev, e
			}
		}
		proposed := old
		proposed.Account.Login = u.Login
		proposed.Account.Name = u.Name
		proposed.Account.Active = u.Active
		if u.Organization.Present {
			if !reflect.DeepEqual(old.Organization, &u.Organization.Value) {
				if e := require("users.scope.assign"); e != nil {
					return ev, e
				}
			}
			if e := validateOrganizationReference(ctx, conn, u.Organization.Value); e != nil {
				return ev, e
			}
			s := u.Organization.Value
			proposed.Organization = &s
		}
		roles := slices.Clone(u.RoleIDs)
		slices.Sort(roles)
		groups := slices.Clone(u.GroupIDs)
		slices.Sort(groups)
		if !slices.Equal(roles, directAccessRoles(old)) {
			if e := require("users.roles.assign"); e != nil {
				return ev, e
			}
		}
		if !slices.Equal(groups, g.userGroups(id)) {
			if e := require("users.groups.assign"); e != nil {
				return ev, e
			}
		}
		if e := checkRoles(roles); e != nil {
			return ev, e
		}
		for _, gid := range groups {
			gi := g.groupIndex(gid)
			if gi < 0 {
				return ev, accessInvalid()
			}
			if e := checkRoles(g.Groups[gi].RoleIDs); e != nil {
				return ev, e
			}
		}
		proposed.Grants = []accessRoleGrant{}
		for _, rid := range roles {
			proposed.Grants = append(proposed.Grants, accessRoleGrant{Role: g.Roles[g.roleIndex(rid)]})
		}
		after.Members = slices.DeleteFunc(after.Members, func(m groupMember) bool { return m.UserID == id })
		for _, gid := range groups {
			after.Members = append(after.Members, groupMember{gid, id})
		}
		if creating {
			after.Accounts = append(after.Accounts, proposed)
			idx = len(after.Accounts) - 1
		} else {
			after.Accounts[idx] = proposed
		}
		after.rebuildGrants()
		if !g.Actor.CanManageAccount(after.Accounts[idx].snapshot()) {
			return ev, accessDenied()
		}
		var e error
		ev.ProfileChange, ev.ProfileChanged, e = evaluateProfile(ctx, conn, u)
		if e != nil {
			return ev, e
		}
		ev.MetadataOnly = metadataOnly(g, u)
		if !ev.MetadataOnly {
			affected[id] = true
			impacted[id] = true
		}
		ev.Assigned = 1
	}
	if role := c.Role; role != nil {
		if e := require("roles.manage"); e != nil {
			return ev, e
		}
		idx := -1
		id := int64(-1)
		if role.ID != nil {
			id = *role.ID
			idx = g.roleIndex(id)
			if idx < 0 {
				return ev, accessInvalid()
			}
			if g.Roles[idx].Revision != role.Revision {
				return ev, accessConflict()
			}
		}
		if !g.Actor.CanGrant(role.PermissionKeys) {
			return ev, accessDenied()
		}
		if idx >= 0 && !g.Actor.CanGrant(g.Roles[idx].PermissionKeys) {
			return ev, accessDenied()
		}
		for _, a := range g.Accounts {
			for _, gr := range a.Grants {
				if gr.Role.ID == id {
					ev.Assigned++
					impacted[a.Account.ID] = true
					break
				}
			}
		}
		if role.Delete {
			users, groups := 0, 0
			for _, a := range g.Accounts {
				if slices.Contains(directAccessRoles(a), id) {
					users++
				}
			}
			for _, group := range g.Groups {
				if slices.Contains(group.RoleIDs, id) {
					groups++
				}
			}
			if users > 0 || groups > 0 {
				return ev, domain.NewError(domain.Duplicate, fmt.Sprintf("Роль используется: users=%d, groups=%d", users, groups))
			}
			after.Roles = append(after.Roles[:idx], after.Roles[idx+1:]...)

		} else {
			proposed := domain.AccessRole{ID: id, Name: role.Name, Description: role.Description, Revision: 1, PermissionKeys: slices.Clone(role.PermissionKeys)}
			if idx < 0 {
				after.Roles = append(after.Roles, proposed)
			} else {
				proposed.BuiltinKey = g.Roles[idx].BuiltinKey
				proposed.Revision = g.Roles[idx].Revision + 1
				after.Roles[idx] = proposed
				if !samePermissionSet(g.Roles[idx].PermissionKeys, role.PermissionKeys) {
					for _, a := range g.Accounts {
						for _, gr := range a.Grants {
							if gr.Role.ID == id {
								affected[a.Account.ID] = true
							}
						}
					}
				}
			}
		}
		after.rebuildGrants()
	}
	if group := c.Group; group != nil {
		if e := require("groups.manage"); e != nil {
			return ev, e
		}
		if e := checkRoles(group.RoleIDs); e != nil {
			return ev, e
		}
		idx := -1
		id := int64(-1)
		oldMembers := []int64{}
		if group.ID != nil {
			id = *group.ID
			idx = g.groupIndex(id)
			if idx < 0 {
				return ev, accessInvalid()
			}
			if g.Groups[idx].Revision != group.Revision {
				return ev, accessConflict()
			}
			if e := checkRoles(g.Groups[idx].RoleIDs); e != nil {
				return ev, e
			}
			for _, m := range g.Members {
				if m.GroupID == id {
					oldMembers = append(oldMembers, m.UserID)
				}
			}
		}
		members := slices.Clone(group.MemberIDs)
		if group.Delete {
			members = []int64{}
		}
		for _, uid := range members {
			ai := g.accountIndex(uid)
			if ai < 0 {
				return ev, accessInvalid()
			}
			if !g.Actor.CanManageAccount(g.Accounts[ai].snapshot()) {
				return ev, accessDenied()
			}
		}
		for _, uid := range oldMembers {
			impacted[uid] = true
		}
		for _, uid := range members {
			impacted[uid] = true
		}
		ev.Assigned = len(impacted)
		if group.Delete {
			ev.Assigned = len(impacted)
		}
		rolesChanged := idx >= 0 && !sameIDSet(g.Groups[idx].RoleIDs, group.RoleIDs)
		for _, uid := range oldMembers {
			if rolesChanged || !slices.Contains(members, uid) {
				affected[uid] = true
			}
		}
		for _, uid := range members {
			if rolesChanged || !slices.Contains(oldMembers, uid) {
				affected[uid] = true
			}
		}
		after.Members = slices.DeleteFunc(after.Members, func(m groupMember) bool { return m.GroupID == id })
		for _, uid := range members {
			after.Members = append(after.Members, groupMember{id, uid})
		}
		if group.Delete {
			after.Groups = append(after.Groups[:idx], after.Groups[idx+1:]...)
		} else {
			proposed := domain.AccessGroup{ID: id, Name: group.Name, Description: group.Description, Revision: 1, RoleIDs: slices.Clone(group.RoleIDs)}
			if idx < 0 {
				after.Groups = append(after.Groups, proposed)
			} else {
				proposed.Revision = g.Groups[idx].Revision + 1
				after.Groups[idx] = proposed
			}
		}
		after.rebuildGrants()
	}
	managers := 0
	for _, a := range after.Accounts {
		if a.snapshot().Require(managerPermissions...) == nil {
			managers++
		}
	}
	if managers == 0 {
		return ev, domain.NewError(domain.Duplicate, "Нельзя удалить последнего управляющего доступом")
	}
	for id := range affected {
		ev.Affected = append(ev.Affected, id)
	}
	slices.Sort(ev.Affected)
	impactIDs := []int64{}
	for id := range impacted {
		impactIDs = append(impactIDs, id)
	}
	slices.Sort(impactIDs)
	ev.Assigned = len(impactIDs)
	for _, id := range impactIDs {
		ai := after.accountIndex(id)
		a := after.Accounts[ai]
		var before domain.AuthorizationSnapshot
		if bi := g.accountIndex(id); bi >= 0 {
			view, e := g.accountView(id)
			if e != nil {
				return ev, e
			}
			before = view.Authorization
		}
		retained := []domain.Permission{}
		for _, k := range before.Permissions {
			if slices.Contains(a.snapshot().Permissions, k) && removedAccessSource(before.Sources[k], a.snapshot().Sources[k]) {
				retained = append(retained, k)
			}
		}
		view, e := after.accountView(id)
		if e != nil {
			return ev, e
		}
		ev.Impacts = append(ev.Impacts, domain.AccessImpact{User: a.Account, Before: before, After: view.Authorization, RetainedByOtherSource: retained})
	}
	return ev, nil
}
func sameIDSet(a, b []int64) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}
func samePermissionSet(a, b []domain.Permission) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}
func changePermission(c domain.AccessChange) domain.Permission {
	if c.Role != nil {
		return "roles.manage"
	}
	if c.Group != nil {
		return "groups.manage"
	}
	if c.User != nil && c.User.ID == nil {
		return "users.create"
	}
	return "users.update"
}
func (r *Repository) PreviewAccessChange(ctx context.Context, id int64, c domain.AccessChange, q domain.AccessPageQuery) (out domain.AccessPreview, e error) {
	e = r.withAccessGraph(ctx, id, changePermission(c), func(conn db, g accessGraph) error {
		ev, e := evaluateAccessChange(ctx, conn, g, c)
		if e != nil {
			return e
		}
		if q.Table != nil {
			var err error
			out, err = previewTablePage(g, c, ev, q)
			return err
		}
		fingerprint := accessFingerprint(c, g)
		limit, last, e := pageBounds(g, q, "preview:"+fingerprint)
		if e != nil {
			return e
		}
		out = domain.AccessPreview{GraphRevision: g.Revision, ActorEpoch: g.Actor.Epoch, Fingerprint: fingerprint, AssignedUsers: ev.Assigned, ChangedUsers: 0, Items: []domain.AccessImpact{}, ProfileChange: ev.ProfileChange, SessionEffect: "none"}
		if len(ev.Affected) > 0 && !(c.User != nil && c.User.ID == nil) {
			out.SessionEffect = "revoke_on_apply"
		}
		for _, i := range ev.Impacts {
			if !samePermissionSet(i.Before.Permissions, i.After.Permissions) || !reflect.DeepEqual(i.Before.Organization, i.After.Organization) || !reflect.DeepEqual(i.Before.LegacyScope, i.After.LegacyScope) || i.Before.Active != i.After.Active {
				out.ChangedUsers++
			}
			if !i.After.Active {
				out.DisabledUsers++
			}
			if i.User.ID == id {
				out.SelfAffected = true
			}
			if !g.Actor.CanManageAccount(i.After) {
				return accessDenied()
			}
			if !matchesAccessSearch(q, i.User.Login, i.User.Name) {
				continue
			}
			if i.User.ID <= last && i.User.ID != -1 {
				continue
			}
			if len(out.Items) < limit {
				out.Items = append(out.Items, i)
			} else if out.NextCursor == nil {
				out.NextCursor = nextAccessCursor(g, q, "preview:"+fingerprint, out.Items[len(out.Items)-1].User.ID)
			}
		}
		return nil
	})
	return
}

func removedAccessSource(before, after []domain.PermissionSource) bool {
	for _, old := range before {
		found := false
		for _, current := range after {
			if old.RoleID == current.RoleID && reflect.DeepEqual(old.GroupID, current.GroupID) {
				found = true
				break
			}
		}
		if !found {
			return true
		}
	}
	return false
}
