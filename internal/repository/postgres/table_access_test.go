package postgres

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func accessTestRequest(filters []domain.TableFilter, sorts ...domain.TableSort) domain.TableRequest {
	return domain.TableRequest{Query: domain.TableQuery{Version: 1, Filters: filters, Sort: sorts}, PageSize: 25, Fingerprint: "test"}
}
func TestAccessTableFiltersWholeGraphBeforePage(t *testing.T) {
	g := accessGraph{Revision: 7, Actor: domain.AuthorizationSnapshot{AccountID: 1, Epoch: 3}}
	rows := []accessTableRow{}
	for i := int64(1); i <= 61; i++ {
		rows = append(rows, accessTableRow{id: i, fields: map[string]accessTableValue{"name": accessText(fmt.Sprintf("row%03d", i))}})
	}
	needle := "row061"
	req := accessTestRequest([]domain.TableFilter{{Field: "name", Op: "contains", Value: &needle}})
	ids, total, next, err := accessTablePage(g, req, "users", rows)
	if err != nil || total != 1 || len(ids) != 1 || ids[0] != 61 || next != nil {
		t.Fatalf("whole graph: %v %d %v %v", ids, total, next, err)
	}
}
func TestAccessTableCompleteOrderAndTerminalPage(t *testing.T) {
	g := accessGraph{Revision: 7, Actor: domain.AuthorizationSnapshot{AccountID: 1, Epoch: 3}}
	rows := []accessTableRow{}
	for i := int64(50); i > 0; i-- {
		rows = append(rows, accessTableRow{id: i, fields: map[string]accessTableValue{"name": accessText("ӘБВ"), "active": accessBool(i%2 == 0)}})
	}
	req := accessTestRequest(nil, domain.TableSort{Field: "name", Direction: "asc"}, domain.TableSort{Field: "active", Direction: "asc"})
	ids, total, next, err := accessTablePage(g, req, "users", rows)
	if err != nil || total != 50 || len(ids) != 25 || ids[0] != 1 || ids[24] != 49 || next == nil {
		t.Fatalf("first: %v %d %v", ids, total, err)
	}
	// Decode using the shared service parser in service tests; repository consumes authenticated keys.
	req.After = []json.RawMessage{json.RawMessage(`7`), json.RawMessage(`3`), json.RawMessage(`"users"`), json.RawMessage(`49`)}
	ids, _, next, err = accessTablePage(g, req, "users", rows)
	if err != nil || len(ids) != 25 || ids[0] != 2 || ids[24] != 50 || next != nil {
		t.Fatalf("terminal: %v %v %v", ids, next, err)
	}
	req.After[0] = json.RawMessage(`6`)
	if _, _, _, err = accessTablePage(g, req, "users", rows); err == nil {
		t.Fatal("stale graph accepted")
	}
	req.After[0] = json.RawMessage(`7`)
	req.After[2] = json.RawMessage(`"members:9"`)
	if _, _, _, err = accessTablePage(g, req, "users", rows); err == nil {
		t.Fatal("different parent accepted")
	}
}
func TestAccessTableNullZeroAndExactIDSets(t *testing.T) {
	g := accessGraph{Revision: 1, Actor: domain.AuthorizationSnapshot{AccountID: 1, Epoch: 1}}
	rows := []accessTableRow{{id: 1, fields: map[string]accessTableValue{"code": accessNumber(0)}}, {id: 2, fields: map[string]accessTableValue{"code": {null: true}}}, {id: 3, fields: map[string]accessTableValue{"code": accessNumber(-900)}}}
	for _, direction := range []string{"asc", "desc"} {
		req := accessTestRequest(nil, domain.TableSort{Field: "code", Direction: direction})
		ids, _, _, err := accessTablePage(g, req, "users", rows)
		if err != nil || ids[2] != 2 {
			t.Fatalf("null last %s: %v %v", direction, ids, err)
		}
	}
	rows = []accessTableRow{{id: 1, fields: map[string]accessTableValue{"role_ids": accessIDs([]int64{9007199254740993, 2})}}, {id: 2, fields: map[string]accessTableValue{"role_ids": accessIDs([]int64{9007199254740992, 2})}}}
	req := accessTestRequest([]domain.TableFilter{{Field: "role_ids", Op: "in", Values: []json.RawMessage{json.RawMessage(`"9007199254740993"`)}}})
	ids, _, _, err := accessTablePage(g, req, "users", rows)
	if err != nil || len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("lossy ID comparison: %v %v", ids, err)
	}
}

func TestAccessTableOrganizationUsesExistingVisibleLabel(t *testing.T) {
	ngdu, dep := int64(0), int64(-900)
	cases := []struct {
		scope *domain.OrganizationScope
		want  string
	}{
		{nil, "Прежняя область доступа"},
		{&domain.OrganizationScope{Kind: "none"}, "Подразделение не назначено"},
		{&domain.OrganizationScope{Kind: "organization"}, "Вся организация"},
		{&domain.OrganizationScope{Kind: "ngdu", NGDUID: &ngdu}, "НГДУ 0"},
		{&domain.OrganizationScope{Kind: "department", NGDUID: &ngdu, DepartmentID: &dep}, "НГДУ 0 / Цех -900"},
	}
	for _, v := range cases {
		got := accessOrganizationText(v.scope)
		if got.null || got.text != v.want {
			t.Fatalf("label: %+v want %q", got, v.want)
		}
	}
}

func TestAccessTablePreviewOptionsUseFullAfterSourcesOnly(t *testing.T) {
	group := int64(-1)
	oldGroup := int64(8)
	name := "Новая группа"
	old := domain.PermissionSource{RoleID: 99, RoleName: "old-only", GroupID: &oldGroup}
	newSource := domain.PermissionSource{RoleID: 9007199254740993, RoleName: "Новая роль", GroupID: &group, GroupName: &name}
	impacts := make([]domain.AccessImpact, 30)
	for i := range impacts {
		impacts[i] = domain.AccessImpact{Before: domain.AuthorizationSnapshot{Sources: map[domain.Permission][]domain.PermissionSource{"wells.read": {old}}}, After: domain.AuthorizationSnapshot{Sources: map[domain.Permission][]domain.PermissionSource{"wells.read": {newSource}}}}
	}
	impacts[29].After.Sources["other"] = []domain.PermissionSource{{RoleID: 2, RoleName: "Ещё роль"}}
	options := accessPreviewFilterOptions(impacts)
	if len(options.SourceRoleIDs) != 2 || options.SourceRoleIDs[0].Value != 2 || options.SourceRoleIDs[1].Value != 9007199254740993 || len(options.SourceGroupIDs) != 1 || options.SourceGroupIDs[0].Value != -1 || options.SourceGroupIDs[0].Label != name {
		t.Fatalf("incomplete/old source options %+v", options)
	}
	fields := accessImpactFields(impacts[0])
	if slices.Contains(fields["source_role_ids"].ids, 99) || slices.Contains(fields["source_group_ids"].ids, 8) {
		t.Fatal("before-only sources remain filterable", fields)
	}
}

func TestAccessTableOrganizationFacetPreservesZeroNegativeAndMissing(t *testing.T) {
	zero, negative := int64(0), int64(-900)
	view := domain.AccountAccessView{Authorization: domain.AuthorizationSnapshot{Organization: &domain.OrganizationScope{Kind: "department", NGDUID: &zero, DepartmentID: &negative}}}
	for _, test := range []struct {
		field string
		want  int64
	}{{"ngdu_id", 0}, {"department_id", -900}} {
		option, ok := accessUserOrganizationOption(view, test.field)
		if !ok || option.Value != test.want || option.Label != fmt.Sprintf("ID %d", test.want) {
			t.Fatal("organization code changed", option, ok)
		}
	}
	view.Authorization.Organization = nil
	if _, ok := accessUserOrganizationOption(view, "ngdu_id"); ok {
		t.Fatal("legacy missing organization invented a code")
	}
}
