package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

var passportDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type passportPreviewPayload struct {
	Preview    domain.PassportPreview    `json:"preview"`
	Provenance domain.PassportProvenance `json:"provenance"`
}
type passportPreviewRecord struct {
	ID            domain.PassportID     `json:"id"`
	Epoch         int64                 `json:"epoch"`
	AppScope      string                `json:"app_scope"`
	CohortVersion string                `json:"cohort_version"`
	Source        domain.PassportSource `json:"source"`
	Digest        string                `json:"digest"`
	ExpiresAt     time.Time             `json:"expires_at"`
	Expired       bool                  `json:"expired"`
	Consumed      bool                  `json:"consumed"`
	CancelledAt   *time.Time            `json:"cancelled_at"`
	Payload       json.RawMessage       `json:"payload"`
}

func passportPreviewHeader(ctx context.Context, tx pgx.Tx, owner int64, id domain.PassportID) (passportPreviewRecord, error) {
	return one[passportPreviewRecord](ctx, tx, sqlBuilder.Select("id::text AS id,epoch,app_scope,cohort_version,source,digest,expires_at,cancelled_at,payload,expires_at<=clock_timestamp() AS expired", "EXISTS(SELECT 1 FROM equipment_passport_receipt r WHERE r.preview_id=equipment_passport_preview.id) AS consumed").From("equipment_passport_preview").Where(sq.Eq{"id": int64(id), "owner_id": owner}))
}
func passportPreviewAccess(_ context.Context, _ pgx.Tx, policy domain.PassportPolicy, a domain.AuthorizationSnapshot, h passportPreviewRecord) (passportPreviewPayload, error) {
	var saved passportPreviewPayload
	if e := a.Require(passportPermission(h.Source)); e != nil {
		return saved, e
	}
	if policy.AppScope != h.AppScope || policy.CohortVersion != h.CohortVersion {
		return saved, passportError(domain.Missing, "preview_unavailable")
	}
	if a.Epoch != h.Epoch {
		return saved, passportError(domain.Duplicate, "preview_epoch_changed")
	}
	if h.Consumed {
		return saved, passportError(domain.Missing, "preview_consumed")
	}
	if h.Expired || h.CancelledAt != nil || len(h.Payload) == 0 || string(h.Payload) == "null" {
		return saved, passportError(domain.Missing, "preview_expired")
	}
	if e := json.Unmarshal(h.Payload, &saved); e != nil {
		return saved, e
	}
	return saved, nil
}
func (r *Repository) PreparePassport(ctx context.Context, policy domain.PassportPolicy, in domain.PassportChangeSet, source domain.PassportProvenance, today domain.PassportDate) (domain.PassportPreview, error) {
	var zero domain.PassportPreview
	raw, e := json.Marshal(in)
	if e != nil {
		return zero, e
	}
	if len(raw) > 1048576 {
		return zero, passportError(domain.Invalid, "file_limit_exceeded")
	}
	if e = passportValidateSource(in, source); e != nil {
		return zero, e
	}
	tx, e := passportTx(ctx, r, false)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	a, _, e := passportAuthority(ctx, tx, passportPermission(source.Source))
	if e != nil {
		return zero, e
	}
	wells, e := passportWells(ctx, tx, policy, a, passportIDs(in.Rows), true)
	if e != nil {
		return zero, e
	}
	bases := make([]domain.PassportBasis, 0, len(wells))
	for _, w := range wells {
		b, e := passportLoadBasis(ctx, tx, w, false)
		if e != nil {
			return zero, e
		}
		bases = append(bases, b)
	}
	prepared, e := domain.PreparePassportChanges(in, bases, today)
	if e != nil {
		return zero, e
	}
	// Quotas serialized globally; expired payloads never count as active reservations.
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(74019327)`); e != nil {
		return zero, e
	}
	var total, own, size int
	if e = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE owner_id=$1),COALESCE(sum(octet_length(payload::text)),0) FROM equipment_passport_preview WHERE expires_at>clock_timestamp() AND cancelled_at IS NULL AND payload IS NOT NULL`, a.AccountID).Scan(&total, &own, &size); e != nil {
		return zero, e
	}
	if total >= 16 || own >= 2 {
		return zero, passportError(domain.Limited, "preview_quota_exceeded")
	}
	type identity struct {
		ID        domain.PassportID `json:"id"`
		ExpiresAt time.Time         `json:"expires_at"`
	}
	id, e := one[identity](ctx, tx, sqlBuilder.Select("nextval(pg_get_serial_sequence('equipment_passport_preview','id'))::text AS id", "clock_timestamp()+interval '30 minutes' AS expires_at"))
	if e != nil {
		return zero, e
	}
	digestInput := struct {
		Commands   domain.PassportChangeSet
		Provenance domain.PassportProvenance
		Bases      []domain.PassportBasis
		Owner      int64
		Epoch      int64
		Policy     domain.PassportPolicy
		ExpiresAt  time.Time
		ID         domain.PassportID
	}{prepared.ChangeSet, source, bases, a.AccountID, a.Epoch, policy, id.ExpiresAt, id.ID}
	canonical, e := json.Marshal(digestInput)
	if e != nil {
		return zero, e
	}
	sum := sha256.Sum256(canonical)
	preview := domain.PassportPreview{ID: id.ID, Digest: hex.EncodeToString(sum[:]), ExpiresAt: id.ExpiresAt, Commands: prepared.ChangeSet, Diff: prepared.Diff, Issues: prepared.Issues, ChangedCount: prepared.ChangedCount, UnchangedCount: prepared.UnchangedCount}
	payload, e := json.Marshal(passportPreviewPayload{Preview: preview, Provenance: source})
	if e != nil {
		return zero, e
	}
	var bytes int
	if e = tx.QueryRow(ctx, "SELECT octet_length($1::jsonb::text)", payload).Scan(&bytes); e != nil {
		return zero, e
	}
	if bytes > 1048576 {
		return zero, passportError(domain.Invalid, "file_limit_exceeded")
	}
	if size+bytes > 16777216 {
		return zero, passportError(domain.Limited, "preview_quota_exceeded")
	}
	// IDs are always server-generated, explicit insertion consumes the sequence once.
	if _, e = execute(ctx, tx, sqlBuilder.Insert("equipment_passport_preview").Columns("id", "owner_id", "epoch", "app_scope", "cohort_version", "source", "digest", "expires_at", "payload").Values(int64(preview.ID), a.AccountID, a.Epoch, policy.AppScope, policy.CohortVersion, string(source.Source), preview.Digest, preview.ExpiresAt, payload)); e != nil {
		return zero, e
	}
	return preview, tx.Commit(ctx)
}
func (r *Repository) ReadPassportPreview(ctx context.Context, policy domain.PassportPolicy, id domain.PassportID) (domain.PassportPreview, error) {
	var zero domain.PassportPreview
	tx, e := passportTx(ctx, r, true)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	a, _, e := passportAuthority(ctx, tx, "equipment_passports.read")
	if e != nil {
		return zero, e
	}
	h, e := passportPreviewHeader(ctx, tx, a.AccountID, id)
	if e != nil {
		return zero, e
	}
	saved, e := passportPreviewAccess(ctx, tx, policy, a, h)
	if e != nil {
		return zero, e
	}
	if _, e = passportWells(ctx, tx, policy, a, passportIDs(saved.Preview.Commands.Rows), false); e != nil {
		return zero, e
	}
	return saved.Preview, tx.Commit(ctx)
}
func (r *Repository) CancelPassportPreview(ctx context.Context, policy domain.PassportPolicy, id domain.PassportID) error {
	tx, e := passportTx(ctx, r, false)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	a, _, e := passportAuthority(ctx, tx, "equipment_passports.read")
	if e != nil {
		return e
	}
	h, e := passportPreviewHeader(ctx, tx, a.AccountID, id)
	if e != nil {
		return e
	}
	saved, e := passportPreviewAccess(ctx, tx, policy, a, h)
	if e != nil {
		return e
	}
	if _, e = passportWells(ctx, tx, policy, a, passportIDs(saved.Preview.Commands.Rows), false); e != nil {
		return e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Update("equipment_passport_preview").Set("cancelled_at", sq.Expr("clock_timestamp()")).Set("payload", nil).Where(sq.Eq{"id": int64(id)})); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

// PurgeExpiredPassportPreviews removes only private payloads, not durable keys/history.
func (r *Repository) PurgeExpiredPassportPreviews(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, e := execute(ctx, r.pool, sqlBuilder.Update("equipment_passport_preview").Set("payload", nil).Where(sq.Or{sq.Expr("expires_at<=clock_timestamp()"), sq.NotEq{"cancelled_at": nil}}).Where(sq.NotEq{"payload": nil}))
	return e
}
