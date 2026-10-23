package domain

import (
	"context"
	"slices"
)

// JobAuthorization хранит audit initiator, но не заменяет текущие полномочия.
type JobAuthorization struct {
	Version   string              `json:"version"`
	AccountID int64               `json:"account_id"`
	Epoch     int64               `json:"epoch"`
	Roles     []AccessRoleSummary `json:"roles"`
}

type ExecutionActorKind string

const (
	ExecutionHistorical    ExecutionActorKind = "historical"
	ExecutionAccount       ExecutionActorKind = "account"
	ExecutionSystemML      ExecutionActorKind = "system_ml"
	ExecutionSystemCollect ExecutionActorKind = "system_collect"
	ExecutionLegacyHTTP    ExecutionActorKind = "legacy_http"
	ExecutionCohortJob     ExecutionActorKind = "cohort_job"
)

func NewJobAuthorization(a AuthorizationSnapshot) JobAuthorization {
	roles := slices.Clone(a.Assignments.Roles)
	if roles == nil {
		roles = []AccessRoleSummary{}
	}
	for i := range roles {
		if roles[i].BuiltinKey != nil {
			key := *roles[i].BuiltinKey
			roles[i].BuiltinKey = &key
		}
	}
	slices.SortFunc(roles, func(a, b AccessRoleSummary) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return JobAuthorization{Version: "access_v1", AccountID: a.AccountID, Epoch: a.Epoch, Roles: roles}
}
func (a JobAuthorization) Validate() error {
	if a.Version != "access_v1" || a.AccountID <= 0 || a.Epoch <= 0 || a.Roles == nil {
		return NewError(Forbidden, "invalid_job_authorization")
	}
	seen := map[int64]bool{}
	for _, r := range a.Roles {
		if r.ID <= 0 || r.Revision <= 0 || seen[r.ID] {
			return NewError(Forbidden, "invalid_job_authorization")
		}
		seen[r.ID] = true
	}
	return nil
}

// ExecutionActor различает только явно доверенные серверные механизмы.
func ExecutionActor(ctx context.Context) ExecutionActorKind {
	p, ok := CurrentPrincipal(ctx)
	if !ok || !p.Authenticated || p.Authorization == nil {
		return ExecutionHistorical
	}
	if p.AccountID != nil {
		return ExecutionAccount
	}
	if !p.trustedCompatibility {
		return ExecutionHistorical
	}
	switch p.Mechanism {
	case "system_ml":
		return ExecutionSystemML
	case "system_collect":
		return ExecutionSystemCollect
	case "legacy":
		return ExecutionLegacyHTTP
	}
	return ExecutionHistorical
}

// WithSchedulerPrincipal фиксирует права конкретного server-owned scheduler.
func WithSchedulerPrincipal(ctx context.Context, kind ExecutionActorKind, scope AccessScope) context.Context {
	keys := []Permission{}
	mechanism := ""
	switch kind {
	case ExecutionSystemML:
		keys = []Permission{"runs.start", "runs.read", "risks.read", "wells.read", "telemetry.read"}
		mechanism = "system_ml"
	case ExecutionSystemCollect:
		keys = []Permission{"integrations.collect", "integrations.read", "telemetry.import", "telemetry.read", "wells.read"}
		mechanism = "system_collect"
	}
	a := AuthorizationSnapshot{Active: true, Permissions: keys, Organization: &OrganizationScope{Kind: "organization"}, LegacyScope: scope}
	p := Principal{Authenticated: true, Mechanism: mechanism, Name: "Планировщик", Scope: scope, Authorization: &a, trustedCompatibility: true}
	return WithPrincipal(ctx, p)
}
