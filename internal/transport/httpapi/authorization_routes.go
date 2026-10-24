package httpapi

import "github.com/effitech-pulse/pulse-backend/internal/domain"

// RoutePolicy declares operation permissions; object/current-write guards remain in services.
type RoutePolicy struct {
	Method, Path string
	All          []domain.Permission
	Any          [][]domain.Permission
	ScopeRule    string
	Public       bool
}

var authorizationPolicies = []RoutePolicy{
	{Method: "GET", Path: "/people/organization/tree", All: []domain.Permission{}, Any: [][]domain.Permission{}, ScopeRule: "SELF"},
	{Method: "GET", Path: "/people", All: []domain.Permission{}, Any: [][]domain.Permission{}, ScopeRule: "SELF"},
	{Method: "GET", Path: "/people/:account_id", All: []domain.Permission{}, Any: [][]domain.Permission{}, ScopeRule: "SELF"},
	{Method: "GET", Path: "/health", All: []domain.Permission{}, Any: [][]domain.Permission{}, ScopeRule: "PUBLIC", Public: true},
	{Method: "GET", Path: "/openapi.json", All: []domain.Permission{}, Any: [][]domain.Permission{}, ScopeRule: "PUBLIC", Public: true},
	{Method: "POST", Path: "/collection-jobs", All: []domain.Permission{"collectionjobs.run"}, Any: [][]domain.Permission{}, ScopeRule: "LJ", Public: false},
	{Method: "GET", Path: "/collection-jobs/:job_id", All: []domain.Permission{"collectionjobs.read"}, Any: [][]domain.Permission{}, ScopeRule: "LJ", Public: false},
	{Method: "POST", Path: "/collection-jobs/:job_id/cancel", All: []domain.Permission{"collectionjobs.cancel"}, Any: [][]domain.Permission{}, ScopeRule: "LJ", Public: false},
	{Method: "POST", Path: "/ml/cohort-jobs", All: []domain.Permission{"cohortjobs.run"}, Any: [][]domain.Permission{}, ScopeRule: "CJ+FULL100", Public: false},
	{Method: "GET", Path: "/ml/cohort-jobs/:job_id", All: []domain.Permission{"cohortjobs.read"}, Any: [][]domain.Permission{}, ScopeRule: "CJ", Public: false},
	{Method: "POST", Path: "/ml/cohort-jobs/:job_id/cancel", All: []domain.Permission{"cohortjobs.cancel"}, Any: [][]domain.Permission{}, ScopeRule: "CJ", Public: false},
	{Method: "GET", Path: "/file-check-capabilities", All: []domain.Permission{}, Any: [][]domain.Permission{}, ScopeRule: "CAPABILITY", Public: false},
	{Method: "POST", Path: "/wells/:well_id/file-check-jobs", All: []domain.Permission{"filechecks.run"}, Any: [][]domain.Permission{}, ScopeRule: "PRIVATE", Public: false},
	{Method: "GET", Path: "/wells/:well_id/file-check-jobs", All: []domain.Permission{"filechecks.read"}, Any: [][]domain.Permission{}, ScopeRule: "PRIVATE", Public: false},
	{Method: "GET", Path: "/wells/:well_id/file-check-jobs/:job_id", All: []domain.Permission{"filechecks.read"}, Any: [][]domain.Permission{}, ScopeRule: "PRIVATE", Public: false},
	{Method: "GET", Path: "/wells/:well_id/file-check-jobs/:job_id/result", All: []domain.Permission{"filechecks.read"}, Any: [][]domain.Permission{}, ScopeRule: "PRIVATE", Public: false},
	{Method: "GET", Path: "/wells/:well_id/file-check-jobs/:job_id/review", All: []domain.Permission{"filechecks.read"}, Any: [][]domain.Permission{}, ScopeRule: "PRIVATE", Public: false},
	{Method: "PUT", Path: "/wells/:well_id/file-check-jobs/:job_id/review", All: []domain.Permission{"filechecks.review"}, Any: [][]domain.Permission{}, ScopeRule: "PRIVATE", Public: false},
	{Method: "POST", Path: "/wells/:well_id/file-check-jobs/:job_id/cancel", All: []domain.Permission{"filechecks.cancel"}, Any: [][]domain.Permission{}, ScopeRule: "PRIVATE", Public: false},
	{Method: "GET", Path: "/auth/me", All: []domain.Permission{}, Any: [][]domain.Permission{}, ScopeRule: "ANONYMOUS_OR_SELF", Public: true},
	{Method: "POST", Path: "/auth/login", All: []domain.Permission{}, Any: [][]domain.Permission{}, ScopeRule: "PUBLIC_LOGIN", Public: true},
	{Method: "POST", Path: "/auth/logout", All: []domain.Permission{}, Any: [][]domain.Permission{}, ScopeRule: "SELF", Public: false},
	{Method: "GET", Path: "/auth/users", All: []domain.Permission{"users.read"}, Any: [][]domain.Permission{}, ScopeRule: "M", Public: false},
	{Method: "POST", Path: "/auth/users", All: []domain.Permission{"users.create"}, Any: [][]domain.Permission{}, ScopeRule: "M", Public: false},
	{Method: "PUT", Path: "/auth/users/:user_id", All: []domain.Permission{"users.update"}, Any: [][]domain.Permission{}, ScopeRule: "M", Public: false},
	{Method: "GET", Path: "/presentation/scenario", All: []domain.Permission{"presentation.scenario.read"}, Any: [][]domain.Permission{}, ScopeRule: "FULL", Public: false},
	{Method: "GET", Path: "/presentation/summary", All: []domain.Permission{"wells.read", "risks.read", "incidents.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/wells", All: []domain.Permission{"wells.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "POST", Path: "/wells", All: []domain.Permission{"wells.create"}, Any: [][]domain.Permission{}, ScopeRule: "WT", Public: false},
	{Method: "POST", Path: "/wells/bulk", All: []domain.Permission{"wells.import"}, Any: [][]domain.Permission{}, ScopeRule: "WT", Public: false},
	{Method: "POST", Path: "/wells/import", All: []domain.Permission{"wells.import"}, Any: [][]domain.Permission{}, ScopeRule: "WT", Public: false},
	{Method: "GET", Path: "/wells/export", All: []domain.Permission{"wells.export"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/wells/:well_id/telemetry", All: []domain.Permission{"telemetry.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/wells/:well_id/telemetry/bounds", All: []domain.Permission{"telemetry.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "POST", Path: "/wells/:well_id/telemetry/score", All: []domain.Permission{"checks.run"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/wells/:well_id/checks", All: []domain.Permission{"checks.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "POST", Path: "/wells/:well_id/checks", All: []domain.Permission{"checks.run"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/wells/:well_id/checks/:check_id", All: []domain.Permission{"checks.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/wells/:well_id/checks/:check_id/review", All: []domain.Permission{"checks.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "PUT", Path: "/wells/:well_id/checks/:check_id/review", All: []domain.Permission{"checks.review"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/ml/alerts/:alert_id/review", All: []domain.Permission{"alerts.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "PUT", Path: "/ml/alerts/:alert_id/review", All: []domain.Permission{"alerts.review"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/wells/:well_id/risk", All: []domain.Permission{"risks.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/wells/:well_id/risk/history", All: []domain.Permission{"risks.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "POST", Path: "/telemetry/import", All: []domain.Permission{"telemetry.import"}, Any: [][]domain.Permission{}, ScopeRule: "WT", Public: false},
	{Method: "POST", Path: "/wells/:well_id/archive", All: []domain.Permission{"wells.archive"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "POST", Path: "/wells/:well_id/restore", All: []domain.Permission{"wells.restore"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/wells/:well_id/history", All: []domain.Permission{"wells.history.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/wells/:well_id", All: []domain.Permission{"wells.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "PATCH", Path: "/wells/:well_id", All: []domain.Permission{"wells.update"}, Any: [][]domain.Permission{}, ScopeRule: "WT", Public: false},
	{Method: "DELETE", Path: "/wells/:well_id", All: []domain.Permission{"wells.delete"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/wells/:well_id/predictions", All: []domain.Permission{"risks.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/wells/:well_id/predictions/export", All: []domain.Permission{"risks.export"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/wells/:well_id/complications", All: []domain.Permission{"risks.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/wells/:well_id/classification", All: []domain.Permission{"risks.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/wells/:well_id/incidents", All: []domain.Permission{"incidents.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "POST", Path: "/wells/:well_id/incidents", All: []domain.Permission{"incidents.create"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "DELETE", Path: "/incidents/:incident_id", All: []domain.Permission{"incidents.delete"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/incidents/stats", All: []domain.Permission{"incidents.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/ngdu", All: []domain.Permission{"references.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "POST", Path: "/ngdu", All: []domain.Permission{"references.create"}, Any: [][]domain.Permission{}, ScopeRule: "FULL", Public: false},
	{Method: "PUT", Path: "/ngdu/:ref_id", All: []domain.Permission{"references.update"}, Any: [][]domain.Permission{}, ScopeRule: "FULL", Public: false},
	{Method: "DELETE", Path: "/ngdu/:ref_id", All: []domain.Permission{"references.delete"}, Any: [][]domain.Permission{}, ScopeRule: "FULL", Public: false},
	{Method: "GET", Path: "/departments", All: []domain.Permission{"references.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "POST", Path: "/departments", All: []domain.Permission{"references.create"}, Any: [][]domain.Permission{}, ScopeRule: "FULL", Public: false},
	{Method: "PUT", Path: "/departments/:ref_id", All: []domain.Permission{"references.update"}, Any: [][]domain.Permission{}, ScopeRule: "FULL", Public: false},
	{Method: "DELETE", Path: "/departments/:ref_id", All: []domain.Permission{"references.delete"}, Any: [][]domain.Permission{}, ScopeRule: "FULL", Public: false},
	{Method: "GET", Path: "/runs", All: []domain.Permission{"runs.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/runs/export", All: []domain.Permission{"runs.export"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/runs/:dag_id/:run_id", All: []domain.Permission{"runs.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/audit", All: []domain.Permission{"audit.read"}, Any: [][]domain.Permission{}, ScopeRule: "FULL", Public: false},
	{Method: "GET", Path: "/parameters", All: []domain.Permission{"parameters.read"}, Any: [][]domain.Permission{}, ScopeRule: "GLOBAL", Public: false},
	{Method: "GET", Path: "/analytics/status", All: []domain.Permission{"analytics.read"}, Any: [][]domain.Permission{}, ScopeRule: "GLOBAL", Public: false},
	{Method: "GET", Path: "/analytics/dashboards/:dashboard_id/embed", All: []domain.Permission{"analytics.read"}, Any: [][]domain.Permission{}, ScopeRule: "FULL", Public: false},
	{Method: "GET", Path: "/ml/wells", All: []domain.Permission{"risks.read"}, Any: [][]domain.Permission{}, ScopeRule: "PROFILE_WELLS", Public: false},
	{Method: "POST", Path: "/ml/wells/refresh", All: []domain.Permission{"runs.start"}, Any: [][]domain.Permission{}, ScopeRule: "FULL", Public: false},
	{Method: "GET", Path: "/ml/runs/:run_id/snapshot", All: []domain.Permission{"cohort.runs.read"}, Any: [][]domain.Permission{}, ScopeRule: "PROJ", Public: false},
	{Method: "GET", Path: "/ml/runs", All: []domain.Permission{}, Any: [][]domain.Permission{}, ScopeRule: "PROFILE_RUNS", Public: false},
	{Method: "PATCH", Path: "/ml/alerts/:alert_id", All: []domain.Permission{"alerts.review"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/ml/wells/by-unitek/:unitek_id", All: []domain.Permission{"risks.external.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/ml/wells/:well_id/explain", All: []domain.Permission{"risks.explain"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/ml/wells/:well_id/history", All: []domain.Permission{"risks.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/ml/health", All: []domain.Permission{"services.read"}, Any: [][]domain.Permission{}, ScopeRule: "GLOBAL", Public: false},
	{Method: "GET", Path: "/ml/metrics", All: []domain.Permission{"metrics.read"}, Any: [][]domain.Permission{}, ScopeRule: "GLOBAL", Public: false},
	{Method: "POST", Path: "/ml/upload", All: []domain.Permission{"filechecks.legacy.run"}, Any: [][]domain.Permission{}, ScopeRule: "FULL", Public: false},
	{Method: "GET", Path: "/integrations", All: []domain.Permission{"integrations.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "POST", Path: "/integrations", All: []domain.Permission{"integrations.create"}, Any: [][]domain.Permission{}, ScopeRule: "FULL", Public: false},
	{Method: "POST", Path: "/integrations/check", All: []domain.Permission{"integrations.check"}, Any: [][]domain.Permission{}, ScopeRule: "FULL", Public: false},
	{Method: "GET", Path: "/integrations/logs", All: []domain.Permission{"integrations.logs.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "PUT", Path: "/integrations/:source_id", All: []domain.Permission{"integrations.update"}, Any: [][]domain.Permission{}, ScopeRule: "FULL", Public: false},
	{Method: "PATCH", Path: "/integrations/:source_id/status", All: []domain.Permission{"integrations.status"}, Any: [][]domain.Permission{}, ScopeRule: "FULL", Public: false},
	{Method: "POST", Path: "/integrations/:source_id/collect", All: []domain.Permission{"integrations.collect"}, Any: [][]domain.Permission{}, ScopeRule: "FULL", Public: false},
	{Method: "POST", Path: "/integrations/:source_id/ingest", All: []domain.Permission{"integrations.ingest"}, Any: [][]domain.Permission{}, ScopeRule: "WT", Public: false},
	{Method: "GET", Path: "/telemetry/bounds", All: []domain.Permission{"telemetry.compat.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/telemetry", All: []domain.Permission{"telemetry.compat.read"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "POST", Path: "/telemetry", All: []domain.Permission{"telemetry.import"}, Any: [][]domain.Permission{}, ScopeRule: "WT", Public: false},
	{Method: "POST", Path: "/telemetry/score", All: []domain.Permission{"telemetry.compat.score"}, Any: [][]domain.Permission{}, ScopeRule: "W", Public: false},
	{Method: "GET", Path: "/auth/permissions", All: []domain.Permission{}, Any: [][]domain.Permission{{"roles.read"}, {"groups.read"}, {"users.read"}}, ScopeRule: "M", Public: false},
	{Method: "GET", Path: "/auth/users/:user_id", All: []domain.Permission{"users.read"}, Any: [][]domain.Permission{}, ScopeRule: "M", Public: false},
	{Method: "GET", Path: "/auth/organization-options", All: []domain.Permission{"users.scope.assign"}, Any: [][]domain.Permission{}, ScopeRule: "M", Public: false},
	{Method: "GET", Path: "/auth/roles", All: []domain.Permission{"roles.read"}, Any: [][]domain.Permission{}, ScopeRule: "M", Public: false},
	{Method: "GET", Path: "/auth/roles/:role_id", All: []domain.Permission{"roles.read"}, Any: [][]domain.Permission{}, ScopeRule: "M", Public: false},
	{Method: "POST", Path: "/auth/roles", All: []domain.Permission{"roles.manage"}, Any: [][]domain.Permission{}, ScopeRule: "G", Public: false},
	{Method: "PUT", Path: "/auth/roles/:role_id", All: []domain.Permission{"roles.manage"}, Any: [][]domain.Permission{}, ScopeRule: "G", Public: false},
	{Method: "DELETE", Path: "/auth/roles/:role_id", All: []domain.Permission{"roles.manage"}, Any: [][]domain.Permission{}, ScopeRule: "G", Public: false},
	{Method: "GET", Path: "/auth/groups", All: []domain.Permission{"groups.read"}, Any: [][]domain.Permission{}, ScopeRule: "M", Public: false},
	{Method: "GET", Path: "/auth/groups/:group_id", All: []domain.Permission{"groups.read"}, Any: [][]domain.Permission{}, ScopeRule: "M", Public: false},
	{Method: "GET", Path: "/auth/groups/:group_id/members", All: []domain.Permission{"groups.read", "users.read"}, Any: [][]domain.Permission{}, ScopeRule: "M", Public: false},
	{Method: "POST", Path: "/auth/groups", All: []domain.Permission{"groups.manage"}, Any: [][]domain.Permission{}, ScopeRule: "G", Public: false},
	{Method: "PUT", Path: "/auth/groups/:group_id", All: []domain.Permission{"groups.manage"}, Any: [][]domain.Permission{}, ScopeRule: "G", Public: false},
	{Method: "DELETE", Path: "/auth/groups/:group_id", All: []domain.Permission{"groups.manage"}, Any: [][]domain.Permission{}, ScopeRule: "G", Public: false},
	{Method: "POST", Path: "/auth/access/preview", All: []domain.Permission{}, Any: [][]domain.Permission{}, ScopeRule: "CHANGE", Public: false},
}

func authorizationPolicy(method, path string) (RoutePolicy, bool) {
	for _, p := range authorizationPolicies {
		if p.Method == method && p.Path == path {
			return p, true
		}
	}
	return RoutePolicy{}, false
}

func (s *Server) routePermission(p domain.Principal, policy RoutePolicy) error {
	if !p.Authenticated || p.Authorization == nil || p.AccountID == nil || p.Authorization.AccountID != *p.AccountID {
		return domain.NewError(domain.Forbidden, "Требуются текущие полномочия")
	}
	switch policy.ScopeRule {
	case "SELF", "ANONYMOUS_OR_SELF", "CAPABILITY":
		return nil
	}
	if policy.ScopeRule == "PROFILE_RUNS" {
		key := domain.Permission("runs.read")
		if s.ML != nil && s.ML.IsCohort() {
			key = "cohort.runs.read"
		}
		return p.Authorization.Require(key)
	}
	if e := p.Authorization.Require(policy.All...); e != nil {
		return e
	}
	if len(policy.Any) > 0 {
		for _, keys := range policy.Any {
			if p.Authorization.Require(keys...) == nil {
				return nil
			}
		}
		return domain.NewError(domain.Forbidden, "Недостаточно прав для действия")
	}
	return nil
}
