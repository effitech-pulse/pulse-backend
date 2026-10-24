package domain

import (
	"context"
	"slices"
)

type AccessRoleSummary struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Revision   int64   `json:"revision"`
	BuiltinKey *string `json:"builtin_key"`
}
type AccessGroupMembership struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Revision int64   `json:"revision"`
	RoleIDs  []int64 `json:"role_ids"`
}
type AccessAssignments struct {
	DirectRoleIDs []int64                 `json:"direct_role_ids"`
	Groups        []AccessGroupMembership `json:"groups"`
	Roles         []AccessRoleSummary     `json:"roles"`
}

type AccountAccessView struct {
	Profile       *ManagedProfile       `json:"profile,omitempty"`
	Account       Account               `json:"account"`
	Authorization AuthorizationSnapshot `json:"authorization"`
}

// RBACMode is selected by trusted server wiring; compat remains the default.
type RBACMode string

const (
	RBACCompat  RBACMode = "compat"
	RBACEnforce RBACMode = "enforce"
)

type rbacModeKey struct{}

func WithRBACMode(ctx context.Context, mode RBACMode) context.Context {
	return context.WithValue(ctx, rbacModeKey{}, mode)
}
func RBACEnforced(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	mode, _ := ctx.Value(rbacModeKey{}).(RBACMode)
	return mode == RBACEnforce
}

type AccountAccessViewPage struct {
	Items         []AccountAccessView `json:"items"`
	NextCursor    *string             `json:"next_cursor"`
	TotalVisible  int                 `json:"total_visible"`
	GraphRevision int64               `json:"graph_revision"`
}

// Frozen migration16 sets: future catalog keys must not silently change compatibility.
func BuiltinAccessPermissions(key string) []Permission {
	switch key {
	case "admin":
		return []Permission{"access.manage_global", "alerts.read", "alerts.review", "analytics.read", "audit.read", "checks.read", "checks.review", "checks.run", "cohort.runs.read", "cohortjobs.cancel", "cohortjobs.read", "cohortjobs.run", "collectionjobs.cancel", "collectionjobs.read", "collectionjobs.run", "filechecks.cancel", "filechecks.legacy.run", "filechecks.read", "filechecks.review", "filechecks.run", "groups.manage", "groups.read", "incidents.create", "incidents.delete", "incidents.read", "integrations.check", "integrations.collect", "integrations.create", "integrations.ingest", "integrations.logs.read", "integrations.read", "integrations.status", "integrations.update", "metrics.read", "parameters.read", "presentation.scenario.read", "references.create", "references.delete", "references.read", "references.update", "risks.explain", "risks.export", "risks.external.read", "risks.read", "roles.manage", "roles.read", "runs.export", "runs.read", "runs.start", "services.read", "telemetry.compat.read", "telemetry.compat.score", "telemetry.import", "telemetry.read", "users.create", "users.groups.assign", "users.read", "users.roles.assign", "users.scope.assign", "users.update", "wells.archive", "wells.create", "wells.delete", "wells.export", "wells.history.read", "wells.import", "wells.read", "wells.restore", "wells.update"}
	case "viewer":
		return []Permission{"alerts.read", "checks.read", "cohort.runs.read", "incidents.read", "parameters.read", "references.read", "risks.explain", "risks.export", "risks.read", "telemetry.read", "wells.export", "wells.history.read", "wells.read"}
	case "technologist":
		return []Permission{"alerts.read", "alerts.review", "checks.read", "checks.review", "checks.run", "cohort.runs.read", "filechecks.cancel", "filechecks.read", "filechecks.review", "filechecks.run", "incidents.create", "incidents.read", "parameters.read", "references.read", "risks.explain", "risks.export", "risks.read", "telemetry.read", "wells.export", "wells.history.read", "wells.read"}
	}
	return nil
}
func LegacyAccessRole(a AuthorizationSnapshot) *string {
	if len(a.Assignments.DirectRoleIDs) != 1 || len(a.Assignments.Groups) != 0 {
		return nil
	}
	for _, r := range a.Assignments.Roles {
		if r.ID == a.Assignments.DirectRoleIDs[0] && r.BuiltinKey != nil {
			expected := BuiltinAccessPermissions(*r.BuiltinKey)
			actual := slices.Clone(a.Permissions)
			slices.Sort(actual)
			if expected != nil && slices.Equal(expected, actual) {
				key := *r.BuiltinKey
				return &key
			}
		}
	}
	return nil
}
