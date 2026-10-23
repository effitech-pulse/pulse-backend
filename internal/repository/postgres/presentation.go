package postgres

import (
	"context"

	sq "github.com/Masterminds/squirrel"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func (r *Repository) PresentationWells(ctx context.Context) ([]domain.PresentationWell, error) {
	latest := sqlBuilder.Select("id", "result", "created_at").From("manual_check").Where("well_id = w.id").OrderBy("created_at DESC", "id DESC").Limit(1)
	q := sqlBuilder.Select("w.revision", "w.archived_at", "w.responsible", "w.id", "w.unitek_id", "w.armits_id", "w.well_number", "w.ngdu", "w.department", "w.active", "w.fund_type").Column("CASE WHEN c.id IS NULL THEN NULL ELSE jsonb_build_object('id',c.id,'verdict',c.result->>'verdict','latest_risk',c.result->'latest_risk','created_at',c.created_at,'model_version',c.result->'model_version') END AS latest_check").From("well w").Where(contextWellPredicate(ctx, "w")).JoinClause(sq.Expr("LEFT JOIN LATERAL (?) AS c ON true", latest)).Where(sq.Eq{"w.fund_type": "presentation", "w.active": true, "w.archived_at": nil}).OrderBy("w.id")
	return list[domain.PresentationWell](ctx, r.pool, q)
}
