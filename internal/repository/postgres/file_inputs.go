package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// Metadata projections intentionally never reference file_input_payload.
func fileInputQuery() sq.SelectBuilder {
	return sqlBuilder.Select("id", "well_id AS pulse_well_id", "actor_account_id", "actor", "request_key", "source_kind", "created_at", "captured_at", "raw_sha256", "canonical_sha256", "request_commitment_sha256", "fingerprint_sha256", "parse_spec", "parse_recipe_sha256", "parse_recipe", "execution_pin", "encode(parse_identity_bytes,'base64') AS identity_json", "raw_length AS raw_bytes", "canonical_length AS canonical_bytes", "rows_count AS rows", "columns_count AS columns", "ignored_columns").From("file_input")
}
func (r *Repository) FileInputRequest(ctx context.Context, actor int64, key string) (domain.FileInputSummary, error) {
	return one[domain.FileInputSummary](ctx, r.pool, fileInputQuery().Where(sq.Eq{"actor_account_id": actor, "operation_kind": "file_check", "request_key": key}))
}
func (r *Repository) FileInputSummary(ctx context.Context, id string) (domain.FileInputSummary, error) {
	return one[domain.FileInputSummary](ctx, r.pool, fileInputQuery().Where(sq.Eq{"id": id}))
}
func (r *Repository) FileInputSummaries(ctx context.Context, well int64, before string) ([]domain.FileInputSummary, error) {
	q := fileInputQuery().Where(sq.Eq{"well_id": well})
	cursor := sqlBuilder.Select("created_at", "id").From("file_input").Where(sq.Eq{"well_id": well, "id": before})
	if p, ok := domain.CurrentPrincipal(ctx); ok && p.AccountID != nil {
		q = q.Where(sq.Eq{"actor_account_id": *p.AccountID})
		cursor = cursor.Where(sq.Eq{"actor_account_id": *p.AccountID})
	}
	if before != "" {
		q = q.Where(sq.Expr("(created_at,id)<(?)", cursor))
	}
	return list[domain.FileInputSummary](ctx, r.pool, q.OrderBy("created_at DESC", "id DESC").Limit(25))
}
func (r *Repository) AcceptFileInput(ctx context.Context, v domain.PreparedFileInput) (domain.FileInputSummary, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return domain.FileInputSummary{}, e
	}
	defer tx.Rollback(ctx)
	out, e := insertFileInputTx(ctx, tx, v)
	if e != nil {
		return out, e
	}
	if e = tx.Commit(ctx); e != nil {
		return domain.FileInputSummary{}, e
	}
	return out, nil
}

// insertFileInputTx composes admission+payload+budget with future Q1 job/outbox tx.
// It does not begin, commit or roll back its caller's transaction.
func insertFileInputTx(ctx context.Context, tx pgx.Tx, v domain.PreparedFileInput) (domain.FileInputSummary, error) {
	if e := domain.ValidateFileInput(v); e != nil {
		return domain.FileInputSummary{}, e
	}
	type budget struct {
		Bytes int64 `json:"bytes"`
		Count int64 `json:"count"`
	}
	global, e := one[budget](ctx, tx, sqlBuilder.Select("retained_bytes AS bytes", "retained_count AS count").From("file_input_budget").Where(sq.Eq{"budget_key": "global"}).Suffix("FOR UPDATE"))
	if e != nil {
		return domain.FileInputSummary{}, e
	}
	key := "account:" + strconv.FormatInt(v.ActorAccountID, 10)
	if _, e = execute(ctx, tx, sqlBuilder.Insert("file_input_budget").Columns("budget_key").Values(key).Suffix("ON CONFLICT DO NOTHING")); e != nil {
		return domain.FileInputSummary{}, e
	}
	account, e := one[budget](ctx, tx, sqlBuilder.Select("retained_bytes AS bytes", "retained_count AS count").From("file_input_budget").Where(sq.Eq{"budget_key": key}).Suffix("FOR UPDATE"))
	if e != nil {
		return domain.FileInputSummary{}, e
	}
	previous, e := one[domain.FileInputSummary](ctx, tx, fileInputQuery().Where(sq.Eq{"actor_account_id": v.ActorAccountID, "operation_kind": "file_check", "request_key": v.RequestKey}))
	if e == nil {
		if previous.FingerprintSHA256 != v.FingerprintSHA256 || previous.RequestCommitmentSHA256 != v.RequestCommitmentSHA256 {
			return domain.FileInputSummary{}, domain.NewError(domain.Duplicate, "request_key_conflict")
		}
		return previous, nil
	}
	if de, ok := e.(*domain.Error); !ok || de.Kind != domain.Missing {
		return domain.FileInputSummary{}, e
	}
	spec, e := json.Marshal(v.ParseSpec)
	if e != nil {
		return domain.FileInputSummary{}, e
	}
	pin, e := json.Marshal(v.ExecutionPin)
	if e != nil {
		return domain.FileInputSummary{}, e
	}
	actor, e := json.Marshal(v.Actor)
	if e != nil {
		return domain.FileInputSummary{}, e
	}
	ignored, e := json.Marshal(v.IgnoredColumns)
	if e != nil {
		return domain.FileInputSummary{}, e
	}
	recipe, e := json.Marshal(v.ParseRecipe)
	if e != nil {
		return domain.FileInputSummary{}, e
	}
	charge := v.RawBytes + v.CanonicalBytes + int64(len(spec)) + int64(len(v.IdentityJSON))
	if global.Count >= v.Limits.Inputs || global.Bytes+charge > v.Limits.RetainedBytes || account.Bytes+charge > v.Limits.AccountBytes {
		return domain.FileInputSummary{}, domain.NewError(domain.Limited, "file_input_quota")
	}
	_, e = execute(ctx, tx, sqlBuilder.Insert("file_input").Columns("id", "well_id", "actor_account_id", "actor", "request_key", "source_kind", "created_at", "captured_at", "raw_sha256", "canonical_sha256", "request_commitment_sha256", "fingerprint_sha256", "parse_spec", "parse_recipe_sha256", "schema_sha256", "parse_identity_bytes", "execution_pin", "parse_recipe", "raw_length", "canonical_length", "rows_count", "columns_count", "ignored_columns").Values(v.ID, v.PulseWellID, v.ActorAccountID, actor, v.RequestKey, v.SourceKind, v.CreatedAt, v.CapturedAt, v.RawSHA256, v.CanonicalSHA256, v.RequestCommitmentSHA256, v.FingerprintSHA256, spec, v.ParseRecipeSHA256, v.ParseSpec.SchemaSHA256, v.IdentityJSON, pin, recipe, v.RawBytes, v.CanonicalBytes, v.Rows, v.Columns, ignored))
	if e != nil {
		return domain.FileInputSummary{}, e
	}
	_, e = execute(ctx, tx, sqlBuilder.Insert("file_input_payload").Columns("input_id", "raw_length", "canonical_length", "raw_bytes", "canonical_bytes").Values(v.ID, v.RawBytes, v.CanonicalBytes, v.Raw, v.Canonical))
	if e != nil {
		return domain.FileInputSummary{}, e
	}
	for _, k := range []string{"global", key} {
		if _, e = execute(ctx, tx, sqlBuilder.Update("file_input_budget").Set("retained_bytes", sq.Expr("retained_bytes+?", charge)).Set("retained_count", sq.Expr("retained_count+1")).Where(sq.Eq{"budget_key": k})); e != nil {
			return domain.FileInputSummary{}, e
		}
	}
	return one[domain.FileInputSummary](ctx, tx, fileInputQuery().Where(sq.Eq{"id": v.ID}))
}
func (r *Repository) StoredFileInput(ctx context.Context, id string) (domain.PreparedFileInput, error) {
	summary, e := r.FileInputSummary(ctx, id)
	if e != nil {
		return domain.PreparedFileInput{}, e
	}
	if summary.ParseRecipe.Version != "file_parser_v2" {
		return domain.PreparedFileInput{}, domain.NewError(domain.Unavailable, "parser_identity_unavailable")
	}
	// Payload is opened only by execution; lost bytes never trigger source refill.
	sql, args, e := build(sqlBuilder.Select("p.raw_bytes", "p.canonical_bytes").From("file_input_payload p").Where(sq.Eq{"p.input_id": id}))
	if e != nil {
		return domain.PreparedFileInput{}, e
	}
	v := domain.PreparedFileInput{FileInputSummary: summary, Limits: summary.ParseRecipe.Limits}
	e = r.pool.QueryRow(ctx, sql, args...).Scan(&v.Raw, &v.Canonical)
	if e == pgx.ErrNoRows {
		return v, domain.NewError(domain.Unavailable, "input_unavailable")
	}
	if e != nil {
		return v, e
	}
	if e = domain.ValidateFileInput(v); e != nil {
		return domain.PreparedFileInput{}, domain.NewError(domain.Unavailable, "input_corrupt")
	}
	return v, nil
}
func fileResultQuery() sq.SelectBuilder {
	return sqlBuilder.Select("input_id", "fingerprint_sha256", "execution_pin", "computed_at", "result").From("file_check_result")
}
func (r *Repository) StoredFileResult(ctx context.Context, id string) (domain.StoredFileResult, error) {
	return one[domain.StoredFileResult](ctx, r.pool, fileResultQuery().Where(sq.Eq{"input_id": id}))
}
func (r *Repository) CommitStoredResult(ctx context.Context, v domain.StoredFileResult) (domain.StoredFileResult, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return domain.StoredFileResult{}, e
	}
	defer tx.Rollback(ctx)
	out, e := insertFileResultTx(ctx, tx, v)
	if e != nil {
		return out, e
	}
	if e = tx.Commit(ctx); e != nil {
		return domain.StoredFileResult{}, e
	}
	return out, nil
}

// insertFileResultTx composes with future lease/fence/terminal state commit.
func insertFileResultTx(ctx context.Context, tx pgx.Tx, v domain.StoredFileResult) (domain.StoredFileResult, error) {
	input, e := one[domain.FileInputSummary](ctx, tx, fileInputQuery().Where(sq.Eq{"id": v.InputID}))
	if e != nil {
		return domain.StoredFileResult{}, e
	}
	if input.ParseRecipe.Version != "file_parser_v2" {
		return domain.StoredFileResult{}, domain.NewError(domain.Unavailable, "parser_identity_unavailable")
	}
	expected, _ := json.Marshal(input.ExecutionPin)
	pin, e := json.Marshal(v.ExecutionPin)
	if e != nil {
		return domain.StoredFileResult{}, e
	}
	if input.FingerprintSHA256 != v.FingerprintSHA256 || !bytes.Equal(expected, pin) || v.ComputedAt.IsZero() {
		return domain.StoredFileResult{}, domain.NewError(domain.Duplicate, "result_identity_conflict")
	}
	if e = domain.ValidateFileResult(input, v); e != nil {
		return domain.StoredFileResult{}, e
	}
	raw, e := json.Marshal(v.Result)
	if e != nil {
		return domain.StoredFileResult{}, e
	}
	if len(raw) > 2<<20 {
		return domain.StoredFileResult{}, domain.NewError(domain.Limited, "result_too_large")
	}
	_, e = execute(ctx, tx, sqlBuilder.Insert("file_check_result").Columns("input_id", "fingerprint_sha256", "execution_pin", "computed_at", "result").Values(v.InputID, v.FingerprintSHA256, pin, v.ComputedAt, raw).Suffix("ON CONFLICT (input_id) DO NOTHING"))
	if e != nil {
		return domain.StoredFileResult{}, e
	}
	return one[domain.StoredFileResult](ctx, tx, fileResultQuery().Where(sq.Eq{"input_id": v.InputID}))
}
