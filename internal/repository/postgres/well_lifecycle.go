package postgres

import (
	"context"
	"encoding/json"
	"strconv"

	sq "github.com/Masterminds/squirrel"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func wellActor(ctx context.Context) domain.ReviewActor {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok {
		return domain.ReviewActor{ID: "service", Name: "Сервис", Authenticated: false}
	}
	actor := domain.ReviewActor{ID: "shared", Name: p.Name, Authenticated: p.Authenticated}
	if p.AccountID != nil {
		actor.ID = "user:" + strconv.FormatInt(*p.AccountID, 10)
	}
	return actor
}
func appendWellEvent(ctx context.Context, db db, kind, reason string, before, after domain.Well) error {
	actor, err := json.Marshal(wellActor(ctx))
	if err != nil {
		return err
	}
	old, err := json.Marshal(before)
	if err != nil {
		return err
	}
	next, err := json.Marshal(after)
	if err != nil {
		return err
	}
	_, err = execute(ctx, db, sqlBuilder.Insert("well_event").Columns("well_id", "revision", "kind", "actor", "reason", "before_state", "after_state").Values(after.ID, after.Revision, kind, actor, reason, old, next))
	return err
}
func (r *Repository) SetWellArchive(ctx context.Context, id int64, revision *int64, archived bool, reason string) (domain.Well, error) {
	permission := domain.Permission("wells.restore")
	if archived {
		permission = "wells.archive"
	}
	return r.setWellArchive(ctx, id, revision, archived, reason, permission)
}
func (r *Repository) setWellArchive(ctx context.Context, id int64, revision *int64, archived bool, reason string, permission domain.Permission) (domain.Well, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Well{}, err
	}
	defer tx.Rollback(ctx)
	{
		if _, err = requireScopedWellTx(ctx, tx, permission, id); err != nil {
			return domain.Well{}, err
		}
	}
	before, err := one[domain.Well](ctx, tx, sqlBuilder.Select(wellColumns).From("well").Where(sq.Eq{"id": id}).Suffix("FOR UPDATE"))
	if err != nil {
		return before, err
	}
	if revision != nil && *revision != before.Revision {
		return before, domain.NewError(domain.Duplicate, "Карточка уже изменена. Обновите её перед сохранением")
	}
	if (before.ArchivedAt != nil) == archived {
		if revision == nil {
			return before, nil
		}
		return before, domain.NewError(domain.Duplicate, "Состояние архива уже изменено")
	}
	changes := sq.Eq{"revision": before.Revision + 1, "archived_at": nil}
	kind := "restore"
	if archived {
		changes["archived_at"] = sq.Expr("now()")
		kind = "archive"
	}
	after, err := mutate[domain.Well](ctx, tx, sqlBuilder.Update("well").SetMap(changes).Where(sq.Eq{"id": id}).Suffix("RETURNING "+wellColumns))
	if err != nil {
		return after, err
	}
	if err = appendWellEvent(ctx, tx, kind, reason, before, after); err != nil {
		return after, err
	}
	return after, tx.Commit(ctx)
}
func (r *Repository) WellHistory(ctx context.Context, id, before int64) (domain.WellHistory, error) {
	q := sqlBuilder.Select("revision", "created_at", "kind", "actor", "reason", "before_state AS before", "after_state AS after").From("well_event").Where(sq.Eq{"well_id": id}).OrderBy("revision DESC").Limit(51)
	if before > 0 {
		q = q.Where(sq.Lt{"revision": before})
	}
	rows, err := list[domain.WellEvent](ctx, r.pool, q)
	if err != nil {
		return domain.WellHistory{}, err
	}
	more := len(rows) > 50
	if more {
		rows = rows[:50]
	}
	return domain.WellHistory{Events: rows, HasMore: more}, nil
}
