package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// canonicalPassportEnrollment фиксирует intent независимо от порядка ключей.
func canonicalPassportEnrollment(in domain.PassportEnrollmentInput) (domain.PassportEnrollmentInput, string, error) {
	key, e := uuid.Parse(in.RequestID)
	if e != nil || key.String() != in.RequestID || key == uuid.Nil || in.TargetAccountID <= 0 || in.ExpectedAccountRevision <= 0 || in.ExpectedEpoch <= 0 || in.ExpectedGraphRevision <= 0 || len(in.PermissionKeys) == 0 || len(in.PermissionKeys) > 6 {
		return in, "", domain.NewError(domain.Invalid, "Некорректный enrollment intent")
	}
	allowed := []domain.Permission{"wells.read", "equipment_passports.read", "equipment_passports.update", "equipment_passports.history.read", "equipment_passports.import", "equipment_passports.export"}
	in.PermissionKeys = slices.Clone(in.PermissionKeys)
	slices.Sort(in.PermissionKeys)
	for i, k := range in.PermissionKeys {
		if !slices.Contains(allowed, k) || i > 0 && in.PermissionKeys[i-1] == k {
			return in, "", domain.NewError(domain.Invalid, "Недопустимые права enrollment")
		}
	}
	// Require проверяет полную recursive closure в самом manifest, а не в старой роли.
	manifest := domain.AuthorizationSnapshot{Active: true, Permissions: in.PermissionKeys}
	if manifest.Require(in.PermissionKeys...) != nil {
		return in, "", domain.NewError(domain.Invalid, "Неполные предпосылки enrollment")
	}
	raw, e := json.Marshal(in)
	if e != nil {
		return in, "", e
	}
	digest := sha256.Sum256(raw)
	return in, hex.EncodeToString(digest[:]), nil
}

// EnrollEquipmentPassportPermissions — явная maintenance операция, без session actor.
// Блокировка graph→target сериализует новые роли, ожидания, receipt и отзыв сеансов.
func (r *Repository) EnrollEquipmentPassportPermissions(ctx context.Context, input domain.PassportEnrollmentInput) (domain.PassportEnrollmentReceipt, error) {
	var out domain.PassportEnrollmentReceipt
	in, digest, e := canonicalPassportEnrollment(input)
	if e != nil {
		return out, e
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	graph, e := lockAccessGraph(ctx, tx)
	if e != nil {
		return out, e
	}
	target := int64(in.TargetAccountID)
	if e = lockAccessUsers(ctx, tx, []int64{target}); e != nil {
		return out, e
	}
	current, e := one[authorizationRow](ctx, tx, authorizationQuery().Where(sq.Eq{"u.id": target}))
	if e != nil {
		return out, e
	}
	before := current.snapshot()
	if e = before.Require(managerPermissions...); e != nil {
		return out, domain.NewError(domain.Forbidden, "Требуется активный управляющий доступом")
	}
	// Replay сохраняет первоначальный receipt, но не обходит текущую авторизацию target.
	previous, e := list[struct {
		Digest  string                           `json:"digest"`
		Payload domain.PassportEnrollmentReceipt `json:"payload"`
	}](ctx, tx, sqlBuilder.Select("digest,payload").From("equipment_passport_enrollment").Where(sq.Eq{"request_id": in.RequestID}))
	if e != nil {
		return out, e
	}
	if len(previous) > 0 {
		if previous[0].Digest != digest {
			return out, accessConflict()
		}
		return previous[0].Payload, tx.Commit(ctx)
	}
	if current.Account.Revision != int64(in.ExpectedAccountRevision) || before.Epoch != int64(in.ExpectedEpoch) || graph != int64(in.ExpectedGraphRevision) {
		return out, accessConflict()
	}
	role, e := mutate[domain.AccessRole](ctx, tx, sqlBuilder.Insert("auth_role").Columns("name", "description").Values("Паспорт оборудования · "+in.RequestID, "Явное maintenance enrollment").Suffix("RETURNING id"))
	if e != nil {
		return out, e
	}
	permissions := sqlBuilder.Insert("auth_role_permission").Columns("role_id", "permission_key")
	for _, k := range in.PermissionKeys {
		permissions = permissions.Values(role.ID, k)
	}
	if _, e = execute(ctx, tx, permissions); e != nil {
		return out, e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Insert("auth_user_role").Columns("user_id", "role_id").Values(target, role.ID)); e != nil {
		return out, e
	}
	if e = bumpAccessUsers(ctx, tx, []int64{target}); e != nil {
		return out, e
	}
	if e = bumpAccessGraph(ctx, tx); e != nil {
		return out, e
	}
	after, e := authorization(ctx, tx, target)
	if e != nil {
		return out, e
	}
	var recorded time.Time
	if e = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&recorded); e != nil {
		return out, e
	}
	out = domain.PassportEnrollmentReceipt{RequestID: in.RequestID, ActorKind: "maintenance", TargetAccountID: in.TargetAccountID, Before: before, After: after, Digest: digest, RecordedAt: recorded}
	payload, e := json.Marshal(out)
	if e != nil {
		return out, e
	}
	beforeJSON, e := json.Marshal(before)
	if e != nil {
		return out, e
	}
	afterJSON, e := json.Marshal(after)
	if e != nil {
		return out, e
	}
	keys := make([]string, len(in.PermissionKeys))
	for i, k := range in.PermissionKeys {
		keys[i] = string(k)
	}
	_, e = execute(ctx, tx, sqlBuilder.Insert("equipment_passport_enrollment").Columns("request_id", "actor_kind", "target_account_id", "expected_account_revision", "expected_epoch", "expected_graph_revision", "permission_keys", "digest", "before_state", "after_state", "recorded_at", "payload").Values(in.RequestID, "maintenance", target, int64(in.ExpectedAccountRevision), int64(in.ExpectedEpoch), int64(in.ExpectedGraphRevision), keys, digest, beforeJSON, afterJSON, recorded, payload))
	if e != nil {
		return domain.PassportEnrollmentReceipt{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return domain.PassportEnrollmentReceipt{}, e
	}
	return out, nil
}
