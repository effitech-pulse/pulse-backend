package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

const tableUpper = "ABCDEFGHIJKLMNOPQRSTUVWXYZАБВГДЕЁЖЗИЙКЛМНОПРСТУФХЦЧШЩЪЫЬЭЮЯӘӨҮҖҢҺ"
const tableLower = "abcdefghijklmnopqrstuvwxyzабвгдеёжзийклмнопрстуфхцчшщъыьэюяәөүҗңһ"

func tableFold(expr string) string {
	return "translate(" + expr + ", '" + tableUpper + "', '" + tableLower + "') COLLATE \"C\""
}

type tableColumn struct{ expr, kind, end string }
type tableDataset struct {
	columns  map[string]tableColumn
	stable   string
	tail     []string
	defaults []domain.TableSort
}

// Исторические JSON snapshots допускают отсутствующие ключи. Текст тот же, что в карточке.
func wellChangesExpression() string {
	fields := [][2]string{{"well_number", "№ скважины"}, {"ngdu", "НГДУ"}, {"department", "Цех"}, {"active", "Активна"}, {"responsible", "Ответственный"}, {"unitek_id", "Связь Unitek"}, {"armits_id", "Связь Armits"}, {"archived_at", "Архив"}}
	parts := make([]string, 0, len(fields))
	value := func(state, key string) string {
		v := state + "->>'" + key + "'"
		return "CASE WHEN jsonb_typeof(" + state + "->'" + key + "')='boolean' THEN CASE " + v + " WHEN 'true' THEN 'Да' ELSE 'Нет' END ELSE COALESCE(" + v + ", '—') END"
	}
	for _, f := range fields {
		parts = append(parts, "CASE WHEN before_state->'"+f[0]+"' IS DISTINCT FROM after_state->'"+f[0]+"' THEN '"+f[1]+": ' || "+value("before_state", f[0])+" || ' → ' || "+value("after_state", f[0])+" END")
	}
	return "concat_ws(E'\\n', " + strings.Join(parts, ", ") + ")"
}
func tableDefinition(dataset string) (tableDataset, error) {
	d := tableDataset{stable: "id", defaults: []domain.TableSort{{Field: "created_at", Direction: "desc"}}}
	switch dataset {
	case "equipmenthistory":
		d.stable = "e.id"
		d.defaults = []domain.TableSort{{Field: "recorded_at", Direction: "desc"}}
		d.columns = map[string]tableColumn{"recorded_at": {"e.recorded_at", "time", ""}, "action": {"e.action", "text", ""}, "slot": {"e.slot", "text", ""}, "source": {"e.source", "text", ""}, "actor_name": {"e.actor_name", "text", ""}, "reason": {"e.reason", "text", ""}, "changes": {"e.diff::text", "text", ""}}

	case "events":
		return eventTableDefinition(), nil
	case "people":
		d.stable = "u.id"
		d.defaults = nil
		d.columns = map[string]tableColumn{"name": {"u.name", "text", ""}, "job_title": {"u.job_title", "text", ""}, "workplace_organization": {"u.workplace_organization", "text", ""}, "workplace_division": {"u.workplace_division", "text", ""}, "workplace_department": {"u.workplace_department", "text", ""}, "manager_name": {"m.name", "text", ""}, "active": {"u.active", "bool", ""}}
	case "audit":
		d.columns = map[string]tableColumn{"ts": {"ts", "time", ""}, "actor": {"actor", "text", ""}, "method": {"method", "text", ""}, "path": {"path", "text", ""}, "status_code": {"status_code", "number", ""}}
		d.defaults = []domain.TableSort{{Field: "ts", Direction: "desc"}}
	case "ingestlogs":
		d.stable = "telemetry_collection_log.id"
		d.columns = map[string]tableColumn{"timestamp": {"timestamp", "time", ""}, "source_id": {"source_id", "text", ""}, "accepted": {"accepted", "number", ""}, "duplicates": {"duplicates", "number", ""}, "rejected": {"rejected", "number", ""}, "message": {"message", "text", ""}}
		d.defaults = nil
	case "manualchecks":
		d.columns = map[string]tableColumn{"created_at": {"created_at", "time", ""}, "period": {"start_at", "time", "end_at"}, "verdict": {"result->>'verdict'", "text", ""}, "model_name": {"result->>'model_name'", "text", ""}}
	case "filejobs":
		d.stable = "j.id"
		d.columns = map[string]tableColumn{"created_at": {"j.created_at", "time", ""}, "state": {"j.state", "text", ""}, "model_name": {"i.execution_pin->>'model_name'", "text", ""}}
	case "wellhistory":
		d.stable = "revision"
		d.defaults = nil
		d.columns = map[string]tableColumn{"created_at": {"created_at", "time", ""}, "kind": {"kind", "text", ""}, "actor_name": {"actor->>'name'", "text", ""}, "changes": {wellChangesExpression(), "text", ""}, "reason": {"reason", "text", ""}}
	default:
		return d, domain.NewError(domain.Invalid, "invalid_table_query")
	}
	return d, nil
}
func tableArgument(raw json.RawMessage) (any, error) {
	if bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, domain.NewError(domain.Invalid, "invalid_table_cursor")
	}
	if n, ok := v.(json.Number); ok {
		return string(n), nil
	}
	return v, nil
}
func applyTableQuery(q sq.SelectBuilder, dataset string, req domain.TableRequest) (sq.SelectBuilder, error) {
	d, err := tableDefinition(dataset)
	if err != nil {
		return q, err
	}
	for _, f := range req.Query.Filters {
		col, ok := d.columns[f.Field]
		if !ok {
			return q, domain.NewError(domain.Invalid, "invalid_table_query")
		}
		switch f.Op {
		case "contains":
			if f.Value == nil {
				return q, domain.NewError(domain.Invalid, "invalid_table_query")
			}
			escape := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
			q = q.Where(sq.Expr(tableFold(col.expr)+" LIKE "+tableFold("?")+" ESCAPE E'\\\\'", "%"+escape.Replace(*f.Value)+"%"))
		case "in":
			if len(f.Values) == 0 {
				continue
			}
			or := sq.Or{}
			for _, v := range f.Values {
				arg, e := tableArgument(v)
				if e != nil {
					return q, e
				}
				or = append(or, sq.Eq{col.expr: arg})
			}
			q = q.Where(or)
		case "range", "overlap":
			if f.Op == "overlap" && col.end == "" {
				return q, domain.NewError(domain.Invalid, "invalid_table_query")
			}
			if len(f.From) > 0 {
				arg, e := tableArgument(f.From)
				if e != nil {
					return q, e
				}
				expr := col.expr
				if f.Op == "overlap" {
					expr = col.end
				}
				if col.kind == "number" {
					q = q.Where(sq.Expr(expr+" >= ?::numeric", arg))
				} else {
					q = q.Where(sq.GtOrEq{expr: arg})
				}
			}
			if len(f.To) > 0 {
				arg, e := tableArgument(f.To)
				if e != nil {
					return q, e
				}
				if col.kind == "number" {
					q = q.Where(sq.Expr(col.expr+" <= ?::numeric", arg))
				} else {
					q = q.Where(sq.LtOrEq{col.expr: arg})
				}
			}
		default:
			return q, domain.NewError(domain.Invalid, "invalid_table_query")
		}
	}
	sorts := req.Query.Sort
	if len(sorts) == 0 {
		sorts = d.defaults
	}
	exprs := make([]string, 0, len(sorts)+1)
	directions := make([]string, 0, len(sorts)+1)
	for _, s := range sorts {
		col, ok := d.columns[s.Field]
		if !ok || s.Direction != "asc" && s.Direction != "desc" {
			return q, domain.NewError(domain.Invalid, "invalid_table_query")
		}
		expr := col.expr
		if dataset == "events" && s.Field == "well_id" {
			expr = "well_id"
		} else if col.kind == "text" {
			expr = tableFold(expr)
		}
		exprs = append(exprs, expr)
		directions = append(directions, strings.ToUpper(s.Direction))
	}
	stable := d.stable
	if dataset == "manualchecks" || dataset == "filejobs" {
		stable += " COLLATE \"C\""
	}
	if len(d.tail) > 0 {
		for _, expr := range d.tail {
			exprs = append(exprs, expr)
			directions = append(directions, "DESC")
		}
	} else {
		exprs = append(exprs, stable)
		stableDirection := "DESC"
		if dataset == "people" {
			stableDirection = "ASC"
		}
		directions = append(directions, stableDirection)
	}
	if len(req.After) > 0 {
		if len(req.After) != len(exprs) {
			return q, domain.NewError(domain.Invalid, "invalid_table_cursor")
		}
		// Equality uses IS NOT DISTINCT FROM, keeping NULL a distinct cursor component.
		branches := sq.Or{}
		prefix := sq.And{}
		for i, expr := range exprs {
			arg, e := tableArgument(req.After[i])
			if e != nil {
				return q, e
			}
			if arg != nil {
				op := ">"
				if directions[i] == "DESC" {
					op = "<"
				}
				branch := append(sq.And{}, prefix...)
				branch = append(branch, sq.Or{sq.Expr(expr+" "+op+" ?", arg), sq.Expr(expr + " IS NULL")})
				branches = append(branches, branch)
			}
			prefix = append(prefix, sq.Expr(expr+" IS NOT DISTINCT FROM ?", arg))
		}
		q = q.Where(branches)
	}
	for i, expr := range exprs {
		q = q.OrderBy(expr + " " + directions[i] + " NULLS LAST")
	}
	q = q.Column("jsonb_build_array(" + strings.Join(exprs, ", ") + ") AS _table_keys")
	return q.Limit(uint64(req.PageSize + 1)), nil
}
func tableRows[T any](ctx context.Context, db db, q sq.SelectBuilder, dataset string, req domain.TableRequest) (domain.TableRows[T], error) {
	var out domain.TableRows[T]
	q, err := applyTableQuery(q, dataset, req)
	if err != nil {
		return out, err
	}
	query := sqlBuilder.Select("to_jsonb(t) - '_table_keys' AS item", "_table_keys AS keys").FromSelect(q, "t")
	records, err := list[struct {
		Item T                 `json:"item"`
		Keys []json.RawMessage `json:"keys"`
	}](ctx, db, query)
	if err != nil {
		return out, fmt.Errorf("table rows: %w", err)
	}
	out.Items = make([]T, 0, len(records))
	if len(records) > req.PageSize {
		out.HasMore = true
		records = records[:req.PageSize]
	}
	for _, record := range records {
		out.Items = append(out.Items, record.Item)
		out.Keys = record.Keys
	}
	return out, nil
}
func (r *Repository) AuditTable(ctx context.Context, req domain.TableRequest) (domain.TableRows[domain.AuditEntry], error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return authorizedTableRows[domain.AuditEntry](ctx, r, "audit.read", nil, sqlBuilder.Select("id,ts,actor,method,path,status_code").From("action_log"), "audit", req)
}
func (r *Repository) CollectionLogsTable(ctx context.Context, req domain.TableRequest) (domain.TableRows[domain.TableCollectionLog], error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return authorizedTableRows[domain.TableCollectionLog](ctx, r, "integrations.logs.read", nil, sqlBuilder.Select("id::text AS id,timestamp,source_id,accepted,duplicates,rejected,message").From("telemetry_collection_log"), "ingestlogs", req)
}
func (r *Repository) ManualChecksTable(ctx context.Context, well int64, req domain.TableRequest) (domain.TableRows[domain.ManualCheck], error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return authorizedTableRows[domain.ManualCheck](ctx, r, "checks.read", &well, checkQuery().Where(sq.Eq{"well_id": well}), "manualchecks", req)
}
func (r *Repository) WellHistoryTable(ctx context.Context, well int64, req domain.TableRequest) (domain.TableRows[domain.TableWellEvent], error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return authorizedTableRows[domain.TableWellEvent](ctx, r, "wells.history.read", &well, sqlBuilder.Select("revision,created_at,kind,actor,reason,before_state AS before,after_state AS after").Column(wellChangesExpression()+" AS changes").From("well_event").Where(sq.Eq{"well_id": well}), "wellhistory", req)
}
func (r *Repository) FileJobsTable(ctx context.Context, well int64, req domain.TableRequest) (domain.TableRows[domain.FileJobStatus], error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var zero domain.TableRows[domain.FileJobStatus]
	actor, e := fileJobSession(ctx)
	if e != nil {
		return zero, e
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	ok, e := r.authorizeFileJobTx(ctx, tx, actor, well, nil, "filechecks.read", false)
	if e != nil {
		return zero, e
	}
	if !ok {
		return zero, missingFileJob()
	}
	p := r.fileJobPolicy
	q := jobStatusQuery().Where(sq.Eq{"j.actor_account_id": actor, "j.well_id": well, "j.pilot_run_id": p.RunID, "j.cohort_version": p.CohortVersion, "j.cohort_manifest_sha256": p.CohortManifestSHA256})
	out, e := tableRows[domain.FileJobStatus](ctx, tx, q, "filejobs", req)
	if e != nil {
		return zero, e
	}
	return out, tx.Commit(ctx)
}

// Повторная проверка authority и родителя происходит в той же транзакции, что страница.
func authorizedTableRows[T any](ctx context.Context, r *Repository, permission domain.Permission, well *int64, q sq.SelectBuilder, dataset string, req domain.TableRequest) (domain.TableRows[T], error) {
	var zero domain.TableRows[T]
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback(ctx)
	if well != nil {
		if _, err := requireScopedWellTx(ctx, tx, permission, *well); err != nil {
			return zero, err
		}
	} else {
		a, err := currentScopeAuthorization(ctx, tx, permission)
		if err != nil {
			return zero, err
		}
		if !a.FullApplicationScope() {
			return zero, domain.NewError(domain.Forbidden, "Требуется полная область приложения")
		}
	}
	out, err := tableRows[T](ctx, tx, q, dataset, req)
	if err != nil {
		return zero, err
	}
	return out, tx.Commit(ctx)
}
