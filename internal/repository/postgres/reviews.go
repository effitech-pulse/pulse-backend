package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/jackc/pgx/v5"
)

func (r *Repository) ReviewTarget(ctx context.Context, kind, id string) (domain.ReviewTarget, error) {
	target := domain.ReviewTarget{Kind: kind, ID: id}
	if kind == "check" {
		row, err := one[domain.ManualCheck](ctx, r.pool, checkQuery().Where(sq.Eq{"id": id}))
		if err != nil {
			return target, err
		}
		target.WellID = &row.WellID
		target.CheckID = &row.ID
		return target, nil
	}
	row, err := one[struct {
		ID     int64  `json:"id"`
		WellID *int64 `json:"app_well_id"`
		Status string `json:"status"`
	}](ctx, r.pool, sqlBuilder.Select("id", "app_well_id", "status").From("ml_alert").Where(sq.Expr("id::text = ?", id)))
	if err != nil {
		return target, err
	}
	target.WellID = row.WellID
	target.AlertID = &row.ID
	target.LegacyStatus = row.Status
	return target, nil
}
func reviewWhere(target domain.ReviewTarget) sq.Eq {
	return sq.Eq{"kind": target.Kind, "target_id": target.ID}
}

type reviewRecord struct {
	State domain.ReviewState `json:"state"`
}

func readReviewState(ctx context.Context, db db, target domain.ReviewTarget, lock bool) (domain.ReviewState, error) {
	query := sqlBuilder.Select("state").From("case_review").Where(reviewWhere(target))
	if lock {
		query = query.Suffix("FOR UPDATE")
	}
	row, err := one[reviewRecord](ctx, db, query)
	var de *domain.Error
	if errors.As(err, &de) && de.Kind == domain.Missing {
		return target.Initial, nil
	}
	return row.State, err
}
func (r *Repository) Review(ctx context.Context, target domain.ReviewTarget, before int64) (domain.Review, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return domain.Review{}, err
	}
	defer tx.Rollback(ctx)
	state, err := readReviewState(ctx, tx, target, false)
	if err != nil {
		return domain.Review{}, err
	}
	query := sqlBuilder.Select("revision", "created_at", "actor", "comment", "before_state AS before", "after_state AS after").From("review_event").Where(reviewWhere(target)).OrderBy("revision DESC").Limit(51)
	if before > 0 {
		query = query.Where(sq.Lt{"revision": before})
	}
	events, err := list[domain.ReviewEvent](ctx, tx, query)
	if err != nil {
		return domain.Review{}, err
	}
	more := len(events) > 50
	if more {
		events = events[:50]
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Review{}, err
	}
	return domain.Review{State: state, Events: events, HasMore: more}, nil
}

// CommitReview сериализует версии и записывает состояние, статус тревоги и событие одной транзакцией.
func (r *Repository) CommitReview(ctx context.Context, target domain.ReviewTarget, in domain.ReviewInput, actor domain.ReviewActor, fingerprint, legacyStatus string) (domain.Review, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Review{}, err
	}
	defer tx.Rollback(ctx)
	initial, err := json.Marshal(target.Initial)
	if err != nil {
		return domain.Review{}, err
	}
	_, err = execute(ctx, tx, sqlBuilder.Insert("case_review").Columns("kind", "target_id", "check_id", "alert_id", "well_id", "state", "revision").Values(target.Kind, target.ID, target.CheckID, target.AlertID, target.WellID, initial, 0).Suffix("ON CONFLICT (kind,target_id) DO NOTHING"))
	if err != nil {
		return domain.Review{}, err
	}
	before, err := readReviewState(ctx, tx, target, true)
	if err != nil {
		return domain.Review{}, err
	}
	previous, err := list[struct {
		Fingerprint string `json:"fingerprint"`
	}](ctx, tx, sqlBuilder.Select("fingerprint").From("review_event").Where(reviewWhere(target)).Where(sq.Eq{"request_id": in.RequestID}))
	if err != nil {
		return domain.Review{}, err
	}
	if len(previous) > 0 {
		if previous[0].Fingerprint != fingerprint {
			return domain.Review{}, domain.NewError(domain.Duplicate, "Ключ запроса использован с другими изменениями")
		}
		if err = tx.Commit(ctx); err != nil {
			return domain.Review{}, err
		}
		return r.Review(ctx, target, 0)
	}
	if before.Revision != in.Revision {
		return domain.Review{}, domain.NewError(domain.Duplicate, "Результат уже изменён другим пользователем. Обновите форму, сохранив свой комментарий.")
	}
	now := time.Now().UTC()
	after := domain.ReviewState{Revision: before.Revision + 1, Status: in.Status, Owner: in.Owner, Conclusion: in.Conclusion, UpdatedAt: &now}
	beforeRaw, err := json.Marshal(before)
	if err != nil {
		return domain.Review{}, err
	}
	afterRaw, err := json.Marshal(after)
	if err != nil {
		return domain.Review{}, err
	}
	actorRaw, err := json.Marshal(actor)
	if err != nil {
		return domain.Review{}, err
	}
	_, err = execute(ctx, tx, sqlBuilder.Update("case_review").Set("state", afterRaw).Set("revision", after.Revision).Where(reviewWhere(target)))
	if err != nil {
		return domain.Review{}, err
	}
	_, err = execute(ctx, tx, sqlBuilder.Insert("review_event").Columns("kind", "target_id", "revision", "request_id", "fingerprint", "created_at", "actor", "comment", "before_state", "after_state").Values(target.Kind, target.ID, after.Revision, in.RequestID, fingerprint, now, actorRaw, in.Comment, beforeRaw, afterRaw))
	if err != nil {
		return domain.Review{}, err
	}
	if target.AlertID != nil {
		_, err = execute(ctx, tx, sqlBuilder.Update("ml_alert").Set("status", legacyStatus).Set("updated_at", now).Where(sq.Eq{"id": *target.AlertID}))
		if err != nil {
			return domain.Review{}, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Review{}, err
	}
	return r.Review(ctx, target, 0)
}
