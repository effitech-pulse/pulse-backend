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
	table, err := referenceTable(kind)
	if err != nil {
		return domain.Reference{}, err
	}
	query := sqlBuilder.Insert(table).Columns("id", "name").Values(id, name).Suffix("RETURNING id,name")
	return mutate[domain.Reference](ctx, r.pool, query)
}

func (r *Repository) UpdateReference(ctx context.Context, kind domain.ReferenceKind, id int64, name string) (domain.Reference, error) {
	table, err := referenceTable(kind)
	if err != nil {
		return domain.Reference{}, err
	}
	query := sqlBuilder.Update(table).Set("name", name).Where(sq.Eq{"id": id}).Suffix("RETURNING id,name")
	return mutate[domain.Reference](ctx, r.pool, query)
}

func (r *Repository) DeleteReference(ctx context.Context, kind domain.ReferenceKind, id int64) error {
	table, err := referenceTable(kind)
	if err != nil {
		return err
	}
	return remove(ctx, r.pool, sqlBuilder.Delete(table).Where(sq.Eq{"id": id}))
}

const incidentColumns = "id,well_id,failure_type,failure_date,days_before,created_at"

func (r *Repository) Incidents(ctx context.Context, id int64) ([]domain.Incident, error) {
	query := sqlBuilder.Select(incidentColumns).From("incident").
		Where(sq.Eq{"well_id": id}).OrderBy("failure_date DESC", "id DESC")
	return list[domain.Incident](ctx, r.pool, query)
}

func (r *Repository) CreateIncident(ctx context.Context, v domain.Incident) (domain.Incident, error) {
	query := sqlBuilder.Insert("incident").
		Columns("well_id", "failure_type", "failure_date", "days_before").
		Values(v.WellID, v.FailureType, sq.Expr("?::date", v.FailureDate), v.DaysBefore).
		Suffix("RETURNING " + incidentColumns)
	return mutate[domain.Incident](ctx, r.pool, query)
}

func (r *Repository) DeleteIncident(ctx context.Context, id int64) error {
	return remove(ctx, r.pool, sqlBuilder.Delete("incident").Where(sq.Eq{"id": id}))
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
	byType := sqlBuilder.Select("failure_type", "count(*) AS count").From("incident").
		GroupBy("failure_type").OrderBy("count DESC", "failure_type")
	if ids != nil {
		byType = byType.Where(sq.Eq{"well_id": ids})
	}
	out.ByType, err = list[domain.TypeCount](ctx, r.pool, byType)
	if err != nil {
		return out, err
	}
	byMonth := sqlBuilder.Select("to_char(failure_date,'YYYY-MM') AS month", "count(*) AS count").
		From("incident").GroupBy("month").OrderBy("month")
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
		From("prediction").Where(sq.Eq{"well_id": id}).OrderBy("processing_date DESC").Limit(uint64(count))
	return list[domain.Prediction](ctx, r.pool, query)
}

func (r *Repository) Complications(ctx context.Context, id int64) ([]domain.Complication, error) {
	query := sqlBuilder.Select("id,well_id,code,name,category,description,active,created_at").
		From("complication_reference").Where(sq.Eq{"well_id": id, "active": true}).OrderBy("created_at DESC")
	return list[domain.Complication](ctx, r.pool, query)
}

const runColumns = "id,dag_id,run_id,execution_date,well_id,ngdu,department,status_type,severity,source,message,parameters,artifacts_path,created_at"

func (r *Repository) Runs(ctx context.Context, filter domain.RunFilter) ([]domain.RunEvent, error) {
	query := sqlBuilder.Select(runColumns).From("run_archive").
		OrderBy("execution_date DESC").Limit(uint64(filter.Limit))
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
	query := sqlBuilder.Select(runColumns).From("run_archive").
		Where(sq.Eq{"dag_id": dag, "run_id": run}).OrderBy("created_at")
	return list[domain.RunEvent](ctx, r.pool, query)
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
