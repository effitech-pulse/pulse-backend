package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// eventReadTx проверяет captured fence в той же RR snapshot, что источники.
func (r *Repository) eventReadTx(ctx context.Context, scope domain.EventReadScope) (pgx.Tx, error) {
	if scope.Mode != "research" && scope.Mode != "cohort" {
		return nil, fmt.Errorf("missing event scope")
	}
	tx, e := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return nil, e
	}
	fail := func(e error) (pgx.Tx, error) { _ = tx.Rollback(ctx); return nil, e }
	if _, e = execute(ctx, tx, sq.Expr("SET LOCAL statement_timeout = '3s'")); e != nil {
		return fail(e)
	}
	if scope.Mode == "cohort" {
		f := scope.Cohort
		if f == nil || f.Version == "" || f.Manifest == "" || len(f.IDs) != 100 {
			return fail(fmt.Errorf("missing event cohort fence"))
		}
		q := sqlBuilder.Select("c.manifest_sha256", "w.id").From("app_cohort c").Join("app_cohort_member m ON m.version=c.version").Join("well w ON w.id=m.well_id").Where(sq.Eq{"c.version": f.Version}).OrderBy("w.id")
		query, args, e := build(q)
		if e != nil {
			return fail(e)
		}
		rows, e := tx.Query(ctx, query, args...)
		if e != nil {
			return fail(e)
		}
		ids := make([]int64, 0, 100)
		var manifest string
		valid := true
		for rows.Next() {
			var id int64
			if e = rows.Scan(&manifest, &id); e != nil {
				break
			}
			if manifest != f.Manifest || id <= 0 {
				valid = false
			}
			ids = append(ids, id)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		expected := slices.Clone(f.IDs)
		slices.Sort(expected)
		if e != nil {
			return fail(e)
		}
		if !valid || len(ids) != 100 || !slices.Equal(ids, expected) {
			return fail(fmt.Errorf("event cohort changed"))
		}
		for i := 1; i < len(ids); i++ {
			if ids[i] == ids[i-1] {
				return fail(fmt.Errorf("duplicate event cohort member"))
			}
		}
	}
	return tx, nil
}
func eventWellScope(scope domain.EventReadScope) sq.And {
	p := scope.Principal
	where := sq.And{}
	if p.Role != "admin" {
		if len(p.Scope.Funds) == 0 {
			where = append(where, sq.Expr("FALSE"))
		} else {
			where = append(where, sq.Eq{"COALESCE(NULLIF(w.fund_type,''),'operational')": p.Scope.Funds})
		}
		for _, v := range []struct {
			col string
			ids *[]int64
		}{{"w.ngdu", p.Scope.NGDUs}, {"w.department", p.Scope.Departments}} {
			if v.ids != nil {
				if len(*v.ids) == 0 {
					where = append(where, sq.Expr("FALSE"))
				} else {
					where = append(where, sq.Eq{v.col: *v.ids})
				}
			}
		}
	}
	if scope.Mode == "cohort" {
		if scope.Cohort == nil {
			where = append(where, sq.Expr("FALSE"))
		} else {
			where = append(where, sq.Eq{"w.id": scope.Cohort.IDs})
		}
	}
	return where
}
func eventDirectWell(ctx context.Context, tx pgx.Tx, scope domain.EventReadScope, well *int64) error {
	if well == nil {
		return nil
	}
	q := sqlBuilder.Select("w.id").From("well w").Where(eventWellScope(scope)).Where(sq.Eq{"w.id": *well})
	query, args, e := build(q)
	if e != nil {
		return e
	}
	var id int64
	e = tx.QueryRow(ctx, query, args...).Scan(&id)
	if e == pgx.ErrNoRows {
		return domain.NewError(domain.Missing, "Событие недоступно.")
	}
	return e
}

type eventBranch struct {
	source, from, at, target, revision, actor, verdict, status, conclusion, change, payload string
	rank                                                                                    int16
	joins                                                                                   []string
	where                                                                                   sq.And
}

func eventSourceBranches(exact bool) []eventBranch {
	null := "NULL::text"
	manualPayload := "NULL::jsonb"
	reviewPayload := "NULL::jsonb"
	wellPayload := "NULL::jsonb"
	if exact {
		manualPayload = "jsonb_build_object('id',mc.id,'well_id',mc.well_id,'created_at',mc.created_at,'start',mc.start_at,'end',mc.end_at,'result',mc.result)"
		reviewPayload = "jsonb_build_object('revision',re.revision,'created_at',re.created_at,'before',re.before_state,'after',re.after_state,'comment',re.comment)"
		wellPayload = "jsonb_build_object('revision',we.revision,'created_at',we.created_at,'kind',we.kind,'before',we.before_state,'after',we.after_state,'reason',we.reason)"
	}
	return []eventBranch{
		{source: "mc", from: "manual_check mc", at: "mc.created_at", target: "mc.id", revision: "0::bigint", actor: "NULL::jsonb", verdict: "CASE WHEN jsonb_typeof(mc.result->'verdict')='string' THEN mc.result->>'verdict' END", status: null, conclusion: null, change: null, payload: manualPayload, rank: 1, joins: []string{"well w ON w.id=mc.well_id"}},
		{source: "review.check", from: "review_event re", at: "re.created_at", target: "re.target_id", revision: "re.revision", actor: "re.actor", verdict: null, status: "CASE WHEN jsonb_typeof(re.after_state->'status')='string' THEN re.after_state->>'status' END", conclusion: "CASE WHEN jsonb_typeof(re.after_state->'conclusion')='string' THEN re.after_state->>'conclusion' END", change: null, payload: reviewPayload, rank: 2, joins: []string{"case_review cr ON (cr.kind,cr.target_id)=(re.kind,re.target_id)", "manual_check mc ON mc.id=cr.check_id AND mc.id=re.target_id", "well w ON w.id=mc.well_id"}, where: sq.And{sq.Eq{"re.kind": "check", "cr.alert_id": nil, "cr.file_job_id": nil}, sq.Expr("(cr.well_id IS NULL OR cr.well_id=w.id)")}},
		{source: "review.alert", from: "review_event re", at: "re.created_at", target: "re.target_id", revision: "re.revision", actor: "re.actor", verdict: null, status: "CASE WHEN jsonb_typeof(re.after_state->'status')='string' THEN re.after_state->>'status' END", conclusion: "CASE WHEN jsonb_typeof(re.after_state->'conclusion')='string' THEN re.after_state->>'conclusion' END", change: null, payload: reviewPayload, rank: 3, joins: []string{"case_review cr ON (cr.kind,cr.target_id)=(re.kind,re.target_id)", "ml_alert a ON a.id=cr.alert_id AND a.id::text=re.target_id", "well w ON w.id=a.app_well_id"}, where: sq.And{sq.Eq{"re.kind": "alert", "cr.check_id": nil, "cr.file_job_id": nil}, sq.Expr("(cr.well_id IS NULL OR cr.well_id=w.id)")}},
		{source: "well", from: "well_event we", at: "we.created_at", target: "we.well_id::text", revision: "we.revision", actor: "we.actor", verdict: null, status: null, conclusion: null, change: "we.kind", payload: wellPayload, rank: 4, joins: []string{"well w ON w.id=we.well_id"}, where: sq.And{sq.Eq{"we.kind": []string{"metadata", "archive", "restore"}}}},
	}
}
func eventBranchQuery(b eventBranch, q domain.EventReadQuery, locator *domain.EventLocator) sq.SelectBuilder {
	// Only fixed allowlisted SQL expressions are concatenated; all data are parameters.
	actorValid := "(jsonb_typeof(" + b.actor + ")='object' AND jsonb_typeof(" + b.actor + "->'id')='string' AND jsonb_typeof(" + b.actor + "->'authenticated')='boolean' AND (" + b.actor + "->'name' IS NULL OR jsonb_typeof(" + b.actor + "->'name') IN ('null','string')))"
	actorID := "CASE WHEN " + actorValid + " THEN " + b.actor + "->>'id' END"
	actorName := "CASE WHEN " + actorValid + " THEN " + b.actor + "->>'name' END"
	actorAuth := "CASE WHEN " + actorValid + " THEN (" + b.actor + "->>'authenticated')::boolean END"
	v := sqlBuilder.Select(b.at+" AS occurred_at", fmt.Sprintf("%d::smallint AS source_rank", b.rank), b.target+" COLLATE \"C\" AS target_key", b.revision+" AS revision").Column("?::text AS source", b.source).Columns("w.id AS well_id", "w.well_number", "w.active", "w.archived_at", b.verdict+" AS verdict", b.status+" AS review_status", b.conclusion+" AS conclusion", actorID+" AS actor_id", actorName+" AS actor_name", actorAuth+" AS actor_authenticated", b.change+" AS change_kind", b.payload+" AS payload").From(b.from)
	for _, join := range b.joins {
		v = v.Join(join)
	}
	v = v.Where(b.where).Where(eventWellScope(q.Scope))
	if locator != nil {
		v = v.Where(sq.Expr(b.target+" = ?", locator.Target)).Where(sq.Expr(b.revision+" = ?", locator.Revision))
		return v
	}
	f := q.Filter
	if f.WellID != nil {
		v = v.Where(sq.Eq{"w.id": *f.WellID})
	}
	if f.From != nil {
		v = v.Where(sq.Expr(b.at+" >= ?", *f.From))
	}
	if f.To != nil {
		v = v.Where(sq.Expr(b.at+" < ?", *f.To))
	}
	if f.Archive == "current" {
		v = v.Where("w.archived_at IS NULL")
	}
	if f.Archive == "archived" {
		v = v.Where("w.archived_at IS NOT NULL")
	}
	if b.source == "well" {
		kinds := []string{}
		for _, pair := range []struct{ typ, kind string }{{"well.metadata_changed.v1", "metadata"}, {"well.archived.v1", "archive"}, {"well.restored.v1", "restore"}} {
			if slices.Contains(f.Types, pair.typ) {
				kinds = append(kinds, pair.kind)
			}
		}
		v = v.Where(sq.Eq{"we.kind": kinds})
	}
	if q.Before != nil {
		k := q.Before
		v = v.Where(sq.Expr("("+b.at+", "+fmt.Sprint(b.rank)+"::smallint, "+b.target+" COLLATE \"C\", "+b.revision+") < (?::timestamptz, ?::smallint, ?::text COLLATE \"C\", ?::bigint)", k.OccurredAt, k.SourceRank, k.TargetKey, k.Revision))
	}
	return v
}
func readEventRows(ctx context.Context, tx pgx.Tx, query sq.Sqlizer) ([]domain.EventRecord, error) {
	sql, args, e := build(query)
	if e != nil {
		return nil, e
	}
	rows, e := tx.Query(ctx, sql, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.EventRecord{}
	for rows.Next() {
		var r domain.EventRecord
		var actor domain.EventActorSnapshot
		var kind *string
		var payload []byte
		if e = rows.Scan(&r.Key.OccurredAt, &r.Key.SourceRank, &r.Key.TargetKey, &r.Key.Revision, &r.Locator.Source, &r.Well.ID, &r.Well.WellNumber, &r.Well.Active, &r.Well.ArchivedAt, &r.Verdict, &r.ReviewStatus, &r.Conclusion, &actor.ID, &actor.Name, &actor.Authenticated, &kind, &payload); e != nil {
			return nil, e
		}
		r.Locator.Target = r.Key.TargetKey
		r.Locator.Revision = r.Key.Revision
		r.ActorSnapshot = &actor
		if kind != nil {
			r.ChangeKind = *kind
		}
		if len(payload) > 0 {
			switch r.Locator.Source {
			case "mc":
				r.Check = &domain.ManualCheck{}
				e = json.Unmarshal(payload, r.Check)
			case "review.check", "review.alert":
				r.Review = &domain.ReviewEvent{}
				e = json.Unmarshal(payload, r.Review)
			case "well":
				r.WellRevision = &domain.WellEvent{}
				e = json.Unmarshal(payload, r.WellRevision)
			}
			if e != nil {
				return nil, fmt.Errorf("decode saved event: %w", e)
			}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (r *Repository) ReadEventPage(ctx context.Context, q domain.EventReadQuery) ([]domain.EventRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := r.eventReadTx(ctx, q.Scope)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if e = eventDirectWell(ctx, tx, q.Scope, q.Filter.WellID); e != nil {
		return nil, e
	}
	branches := []sq.Sqlizer{}
	types := map[string]string{"mc": "manual_check.saved.v1", "review.check": "review.check.recorded.v1", "review.alert": "review.alert.recorded.v1"}
	for _, b := range eventSourceBranches(false) {
		if b.source != "well" && !slices.Contains(q.Filter.Types, types[b.source]) {
			continue
		}
		branches = append(branches, eventBranchQuery(b, q, nil))
	}
	parts := ""
	args := []any{}
	for i, b := range branches {
		if i > 0 {
			parts += " UNION ALL "
		}
		parts += "(?)"
		args = append(args, b)
	}
	union := sq.Expr(parts, args...)
	query := sq.Expr("SELECT * FROM (?) AS events ORDER BY occurred_at DESC, source_rank DESC, target_key COLLATE \"C\" DESC, revision DESC LIMIT ?", union, q.Filter.Limit+1)
	records, e := readEventRows(ctx, tx, query)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return records, nil
}
func (r *Repository) ReadEvent(ctx context.Context, scope domain.EventReadScope, l domain.EventLocator) (domain.EventRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := r.eventReadTx(ctx, scope)
	if e != nil {
		return domain.EventRecord{}, e
	}
	defer tx.Rollback(ctx)
	for _, b := range eventSourceBranches(true) {
		if b.source != l.Source {
			continue
		}
		records, e := readEventRows(ctx, tx, eventBranchQuery(b, domain.EventReadQuery{Scope: scope}, &l).Limit(1))
		if e != nil {
			return domain.EventRecord{}, e
		}
		if len(records) != 1 {
			return domain.EventRecord{}, domain.NewError(domain.Missing, "Событие недоступно.")
		}
		if e = tx.Commit(ctx); e != nil {
			return domain.EventRecord{}, e
		}
		return records[0], nil
	}
	return domain.EventRecord{}, domain.NewError(domain.Missing, "Событие недоступно.")
}
