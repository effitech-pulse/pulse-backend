package postgres

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func collectionError(kind domain.Kind, code string) error { return domain.NewError(kind, code) }
func collectionHash(v any) string                         { b, _ := json.Marshal(v); return telemetryTokenSHA(string(b)) }
func collectionSourceHash(s domain.TelemetrySource) string {
	s.LastReceived = nil
	s.LastMeasured = nil
	s.Error = ""
	s.EncryptedSecret = ""
	s.ProfileVersion = 0
	return collectionHash(s)
}
func (r *Repository) WithCollectionJobPolicy(p domain.CollectionPolicy) (*Repository, error) {
	if e := domain.ValidateCollectionPolicy(p); e != nil {
		return nil, e
	}
	b, e := json.Marshal(p)
	if e != nil {
		return nil, e
	}
	var cp domain.CollectionPolicy
	if e = json.Unmarshal(b, &cp); e != nil {
		return nil, e
	}
	out := *r
	out.collectionJobPolicy = &cp
	out.collectionClaims = &collectionClaimRegistry{claims: make(map[string]*collectionClaim)}
	return &out, nil
}
func (r *Repository) ValidateCollectionJobPolicy(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var ok bool
	if e := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM backend_schema_migration WHERE version=15)`).Scan(&ok); e != nil {
		return collectionError(domain.Unavailable, "schema_required")
	}
	if !ok {
		return collectionError(domain.Unavailable, "schema_required")
	}
	if r.collectionJobPolicy == nil {
		return nil
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	_, _, e = r.collectionGuard(ctx, tx, false)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (r *Repository) collectionGuard(ctx context.Context, tx pgx.Tx, admission bool) (int, bool, error) {
	p := r.collectionJobPolicy
	if p == nil {
		return 0, false, collectionError(domain.Unavailable, "collection_jobs_disabled")
	}
	var schema string
	if e := tx.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); e != nil {
		return 0, false, e
	}
	var count int
	var accepting bool
	var run, sha, test, origin string
	var synthetic, alive bool
	e := tx.QueryRow(ctx, `SELECT accepted_jobs,accepting,run_id,manifest_sha256,test_schema,fixture_origin,synthetic_only,expires_at>clock_timestamp() FROM collection_job_guard WHERE id=true FOR UPDATE`).Scan(&count, &accepting, &run, &sha, &test, &origin, &synthetic, &alive)
	if e == pgx.ErrNoRows {
		return 0, false, collectionError(domain.Forbidden, "access_revoked")
	}
	if e != nil {
		return 0, false, e
	}
	if !synthetic || !alive || schema != p.TestSchema || test != schema || run != p.RunID || sha != p.ManifestSHA256 || origin != p.FixtureOrigin {
		return 0, false, collectionError(domain.Forbidden, "access_revoked")
	}
	if admission && !accepting {
		return count, accepting, collectionError(domain.Unavailable, "collection_jobs_disabled")
	}
	return count, accepting, nil
}
func collectionAccount(ctx context.Context, tx pgx.Tx, id int64) (domain.Account, error) {
	return one[domain.Account](ctx, tx, sqlBuilder.Select(accountColumns).From("auth_user").Where(sq.Eq{"id": id}).Suffix("FOR SHARE"))
}
func collectionWell(ctx context.Context, tx pgx.Tx, id int64) (domain.Well, error) {
	return one[domain.Well](ctx, tx, sqlBuilder.Select(wellColumns).From("well").Where(sq.Eq{"id": id}).Suffix("FOR SHARE"))
}
func collectionWellView(w domain.Well, b domain.CollectionBinding) (domain.CollectionWellAuthority, bool) {
	var external *int64
	switch b.IdentityKind {
	case "unitek":
		external = w.UnitekID
	case "armits":
		external = w.ArmitsID
	case "pulse":
		external = &w.ID
	}
	if external == nil {
		return domain.CollectionWellAuthority{}, false
	}
	return domain.CollectionWellAuthority{WellID: w.ID, FundType: w.FundType, NGDU: w.NGDU, Department: w.Department, Active: w.Active, ArchivedAt: w.ArchivedAt, ExternalID: strconv.FormatInt(*external, 10), IdentityKind: b.IdentityKind}, true
}
func collectionAllows(a domain.AuthorizationSnapshot, w domain.Well) bool {
	return a.AllowsWell(w) && w.Active && w.ArchivedAt == nil && w.FundType == "presentation"
}
func collectionSource(ctx context.Context, tx pgx.Tx, id string) (domain.TelemetrySource, time.Time, error) {
	var s domain.TelemetrySource
	var raw []byte
	var until time.Time
	e := tx.QueryRow(ctx, `SELECT config,encrypted_secret,GREATEST(lease_until,'1970-01-01'::timestamptz) FROM telemetry_source WHERE id=$1 FOR UPDATE`, id).Scan(&raw, &s.EncryptedSecret, &until)
	if e != nil {
		return s, until, e
	}
	secret := s.EncryptedSecret
	e = json.Unmarshal(raw, &s)
	s.EncryptedSecret = secret
	return s, until, e
}

// collectionAuthority берёт current account/well/source в едином порядке; SQL error не означает revoke.
func (r *Repository) collectionAuthority(ctx context.Context, tx pgx.Tx, i domain.CollectionIntent) (string, error) {
	p := r.collectionJobPolicy
	if p == nil || !slices.Contains(p.AllowedAccountIDs, i.ActorAccountID) {
		return "access_revoked", nil
	}
	auth, e := jobAuthority(ctx, tx, i.ActorAccountID, i.Authorization, "collectionjobs.run", true)
	if authorityDenied(e) {
		return "access_revoked", nil
	}
	if e != nil {
		return "", e
	}
	a, e := collectionAccount(ctx, tx, i.ActorAccountID)
	if isMissing(e) {
		return "access_revoked", nil
	}
	if e != nil {
		return "", e
	}
	w, e := collectionWell(ctx, tx, i.Binding.WellID)
	if isMissing(e) {
		return "binding_changed", nil
	}
	if e != nil {
		return "", e
	}
	s, _, e := collectionSource(ctx, tx, i.Binding.SourceID)
	if e == pgx.ErrNoRows {
		return "source_changed", nil
	}
	if e != nil {
		return "", e
	}
	if !collectionAllows(auth, w) || a.Revision != i.ActorRevision || collectionHash(a.Scope) != i.ScopeSHA256 {
		return "access_revoked", nil
	}
	v, ok := collectionWellView(w, i.Binding)
	if !ok || v.WellID != i.Well.WellID || v.FundType != i.Well.FundType || v.NGDU != i.Well.NGDU || v.Department != i.Well.Department || v.Active != i.Well.Active || !sameCohortLifecycleTime(v.ArchivedAt, i.Well.ArchivedAt) || v.ExternalID != i.Binding.ExternalID || v.IdentityKind != i.Binding.IdentityKind {
		return "binding_changed", nil
	}
	if s.ProfileVersion != i.SourceProfileVersion || collectionSourceHash(s) != i.SourceConfigSHA256 || collectionHash(s.Mappings) != i.MappingSHA256 || s.Status != "running" || s.EncryptedSecret != "" || s.Endpoint != i.FixtureOrigin+"/v1/collection-capture" {
		return "source_changed", nil
	}
	return "", nil
}
