package postgres

import (
	"context"
	"log/slog"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

const runLock int64 = 74019321

// lease владеет выделенным соединением до освобождения advisory lock.
type lease struct{ conn *pgxpool.Conn }

func (l *lease) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := execute(ctx, l.conn, sq.Expr("SELECT pg_advisory_unlock(?)", runLock)); err != nil {
		slog.ErrorContext(ctx, "unlock failed", "error_code", "operation_failed")
		raw := l.conn.Hijack()
		_ = raw.Close(ctx)
		return
	}
	l.conn.Release()
}

func (r *Repository) Reserve(ctx context.Context) (service.RunLease, bool, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	query, args, err := build(sq.Expr("SELECT pg_try_advisory_lock(?)", runLock))
	if err != nil {
		conn.Release()
		return nil, false, err
	}
	var acquired bool
	if err = conn.QueryRow(ctx, query, args...).Scan(&acquired); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !acquired {
		conn.Release()
		return nil, false, nil
	}
	return &lease{conn: conn}, true, nil
}

func (r *Repository) Running(ctx context.Context) (bool, error) {
	held, acquired, err := r.Reserve(ctx)
	if err != nil {
		return false, err
	}
	if acquired {
		held.Close()
	}
	return !acquired, nil
}

func (l *lease) Begin(ctx context.Context, trigger string) (int64, error) {
	return l.begin(ctx, trigger, "research", nil)
}
func (l *lease) BeginCohort(ctx context.Context, trigger, version string) (int64, error) {
	if version == "" {
		return 0, domain.NewError(domain.Invalid, "Нужна версия когорты")
	}
	return l.begin(ctx, trigger, "cohort", &version)
}
func (l *lease) begin(ctx context.Context, trigger, profile string, version *string) (int64, error) {
	abandon := sqlBuilder.Update("ml_run").
		Set("status", "error").Set("finished_at", sq.Expr("now()")).
		Set("error", "Предыдущий процесс прогона был остановлен").
		Where(sq.Eq{"status": "running"})
	if _, err := execute(ctx, l.conn, abandon); err != nil {
		return 0, err
	}
	insert := sqlBuilder.Insert("ml_run").Columns("status", "trigger", "started_at", "profile", "cohort_version").
		Values("running", trigger, sq.Expr("now()"), profile, version).Suffix("RETURNING id")
	query, args, err := build(insert)
	if err != nil {
		return 0, err
	}
	var id int64
	err = l.conn.QueryRow(ctx, query, args...).Scan(&id)
	return id, err
}

func latestCompletedRun() sq.SelectBuilder {
	return sqlBuilder.Select("id").From("ml_run").Where(sq.Eq{"status": "done"}).
		OrderBy("id DESC").Limit(1)
}

func (l *lease) Persist(ctx context.Context, id int64, snapshot domain.Snapshot) error {
	tx, err := l.conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	previousRun := latestCompletedRun().Where(sq.NotEq{"id": id})
	alerts := 0
	for _, w := range snapshot.Wells {
		if !w.Alert {
			continue
		}
		appWell := sqlBuilder.Select("id").From("well").Where("unitek_id = ?", w.UnitekID)
		previousStatus := sqlBuilder.Select("status").From("ml_alert").
			Where("unitek_id = ?", w.UnitekID).Where(sq.NotEq{"status": "new"}).
			Where(sq.Expr("run_id = (?)", previousRun)).Limit(1)
		insert := sqlBuilder.Insert("ml_alert").
			Columns("run_id", "ml_well_id", "unitek_id", "app_well_id", "oilfield", "risk", "window_end", "status").
			Values(id, w.WellID, w.UnitekID, sq.Expr("(?)", appWell), w.Oilfield, w.Risk,
				sq.Expr("?::timestamp", w.WindowEnd), sq.Expr("COALESCE((?), ?)", previousStatus, "new"))
		if _, err = execute(ctx, tx, insert); err != nil {
			return err
		}
		alerts++
	}
	finish := sqlBuilder.Update("ml_run").
		Set("status", "done").Set("finished_at", sq.Expr("now()")).
		Set("wells_scored", len(snapshot.Wells)).Set("alerts_count", alerts).
		Set("drift_verdict", snapshot.DriftVerdict).Set("drift_psi_max", snapshot.DriftPsiMax).
		Set("drift_top", snapshot.DriftTop).Where(sq.Eq{"id": id})
	if _, err = execute(ctx, tx, finish); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (l *lease) Fail(ctx context.Context, id int64, message string) error {
	query := sqlBuilder.Update("ml_run").Set("status", "error").Set("finished_at", sq.Expr("now()")).
		Set("error", message).Where(sq.Eq{"id": id})
	_, err := execute(ctx, l.conn, query)
	return err
}

func (r *Repository) MLRuns(ctx context.Context, count int) ([]domain.MLRun, error) {
	query := sqlBuilder.Select("id,started_at,finished_at,status,trigger,profile,cohort_version,wells_scored,alerts_count,error,drift_verdict,drift_psi_max,drift_top").
		From("ml_run").OrderBy("id DESC").Limit(uint64(count))
	rows, err := list[domain.MLRun](ctx, r.pool, query)
	for i := range rows {
		unknown := "historical_unknown"
		rows[i].ProvenanceStatus = &unknown
	}
	return rows, err
}

func (r *Repository) LatestAlerts(ctx context.Context) ([]domain.Alert, error) {
	query := sqlBuilder.Select("id,unitek_id,status").From("ml_alert").
		Where(sq.Expr("run_id = (?)", latestCompletedRun()))
	return list[domain.Alert](ctx, r.pool, query)
}

func (r *Repository) UpdateAlert(ctx context.Context, id int64, status string) (domain.Alert, error) {
	query := sqlBuilder.Update("ml_alert").Set("status", status).Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": id}).Suffix("RETURNING id,unitek_id,status")
	return mutate[domain.Alert](ctx, r.pool, query)
}

func (r *Repository) ModelState(ctx context.Context) (domain.ModelState, error) {
	reviewed := sqlBuilder.Select("count(*)").From("ml_alert").Where("run_id = ml_run.id").
		Where(sq.Eq{"status": []string{"confirmed", "false_positive"}})
	falseAlerts := sqlBuilder.Select("count(*)").From("ml_alert").Where("run_id = ml_run.id").
		Where(sq.Eq{"status": "false_positive"})
	query := sqlBuilder.Select("drift_verdict", "drift_psi_max").
		Column(sq.Expr("(?) AS alerts_reviewed", reviewed)).
		Column(sq.Expr("(?) AS alerts_false", falseAlerts)).
		From("ml_run").Where(sq.Eq{"status": "done"}).OrderBy("started_at DESC").Limit(1)
	rows, err := list[domain.ModelState](ctx, r.pool, query)
	if err != nil {
		return domain.ModelState{}, err
	}
	if len(rows) == 0 {
		return domain.ModelState{}, nil
	}
	return rows[0], nil
}
