package domain

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func rbacID(v int64) *int64     { return &v }
func rbacName(v string) *string { return &v }
func rbacSnapshot(keys ...Permission) AuthorizationSnapshot {
	return AuthorizationSnapshot{Active: true, Permissions: keys, Organization: &OrganizationScope{Kind: "department", NGDUID: rbacID(1), DepartmentID: rbacID(3)}, LegacyScope: AccessScope{Funds: []string{"operational"}}}
}
func rbacForbidden(t *testing.T, err error) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Kind != Forbidden {
		t.Fatalf("want forbidden, got %v", err)
	}
}

// Removing one source must not revoke rights still supplied by another role/group.
func TestRBACUnionSources(t *testing.T) {
	r1 := AccessRole{ID: 1, Name: "telemetry", Revision: 2, PermissionKeys: []Permission{"telemetry.read"}}
	r2 := AccessRole{ID: 2, Name: "wells", Revision: 1, PermissionKeys: []Permission{"wells.read"}}
	direct := RolePermissionGrant{Role: r1}
	groupA := RolePermissionGrant{Role: r2, GroupID: rbacID(10), GroupName: rbacName("A")}
	groupB := RolePermissionGrant{Role: r1, GroupID: rbacID(11), GroupName: rbacName("B")}
	keys, sources := ResolveRolePermissions([]RolePermissionGrant{groupB, direct, groupA, direct})
	if !reflect.DeepEqual(keys, []Permission{"telemetry.read", "wells.read"}) || len(sources["telemetry.read"]) != 2 {
		t.Fatalf("union %v sources %v", keys, sources)
	}
	if sources["telemetry.read"][0].GroupID != nil || sources["telemetry.read"][0].RoleRevision != 2 {
		t.Fatal("missing deterministic direct role revision")
	}
	a := rbacSnapshot(keys...)
	if err := a.Require("telemetry.read"); err != nil {
		t.Fatal(err)
	}
	keys, sources = ResolveRolePermissions([]RolePermissionGrant{groupA, groupB})
	a.Permissions = keys
	if a.Require("telemetry.read") != nil || len(sources["telemetry.read"]) != 1 || len(sources["wells.read"]) != 1 {
		t.Fatal("remaining sources lost")
	}
	keys, _ = ResolveRolePermissions([]RolePermissionGrant{groupA})
	a.Permissions = keys
	rbacForbidden(t, a.Require("telemetry.read"))
	keys, _ = ResolveRolePermissions([]RolePermissionGrant{direct})
	if slices.Contains(keys, Permission("wells.read")) {
		t.Fatal("prerequisites silently granted")
	}
	a.Permissions = keys
	rbacForbidden(t, a.Require("telemetry.read"))
	// Returned provenance must not alias mutable caller pointers.
	_, sources = ResolveRolePermissions([]RolePermissionGrant{groupB})
	*groupB.GroupID = 99
	if *sources["telemetry.read"][0].GroupID != 11 {
		t.Fatal("source aliases caller")
	}
}

func TestRBACScope(t *testing.T) {
	a := rbacSnapshot("wells.read")
	for _, tc := range []struct {
		ngdu, department int64
		fund             string
		want             bool
	}{{1, 3, "", true}, {2, 3, "", false}, {1, 4, "", false}, {1, 3, "presentation", false}} {
		if got := a.AllowsWell(Well{NGDU: tc.ngdu, Department: tc.department, FundType: tc.fund}); got != tc.want {
			t.Fatalf("%+v got %v", tc, got)
		}
	}
	a.Organization = &OrganizationScope{Kind: "ngdu", NGDUID: rbacID(1)}
	if !a.AllowsWell(Well{NGDU: 1, Department: 99}) || a.AllowsWell(Well{NGDU: 2}) {
		t.Fatal("ngdu scope")
	}
	a.Organization = &OrganizationScope{Kind: "department", NGDUID: rbacID(0), DepartmentID: rbacID(-1)}
	if !a.AllowsWell(Well{NGDU: 0, Department: -1}) || a.AllowsWell(Well{NGDU: 0, Department: 0}) {
		t.Fatal("literal scope IDs")
	}
	a.Organization = &OrganizationScope{Kind: "organization"}
	if !a.AllowsWell(Well{NGDU: 999}) || a.AllowsWell(Well{FundType: "presentation"}) {
		t.Fatal("organization funds intersection")
	}
	a.Organization = &OrganizationScope{Kind: "none"}
	if a.AllowsWell(Well{}) {
		t.Fatal("none grants data")
	}
	for _, org := range []*OrganizationScope{{Kind: "unknown"}, {Kind: "department", NGDUID: rbacID(1)}, {Kind: "organization", NGDUID: rbacID(1)}} {
		a.Organization = org
		if a.AllowsWell(Well{NGDU: 1}) {
			t.Fatalf("invalid org accepted %+v", org)
		}
	}
	a.Organization = nil
	ids := []int64{0, -1}
	empty := []int64{}
	a.LegacyScope.NGDUs = &ids
	if !a.AllowsWell(Well{NGDU: -1}) || a.AllowsWell(Well{NGDU: 1}) {
		t.Fatal("legacy IDs")
	}
	a.LegacyScope.Departments = &empty
	if a.AllowsWell(Well{NGDU: 0}) {
		t.Fatal("legacy empty array unrestricted")
	}
	a.LegacyScope.Departments = nil
	a.LegacyScope.NGDUs = nil
	a.LegacyScope.Funds = nil
	if a.AllowsWell(Well{}) {
		t.Fatal("nil funds unrestricted")
	}
}

func TestRBACNoImplicitAuthority(t *testing.T) {
	rbacForbidden(t, RequirePermissions(context.Background(), "wells.read"))
	a := rbacSnapshot()
	p := Principal{Role: "admin", Authenticated: true, Authorization: &a}
	ctx := WithPrincipal(context.Background(), p)
	rbacForbidden(t, RequirePermissions(ctx, "wells.read"))
	a.Permissions = []Permission{"wells.read"}
	if RequirePermissions(ctx, "wells.read") != nil {
		t.Fatal("explicit key denied")
	}
	rbacForbidden(t, RequirePermissions(ctx, "*"))
	a.Active = false
	rbacForbidden(t, RequirePermissions(ctx, "wells.read"))
	a.Active = true
	p.Authenticated = false
	rbacForbidden(t, RequirePermissions(WithPrincipal(context.Background(), p), "wells.read"))
	p.Authenticated = true
	p.Authorization = nil
	rbacForbidden(t, RequirePermissions(WithPrincipal(context.Background(), p), "wells.read"))
	a = rbacSnapshot("wells.read")
	p.Authorization = &a
	if p.Allows(Well{NGDU: 2, Department: 3}) {
		t.Fatal("admin label bypasses snapshot scope")
	}
	a.Permissions = []Permission{"checks.run", "checks.read", "wells.read"}
	rbacForbidden(t, a.Require("checks.run"))
	if slices.Contains(a.Permissions, Permission("telemetry.read")) {
		t.Fatal("Require mutates grants")
	}
	a.Permissions = append(a.Permissions, "telemetry.read")
	if a.Require("checks.run") != nil {
		t.Fatal("union prerequisites denied")
	}
	a.Permissions = append(a.Permissions, "unknown")
	rbacForbidden(t, a.Require("wells.read"))
}

func TestRBACManagementBoundary(t *testing.T) {
	actor := rbacSnapshot("users.read", "users.update", "wells.read")
	target := rbacSnapshot("wells.read")
	if !actor.CanManageAccount(target) || !actor.CanGrant([]Permission{"wells.read"}) {
		t.Fatal("safe delegation denied")
	}
	target.Organization = &OrganizationScope{Kind: "ngdu", NGDUID: rbacID(1)}
	if actor.CanManageAccount(target) {
		t.Fatal("department manages ngdu")
	}
	actor.Organization = &OrganizationScope{Kind: "ngdu", NGDUID: rbacID(1)}
	if !actor.CanManageAccount(target) {
		t.Fatal("ngdu cannot manage itself")
	}
	target.Organization = &OrganizationScope{Kind: "department", NGDUID: rbacID(2), DepartmentID: rbacID(3)}
	if actor.CanManageAccount(target) {
		t.Fatal("matching department bypasses ngdu")
	}
	target = rbacSnapshot("access.manage_global")
	if actor.CanManageAccount(target) {
		t.Fatal("local manages privileged same-department target")
	}
	target = rbacSnapshot("wells.read")
	target.LegacyScope.Funds = append(target.LegacyScope.Funds, "presentation")
	if actor.CanManageAccount(target) {
		t.Fatal("target funds exceed actor")
	}
	target = rbacSnapshot("wells.read")
	target.Organization = &OrganizationScope{Kind: "none"}
	if actor.CanManageAccount(target) {
		t.Fatal("none target captured")
	}
	target.Organization = nil
	if actor.CanManageAccount(target) {
		t.Fatal("legacy target captured")
	}
	target = rbacSnapshot("wells.read")
	actor.Organization = nil
	if actor.CanManageAccount(target) {
		t.Fatal("legacy actor delegates locally")
	}
	actor.Permissions = append(actor.Permissions, "access.manage_global")
	if !actor.CanManageAccount(target) {
		t.Fatal("explicit global management denied")
	}
	if actor.CanGrant([]Permission{"runs.start"}) || actor.CanGrant([]Permission{"unknown"}) {
		t.Fatal("global manager grants unowned keys")
	}
	actor = rbacSnapshot("access.manage_global")
	if actor.AllowsWell(Well{NGDU: 2, Department: 3}) {
		t.Fatal("global management expands data scope")
	}
	actor.Active = false
	if actor.CanManageAccount(target) || actor.CanGrant(nil) {
		t.Fatal("inactive actor manages")
	}
	actor = rbacSnapshot("wells.read")
	actor.Organization = &OrganizationScope{Kind: "organization"}
	target = rbacSnapshot("checks.run") // Incomplete prerequisites are stored, but not effective.
	if !actor.CanManageAccount(target) || actor.CanGrant(target.Permissions) {
		t.Fatal("effective target rights / raw grants confused")
	}
}

func TestRBACCatalog(t *testing.T) {
	expected := strings.Fields(`equipment_passports.read equipment_passports.update equipment_passports.import equipment_passports.export equipment_passports.history.read wells.read wells.create wells.update wells.import wells.export wells.archive wells.restore wells.delete wells.history.read telemetry.read telemetry.import telemetry.compat.read telemetry.compat.score risks.read risks.explain risks.export risks.external.read checks.read checks.run checks.review filechecks.read filechecks.run filechecks.cancel filechecks.review filechecks.legacy.run alerts.read alerts.review incidents.read incidents.create incidents.delete references.read parameters.read references.create references.update references.delete runs.read runs.export runs.start cohort.runs.read services.read metrics.read analytics.read audit.read presentation.scenario.read integrations.read integrations.create integrations.update integrations.check integrations.status integrations.logs.read integrations.collect integrations.ingest collectionjobs.read collectionjobs.run collectionjobs.cancel cohortjobs.read cohortjobs.run cohortjobs.cancel users.read users.update users.scope.assign users.create roles.read groups.read users.roles.assign users.groups.assign roles.manage groups.manage access.manage_global`)
	slices.Sort(expected)
	catalog := PermissionCatalog()
	got := []string{}
	all := rbacSnapshot()
	seen := map[Permission]bool{}
	for i, d := range catalog {
		got = append(got, string(d.Key))
		all.Permissions = append(all.Permissions, d.Key)
		if seen[d.Key] || d.Name == "" || d.Description == "" || d.Resource == "" || d.Action == "" || (d.ScopeKind != "organization" && d.ScopeKind != "global") {
			t.Fatalf("invalid definition %+v", d)
		}
		seen[d.Key] = true
		if i > 0 && (catalog[i-1].Resource > d.Resource || catalog[i-1].Resource == d.Resource && catalog[i-1].Key >= d.Key) {
			t.Fatal("catalog not sorted")
		}
	}
	slices.Sort(got)
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("catalog keys %v", got)
	}
	// Every dependency is real and the graph is acyclic; every direct prerequisite is enforced.
	var visit func(Permission, map[Permission]bool)
	byKey := map[Permission]PermissionDefinition{}
	for _, d := range catalog {
		byKey[d.Key] = d
	}
	visit = func(k Permission, path map[Permission]bool) {
		if path[k] {
			t.Fatalf("cycle at %s", k)
		}
		d, ok := byKey[k]
		if !ok {
			t.Fatalf("unknown dependency %s", k)
		}
		path[k] = true
		for _, r := range d.Requires {
			visit(r, path)
		}
		delete(path, k)
	}
	for _, d := range catalog {
		visit(d.Key, map[Permission]bool{})
		if all.Require(d.Key) != nil {
			t.Fatalf("complete catalog denies %s", d.Key)
		}
		for _, req := range d.Requires {
			a := all
			a.Permissions = slices.DeleteFunc(slices.Clone(all.Permissions), func(k Permission) bool { return k == req })
			rbacForbidden(t, a.Require(d.Key))
		}
	}
	catalog[0].Name = "mutated"
	if PermissionCatalog()[0].Name == "mutated" {
		t.Fatal("catalog exposes shared state")
	}
	for _, d := range catalog {
		if len(d.Requires) > 0 {
			d.Requires[0] = "unknown"
			break
		}
	}
	if all.Require("checks.run") != nil {
		t.Fatal("catalog prerequisites expose shared state")
	}
}

// Disabled users remain manageable; disabling never erases their privilege boundary.
func TestRBACInactiveTargetManagement(t *testing.T) {
	actor := rbacSnapshot("users.read", "users.update", "wells.read")
	target := rbacSnapshot("wells.read")
	target.Active = false
	if !actor.CanManageAccount(target) {
		t.Fatal("safe disabled target cannot be managed/reactivated")
	}
	target.Permissions = []Permission{"access.manage_global"}
	if actor.CanManageAccount(target) {
		t.Fatal("disabled privileged target became locally manageable")
	}
	target.Permissions = []Permission{"wells.read"}
	target.Organization = &OrganizationScope{Kind: "department", NGDUID: rbacID(2), DepartmentID: rbacID(3)}
	if actor.CanManageAccount(target) {
		t.Fatal("disabled target outside actor scope became manageable")
	}
	actor.Active = false
	target = rbacSnapshot("wells.read")
	if actor.CanManageAccount(target) {
		t.Fatal("inactive actor manages active target")
	}
}

func TestRBACUnknownRolePermissionFailsClosed(t *testing.T) {
	keys, sources := ResolveRolePermissions([]RolePermissionGrant{{Role: AccessRole{ID: 1, PermissionKeys: []Permission{"wells.read", "unregistered.read"}}}})
	if !slices.Contains(keys, Permission("unregistered.read")) || len(sources["unregistered.read"]) != 1 {
		t.Fatal("resolver concealed an unknown stored permission")
	}
	a := rbacSnapshot(keys...)
	rbacForbidden(t, a.Require("wells.read"))
	if a.CanGrant([]Permission{"wells.read"}) {
		t.Fatal("invalid actor grants rights")
	}
	actor := rbacSnapshot("wells.read")
	if actor.CanManageAccount(a) {
		t.Fatal("invalid target accepted for local delegation")
	}
}

func TestEquipmentPassportPermissions(t *testing.T) {
	deps := map[Permission][]Permission{
		"equipment_passports.read":         {"wells.read"},
		"equipment_passports.update":       {"equipment_passports.read"},
		"equipment_passports.history.read": {"equipment_passports.read"},
		"equipment_passports.import":       {"equipment_passports.update"},
		"equipment_passports.export":       {"equipment_passports.read"},
	}
	for key, want := range deps {
		var found bool
		for _, d := range PermissionCatalog() {
			if d.Key == key {
				found = true
				if !reflect.DeepEqual(d.Requires, want) {
					t.Fatalf("%s requires %v", key, d.Requires)
				}
			}
		}
		if !found {
			t.Fatalf("missing %s", key)
		}
		keys := []Permission{"wells.read", "equipment_passports.read", "equipment_passports.update", key}
		a := rbacSnapshot(keys...)
		if a.Require(key) != nil {
			t.Fatalf("closure denied %s", key)
		}
		a.Permissions = []Permission{key}
		rbacForbidden(t, a.Require(key))
		for _, role := range []string{"admin", "operator", "viewer"} {
			if slices.Contains(BuiltinAccessPermissions(role), key) {
				t.Fatalf("%s automatically granted %s", role, key)
			}
		}
	}
}
