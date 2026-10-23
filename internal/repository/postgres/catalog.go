package postgres

import (
	"context"

	sq "github.com/Masterminds/squirrel"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func referenceTable(kind domain.ReferenceKind) (string, error) {
	switch kind {
	case domain.NGDUReference:
		return "ngdu", nil
	case domain.DepartmentReference:
		return "department", nil
	default:
		return "", domain.NewError(domain.Invalid, "Неизвестный справочник")
	}
}

func (r *Repository) References(ctx context.Context, kind domain.ReferenceKind) ([]domain.Reference, error) {
	table, err := referenceTable(kind)
	if err != nil {
		return nil, err
	}
	return list[domain.Reference](ctx, r.pool, sqlBuilder.Select("id", "name").From(table).OrderBy("name"))
}

func (r *Repository) CreateReference(ctx context.Context, kind domain.ReferenceKind, id int64, name string) (domain.Reference, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return domain.Reference{}, e
	}
	defer tx.Rollback(ctx)
	if domain.RBACEnforced(ctx) {
		a, e := currentScopeAuthorization(ctx, tx, "references.create")
		if e != nil {
			return domain.Reference{}, e
		}
		if !a.FullApplicationScope() {
			return domain.Reference{}, domain.NewError(domain.Forbidden, "Требуется полная область приложения")
		}
	}

	table, err := referenceTable(kind)
	if err != nil {
		return domain.Reference{}, err
	}
	query := sqlBuilder.Insert(table).Columns("id", "name").Values(id, name).Suffix("RETURNING id,name")
	out, e := mutate[domain.Reference](ctx, tx, query)
	if e != nil {
		return out, e
	}
	return out, tx.Commit(ctx)
}

func (r *Repository) UpdateReference(ctx context.Context, kind domain.ReferenceKind, id int64, name string) (domain.Reference, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return domain.Reference{}, e
	}
	defer tx.Rollback(ctx)
	if domain.RBACEnforced(ctx) {
		a, e := currentScopeAuthorization(ctx, tx, "references.update")
		if e != nil {
			return domain.Reference{}, e
		}
		if !a.FullApplicationScope() {
			return domain.Reference{}, domain.NewError(domain.Forbidden, "Требуется полная область приложения")
		}
	}

	table, err := referenceTable(kind)
	if err != nil {
		return domain.Reference{}, err
	}
	query := sqlBuilder.Update(table).Set("name", name).Where(sq.Eq{"id": id}).Suffix("RETURNING id,name")
	out, e := mutate[domain.Reference](ctx, tx, query)
	if e != nil {
		return out, e
	}
	return out, tx.Commit(ctx)
}

func (r *Repository) DeleteReference(ctx context.Context, kind domain.ReferenceKind, id int64) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if domain.RBACEnforced(ctx) {
		a, e := currentScopeAuthorization(ctx, tx, "references.delete")
		if e != nil {
			return e
		}
		if !a.FullApplicationScope() {
			return domain.NewError(domain.Forbidden, "Требуется полная область приложения")
		}
	}

	table, err := referenceTable(kind)
	if err != nil {
		return err
	}
	if e = remove(ctx, tx, sqlBuilder.Delete(table).Where(sq.Eq{"id": id})); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

const incidentColumns = "id,well_id,failure_type,failure_date,days_before,created_at"

func (r *Repository) Incidents(ctx context.Context, id int64) ([]domain.Incident, error) {
	query := sqlBuilder.Select(incidentColumns).From("incident").
		Where(sq.Eq{"well_id": id}).OrderBy("failure_date DESC", "id DESC")
	return list[domain.Incident](ctx, r.pool, query)
}

func (r *Repository) CreateIncident(ctx context.Context, v domain.Incident) (domain.Incident, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Incident{}, err
	}
	defer tx.Rollback(ctx)
	if domain.RBACEnforced(ctx) {
		if _, err = requireScopedWellTx(ctx, tx, "incidents.create", v.WellID); err != nil {
			return domain.Incident{}, err
		}
	}
	query := sqlBuilder.Insert("incident").
		Columns("well_id", "failure_type", "failure_date", "days_before").
		Values(v.WellID, v.FailureType, sq.Expr("?::date", v.FailureDate), v.DaysBefore).
		Suffix("RETURNING " + incidentColumns)
	out, err := mutate[domain.Incident](ctx, tx, query)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (r *Repository) DeleteIncident(ctx context.Context, id int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if domain.RBACEnforced(ctx) {
		a, e := currentScopeAuthorization(ctx, tx, "incidents.delete")
		if e != nil {
			return e
		}
		v, e := one[domain.Incident](ctx, tx, sqlBuilder.Select(incidentColumns).From("incident").Where(sq.Eq{"id": id}).Suffix("FOR UPDATE"))
		if e != nil {
			return e
		}
		w, e := one[domain.Well](ctx, tx, sqlBuilder.Select(wellColumns).From("well").Where(sq.Eq{"id": v.WellID}).Suffix("FOR SHARE"))
		if e != nil {
			return e
		}
		if !a.AllowsWell(w) {
			return domain.NewError(domain.Missing, "Скважина не найдена")
		}
	}
	if err = remove(ctx, tx, sqlBuilder.Delete("incident").Where(sq.Eq{"id": id})); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) IncidentStats(ctx context.Context) (domain.IncidentStats, error) {
	return r.incidentStats(ctx, nil)
}
func (r *Repository) IncidentStatsForWells(ctx context.Context, ids []int64) (domain.IncidentStats, error) {
	return r.incidentStats(ctx, ids)
}
func (r *Repository) incidentStats(ctx context.Context, ids []int64) (domain.IncidentStats, error) {
	out := domain.IncidentStats{}
	var err error
	byType := sqlBuilder.Select("failure_type", "count(*) AS count").From("incident").Where(linkedWellPredicate(ctx, "well_id", false)).
		GroupBy("failure_type").OrderBy("count DESC", "failure_type")
	if ids != nil {
		byType = byType.Where(sq.Eq{"well_id": ids})
	}
	out.ByType, err = list[domain.TypeCount](ctx, r.pool, byType)
	if err != nil {
		return out, err
	}
	byMonth := sqlBuilder.Select("to_char(failure_date,'YYYY-MM') AS month", "count(*) AS count").
		From("incident").Where(linkedWellPredicate(ctx, "well_id", false)).GroupBy("month").OrderBy("month")
	if ids != nil {
		byMonth = byMonth.Where(sq.Eq{"well_id": ids})
	}
	out.ByMonth, err = list[domain.MonthCount](ctx, r.pool, byMonth)
	for _, v := range out.ByType {
		out.Total += v.Count
	}
	return out, err
}

func (r *Repository) Predictions(ctx context.Context, id int64, count int) ([]domain.Prediction, error) {
	query := sqlBuilder.Select("id,well_id,status,parameters,processing_date,created_at").
		From("prediction").Where(linkedWellPredicate(ctx, "well_id", false)).Where(sq.Eq{"well_id": id}).OrderBy("processing_date DESC").Limit(uint64(count))
	return list[domain.Prediction](ctx, r.pool, query)
}

func (r *Repository) Complications(ctx context.Context, id int64) ([]domain.Complication, error) {
	query := sqlBuilder.Select("id,well_id,code,name,category,description,active,created_at").
		From("complication_reference").Where(linkedWellPredicate(ctx, "well_id", false)).Where(sq.Eq{"well_id": id, "active": true}).OrderBy("created_at DESC")
	return list[domain.Complication](ctx, r.pool, query)
}

const runColumns = "id,dag_id,run_id,execution_date,well_id,ngdu,department,status_type,severity,source,message,parameters,artifacts_path,created_at"

func (r *Repository) Runs(ctx context.Context, filter domain.RunFilter) ([]domain.RunEvent, error) {
	return r.runsForWells(ctx, filter, nil)
}
func (r *Repository) RunsForWells(ctx context.Context, filter domain.RunFilter, ids []int64) ([]domain.RunEvent, error) {
	return r.runsForWells(ctx, filter, ids)
}
func (r *Repository) runsForWells(ctx context.Context, filter domain.RunFilter, ids []int64) ([]domain.RunEvent, error) {
	query := sqlBuilder.Select(runColumns).From("run_archive").Where(linkedWellPredicate(ctx, "well_id", true)).
		OrderBy("execution_date DESC").Limit(uint64(filter.Limit))
	if ids != nil {
		query = query.Where(sq.Eq{"well_id": ids})
	}
	if filter.DAGID != nil {
		query = query.Where(sq.Eq{"dag_id": *filter.DAGID})
	}
	if filter.WellID != nil {
		query = query.Where(sq.Eq{"well_id": *filter.WellID})
	}
	if filter.StatusType != nil {
		query = query.Where(sq.Eq{"status_type": *filter.StatusType})
	}
	return list[domain.RunEvent](ctx, r.pool, query)
}

func (r *Repository) RunEvents(ctx context.Context, dag, run string) ([]domain.RunEvent, error) {
	return r.runEventsForWells(ctx, dag, run, nil)
}
func (r *Repository) RunEventsForWells(ctx context.Context, dag, run string, ids []int64) ([]domain.RunEvent, error) {
	return r.runEventsForWells(ctx, dag, run, ids)
}
func (r *Repository) runEventsForWells(ctx context.Context, dag, run string, ids []int64) ([]domain.RunEvent, error) {
	q := sqlBuilder.Select(runColumns).From("run_archive").Where(linkedWellPredicate(ctx, "well_id", true)).Where(sq.Eq{"dag_id": dag, "run_id": run}).OrderBy("created_at")
	if ids != nil {
		q = q.Where(sq.Eq{"well_id": ids})
	}
	return list[domain.RunEvent](ctx, r.pool, q)
}

func (r *Repository) Audit(ctx context.Context, count int) ([]domain.AuditEntry, error) {
	query := sqlBuilder.Select("id,ts,actor,method,path,status_code").From("action_log").
		OrderBy("ts DESC", "id DESC").Limit(uint64(count))
	return list[domain.AuditEntry](ctx, r.pool, query)
}

func (r *Repository) RecordAudit(ctx context.Context, actor, method, path string, status int) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	insert := sqlBuilder.Insert("action_log").Columns("actor", "method", "path", "status_code").
		Values(actor, method, path, status)
	if _, err = execute(ctx, tx, insert); err != nil {
		return err
	}
	cleanup := sqlBuilder.Delete("action_log").Where("ts < now() - interval '7 days'")
	if _, err = execute(ctx, tx, cleanup); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repository) IncidentWell(ctx context.Context, id int64) (int64, error) {
	v, err := one[struct {
		WellID int64 `json:"well_id"`
	}](ctx, r.pool, sqlBuilder.Select("well_id").From("incident").Where(sq.Eq{"id": id}))
	return v.WellID, err
}
