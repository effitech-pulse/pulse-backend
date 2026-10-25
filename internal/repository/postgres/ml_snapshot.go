package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// PersistCohort сохраняет неизменяемый полный результат, тревоги и done одной транзакцией.
func (l *lease) PersistCohort(ctx context.Context, id int64, s domain.CohortSnapshot) error {
	tx, err := l.conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	a, err := runAuthorityTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if err = guardManualCohortPublicationTx(ctx, tx, id, a, s); err != nil {
		return err
	}
	if err = persistCohortTx(ctx, tx, id, s, 1); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func persistCohortTx(ctx context.Context, tx pgx.Tx, id int64, s domain.CohortSnapshot, formatVersion int) error {
	if s.RunID == nil || *s.RunID != id || s.SnapshotID == nil || s.AsOf == nil || s.CohortVersion == "" || len(s.Outcomes) != 100 || s.OutcomeCompleteness != "complete" {
		return domain.NewError(domain.Invalid, "Неполный снимок прогона")
	}
	// Живые review-ссылки и running — read overlay, не часть immutable факта.
	s.Outcomes = append([]domain.CohortOutcome(nil), s.Outcomes...)
	for i := range s.Outcomes {
		s.Outcomes[i].AlertID = nil
		s.Outcomes[i].AlertStatus = nil
	}
	s.Running = false
	s.Recount()
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	query, args, err := build(sqlBuilder.Select("status").From("ml_run").Where(sq.Eq{"id": id}).Suffix("FOR UPDATE"))
	if err != nil {
		return err
	}
	var status string
	if err = tx.QueryRow(ctx, query, args...).Scan(&status); err != nil {
		return err
	}
	query, args, err = build(sqlBuilder.Select("output_sha256").From("ml_run_snapshot").Where(sq.Eq{"run_id": id}))
	if err != nil {
		return err
	}
	var old string
	err = tx.QueryRow(ctx, query, args...).Scan(&old)
	if err == nil {
		if old == digest {
			return nil
		}
		return domain.NewError(domain.Duplicate, "Снимок этого прогона уже сохранён с другим содержимым")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if status != "running" {
		return domain.NewError(domain.Duplicate, "Прогон уже завершён")
	}
	members, err := list[struct {
		ID int64 `json:"well_id"`
	}](ctx, tx, sqlBuilder.Select("well_id").From("app_cohort_member").Where(sq.Eq{"version": s.CohortVersion}))
	if err != nil {
		return err
	}
	ids := map[int64]bool{}
	for _, m := range members {
		ids[m.ID] = true
	}
	if len(ids) != 100 {
		return domain.NewError(domain.Invalid, "Неполный состав когорты")
	}
	activation, err := notificationActivationTx(ctx, tx)
	if err != nil {
		return err
	}
	seen := map[int64]bool{}
	alerts := 0
	for _, o := range s.Outcomes {
		if !ids[o.AppWellID] || seen[o.AppWellID] {
			return domain.NewError(domain.Invalid, "Снимок не соответствует составу когорты")
		}
		seen[o.AppWellID] = true
		switch o.Status {
		case "scored", "archived", "inactive", "insufficient", "error":
		default:
			return domain.NewError(domain.Invalid, "Неизвестный terminal outcome")
		}
		if o.ReasonCode == "" {
			return domain.NewError(domain.Invalid, "Отсутствует причина terminal outcome")
		}
		if o.Status != "scored" {
			if o.Result != nil {
				return domain.NewError(domain.Invalid, "Неоценённый outcome содержит риск")
			}
			continue
		}
		if o.Result == nil {
			return domain.NewError(domain.Invalid, "Отсутствует scored result")
		}
		if o.Result.Verdict != "watch" && o.Result.Verdict != "alert" {
			continue
		}
		previous := sqlBuilder.Select("a.status").From("ml_alert a").Join("ml_run_snapshot s ON s.run_id=a.run_id").Where(sq.Eq{"a.app_well_id": o.AppWellID, "s.cohort_version": s.CohortVersion}).OrderBy("a.run_id DESC").Limit(1)
		var end *string
		if n := len(o.Result.Timeline); n > 0 {
			end = &o.Result.Timeline[n-1].WindowEnd
		}
		insert := sqlBuilder.Insert("ml_alert").Columns("run_id", "ml_well_id", "unitek_id", "app_well_id", "risk", "window_end", "status").Values(id, nil, o.Metadata.UnitekID, o.AppWellID, o.Result.LatestRisk, sq.Expr("?::timestamp", end), sq.Expr("COALESCE((?), ?)", previous, "new"))
		q, args, e := build(insert.Suffix("RETURNING id"))
		if e != nil {
			return e
		}
		var alertID int64
		if err = tx.QueryRow(ctx, q, args...).Scan(&alertID); err != nil {
			return err
		}
		if err = appendAlertNotificationTx(ctx, tx, activation, alertID); err != nil {
			return err
		}
		alerts++
	}
	insert := sqlBuilder.Insert("ml_run_snapshot").Columns("run_id", "snapshot_id", "format_version", "cohort_version", "as_of", "provenance_status", "output_sha256", "snapshot").Values(id, *s.SnapshotID, formatVersion, s.CohortVersion, *s.AsOf, s.ProvenanceStatus, digest, raw)
	if _, err = execute(ctx, tx, insert); err != nil {
		return err
	}
	tag, err := execute(ctx, tx, sqlBuilder.Update("ml_run").Set("status", "done").Set("finished_at", sq.Expr("now()")).Set("wells_scored", s.Coverage.ScoredCount).Set("alerts_count", alerts).Where(sq.Eq{"id": id, "status": "running"}))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return domain.NewError(domain.Duplicate, "Прогон уже завершён")
	}
	return nil
}
func (r *Repository) CohortSnapshot(ctx context.Context, version string, run *int64) (domain.CohortSnapshot, error) {
	q := sqlBuilder.Select("snapshot").From("ml_run_snapshot").Where(sq.Eq{"cohort_version": version}).OrderBy("run_id DESC").Limit(1)
	if run != nil {
		q = q.Where(sq.Eq{"run_id": *run})
	}
	query, args, err := build(q)
	if err != nil {
		return domain.CohortSnapshot{}, err
	}
	var raw []byte
	if err = r.pool.QueryRow(ctx, query, args...).Scan(&raw); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.CohortSnapshot{}, domain.NewError(domain.Missing, "Сохранённый снимок когорты не найден")
		}
		return domain.CohortSnapshot{}, err
	}
	var out domain.CohortSnapshot
	err = json.Unmarshal(raw, &out)
	return out, err
}

func (r *Repository) CohortAlerts(ctx context.Context, run int64) ([]domain.CohortAlert, error) {
	return list[domain.CohortAlert](ctx, r.pool, sqlBuilder.Select("app_well_id", "id", "status").From("ml_alert").Where(sq.Eq{"run_id": run}).Where(sq.NotEq{"app_well_id": nil}))
}

// CohortRuns читает попытки той же когорты даже до появления immutable snapshot.
func (r *Repository) CohortRuns(ctx context.Context, version string, count int) ([]domain.MLRun, error) {
	q := sqlBuilder.Select("r.id,r.started_at,r.finished_at,r.status,r.trigger,r.profile,r.cohort_version,s.snapshot_id,s.provenance_status").From("ml_run r").LeftJoin("ml_run_snapshot s ON s.run_id=r.id AND s.cohort_version=r.cohort_version").Where(sq.Eq{"r.profile": "cohort", "r.cohort_version": version}).OrderBy("r.id DESC").Limit(uint64(count))
	return list[domain.MLRun](ctx, r.pool, q)
}

// guardManualCohortPublicationTx использует captured Metadata; current binding читается только из well.
func guardManualCohortPublicationTx(ctx context.Context, tx pgx.Tx, runID int64, a domain.AuthorizationSnapshot, s domain.CohortSnapshot) error {
	if s.RunID == nil || *s.RunID != runID || s.CohortVersion == "" || len(s.Outcomes) != 100 || s.OutcomeCompleteness != "complete" {
		return domain.NewError(domain.Invalid, "invalid_cohort_authority")
	}
	accepted := map[int64]domain.FileJobWellAuthority{}
	ids := make([]int64, 0, 100)
	for _, o := range s.Outcomes {
		if o.AppWellID != o.Metadata.ID || accepted[o.AppWellID].WellID != 0 {
			return domain.NewError(domain.Invalid, "invalid_cohort_authority")
		}
		frame, e := domain.FileJobWellAuthorityFrom(o.Metadata)
		if e != nil {
			return e
		}
		accepted[o.AppWellID] = frame
		ids = append(ids, o.AppWellID)
	}
	members, e := list[struct {
		ID int64 `json:"well_id"`
	}](ctx, tx, sqlBuilder.Select("well_id").From("app_cohort_member").Where(sq.Eq{"version": s.CohortVersion}))
	if e != nil {
		return e
	}
	if len(members) != 100 {
		return domain.NewError(domain.Invalid, "invalid_cohort_authority")
	}
	for _, m := range members {
		if accepted[m.ID].WellID == 0 {
			return domain.NewError(domain.Invalid, "invalid_cohort_authority")
		}
	}
	wells, e := list[domain.Well](ctx, tx, sqlBuilder.Select(wellColumns).From("well").Where(sq.Eq{"id": ids}).OrderBy("id").Suffix("FOR SHARE"))
	if e != nil {
		return e
	}
	if len(wells) != 100 {
		return domain.NewError(domain.Forbidden, "access_revoked")
	}
	for _, w := range wells {
		if !a.AllowsWell(w) || !accepted[w.ID].Matches(w) {
			return domain.NewError(domain.Forbidden, "access_revoked")
		}
	}
	run, e := one[struct {
		Profile *string `json:"profile"`
		Version *string `json:"cohort_version"`
	}](ctx, tx, sqlBuilder.Select("profile", "cohort_version").From("ml_run").Where(sq.Eq{"id": runID}).Suffix("FOR UPDATE"))
	if e != nil {
		return e
	}
	if run.Profile == nil || *run.Profile != "cohort" || run.Version == nil || *run.Version != s.CohortVersion {
		return domain.NewError(domain.Invalid, "invalid_cohort_authority")
	}
	return nil
}
