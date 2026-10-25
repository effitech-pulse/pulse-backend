package postgres

import (
	"context"
	"fmt"
	"regexp"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// notificationActivationTx участвует в barrier каждой публикации, включая off.
func notificationActivationTx(ctx context.Context, tx pgx.Tx) (*domain.NotificationActivation, error) {
	var id *string
	var at *time.Time
	if err := tx.QueryRow(ctx, `SELECT activation_id,activated_at FROM notification_activation WHERE id FOR SHARE`).Scan(&id, &at); err != nil {
		return nil, err
	}
	if id == nil && at == nil {
		return nil, nil
	}
	if id == nil || at == nil {
		return nil, fmt.Errorf("invalid notification activation")
	}
	return &domain.NotificationActivation{ID: *id, ActivatedAt: *at}, nil
}
func appendAlertNotificationTx(ctx context.Context, tx pgx.Tx, a *domain.NotificationActivation, id int64) error {
	if a == nil {
		return nil
	}
	_, err := execute(ctx, tx, sqlBuilder.Insert("notification_fact").Columns("activation_id", "kind", "alert_id", "run_id", "well_id", "code").Select(sqlBuilder.Select().Column("?", a.ID).Columns("'alert.created.v1'", "id", "run_id", "app_well_id", "'alert_created'").From("ml_alert").Where(sq.Eq{"id": id}).Where("app_well_id IS NOT NULL")))
	return err
}
func appendCollectionNotificationTx(ctx context.Context, tx pgx.Tx, a *domain.NotificationActivation, id int64) error {
	if a == nil {
		return nil
	}
	q := sqlBuilder.Insert("notification_fact").Columns("activation_id", "kind", "collection_log_id", "source_id", "code").Select(sqlBuilder.Select().Column("?", a.ID).Columns("'collection.failed.v1'", "id", "source_id", "'collection_failed'").From("telemetry_collection_log").Where(sq.Eq{"id": id, "outcome": "failed"}).Where("attempt_generation IS NOT NULL"))
	tag, err := execute(ctx, tx, q)
	if err == nil && tag.RowsAffected() != 1 {
		return fmt.Errorf("invalid collection notification lineage")
	}
	return err
}
func (r *Repository) ValidateNotifications(ctx context.Context) error {
	var ready bool
	err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM backend_schema_migration WHERE version=19)
 AND (SELECT count(*)=1 FROM notification_activation WHERE id)
 AND NOT EXISTS(SELECT 1 FROM unnest(ARRAY['notification_fact_order','notification_fact_alert_source','notification_fact_alert_pair','notification_fact_collection_source','notification_fact_well_order','notification_fact_source_order','notification_read_fact','telemetry_collection_terminal_attempt','telemetry_collection_log_identity','ml_alert_notification_identity']) n WHERE to_regclass(n) IS NULL)
 AND (SELECT count(*)=5 FROM pg_constraint WHERE conrelid='notification_fact'::regclass AND contype='f')
 AND (SELECT count(*)=2 FROM pg_constraint WHERE conrelid='notification_fact'::regclass AND contype='c')
 AND (SELECT count(*)=2 FROM pg_constraint WHERE conrelid='notification_activation'::regclass AND contype='c')
 AND (SELECT count(*)=2 FROM pg_constraint WHERE conrelid='notification_read'::regclass AND contype='f')
 AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='notification_read'::regclass AND contype='p')
 AND EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='notification_fact'::regclass AND tgname='notification_fact_immutable' AND tgenabled='O')
 AND EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid='notification_activation'::regclass AND tgname='notification_activation_immutable' AND tgenabled='O')
 AND EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='telemetry_collection_log'::regclass AND conname='telemetry_collection_attempt')
 AND EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='ml_run_snapshot'::regclass AND attname='run_id' AND atttypid='bigint'::regtype)
 AND to_regclass('notification_read') IS NOT NULL`).Scan(&ready)
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("notification schema unavailable")
	}
	return nil
}
func (r *Repository) ActivateNotifications(ctx context.Context, id string) (domain.NotificationActivation, error) {
	var out domain.NotificationActivation
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id) {
		return out, domain.NewError(domain.Invalid, "invalid activation ID")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	var old *string
	var at *time.Time
	if err = tx.QueryRow(ctx, `SELECT activation_id,activated_at FROM notification_activation WHERE id FOR UPDATE`).Scan(&old, &at); err != nil {
		return out, err
	}
	if old == nil {
		err = tx.QueryRow(ctx, `UPDATE notification_activation SET activation_id=$1,activated_at=clock_timestamp() WHERE id RETURNING activation_id,activated_at`, id).Scan(&out.ID, &out.ActivatedAt)
	} else {
		out.ID = *old
		out.ActivatedAt = *at
	}
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
