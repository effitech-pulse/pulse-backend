package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func passportError(kind domain.Kind, code string) error {
	return domain.NewPassportError(kind, code, code, nil)
}
func passportPermission(source domain.PassportSource) domain.Permission {
	if source == domain.PassportSourceExcel {
		return "equipment_passports.import"
	}
	return "equipment_passports.update"
}
func passportTx(ctx context.Context, r *Repository, read bool) (pgx.Tx, error) {
	options := pgx.TxOptions{}
	if read {
		options.IsoLevel = pgx.RepeatableRead
	}
	tx, e := r.pool.BeginTx(ctx, options)
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, `SET LOCAL statement_timeout='4s'; SET LOCAL lock_timeout='1s'`); e != nil {
		_ = tx.Rollback(ctx)
		return nil, e
	}
	return tx, nil
}

// Authority is locked before any well/head. Graph singleton is intentionally not locked.
func passportAuthority(ctx context.Context, tx pgx.Tx, permissions ...domain.Permission) (domain.AuthorizationSnapshot, domain.PassportActor, error) {
	var zero domain.AuthorizationSnapshot
	var actor domain.PassportActor
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || !p.Authenticated || p.AccountID == nil || *p.AccountID <= 0 || p.Mechanism != "session" || p.Authorization == nil {
		return zero, actor, passportError(domain.Unauthorized, "personal_session_required")
	}
	if !domain.RBACEnforced(ctx) {
		return zero, actor, passportError(domain.Forbidden, "equipment_passports_require_enforce")
	}
	// Affected-account FOR UPDATE in access mutation serializes role/scope/epoch changes.
	account, e := one[domain.Account](ctx, tx, sqlBuilder.Select("id,name,active").From("auth_user").Where(sq.Eq{"id": *p.AccountID}).Suffix("FOR SHARE"))
	if e != nil {
		return zero, actor, e
	}
	a, e := authorization(ctx, tx, *p.AccountID)
	if e != nil {
		return a, actor, e
	}
	if !a.Active || a.Epoch != p.Authorization.Epoch {
		return a, actor, passportError(domain.Unauthorized, "session_expired")
	}
	if e = a.Require(permissions...); e != nil {
		return a, actor, e
	}
	return a, domain.PassportActor{AccountID: domain.PassportID(account.ID), Name: account.Name}, nil
}
func passportIDs(rows []domain.PassportMutation) []domain.PassportID {
	ids := make([]domain.PassportID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.WellID)
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}
func passportWells(ctx context.Context, tx pgx.Tx, policy domain.PassportPolicy, a domain.AuthorizationSnapshot, ids []domain.PassportID, write bool) ([]domain.Well, error) {
	if policy.AppScope != "research" && policy.AppScope != "cohort" || policy.AppScope == "cohort" && policy.CohortVersion == "" {
		return nil, passportError(domain.Forbidden, "invalid_passport_policy")
	}
	if len(ids) == 0 || len(ids) > 100 {
		return nil, passportError(domain.Invalid, "file_limit_exceeded")
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	out := make([]domain.Well, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, passportError(domain.Missing, "well_unavailable")
		}
		lock := "FOR SHARE"
		if write {
			lock = "FOR UPDATE"
		}
		w, e := one[domain.Well](ctx, tx, sqlBuilder.Select(wellColumns).From("well").Where(sq.Eq{"id": int64(id)}).Suffix(lock))
		if e != nil {
			return nil, e
		}
		if !a.AllowsWell(w) {
			return nil, passportError(domain.Missing, "well_unavailable")
		}
		if policy.AppScope == "cohort" {
			members, e := list[struct{ ID int64 }](ctx, tx, sqlBuilder.Select("well_id AS id").From("app_cohort_member").Where(sq.Eq{"version": policy.CohortVersion, "well_id": int64(id)}))
			if e != nil {
				return nil, e
			}
			if len(members) != 1 {
				return nil, passportError(domain.Missing, "well_unavailable")
			}
		}
		if write && w.ArchivedAt != nil {
			return nil, passportError(domain.Duplicate, "well_archived")
		}
		out = append(out, w)
	}
	return out, nil
}
func passportValidateSource(in domain.PassportChangeSet, source domain.PassportProvenance) error {
	if source.Source == domain.PassportSourceManual {
		if source.SourceFileSHA256 != nil || source.TemplateVersion != nil || len(source.Rows) != 0 {
			return passportError(domain.Invalid, "invalid_source")
		}
		return nil
	}
	if source.Source != domain.PassportSourceExcel || source.SourceFileSHA256 == nil || !passportDigestPattern.MatchString(*source.SourceFileSHA256) || source.TemplateVersion == nil || *source.TemplateVersion != domain.PassportSchemaVersion || len(source.Rows) != len(in.Rows) {
		return passportError(domain.Invalid, "invalid_source")
	}
	seen := map[string]bool{}
	for _, row := range source.Rows {
		if row.Sheet != "components" || row.Index < 2 || seen[row.RowKey] {
			return passportError(domain.Invalid, "invalid_source")
		}
		seen[row.RowKey] = true
	}
	for _, row := range in.Rows {
		if !seen[row.RowKey] || !slices.Contains([]domain.PassportAction{"register", "update", "replace"}, row.Action) {
			return passportError(domain.Invalid, "invalid_source")
		}
	}
	return nil
}

// CommitPassport writes the whole change set/events/heads/key/receipt in one transaction.
func (r *Repository) CommitPassport(ctx context.Context, policy domain.PassportPolicy, in domain.PassportCommitInput, today domain.PassportDate) (domain.PassportReceipt, error) {
	var zero domain.PassportReceipt
	key, e := uuid.Parse(in.IdempotencyKey)
	if e != nil || key.String() != in.IdempotencyKey || in.PreviewID <= 0 {
		return zero, passportError(domain.Invalid, "invalid_commit_identity")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := passportTx(ctx, r, false)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	a, actor, e := passportAuthority(ctx, tx, "equipment_passports.read")
	if e != nil {
		return zero, e
	}
	// Serialize same actor+operation key, without retaining locks outside this tx.
	if _, e = execute(ctx, tx, sq.Expr("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", fmt.Sprintf("equipment-passport:%d:%s", a.AccountID, in.IdempotencyKey))); e != nil {
		return zero, e
	}
	prior, e := passportReceiptByKey(ctx, tx, a.AccountID, in.IdempotencyKey)
	if e == nil {
		if prior.Digest != in.Digest || prior.PreviewID != in.PreviewID {
			return zero, passportError(domain.Duplicate, "idempotency_conflict")
		}
		if e = passportReceiptAccess(ctx, tx, policy, a, prior); e != nil {
			return zero, e
		}
		return prior.Receipt, tx.Commit(ctx)
	}
	var de *domain.Error
	if !errors.As(e, &de) || de.Kind != domain.Missing {
		return zero, e
	}
	header, e := passportPreviewHeader(ctx, tx, a.AccountID, in.PreviewID)
	if e != nil {
		return zero, e
	}
	saved, e := passportPreviewAccess(ctx, tx, policy, a, header)
	if e != nil {
		return zero, e
	}
	if header.Digest != in.Digest {
		return zero, passportError(domain.Duplicate, "digest_mismatch")
	}
	if len(saved.Preview.Issues) > 0 {
		for _, issue := range saved.Preview.Issues {
			if issue.Severity == "error" {
				return zero, domain.NewPassportError(domain.Invalid, "validation_failed", "Проверьте поля паспорта", saved.Preview.Issues)
			}
			if issue.Severity == "warning" && !in.AcknowledgeWarnings {
				return zero, passportError(domain.Invalid, "warnings_not_acknowledged")
			}
		}
	}
	wells, e := passportWells(ctx, tx, policy, a, passportIDs(saved.Preview.Commands.Rows), true)
	if e != nil {
		return zero, e
	}
	bases := make([]domain.PassportBasis, 0, len(wells))
	for _, w := range wells {
		b, e := passportLoadBasis(ctx, tx, w, true)
		if e != nil {
			return zero, e
		}
		bases = append(bases, b)
	}
	prepared, e := domain.PreparePassportChanges(saved.Preview.Commands, bases, today)
	if e != nil {
		return zero, e
	}
	for _, issue := range prepared.Issues {
		if issue.Severity == "error" {
			kind := domain.Invalid
			if issue.Code == "revision_mismatch" || issue.Code == "installation_mismatch" {
				kind = domain.Duplicate
			}
			return zero, domain.NewPassportError(kind, issue.Code, "Паспорт изменился или содержит ошибки", prepared.Issues)
		}
		if issue.Severity == "warning" && !in.AcknowledgeWarnings {
			return zero, passportError(domain.Invalid, "warnings_not_acknowledged")
		}
	}
	receipt := domain.PassportReceipt{RequestID: in.IdempotencyKey, Actor: actor, Source: saved.Provenance.Source, SourceFileSHA256: saved.Provenance.SourceFileSHA256, ChangedCount: prepared.ChangedCount, UnchangedCount: prepared.UnchangedCount, Wells: []domain.PassportWellRevision{}, Rows: []domain.PassportReceiptRow{}}
	// Reserve receipt identity in this tx; immutable audit payload is inserted only once below.
	type stamp struct {
		ID         domain.PassportID `json:"id"`
		RecordedAt time.Time         `json:"recorded_at"`
	}
	reserved, e := one[stamp](ctx, tx, sqlBuilder.Select("nextval(pg_get_serial_sequence('equipment_passport_receipt','id'))::text AS id", "clock_timestamp() AS recorded_at"))
	if e != nil {
		return zero, e
	}
	receipt.ID = reserved.ID
	receipt.RecordedAt = reserved.RecordedAt
	heads := map[domain.PassportID]domain.PassportID{}
	revisions := map[domain.PassportID]domain.PassportRevision{}
	for _, b := range bases {
		changed := slices.ContainsFunc(prepared.Diff, func(d domain.PassportDiff) bool { return d.WellID == domain.PassportID(b.Well.ID) })
		if !changed {
			continue
		}
		id := b.PassportID
		if id == nil {
			created, e := mutate[struct {
				ID domain.PassportID `json:"id"`
			}](ctx, tx, sqlBuilder.Insert("equipment_passport").Columns("well_id").Values(b.Well.ID).Suffix("RETURNING id::text AS id"))
			if e != nil {
				return zero, e
			}
			id = &created.ID
		}
		if _, e = execute(ctx, tx, sqlBuilder.Update("equipment_passport").Set("revision", sq.Expr("revision+1")).Where(sq.Eq{"id": int64(*id)})); e != nil {
			return zero, e
		}
		well := domain.PassportID(b.Well.ID)
		heads[well] = *id
		revisions[well] = b.Revision + 1
		receipt.Wells = append(receipt.Wells, domain.PassportWellRevision{WellID: well, BeforeRevision: b.Revision, AfterRevision: b.Revision + 1})
	}
	for i, diff := range prepared.Diff {
		persisted, e := passportPersistDiff(ctx, tx, heads[diff.WellID], diff)
		if e != nil {
			return zero, e
		}
		prepared.Diff[i] = persisted
		target := persisted.After[len(persisted.After)-1]
		receipt.Rows = append(receipt.Rows, domain.PassportReceiptRow{RowKey: diff.RowKey, WellID: diff.WellID, ComponentID: *target.ComponentID, InstallationID: *target.ID})
	}
	raw, e := json.Marshal(receipt)
	if e != nil {
		return zero, e
	}
	targetIDs := make([]int64, 0, len(wells))
	for _, well := range wells {
		targetIDs = append(targetIDs, well.ID)
	}
	if _, e = execute(ctx, tx, sqlBuilder.Insert("equipment_passport_receipt").Columns("id", "owner_id", "request_id", "target_well_ids", "preview_id", "digest", "app_scope", "cohort_version", "source", "recorded_at", "payload").Values(int64(receipt.ID), a.AccountID, in.IdempotencyKey, targetIDs, int64(in.PreviewID), in.Digest, policy.AppScope, policy.CohortVersion, string(receipt.Source), receipt.RecordedAt, raw)); e != nil {
		return zero, e
	}
	for _, diff := range prepared.Diff {
		event := domain.PassportEvent{WellID: diff.WellID, Revision: revisions[diff.WellID], RowKey: diff.RowKey, Action: diff.Action, Slot: diff.Slot, RecordedAt: receipt.RecordedAt, Actor: actor, Source: receipt.Source, ReceiptID: receipt.ID, SourceFileSHA256: saved.Provenance.SourceFileSHA256, TemplateVersion: saved.Provenance.TemplateVersion, Diff: diff}
		for _, row := range prepared.ChangeSet.Rows {
			if row.RowKey == diff.RowKey {
				event.SourceReference = row.SourceReference
				event.Reason = row.ChangeReason
			}
		}
		for _, row := range saved.Provenance.Rows {
			if row.RowKey == diff.RowKey {
				v := row
				event.SourceRow = &v
			}
		}
		if e = passportInsertEvent(ctx, tx, heads[diff.WellID], event); e != nil {
			return zero, e
		}
	}
	// Successful commit retires the private payload in the same transaction.
	// Durable receipt/key and event history remain independent of preview retention.
	if _, e = execute(ctx, tx, sqlBuilder.Update("equipment_passport_preview").Set("payload", nil).Where(sq.Eq{"id": int64(in.PreviewID)})); e != nil {
		return zero, e
	}
	return receipt, tx.Commit(ctx)
}
