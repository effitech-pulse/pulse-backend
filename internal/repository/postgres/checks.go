package postgres

import (
	"context"
	"encoding/json"

	sq "github.com/Masterminds/squirrel"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func checkQuery() sq.SelectBuilder {
	return sqlBuilder.Select("id", "well_id", "request_id", "start_at AS start", "end_at AS end", "created_at", "input_sha256", "points_count", "resolution", "result").From("manual_check")
}
func (r *Repository) ManualChecks(ctx context.Context, well int64, before string) ([]domain.ManualCheck, error) {
	q := checkQuery().Where(sq.Eq{"well_id": well})
	if before != "" {
		q = q.Where(sq.Expr("(created_at, id) < (SELECT created_at, id FROM manual_check WHERE well_id = ? AND id = ?)", well, before))
	}
	return list[domain.ManualCheck](ctx, r.pool, q.OrderBy("created_at DESC", "id DESC").Limit(25))
}
func (r *Repository) ManualCheck(ctx context.Context, well int64, id string) (domain.ManualCheck, error) {
	return one[domain.ManualCheck](ctx, r.pool, checkQuery().Where(sq.Eq{"well_id": well, "id": id}))
}
func (r *Repository) ManualCheckRequest(ctx context.Context, well int64, request string) (domain.ManualCheck, error) {
	return one[domain.ManualCheck](ctx, r.pool, checkQuery().Where(sq.Eq{"well_id": well, "request_id": request}))
}
func (r *Repository) SaveManualCheck(ctx context.Context, v domain.ManualCheck) (domain.ManualCheck, error) {
	raw, err := json.Marshal(v.Result)
	if err != nil {
		return domain.ManualCheck{}, err
	}
	_, err = execute(ctx, r.pool, sqlBuilder.Insert("manual_check").Columns("id", "well_id", "request_id", "start_at", "end_at", "created_at", "input_sha256", "points_count", "resolution", "result").Values(v.ID, v.WellID, v.RequestID, v.Start, v.End, v.CreatedAt, v.InputSHA256, v.PointsCount, v.Resolution, raw).Suffix("ON CONFLICT (well_id, request_id) DO NOTHING"))
	if err != nil {
		return domain.ManualCheck{}, err
	}
	return r.ManualCheckRequest(ctx, v.WellID, v.RequestID)
}
