package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// CaptureCohort фиксирует только существующие показательные объекты без изменения их состояния.
func (r *Repository) CaptureCohort(ctx context.Context, version string) (domain.Cohort, error) {
	out := domain.Cohort{Version: version}
	if strings.TrimSpace(version) != version || version == "" || len(version) > 120 {
		return out, domain.NewError(domain.Invalid, "Некорректная версия когорты")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if _, err = execute(ctx, tx, sq.Expr("SELECT pg_advisory_xact_lock(?)", int64(74019323))); err != nil {
		return out, err
	}
	wells, err := list[domain.Well](ctx, tx, sqlBuilder.Select(wellColumns).From("well").Where(sq.Or{sq.Eq{"fund_type": "presentation"}, sq.And{sq.GtOrEq{"unitek_id": -900099}, sq.LtOrEq{"unitek_id": -900000}}}).OrderBy("id").Suffix("FOR SHARE"))
	if err != nil {
		return out, err
	}
	if len(wells) != 100 {
		return out, domain.NewError(domain.Invalid, "Capture требует ровно 100 существующих presentation-скважин")
	}
	type binding struct {
		ID       int64
		UnitekID *int64
		ArmitsID *int64
		Revision int64
	}
	bindings := make([]binding, 0, 100)
	seen := map[int64]bool{}
	for _, w := range wells {
		if w.UnitekID == nil || *w.UnitekID < -900099 || *w.UnitekID > -900000 || seen[*w.UnitekID] || w.FundType != "presentation" || w.NGDU != -900 || w.Department != -900 {
			return out, domain.NewError(domain.Invalid, "Некорректные привязки presentation-скважин")
		}
		seen[*w.UnitekID] = true
		bindings = append(bindings, binding{w.ID, w.UnitekID, w.ArmitsID, w.Revision})
	}
	raw, err := json.Marshal(bindings)
	if err != nil {
		return out, err
	}
	digest := sha256.Sum256(raw)
	out.ManifestSHA256 = hex.EncodeToString(digest[:])
	if _, err = execute(ctx, tx, sqlBuilder.Insert("app_cohort").Columns("version", "manifest_sha256").Values(version, out.ManifestSHA256)); err != nil {
		return out, translate(err)
	}
	for _, w := range wells {
		if _, err = execute(ctx, tx, sqlBuilder.Insert("app_cohort_member").Columns("version", "well_id", "unitek_id", "armits_id", "revision").Values(version, w.ID, w.UnitekID, w.ArmitsID, w.Revision)); err != nil {
			return out, err
		}
	}
	return out, tx.Commit(ctx)
}
func (r *Repository) CohortMembers(ctx context.Context, version string) (domain.Cohort, []domain.Well, error) {
	// Один statement даёт согласованные metadata/lifecycle и сохранённые внешние привязки.
	q := sqlBuilder.Select("c.manifest_sha256", "w.revision,w.archived_at,w.responsible,w.fund_type,w.id,m.unitek_id,m.armits_id,w.well_number,w.ngdu,w.department,w.active").From("app_cohort c").Join("app_cohort_member m ON m.version=c.version").Join("well w ON w.id=m.well_id").Where(sq.Eq{"c.version": version}).OrderBy("w.id")
	query, args, err := build(q)
	if err != nil {
		return domain.Cohort{}, nil, err
	}
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return domain.Cohort{}, nil, err
	}
	defer rows.Close()
	c := domain.Cohort{Version: version}
	wells := []domain.Well{}
	for rows.Next() {
		var w domain.Well
		if err = rows.Scan(&c.ManifestSHA256, &w.Revision, &w.ArchivedAt, &w.Responsible, &w.FundType, &w.ID, &w.UnitekID, &w.ArmitsID, &w.WellNumber, &w.NGDU, &w.Department, &w.Active); err != nil {
			return c, nil, err
		}
		wells = append(wells, w)
	}
	if err = rows.Err(); err != nil {
		return c, nil, err
	}
	if len(wells) != 100 {
		return c, nil, domain.NewError(domain.Missing, "Полная версия когорты не найдена")
	}
	return c, wells, nil
}
