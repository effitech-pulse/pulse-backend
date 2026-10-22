package postgres

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

const cohortCapacityGate int64 = 74019323

func cohortError(kind domain.Kind, code string) error { return domain.NewError(kind, code) }
func cohortGate(ctx context.Context, tx pgx.Tx) error {
	var ok bool
	if e := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", cohortCapacityGate).Scan(&ok); e != nil {
		return e
	}
	if !ok {
		return cohortError(domain.Limited, "cohort_capacity_busy")
	}
	return nil
}

// cohortOwnedRuntimeGate ждёт координацию уже принадлежащего claim в пределах caller budget.
// Для final общий context остаётся 10s, само ожидание gate ограничено 5s.
func cohortOwnedRuntimeGate(ctx context.Context, tx pgx.Tx) error {
	gateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := tx.Exec(gateCtx, "SELECT pg_advisory_xact_lock($1)", cohortCapacityGate)
	return err
}
func activeCohort(ctx context.Context, tx pgx.Tx) (bool, error) {
	var yes bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cohort_job WHERE state NOT IN ('succeeded','failed','cancelled','expired','revoked'))`).Scan(&yes)
	return yes, e
}
func (r *Repository) WithCohortJobPolicy(p domain.CohortJobPolicy) (*Repository, error) {
	if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`).MatchString(p.TestSchema) || p.TestSchema == "public" || strings.HasPrefix(p.TestSchema, "pg_") || p.RunID == "" || len(p.RunID) > 80 || !domain.ValidFileSHA256(p.ManifestSHA256) || p.CohortVersion == "" || len(p.CohortManifestSHA256) != 64 || strings.Trim(p.CohortManifestSHA256, "0123456789abcdef") != "" || len(p.AllowedAccountIDs) < 1 || len(p.AllowedAccountIDs) > 16 || len(p.Fixtures) < 1 || len(p.Fixtures) > 2 {
		return nil, cohortError(domain.Invalid, "invalid_cohort_job_policy")
	}
	seen := map[int64]bool{}
	for _, id := range p.AllowedAccountIDs {
		if id <= 0 || seen[id] {
			return nil, cohortError(domain.Invalid, "invalid_cohort_job_policy")
		}
		seen[id] = true
	}
	fixtures := map[string]bool{}
	for _, f := range p.Fixtures {
		if f.FixtureID == "" || fixtures[f.FixtureID] || !domain.ValidFileSHA256(f.FixtureSHA256) {
			return nil, cohortError(domain.Invalid, "invalid_cohort_job_policy")
		}
		fixtures[f.FixtureID] = true
	}
	raw, e := json.Marshal(p)
	if e != nil {
		return nil, e
	}
	var cp domain.CohortJobPolicy
	if e = json.Unmarshal(raw, &cp); e != nil {
		return nil, e
	}
	out := *r
	out.cohortJobPolicy = &cp
	return &out, nil
}
func (r *Repository) ValidateCohortJobPolicy(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if r.cohortJobPolicy == nil {
		return cohortError(domain.Forbidden, "cohort_jobs_disabled")
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	ok, e := r.cohortGuard(ctx, tx)
	if e != nil {
		return e
	}
	if !ok {
		return cohortError(domain.Forbidden, "cohort_job_guard_mismatch")
	}
	return tx.Commit(ctx)
}
func cohortSession(ctx context.Context) (int64, error) {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || !p.Authenticated || p.Mechanism != "session" || p.AccountID == nil || *p.AccountID <= 0 {
		return 0, cohortError(domain.Unauthorized, "session_required")
	}
	return *p.AccountID, nil
}
func (r *Repository) cohortGuard(ctx context.Context, tx pgx.Tx) (bool, error) {
	p := r.cohortJobPolicy
	if p == nil {
		return false, nil
	}
	var schema string
	if e := tx.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); e != nil {
		return false, e
	}
	if schema != p.TestSchema {
		return false, nil
	}
	g, e := list[struct {
		ID bool `json:"id"`
	}](ctx, tx, sqlBuilder.Select("id").From("cohort_job_guard").Where(sq.Eq{"id": true, "run_id": p.RunID, "manifest_sha256": p.ManifestSHA256, "synthetic_only": true}).Suffix("FOR SHARE"))
	if e != nil || len(g) != 1 {
		return false, e
	}
	c, e := list[domain.Cohort](ctx, tx, sqlBuilder.Select("version AS cohort_version").From("app_cohort").Where(sq.Eq{"version": p.CohortVersion, "manifest_sha256": p.CohortManifestSHA256}).Suffix("FOR SHARE"))
	return len(c) == 1, e
}

// authority различает устойчивый отказ и неизвестную SQL-ошибку; admin shortcut не используется.
func (r *Repository) cohortAuthority(ctx context.Context, tx pgx.Tx, actor int64, frozen []domain.CohortFrozenMember) (domain.Account, []domain.Well, bool, error) {
	var a domain.Account
	p := r.cohortJobPolicy
	if p == nil || !slices.Contains(p.AllowedAccountIDs, actor) {
		return a, nil, false, nil
	}
	a, e := one[domain.Account](ctx, tx, sqlBuilder.Select(accountColumns).From("auth_user").Where(sq.Eq{"id": actor}).Suffix("FOR SHARE"))
	if isMissing(e) {
		return a, nil, false, nil
	}
	if e != nil {
		return a, nil, false, e
	}
	if !a.Active || a.Role != "admin" {
		return a, nil, false, nil
	}
	ids := sqlBuilder.Select("well_id").From("app_cohort_member").Where(sq.Eq{"version": p.CohortVersion})
	wells, e := list[domain.Well](ctx, tx, sqlBuilder.Select(wellColumns).From("well").Where(sq.Expr("id IN (?)", ids)).OrderBy("id").Suffix("FOR SHARE"))
	if e != nil {
		return a, nil, false, e
	}
	if len(wells) != 100 {
		return a, nil, false, nil
	}
	for _, w := range wells {
		fund := w.FundType
		if fund == "" {
			fund = "operational"
		}
		if !slices.Contains(a.Scope.Funds, fund) || (a.Scope.NGDUs != nil && !slices.Contains(*a.Scope.NGDUs, w.NGDU)) || (a.Scope.Departments != nil && !slices.Contains(*a.Scope.Departments, w.Department)) {
			return a, nil, false, nil
		}
	}
	ok, e := r.cohortGuard(ctx, tx)
	if e != nil || !ok {
		return a, nil, false, e
	}
	members, e := list[struct {
		WellID int64 `json:"well_id"`
	}](ctx, tx, sqlBuilder.Select("well_id").From("app_cohort_member").Where(sq.Eq{"version": p.CohortVersion}).OrderBy("well_id").Suffix("FOR SHARE"))
	if e != nil || len(members) != 100 {
		return a, nil, false, e
	}
	for i, w := range wells {
		if members[i].WellID != w.ID {
			return a, nil, false, nil
		}
		if frozen != nil && (len(frozen) != 100 || frozen[i].Well.ID != w.ID || frozen[i].Well.Active != w.Active || !sameCohortLifecycleTime(frozen[i].Well.ArchivedAt, w.ArchivedAt)) {
			return a, nil, false, nil
		}
	}
	return a, wells, true, nil
}

// sameCohortLifecycleTime сравнивает момент, сохраняя различие NULL и archived.
func sameCohortLifecycleTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}
