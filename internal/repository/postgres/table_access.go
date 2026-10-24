package postgres

import (
	"context"
	"encoding/json"
	"math/big"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

// Типизированная проекция полного разрешённого графа; null отличается от пустого набора.
type accessTableValue struct {
	null    bool
	kind    string
	text    string
	number  int64
	boolean bool
	ids     []int64
	texts   []string
}
type accessTableRow struct {
	id     int64
	fields map[string]accessTableValue
}

func accessText(v string) accessTableValue  { return accessTableValue{kind: "text", text: v} }
func accessNumber(v int64) accessTableValue { return accessTableValue{kind: "number", number: v} }
func accessBool(v bool) accessTableValue    { return accessTableValue{kind: "bool", boolean: v} }
func accessIDs(v []int64) accessTableValue {
	v = slices.Clone(v)
	slices.Sort(v)
	return accessTableValue{kind: "ids", ids: slices.Compact(v)}
}
func accessTexts(v []domain.Permission) accessTableValue {
	out := make([]string, len(v))
	for i, s := range v {
		out[i] = string(s)
	}
	slices.Sort(out)
	return accessTableValue{kind: "texts", texts: slices.Compact(out)}
}

var accessFoldRunes = func() map[rune]rune {
	upper, lower := []rune(tableUpper), []rune(tableLower)
	out := make(map[rune]rune, len(upper))
	for i, r := range upper {
		out[r] = lower[i]
	}
	return out
}()

func accessFold(v string) string {
	return strings.Map(func(r rune) rune {
		if lower, ok := accessFoldRunes[r]; ok {
			return lower
		}
		return r
	}, v)
}
func accessCompare(a, b accessTableValue) int {
	if a.null {
		if b.null {
			return 0
		}
		return 1
	}
	if b.null {
		return -1
	}
	switch a.kind {
	case "text":
		return strings.Compare(accessFold(a.text), accessFold(b.text))
	case "number":
		if a.number < b.number {
			return -1
		}
		if a.number > b.number {
			return 1
		}
	case "bool":
		if a.boolean != b.boolean {
			if a.boolean {
				return 1
			}
			return -1
		}
	case "ids":
		return slices.Compare(a.ids, b.ids)
	case "texts":
		for i := 0; i < len(a.texts) && i < len(b.texts); i++ {
			if c := strings.Compare(accessFold(a.texts[i]), accessFold(b.texts[i])); c != 0 {
				return c
			}
		}
		if len(a.texts) < len(b.texts) {
			return -1
		}
		if len(a.texts) > len(b.texts) {
			return 1
		}
	}
	return 0
}
func accessMatches(v accessTableValue, f domain.TableFilter) bool {
	switch f.Op {
	case "contains":
		return !v.null && f.Value != nil && strings.Contains(accessFold(v.text), accessFold(*f.Value))
	case "in":
		for _, raw := range f.Values {
			if string(raw) == "null" {
				if v.null {
					return true
				}
				continue
			}
			if v.null {
				continue
			}
			switch v.kind {
			case "text", "texts":
				var s string
				if json.Unmarshal(raw, &s) != nil {
					continue
				}
				if v.kind == "text" && v.text == s || v.kind == "texts" && slices.Contains(v.texts, s) {
					return true
				}
			case "number", "ids":
				s := string(raw)
				if len(s) > 0 && s[0] == '"' {
					_ = json.Unmarshal(raw, &s)
				}
				n, e := strconv.ParseInt(s, 10, 64)
				if e == nil && (v.kind == "number" && v.number == n || v.kind == "ids" && slices.Contains(v.ids, n)) {
					return true
				}
			case "bool":
				var b bool
				if json.Unmarshal(raw, &b) == nil && v.boolean == b {
					return true
				}
			}
		}
		return false
	case "range":
		if v.null {
			return false
		}
		n := new(big.Rat).SetInt64(v.number)
		if len(f.From) > 0 {
			x, ok := new(big.Rat).SetString(string(f.From))
			if !ok || n.Cmp(x) < 0 {
				return false
			}
		}
		if len(f.To) > 0 {
			x, ok := new(big.Rat).SetString(string(f.To))
			if !ok || n.Cmp(x) > 0 {
				return false
			}
		}
		return true
	}
	return false
}
func accessTableInvalid() error { return domain.NewError(domain.Invalid, "invalid_table_cursor") }
func accessTablePage(g accessGraph, req domain.TableRequest, kind string, rows []accessTableRow) ([]int64, int, *string, error) {
	out := []int64{}
	if req.PageSize < 1 || req.PageSize > 100 {
		return out, 0, nil, domain.NewError(domain.Invalid, "invalid_table_query")
	}
	last := int64(0)
	hasLast := len(req.After) > 0
	if hasLast {
		var rev, epoch int64
		var bound string
		if len(req.After) != 4 || json.Unmarshal(req.After[0], &rev) != nil || json.Unmarshal(req.After[1], &epoch) != nil || json.Unmarshal(req.After[2], &bound) != nil || json.Unmarshal(req.After[3], &last) != nil || rev != g.Revision || epoch != g.Actor.Epoch || bound != kind {
			return out, 0, nil, accessTableInvalid()
		}
	}
	filtered := make([]accessTableRow, 0, len(rows))
	for _, r := range rows {
		match := true
		for _, f := range req.Query.Filters {
			v, ok := r.fields[f.Field]
			if !ok || !accessMatches(v, f) {
				match = false
				break
			}
		}
		if match {
			filtered = append(filtered, r)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		a, b := filtered[i], filtered[j]
		for _, s := range req.Query.Sort {
			av, aok := a.fields[s.Field]
			bv, bok := b.fields[s.Field]
			if !aok {
				av.null = true
			}
			if !bok {
				bv.null = true
			}
			c := accessCompare(av, bv)
			if c != 0 {
				if av.null || bv.null {
					return c < 0
				}
				if s.Direction == "desc" {
					return c > 0
				}
				return c < 0
			}
		}
		return a.id < b.id
	})
	start := 0
	if hasLast {
		found := false
		for i, r := range filtered {
			if r.id == last {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return out, 0, nil, accessTableInvalid()
		}
	}
	end := min(start+req.PageSize, len(filtered))
	for _, r := range filtered[start:end] {
		out = append(out, r.id)
	}
	var next *string
	if end < len(filtered) {
		keys := make([]json.RawMessage, 4)
		for i, v := range []any{g.Revision, g.Actor.Epoch, kind, out[len(out)-1]} {
			keys[i], _ = json.Marshal(v)
		}
		token, e := service.AccessTableCursor(req, keys)
		if e != nil {
			return out, 0, nil, e
		}
		next = &token
	}
	return out, len(filtered), next, nil
}
func accessOrganizationFields(fields map[string]accessTableValue, s *domain.OrganizationScope) {
	fields["organization_kind"] = accessTableValue{null: true}
	fields["ngdu_id"] = accessTableValue{null: true}
	fields["department_id"] = accessTableValue{null: true}
	if s != nil {
		fields["organization_kind"] = accessText(s.Kind)
		if s.NGDUID != nil {
			fields["ngdu_id"] = accessNumber(*s.NGDUID)
		}
		if s.DepartmentID != nil {
			fields["department_id"] = accessNumber(*s.DepartmentID)
		}
	}
}
func accessAccountFields(g accessGraph, a authorizationRow) map[string]accessTableValue {
	f := map[string]accessTableValue{"name": accessText(a.Account.Name), "login": accessText(a.Account.Login), "active": accessBool(a.Account.Active), "role_ids": accessIDs(directAccessRoles(a)), "group_ids": accessIDs(g.userGroups(a.Account.ID))}
	// Пользователь видит весь effective набор ролей, включая назначения через группы.
	ids := []int64{}
	for _, grant := range a.Grants {
		ids = append(ids, grant.Role.ID)
	}
	f["role_ids"] = accessIDs(ids)
	accessOrganizationFields(f, a.Organization)
	return f
}
func accountTablePage(g accessGraph, q domain.AccessPageQuery, kind string, groupID *int64) (domain.AccountPage, error) {
	out := domain.AccountPage{Items: []domain.Account{}, GraphRevision: g.Revision}
	if q.GraphRevision != nil && *q.GraphRevision != g.Revision {
		return out, accessTableInvalid()
	}
	rows := []accessTableRow{}
	accounts := map[int64]domain.Account{}
	for _, a := range g.Accounts {
		if !g.Actor.CanManageAccount(a.snapshot()) {
			continue
		}
		if groupID != nil && !slices.Contains(g.userGroups(a.Account.ID), *groupID) {
			continue
		}
		rows = append(rows, accessTableRow{a.Account.ID, accessAccountFields(g, a)})
		accounts[a.Account.ID] = a.Account
	}
	ids, total, next, e := accessTablePage(g, *q.Table, kind, rows)
	if e != nil {
		return out, e
	}
	out.TotalVisible = total
	out.NextCursor = next
	for _, id := range ids {
		out.Items = append(out.Items, accounts[id])
	}
	return out, nil
}
func roleTablePage(g accessGraph, q domain.AccessPageQuery) (domain.RolePage, error) {
	out := domain.RolePage{Items: []domain.AccessRole{}, GraphRevision: g.Revision}
	if q.GraphRevision != nil && *q.GraphRevision != g.Revision {
		return out, accessTableInvalid()
	}
	rows := []accessTableRow{}
	items := map[int64]domain.AccessRole{}
	for _, a := range g.Roles {
		if !g.roleVisible(a) {
			continue
		}
		rows = append(rows, accessTableRow{a.ID, map[string]accessTableValue{"name": accessText(a.Name), "description": accessText(a.Description), "permission_keys": accessTexts(a.PermissionKeys), "permission_count": accessNumber(int64(len(a.PermissionKeys)))}})
		items[a.ID] = a
	}
	ids, total, next, e := accessTablePage(g, *q.Table, "roles", rows)
	if e != nil {
		return out, e
	}
	out.TotalVisible = total
	out.NextCursor = next
	for _, id := range ids {
		out.Items = append(out.Items, items[id])
	}
	return out, nil
}
func groupTablePage(g accessGraph, q domain.AccessPageQuery) (domain.GroupPage, error) {
	out := domain.GroupPage{Items: []domain.AccessGroup{}, GraphRevision: g.Revision}
	if q.GraphRevision != nil && *q.GraphRevision != g.Revision {
		return out, accessTableInvalid()
	}
	rows := []accessTableRow{}
	items := map[int64]domain.AccessGroup{}
	for _, a := range g.Groups {
		if !g.groupVisible(a) {
			continue
		}
		rows = append(rows, accessTableRow{a.ID, map[string]accessTableValue{"name": accessText(a.Name), "description": accessText(a.Description), "role_ids": accessIDs(a.RoleIDs)}})
		items[a.ID] = a
	}
	ids, total, next, e := accessTablePage(g, *q.Table, "groups", rows)
	if e != nil {
		return out, e
	}
	out.TotalVisible = total
	out.NextCursor = next
	for _, id := range ids {
		out.Items = append(out.Items, items[id])
	}
	return out, nil
}

// accessOrganizationText совпадает с существующим organizationLabel интерфейса.
func accessOrganizationText(s *domain.OrganizationScope) accessTableValue {
	if s == nil {
		return accessText("Прежняя область доступа")
	}
	switch s.Kind {
	case "none":
		return accessText("Подразделение не назначено")
	case "organization":
		return accessText("Вся организация")
	default:
		text := "НГДУ "
		if s.NGDUID != nil {
			text += strconv.FormatInt(*s.NGDUID, 10)
		}
		if s.Kind == "department" {
			text += " / Цех "
			if s.DepartmentID != nil {
				text += strconv.FormatInt(*s.DepartmentID, 10)
			}
		}
		return accessText(text)
	}
}
func accessImpactFields(i domain.AccessImpact) map[string]accessTableValue {
	added, removed := []domain.Permission{}, []domain.Permission{}
	for _, p := range i.After.Permissions {
		if !slices.Contains(i.Before.Permissions, p) {
			added = append(added, p)
		}
	}
	for _, p := range i.Before.Permissions {
		if !slices.Contains(i.After.Permissions, p) {
			removed = append(removed, p)
		}
	}
	roleIDs, groupIDs := []int64{}, []int64{}
	for _, s := range []domain.AuthorizationSnapshot{i.After} {
		for _, sources := range s.Sources {
			for _, source := range sources {
				roleIDs = append(roleIDs, source.RoleID)
				if source.GroupID != nil {
					groupIDs = append(groupIDs, *source.GroupID)
				}
			}
		}
	}
	return map[string]accessTableValue{"user_name": accessText(i.User.Name), "before_organization": accessOrganizationText(i.Before.Organization), "after_organization": accessOrganizationText(i.After.Organization), "before_active": accessBool(i.Before.Active), "after_active": accessBool(i.After.Active), "added_permissions": accessTexts(added), "removed_permissions": accessTexts(removed), "source_role_ids": accessIDs(roleIDs), "source_group_ids": accessIDs(groupIDs)}
}
func previewTablePage(g accessGraph, c domain.AccessChange, ev accessEvaluation, q domain.AccessPageQuery) (domain.AccessPreview, error) {
	out := domain.AccessPreview{GraphRevision: g.Revision, ActorEpoch: g.Actor.Epoch, Fingerprint: accessFingerprint(c, g), AssignedUsers: ev.Assigned, Items: []domain.AccessImpact{}, ProfileChange: ev.ProfileChange, SessionEffect: "none"}
	if q.GraphRevision != nil && *q.GraphRevision != g.Revision {
		return out, accessTableInvalid()
	}
	if len(ev.Affected) > 0 && !(c.User != nil && c.User.ID == nil) {
		out.SessionEffect = "revoke_on_apply"
	}
	rows := []accessTableRow{}
	items := map[int64]domain.AccessImpact{}
	for _, i := range ev.Impacts {
		if !samePermissionSet(i.Before.Permissions, i.After.Permissions) || !reflect.DeepEqual(i.Before.Organization, i.After.Organization) || !reflect.DeepEqual(i.Before.LegacyScope, i.After.LegacyScope) || i.Before.Active != i.After.Active {
			out.ChangedUsers++
		}
		if !i.After.Active {
			out.DisabledUsers++
		}
		if i.User.ID == g.Actor.AccountID {
			out.SelfAffected = true
		}
		if !g.Actor.CanManageAccount(i.After) {
			return out, accessDenied()
		}
		rows = append(rows, accessTableRow{i.User.ID, accessImpactFields(i)})
		items[i.User.ID] = i
	}
	out.TableFilterOptions = accessPreviewFilterOptions(ev.Impacts)
	ids, _, next, e := accessTablePage(g, *q.Table, "preview:"+out.Fingerprint, rows)
	if e != nil {
		return out, e
	}
	out.NextCursor = next
	for _, id := range ids {
		out.Items = append(out.Items, items[id])
	}
	return out, nil
}

// ListAccessUserFilterOptions не читает registry definitions: источник — только
// сводки ролей/групп из тех AccountAccessView, которые разрешены этому manager.
func (r *Repository) ListAccessUserFilterOptions(ctx context.Context, actor int64, field string, req domain.TableRequest) (out domain.AccessFilterOptionsPage, e error) {
	if field != "role_ids" && field != "group_ids" && field != "ngdu_id" && field != "department_id" {
		return out, domain.NewError(domain.Invalid, "invalid_table_query")
	}
	e = r.withAccessGraph(ctx, actor, "users.read", func(_ db, g accessGraph) error {
		out = domain.AccessFilterOptionsPage{Items: []domain.AccessFilterOption{}, GraphRevision: g.Revision}
		options := map[int64]domain.AccessFilterOption{}
		for _, a := range g.Accounts {
			if !g.Actor.CanManageAccount(a.snapshot()) {
				continue
			}
			view, err := g.accountView(a.Account.ID)
			if err != nil {
				return err
			}
			if field == "role_ids" {
				for _, role := range view.Authorization.Assignments.Roles {
					options[role.ID] = domain.AccessFilterOption{Value: role.ID, Label: role.Name}
				}
			} else if field == "group_ids" {
				for _, group := range view.Authorization.Assignments.Groups {
					options[group.ID] = domain.AccessFilterOption{Value: group.ID, Label: group.Name}
				}
			} else if option, ok := accessUserOrganizationOption(view, field); ok {
				options[option.Value] = option
			}

		}
		rows := make([]accessTableRow, 0, len(options))
		for id, option := range options {
			rows = append(rows, accessTableRow{id, map[string]accessTableValue{"name": accessText(option.Label)}})
		}
		ids, _, next, err := accessTablePage(g, req, "filter-options:"+field, rows)
		if err != nil {
			return err
		}
		out.NextCursor = next
		for _, id := range ids {
			out.Items = append(out.Items, options[id])
		}
		return nil
	})
	return
}

// ListAccessGroupFilterOptions сохраняет границу существующего Groups DTO:
// только DISTINCT role_ids видимых групп, без раскрытия определений ролей.
func (r *Repository) ListAccessGroupFilterOptions(ctx context.Context, actor int64, req domain.TableRequest) (out domain.AccessFilterOptionsPage, e error) {
	e = r.withAccessGraph(ctx, actor, "groups.read", func(_ db, g accessGraph) error {
		out = domain.AccessFilterOptionsPage{Items: []domain.AccessFilterOption{}, GraphRevision: g.Revision}
		options := map[int64]domain.AccessFilterOption{}
		for _, group := range g.Groups {
			if !g.groupVisible(group) {
				continue
			}
			for _, id := range group.RoleIDs {
				options[id] = domain.AccessFilterOption{Value: id, Label: "ID " + strconv.FormatInt(id, 10)}
			}
		}
		rows := make([]accessTableRow, 0, len(options))
		for id, option := range options {
			rows = append(rows, accessTableRow{id, map[string]accessTableValue{"name": accessText(option.Label)}})
		}
		ids, _, next, err := accessTablePage(g, req, "group-filter-options:role_ids", rows)
		if err != nil {
			return err
		}
		out.NextCursor = next
		for _, id := range ids {
			out.Items = append(out.Items, options[id])
		}
		return nil
	})
	return
}

func accessPreviewFilterOptions(impacts []domain.AccessImpact) *domain.AccessPreviewFilterOptions {
	roles, groups := map[int64]string{}, map[int64]string{}
	for _, impact := range impacts {
		for _, sources := range impact.After.Sources {
			for _, source := range sources {
				roles[source.RoleID] = source.RoleName
				if source.GroupID != nil {
					label := "ID " + strconv.FormatInt(*source.GroupID, 10)
					if source.GroupName != nil {
						label = *source.GroupName
					}
					groups[*source.GroupID] = label
				}
			}
		}
	}
	sorted := func(values map[int64]string) []domain.AccessFilterOption {
		ids := make([]int64, 0, len(values))
		for id := range values {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		out := make([]domain.AccessFilterOption, 0, len(ids))
		for _, id := range ids {
			out = append(out, domain.AccessFilterOption{Value: id, Label: values[id]})
		}
		return out
	}
	return &domain.AccessPreviewFilterOptions{SourceRoleIDs: sorted(roles), SourceGroupIDs: sorted(groups)}
}

// Коды берутся только из уже разрешённого AccountAccessView.Organization.
func accessUserOrganizationOption(view domain.AccountAccessView, field string) (domain.AccessFilterOption, bool) {
	org := view.Authorization.Organization
	if org == nil {
		return domain.AccessFilterOption{}, false
	}
	var code *int64
	switch field {
	case "ngdu_id":
		code = org.NGDUID
	case "department_id":
		code = org.DepartmentID
	default:
		return domain.AccessFilterOption{}, false
	}
	if code == nil {
		return domain.AccessFilterOption{}, false
	}
	return domain.AccessFilterOption{Value: *code, Label: "ID " + strconv.FormatInt(*code, 10)}, true
}
