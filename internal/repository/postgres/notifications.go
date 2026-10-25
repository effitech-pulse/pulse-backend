package postgres

import (
	"context"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

var _ service.NotificationStore = (*Repository)(nil)

func (r *Repository) NotificationCapabilities(ctx context.Context, s domain.NotificationScope) (domain.NotificationCapabilities, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	f, e := r.notificationReadTx(ctx, s)
	if e != nil {
		return domain.NotificationCapabilities{}, e
	}
	defer f.tx.Rollback(ctx)
	out := domain.NotificationCapabilities{Enabled: f.active, Reason: "feature_off", Families: []domain.NotificationFamily{}}
	if f.active {
		out.Families = f.families
		out.Reason = "enabled"
		if len(f.families) == 0 {
			out.Reason = "no_families"
		}
	}
	if e = f.commit(ctx); e != nil {
		return domain.NotificationCapabilities{}, e
	}
	return out, nil
}
func (f notificationReadFrame) feedQuery() sq.SelectBuilder {
	return sqlBuilder.Select("f.id").From("notification_fact f").Where(sq.Eq{"f.activation_id": f.boundary.ActivationID}).Where(f.predicate())
}
func (f notificationReadFrame) unread(q sq.SelectBuilder) sq.SelectBuilder {
	return q.Where(sq.Expr("NOT EXISTS(SELECT 1 FROM notification_read nr WHERE nr.account_id=? AND nr.notification_id=f.id)", f.boundary.AccountID))
}
func notificationRowsQuery(q sq.SelectBuilder, account int64) sq.SelectBuilder {
	return q.Columns("f.recorded_at", "f.kind", "f.code", "nr.read_at", "w.id", "left(w.well_number,256)", "w.active", "w.archived_at", "a.run_id", "a.id", "a.risk", "a.window_end AT TIME ZONE 'UTC'", "s.id", "left(COALESCE(s.config->>'name',''),256)", "f.collection_log_id").LeftJoin("notification_read nr ON nr.notification_id=f.id AND nr.account_id=?", account).LeftJoin("ml_alert a ON a.id=f.alert_id").LeftJoin("well w ON w.id=f.well_id").LeftJoin("telemetry_source s ON s.id=f.source_id")
}
func readNotificationRows(ctx context.Context, tx pgx.Tx, q sq.SelectBuilder, account int64) ([]domain.NotificationRecord, error) {
	q = notificationRowsQuery(q, account)
	query, args, e := build(q)
	if e != nil {
		return nil, e
	}
	rows, e := tx.Query(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.NotificationRecord{}
	for rows.Next() {
		var n domain.NotificationRecord
		var well, run, alert, log *int64
		var number, source, name *string
		var active *bool
		var archived, end *time.Time
		var risk *float64
		if e = rows.Scan(&n.Key.ID, &n.Key.RecordedAt, &n.Kind, &n.Code, &n.ReadAt, &well, &number, &active, &archived, &run, &alert, &risk, &end, &source, &name, &log); e != nil {
			return nil, e
		}
		if n.Kind == domain.NotificationKindAlertCreated && well != nil && run != nil && alert != nil && active != nil {
			n.Alert = &domain.NotificationAlertTarget{Well: domain.NotificationWell{ID: *well, Number: number, Active: *active, ArchivedAt: archived}, RunID: *run, AlertID: *alert, Risk: risk, WindowEnd: end}
		} else if source != nil && name != nil && log != nil {
			n.Collection = &domain.NotificationCollectionTarget{SourceID: *source, SourceName: *name, CollectionLogID: *log}
		}
		if e = n.Validate(); e != nil {
			return nil, e
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
func (r *Repository) ReadNotificationPage(ctx context.Context, q domain.NotificationQuery) (domain.NotificationRecords, error) {
	var out domain.NotificationRecords
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if q.Limit < 1 || q.Limit > 100 || (q.State != domain.NotificationStateAll && q.State != domain.NotificationStateUnread) {
		return out, domain.NewError(domain.Invalid, "Некорректный запрос.")
	}
	f, e := r.notificationReadTx(ctx, q.Scope)
	if e != nil {
		return out, e
	}
	defer f.tx.Rollback(ctx)
	if e = f.requireFeed(); e != nil {
		return out, e
	}
	if q.Before != nil && q.Before.Boundary != f.boundary {
		return out, domain.NewError(domain.Invalid, "Некорректный запрос.")
	}
	query := f.feedQuery()
	if q.State == domain.NotificationStateUnread {
		query = f.unread(query)
	}
	if q.Before != nil {
		query = query.Where(sq.Expr("(f.recorded_at,f.id)<(?::timestamptz,?::bigint)", q.Before.Key.RecordedAt, q.Before.Key.ID))
	}
	out.Items, e = readNotificationRows(ctx, f.tx, query.OrderBy("f.recorded_at DESC", "f.id DESC").Limit(uint64(q.Limit+1)), f.boundary.AccountID)
	if e != nil {
		return domain.NotificationRecords{}, e
	}
	out.Boundary = f.boundary
	if e = f.commit(ctx); e != nil {
		return domain.NotificationRecords{}, e
	}
	return out, nil
}
func (r *Repository) CountUnreadNotifications(ctx context.Context, s domain.NotificationScope) (domain.NotificationCount, error) {
	var out domain.NotificationCount
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	f, e := r.notificationReadTx(ctx, s)
	if e != nil {
		return out, e
	}
	defer f.tx.Rollback(ctx)
	if e = f.requireFeed(); e != nil {
		return out, e
	}
	query, args, e := build(sq.Expr("SELECT count(*) FROM (?) visible", f.unread(f.feedQuery()).Limit(100)))
	if e != nil {
		return out, e
	}
	var n int
	if e = f.tx.QueryRow(ctx, query, args...).Scan(&n); e != nil {
		return out, e
	}
	out.Value = n
	out.Capped = n >= 100
	if out.Capped {
		out.Value = 99
	}
	if e = f.commit(ctx); e != nil {
		return domain.NotificationCount{}, e
	}
	return out, nil
}
func (r *Repository) MarkNotificationRead(ctx context.Context, s domain.NotificationScope, id int64) (domain.NotificationRead, error) {
	var out domain.NotificationRead
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	f, e := r.notificationReadTx(ctx, s)
	if e != nil {
		return out, e
	}
	defer f.tx.Rollback(ctx)
	if !f.active {
		return out, domain.NewError(domain.Unavailable, "Уведомления временно недоступны.")
	}
	// First find only the authorized target, then lock target before immutable fact.
	var well *int64
	var source *string
	query, args, e := build(f.feedQuery().Columns("f.well_id", "f.source_id").Where(sq.Eq{"f.id": id}))
	if e != nil {
		return out, e
	}
	var found int64
	e = f.tx.QueryRow(ctx, query, args...).Scan(&found, &well, &source)
	if e == pgx.ErrNoRows {
		return out, notificationMissing()
	}
	if e != nil {
		return out, e
	}
	if well != nil {
		e = f.tx.QueryRow(ctx, `SELECT id FROM well WHERE id=$1 FOR SHARE`, *well).Scan(&found)
	} else {
		var locked string
		e = f.tx.QueryRow(ctx, `SELECT id FROM telemetry_source WHERE id=$1 FOR SHARE`, *source).Scan(&locked)
	}
	if e == pgx.ErrNoRows {
		return out, notificationMissing()
	}
	if e != nil {
		return out, e
	}
	query, args, e = build(f.feedQuery().Where(sq.Eq{"f.id": id}).Suffix("FOR SHARE OF f"))
	if e != nil {
		return out, e
	}
	if e = f.tx.QueryRow(ctx, query, args...).Scan(&found); e == pgx.ErrNoRows {
		return out, notificationMissing()
	}
	if e != nil {
		return out, e
	}
	_, e = execute(ctx, f.tx, sqlBuilder.Insert("notification_read").Columns("account_id", "notification_id").Values(f.boundary.AccountID, id).Suffix("ON CONFLICT(account_id,notification_id) DO NOTHING"))
	if e != nil {
		return out, e
	}
	out.ID = id
	e = f.tx.QueryRow(ctx, `SELECT read_at FROM notification_read WHERE account_id=$1 AND notification_id=$2`, f.boundary.AccountID, id).Scan(&out.ReadAt)
	if e != nil {
		return out, e
	}
	if e = f.commit(ctx); e != nil {
		return domain.NotificationRead{}, e
	}
	return out, nil
}
