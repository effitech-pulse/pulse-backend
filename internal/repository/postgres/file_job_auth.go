package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// WithFileJobPolicy копирует доверенную immutable политику из bootstrap.
func (r *Repository) WithFileJobPolicy(p domain.FileJobPolicy) (*Repository, error) {
	if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`).MatchString(p.TestSchema) || p.TestSchema == "public" || strings.HasPrefix(p.TestSchema, "pg_") || len(p.AllowedAccountIDs) == 0 || len(p.AllowedAccountIDs) > 16 {
		return nil, domain.NewError(domain.Invalid, "invalid_file_job_policy")
	}
	seen := map[int64]bool{}
	for _, id := range p.AllowedAccountIDs {
		if id <= 0 || seen[id] {
			return nil, domain.NewError(domain.Invalid, "invalid_file_job_policy")
		}
		seen[id] = true
	}
	if p.RunID == "" || len(p.RunID) > 80 || !domain.ValidFileSHA256(p.ManifestSHA256) || p.CohortVersion == "" || len(p.CohortManifestSHA256) != 64 || strings.Trim(p.CohortManifestSHA256, "0123456789abcdef") != "" || len(p.AllowedInputs) == 0 || len(p.AllowedInputs) > 16 || p.ActiveGlobal < 1 || p.ActiveGlobal > 8 || p.ActivePerAccount < 1 || p.ActivePerAccount > 2 {
		return nil, domain.NewError(domain.Invalid, "invalid_file_job_policy")
	}
	for _, a := range p.AllowedInputs {
		var spec domain.FileParseSpec
		if json.Unmarshal(a.ClientSpecJSON, &spec) != nil || spec.SchemaSHA256 != "" || spec.Version != "file_input_v1" || spec.Format != a.Format || domain.FileSHA256(a.ClientSpecJSON) != a.ClientSpecSHA256 {
			return nil, domain.NewError(domain.Invalid, "invalid_file_job_policy")
		}
		normalized, _ := json.Marshal(spec)
		if string(normalized) != string(a.ClientSpecJSON) {
			return nil, domain.NewError(domain.Invalid, "invalid_file_job_policy")
		}
		if a.WellID <= 0 || !domain.ValidFileSHA256(a.RawSHA256) || !domain.ValidFileSHA256(a.ClientSpecSHA256) || (a.Format != "csv" && a.Format != "xlsx") || a.ExecutionPin.Validate() != nil {
			return nil, domain.NewError(domain.Invalid, "invalid_file_job_policy")
		}
	}
	raw, e := json.Marshal(p)
	if e != nil {
		return nil, e
	}
	var copy domain.FileJobPolicy
	if e = json.Unmarshal(raw, &copy); e != nil {
		return nil, e
	}
	out := *r
	out.fileJobPolicy = &copy
	return &out, nil
}

func fileJobSession(ctx context.Context) (int64, error) {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || !p.Authenticated || p.Mechanism != "session" || p.AccountID == nil || *p.AccountID <= 0 {
		return 0, domain.NewFileJobProblem(domain.Unauthorized, "session_required")
	}
	return *p.AccountID, nil
}
func missingFileJob() error { return domain.NewFileJobProblem(domain.Missing, "file_job_not_found") }
func isMissing(err error) bool {
	var de *domain.Error
	return errors.As(err, &de) && de.Kind == domain.Missing
}

// authorizeFileJobTx линеаризует current account/well/cohort/guard до runtime/job locks.
// false означает устойчивый отказ; SQL failures не превращаются в revoked.
func (r *Repository) authorizeFileJobTx(ctx context.Context, tx pgx.Tx, actor, well int64) (bool, error) {
	if r.fileJobPolicy == nil {
		return false, nil
	}
	p := r.fileJobPolicy
	var schema string
	if e := tx.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); e != nil {
		return false, e
	}
	if schema != p.TestSchema {
		return false, nil
	}
	if !slices.Contains(p.AllowedAccountIDs, actor) {
		return false, nil
	}
	a, e := one[domain.Account](ctx, tx, sqlBuilder.Select(accountColumns).From("auth_user").Where(sq.Eq{"id": actor}).Suffix("FOR SHARE"))
	if isMissing(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	w, e := one[domain.Well](ctx, tx, sqlBuilder.Select(wellColumns).From("well").Where(sq.Eq{"id": well}).Suffix("FOR SHARE"))
	if isMissing(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	current := domain.AccountPrincipal(a)
	if !domain.RBACEnforced(ctx) {
		current = domain.TrustedCompatibilityPrincipal(current)
	}
	if !a.Active || (a.Role != "technologist" && a.Role != "admin") || !w.Active || w.ArchivedAt != nil || !current.Allows(w) {
		return false, nil
	}
	guard, e := list[struct {
		ID bool `json:"id"`
	}](ctx, tx, sqlBuilder.Select("id").From("file_job_pilot_guard").Where(sq.Eq{"id": true, "run_id": p.RunID, "manifest_sha256": p.ManifestSHA256, "synthetic_only": true}).Suffix("FOR SHARE"))
	if e != nil {
		return false, e
	}
	if len(guard) != 1 {
		return false, nil
	}
	members, e := list[struct {
		WellID int64 `json:"well_id"`
	}](ctx, tx, sqlBuilder.Select("m.well_id").From("app_cohort_member m").Join("app_cohort c ON c.version=m.version").Where(sq.Eq{"m.well_id": well, "c.version": p.CohortVersion, "c.manifest_sha256": p.CohortManifestSHA256}).Suffix("FOR SHARE OF c,m"))
	return len(members) == 1, e
}
func (r *Repository) allowsPrepared(v domain.PreparedFileInput) bool {
	if r.fileJobPolicy == nil {
		return false
	}
	pin, _ := json.Marshal(v.ExecutionPin)
	for _, a := range r.fileJobPolicy.AllowedInputs {
		want, _ := json.Marshal(a.ExecutionPin)
		commit, _ := json.Marshal(struct {
			Version     string
			Actor, Well int64
			Raw         string
			Spec        json.RawMessage
		}{"request_commitment_v1", v.ActorAccountID, v.PulseWellID, a.RawSHA256, a.ClientSpecJSON})
		if a.WellID == v.PulseWellID && a.RawSHA256 == v.RawSHA256 && a.Format == v.ParseSpec.Format && string(pin) == string(want) && domain.FileSHA256(commit) == v.RequestCommitmentSHA256 {
			return true
		}
	}
	return false
}

// ValidateFileJobPolicy проверяет isolated startup без seed или миграции.
func (r *Repository) ValidateFileJobPolicy(ctx context.Context) error {
	if r.fileJobPolicy == nil {
		return domain.NewError(domain.Invalid, "invalid_file_job_policy")
	}
	p := r.fileJobPolicy
	var schema string
	if e := r.pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); e != nil {
		return e
	}
	if schema != p.TestSchema {
		return domain.NewError(domain.Forbidden, "file_job_schema_mismatch")
	}
	rows, e := list[struct {
		ID bool `json:"id"`
	}](ctx, r.pool, sqlBuilder.Select("g.id").From("file_job_pilot_guard g").Join("app_cohort c ON c.version=?", p.CohortVersion).Where(sq.Eq{"g.id": true, "g.run_id": p.RunID, "g.manifest_sha256": p.ManifestSHA256, "g.synthetic_only": true, "c.manifest_sha256": p.CohortManifestSHA256}))
	if e != nil {
		return e
	}
	if len(rows) != 1 {
		return domain.NewError(domain.Forbidden, "file_job_guard_mismatch")
	}
	return nil
}
