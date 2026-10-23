package postgres

import (
	"context"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// ValidateRBAC проверяет итоговую schema policy без миграции и без activation.
func (r *Repository) ValidateRBAC(ctx context.Context, mode domain.RBACMode) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if mode != domain.RBACCompat && mode != domain.RBACEnforce {
		return domain.NewError(domain.Unavailable, "rbac_mode_invalid")
	}
	var columns, triggers, checks int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND data_type=CASE WHEN column_name IN ('execution_actor','writer_execution_actor') THEN 'text' ELSE 'jsonb' END AND ((column_name='execution_actor' AND is_nullable='NO' AND column_default='''historical''::text') OR (column_name<>'execution_actor' AND is_nullable='YES' AND column_default IS NULL)) AND (table_name,column_name) IN (('file_job','well_authority'),('file_job','actor_authorization'),('cohort_job','actor_authorization'),('ml_run','actor_authorization'),('ml_run','execution_actor'),('telemetry_source','writer_authorization'),('telemetry_source','writer_execution_actor'))`).Scan(&columns)
	if err != nil {
		return err
	}
	err = r.pool.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE tgrelid IN ('file_job'::regclass,'cohort_job'::regclass,'ml_run'::regclass,'telemetry_source'::regclass) AND NOT tgisinternal AND tgenabled='O' AND tgname IN ('file_job_rbac_authority_immutable','cohort_job_rbac_authority_immutable','ml_run_rbac_authority_immutable','telemetry_source_rbac_authority_fence')`).Scan(&triggers)
	if err != nil {
		return err
	}
	err = r.pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE connamespace=current_schema()::regnamespace AND convalidated AND conname IN ('file_job_authorization_shape','file_job_well_authority_shape','file_job_authority_frames','cohort_job_authorization_shape','cohort_job_actor_role_access','ml_run_authorization_shape','telemetry_source_authorization_shape','collection_job_authorization_shape')`).Scan(&checks)
	if err != nil {
		return err
	}
	if columns != 7 || triggers != 4 || checks != 8 {
		return domain.NewError(domain.Unavailable, "rbac_schema_required")
	}
	var version, validator, active bool
	err = r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM backend_schema_migration WHERE version=16),valid_job_authorization('{"version":"access_v1","account_id":1,"epoch":1,"roles":[]}'::jsonb) AND valid_file_job_well_authority('{"version":"file_job_well_v1","well_id":1,"revision":1,"fund_type":"operational","ngdu":0,"department":0,"active":true,"archived_at":null,"unitek_id":null,"armits_id":null}'::jsonb),activated_at IS NOT NULL FROM auth_rbac_state WHERE id=true`).Scan(&version, &validator, &active)
	if err != nil {
		return err
	}
	if !version || !validator {
		return domain.NewError(domain.Unavailable, "rbac_schema_required")
	}
	if active != (mode == domain.RBACEnforce) {
		return domain.NewError(domain.Unavailable, "rbac_activation_mode_mismatch")
	}
	keys, err := list[struct {
		Key domain.Permission `json:"permission_key"`
	}](ctx, r.pool, sqlBuilder.Select("DISTINCT permission_key").From("auth_role_permission"))
	if err != nil {
		return err
	}
	known := map[domain.Permission]bool{}
	for _, d := range domain.PermissionCatalog() {
		known[d.Key] = true
	}
	for _, k := range keys {
		if !known[k.Key] {
			return domain.NewError(domain.Unavailable, "rbac_unknown_permission")
		}
	}
	return nil
}

// ActivateRBAC предназначен только для остановленного maintenance runtime.
func (r *Repository) ActivateRBAC(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Structural validation accepts the already activated idempotent state too.
	var active bool
	if err := r.pool.QueryRow(ctx, "SELECT activated_at IS NOT NULL FROM auth_rbac_state WHERE id=true").Scan(&active); err != nil {
		return err
	}
	mode := domain.RBACCompat
	if active {
		mode = domain.RBACEnforce
	}
	if err := r.ValidateRBAC(ctx, mode); err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = cohortGate(ctx, tx); err != nil {
		return err
	}
	if _, err = lockAccessGraph(ctx, tx); err != nil {
		return err
	}
	if err = tx.QueryRow(ctx, "SELECT activated_at IS NOT NULL FROM auth_rbac_state WHERE id=true").Scan(&active); err != nil {
		return err
	}
	if active {
		return tx.Commit(ctx)
	}
	var drained, physical bool
	err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM file_job WHERE state NOT IN ('succeeded','failed','cancelled','expired','revoked')) AND NOT EXISTS(SELECT 1 FROM cohort_job WHERE state NOT IN ('succeeded','failed','cancelled','expired','revoked')) AND NOT EXISTS(SELECT 1 FROM collection_job WHERE state NOT IN ('succeeded','failed','cancelled','expired','revoked')) AND NOT EXISTS(SELECT 1 FROM ml_run WHERE status='running') AND NOT EXISTS(SELECT 1 FROM file_job_runtime WHERE job_id IS NOT NULL) AND NOT EXISTS(SELECT 1 FROM collection_job_source_hold) AND NOT EXISTS(SELECT 1 FROM telemetry_source WHERE writer_token_sha256 IS NOT NULL OR lease_until>clock_timestamp())`).Scan(&drained)
	if err != nil {
		return err
	}
	if !drained {
		return domain.NewError(domain.Duplicate, "rbac_drain_required")
	}
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", runLock).Scan(&physical); err != nil {
		return err
	}
	if !physical {
		return domain.NewError(domain.Duplicate, "rbac_drain_required")
	}
	users, err := list[struct {
		ID int64 `json:"id"`
	}](ctx, tx, sqlBuilder.Select("id").From("auth_user").OrderBy("id").Suffix("FOR UPDATE"))
	if err != nil {
		return err
	}
	graph := accessGraph{}
	graph.Accounts, err = list[authorizationRow](ctx, tx, authorizationQuery().OrderBy("u.id"))
	if err != nil {
		return err
	}
	if err = ensureAccessManager(graph); err != nil {
		return err
	}
	_ = users // locks above protect the entire maintenance cutover
	if _, err = execute(ctx, tx, sqlBuilder.Update("auth_user").Set("authz_epoch", sq.Expr("authz_epoch+1"))); err != nil {
		return err
	}
	if _, err = execute(ctx, tx, sqlBuilder.Delete("auth_session")); err != nil {
		return err
	}
	if _, err = execute(ctx, tx, sqlBuilder.Update("auth_rbac_state").Set("activated_at", sq.Expr("clock_timestamp()")).Set("revision", sq.Expr("revision+1")).Where(sq.Eq{"id": true})); err != nil {
		return fmt.Errorf("activate RBAC: %w", err)
	}
	return tx.Commit(ctx)
}
