package postgres

import (
	"context"
	"slices"
	"strconv"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func (r *Repository) ReadNotificationAlert(ctx context.Context, s domain.NotificationScope, l domain.NotificationAlertLocator) (domain.NotificationAlertContext, error) {
	var out domain.NotificationAlertContext
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if e := l.Validate(); e != nil {
		return out, e
	}
	f, e := r.notificationReadTx(ctx, s)
	if e != nil {
		return out, e
	}
	defer f.tx.Rollback(ctx)
	if !slices.Contains(f.families, domain.NotificationFamilyAlerts) {
		return out, notificationMissing()
	}
	q := sqlBuilder.Select("w.id", "left(w.well_number,256)", "w.active", "w.archived_at", "a.run_id", "a.id", "a.risk", "a.window_end AT TIME ZONE 'UTC'", "COALESCE(r.profile,'research')", "r.started_at AT TIME ZONE current_setting('TimeZone')", "r.finished_at AT TIME ZONE current_setting('TimeZone')", "CASE WHEN a.status IN ('new','confirmed','false_positive') THEN a.status ELSE 'unknown' END").From("ml_alert a").Join("ml_run r ON r.id=a.run_id").Join("well w ON w.id=a.app_well_id").Where(sq.Eq{"a.id": l.AlertID, "a.run_id": l.RunID, "a.app_well_id": l.WellID, "r.status": "done"}).Where(f.wellPredicate())
	query, args, e := build(q)
	if e != nil {
		return out, e
	}
	e = f.tx.QueryRow(ctx, query, args...).Scan(&out.Target.Well.ID, &out.Target.Well.Number, &out.Target.Well.Active, &out.Target.Well.ArchivedAt, &out.Target.RunID, &out.Target.AlertID, &out.Target.Risk, &out.Target.WindowEnd, &out.RunProfile, &out.RunStartedAt, &out.RunFinishedAt, &out.AlertStatus)
	if e == pgx.ErrNoRows {
		return out, notificationMissing()
	}
	if e != nil {
		return out, e
	}
	if out.RunProfile == "cohort" {
		// Project only this exact saved outcome; never load other wells or raw snapshot.
		q = sqlBuilder.Select().Column(sq.Expr("CASE WHEN octet_length(s.snapshot_id)<=? THEN s.snapshot_id ELSE NULL END", notificationProjectionByteBudget)).Columns("o.value->'result'->>'verdict'", "COALESCE(left(o.value->'result'->>'model_name',256),'')", "COALESCE(left(o.value->'result'->>'model_version',256),'')").Column(sq.Expr("octet_length(s.snapshot_id)>?", notificationProjectionByteBudget)).From("ml_run_snapshot s").CrossJoin("LATERAL jsonb_array_elements(s.snapshot->'outcomes') o(value)").Where(sq.Eq{"s.run_id": l.RunID}).Where(sq.Expr("o.value->>'app_well_id'=?", strconv.FormatInt(l.WellID, 10))).Where("o.value->>'status'='scored'").Where("o.value->'result'->>'verdict' IN ('watch','alert')").Limit(2)
		query, args, e = build(q)
		if e != nil {
			return out, e
		}
		saved := domain.NotificationSavedResult{}
		rows, e := f.tx.Query(ctx, query, args...)
		if e != nil {
			return out, e
		}
		n := 0
		oversized := false
		for rows.Next() {
			n++
			if e = rows.Scan(&out.SnapshotID, &saved.Verdict, &saved.ModelName, &saved.ModelVersion, &oversized); e != nil {
				break
			}
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return out, e
		}
		if oversized || notificationStringBytes(out.SnapshotID)+len(saved.ModelName)+len(saved.ModelVersion)+512 > notificationProjectionByteBudget {
			return domain.NotificationAlertContext{}, notificationProjectionUnavailable()
		}
		if n != 1 {
			return out, notificationMissing()
		}
		out.SavedResult = &saved
	}
	if e = f.commit(ctx); e != nil {
		return domain.NotificationAlertContext{}, e
	}
	return out, nil
}
func (r *Repository) ReadNotificationSource(ctx context.Context, s domain.NotificationScope, id string) (domain.NotificationSourceOverview, error) {
	var out domain.NotificationSourceOverview
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	f, e := r.notificationReadTx(ctx, s)
	if e != nil {
		return out, e
	}
	defer f.tx.Rollback(ctx)
	if !slices.Contains(f.families, domain.NotificationFamilyCollection) {
		return out, notificationMissing()
	}
	q := sqlBuilder.Select().Column(sq.Expr("CASE WHEN octet_length(id)<=? THEN id ELSE NULL END", notificationSourceIdentityByteBudget)).Columns("left(COALESCE(config->>'name',''),256)", "CASE WHEN config->>'kind' IN ('json_pull','json_push','clickhouse','postgres','mysql','file') THEN config->>'kind' ELSE 'unknown' END", "CASE WHEN error<>'' AND config->>'status'='running' THEN 'error' WHEN config->>'status' IN ('draft','running','paused','error') THEN config->>'status' ELSE 'unknown' END", "COALESCE((config->>'interval_seconds')::int,0)", "last_received", "last_measured", "CASE WHEN error<>'' THEN 'collection_failed' ELSE NULL END").Column(sq.Expr("octet_length(id)>?", notificationSourceIdentityByteBudget)).From("telemetry_source").Where(sq.Eq{"id": id})
	query, args, e := build(q)
	if e != nil {
		return out, e
	}
	var identity *string
	var oversized bool
	e = f.tx.QueryRow(ctx, query, args...).Scan(&identity, &out.Name, &out.Kind, &out.Status, &out.IntervalSeconds, &out.LastReceived, &out.LastMeasured, &out.ErrorCode, &oversized)
	if e == pgx.ErrNoRows {
		return out, notificationMissing()
	}
	if e != nil {
		return out, e
	}
	if oversized || identity == nil {
		return domain.NotificationSourceOverview{}, notificationProjectionUnavailable()
	}
	out.ID = *identity
	if e = f.commit(ctx); e != nil {
		return domain.NotificationSourceOverview{}, e
	}
	return out, nil
}
