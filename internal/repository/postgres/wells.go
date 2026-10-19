package postgres

import (
	"context"

	sq "github.com/Masterminds/squirrel"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

const wellColumns = "revision,archived_at,responsible,fund_type,id,unitek_id,armits_id,well_number,ngdu,department,active"

func (r *Repository) Wells(ctx context.Context, active bool) ([]domain.Well, error) {
	query := sqlBuilder.Select(wellColumns).From("well").OrderBy("id")
	if active {
		query = query.Where(sq.Eq{"active": true})
	}
	return list[domain.Well](ctx, r.pool, query)
}

func (r *Repository) Well(ctx context.Context, id int64) (domain.Well, error) {
	query := sqlBuilder.Select(wellColumns).From("well").Where(sq.Eq{"id": id})
	return one[domain.Well](ctx, r.pool, query)
}

func (r *Repository) CreateWell(ctx context.Context, w domain.Well) (domain.Well, error) {
	query := sqlBuilder.Insert("well").
		Columns("unitek_id", "armits_id", "well_number", "ngdu", "department", "active", "fund_type", "responsible").
		Values(w.UnitekID, w.ArmitsID, w.WellNumber, w.NGDU, w.Department, w.Active, sq.Expr("COALESCE(NULLIF(?, ''), 'operational')", w.FundType), w.Responsible).
		Suffix("RETURNING " + wellColumns)
	return mutate[domain.Well](ctx, r.pool, query)
}

func (r *Repository) PatchWell(ctx context.Context, id int64, p domain.WellPatch) (domain.Well, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Well{}, err
	}
	defer tx.Rollback(ctx)
	old, err := one[domain.Well](ctx, tx, sqlBuilder.Select(wellColumns).From("well").Where(sq.Eq{"id": id}).Suffix("FOR UPDATE"))
	if err != nil {
		return old, err
	}
	if p.Revision.Present && old.Revision != p.Revision.Value {
		return old, domain.NewError(domain.Duplicate, "Карточка уже изменена. Обновите её перед сохранением")
	}
	if old.ArchivedAt != nil {
		return old, domain.NewError(domain.Duplicate, "Сначала восстановите скважину из архива")
	}
	changes := sq.Eq{}
	if p.Responsible.Present {
		if p.Responsible.Null {
			changes["responsible"] = nil
		} else {
			changes["responsible"] = p.Responsible.Value
		}
	}
	if p.UnitekID.Present {
		if p.UnitekID.Null {
			changes["unitek_id"] = nil
		} else {
			changes["unitek_id"] = p.UnitekID.Value
		}
	}
	if p.ArmitsID.Present {
		if p.ArmitsID.Null {
			changes["armits_id"] = nil
		} else {
			changes["armits_id"] = p.ArmitsID.Value
		}
	}
	if p.NGDU.Present {
		changes["ngdu"] = p.NGDU.Value
	}
	if p.Department.Present {
		changes["department"] = p.Department.Value
	}
	if p.Active.Present {
		changes["active"] = p.Active.Value
	}
	if p.WellNumber.Present {
		if p.WellNumber.Null {
			changes["well_number"] = nil
		} else {
			changes["well_number"] = p.WellNumber.Value
		}
	}
	if len(changes) == 0 {
		return old, nil
	}
	changes["revision"] = old.Revision + 1
	query := sqlBuilder.Update("well").SetMap(changes).
		Where(sq.Eq{"id": id}).Suffix("RETURNING " + wellColumns)
	out, err := mutate[domain.Well](ctx, tx, query)
	if err != nil {
		return out, err
	}
	if err = appendWellEvent(ctx, tx, "metadata", "", old, out); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (r *Repository) DeleteWell(ctx context.Context, id int64) error {
	_, err := r.SetWellArchive(ctx, id, nil, true, "Архивирование через совместимый DELETE API")
	return err
}

func (r *Repository) ImportWells(ctx context.Context, rows []domain.Well) (domain.ImportResult, error) {
	out := domain.ImportResult{Errors: []string{}}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	for _, w := range rows {
		if w.ID > 0 {
			if _, err := one[domain.Well](ctx, tx, sqlBuilder.Select(wellColumns).From("well").Where(sq.Eq{"id": w.ID})); err != nil {
				return out, domain.NewError(domain.Invalid, "В импорте указан неизвестный ID Pulse")
			}
			out.Skipped++
			continue
		}
		query := sqlBuilder.Insert("well").
			Columns("unitek_id", "armits_id", "well_number", "ngdu", "department", "active", "fund_type", "responsible").
			Values(w.UnitekID, w.ArmitsID, w.WellNumber, w.NGDU, w.Department, true, sq.Expr("COALESCE(NULLIF(?, ''), 'operational')", w.FundType), w.Responsible).
			Suffix("ON CONFLICT DO NOTHING")
		tag, err := execute(ctx, tx, query)
		if err != nil {
			return out, err
		}
		if tag.RowsAffected() == 0 {
			out.Skipped++
		} else {
			out.Created++
		}
	}
	return out, tx.Commit(ctx)
}
