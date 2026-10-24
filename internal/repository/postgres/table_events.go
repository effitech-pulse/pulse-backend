package postgres

import (
	"context"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func eventTableDefinition() tableDataset {
	kind := `CASE WHEN actor_authenticated AND actor_id ~ '^user:[1-9][0-9]{0,18}$' AND (length(substring(actor_id from 6))<19 OR substring(actor_id from 6) COLLATE "C"<='9223372036854775807') THEN 'person' WHEN NOT actor_authenticated AND actor_id IN ('shared','service') THEN actor_id ELSE 'unknown' END`
	actor := `CASE WHEN (` + kind + `) <> 'unknown' THEN NULLIF(actor_name,'') END`
	typ := `CASE source WHEN 'mc' THEN 'manual_check.saved.v1' WHEN 'review.check' THEN 'review.check.recorded.v1' WHEN 'review.alert' THEN 'review.alert.recorded.v1' ELSE CASE change_kind WHEN 'metadata' THEN 'well.metadata_changed.v1' WHEN 'archive' THEN 'well.archived.v1' WHEN 'restore' THEN 'well.restored.v1' END END`
	enum := func(expr string, values string) string {
		return "CASE WHEN " + expr + " IS NULL THEN NULL WHEN " + expr + " IN (" + values + ") THEN " + expr + " ELSE 'unknown' END"
	}
	return tableDataset{columns: map[string]tableColumn{
		"event_type": {typ, "text", ""}, "well_id": {"well_id::text", "text", ""}, "well_number": {"well_number", "text", ""}, "archive": {"CASE WHEN archived_at IS NULL THEN 'current' ELSE 'archived' END", "text", ""}, "occurred_at": {"occurred_at", "time", ""}, "actor_name": {actor, "text", ""}, "actor_kind": {kind, "text", ""}, "verdict": {enum("verdict", "'alert','watch','norm','insufficient'"), "text", ""}, "review_status": {enum("review_status", "'new','in_progress','closed'"), "text", ""}, "conclusion": {enum("NULLIF(conclusion,'')", "'confirmed','not_confirmed','inconclusive'"), "text", ""},
	}, tail: []string{"source_rank", "target_key COLLATE \"C\"", "revision"}, defaults: []domain.TableSort{{Field: "occurred_at", Direction: "desc"}}}
}

type tableEventRow struct {
	OccurredAt         time.Time  `json:"occurred_at"`
	SourceRank         int16      `json:"source_rank"`
	TargetKey          string     `json:"target_key"`
	Revision           int64      `json:"revision"`
	Source             string     `json:"source"`
	WellID             int64      `json:"well_id"`
	WellNumber         *string    `json:"well_number"`
	Active             bool       `json:"active"`
	ArchivedAt         *time.Time `json:"archived_at"`
	Verdict            *string    `json:"verdict"`
	ReviewStatus       *string    `json:"review_status"`
	Conclusion         *string    `json:"conclusion"`
	ActorID            *string    `json:"actor_id"`
	ActorName          *string    `json:"actor_name"`
	ActorAuthenticated *bool      `json:"actor_authenticated"`
	ChangeKind         *string    `json:"change_kind"`
}

func (r *Repository) EventsTable(ctx context.Context, scope domain.EventReadScope, req domain.TableRequest) (domain.TableRows[domain.EventRecord], error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out := domain.TableRows[domain.EventRecord]{Items: []domain.EventRecord{}}
	tx, e := r.eventReadTx(ctx, scope)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	f := domain.EventFilter{Types: []string{"manual_check.saved.v1", "review.check.recorded.v1", "review.alert.recorded.v1", "well.metadata_changed.v1", "well.archived.v1", "well.restored.v1"}, Archive: "all"}
	args := []any{}
	parts := ""
	for _, branch := range eventSourceBranches(false) {
		if !eventSourceAllowed(scope, branch.source) {
			continue
		}
		if parts != "" {
			parts += " UNION ALL "
		}
		parts += "(?)"
		args = append(args, eventBranchQuery(branch, domain.EventReadQuery{Scope: scope, Filter: f}, nil))
	}
	if len(args) == 0 {
		return out, tx.Commit(ctx)
	}
	q := sqlBuilder.Select("*").Prefix("WITH table_events AS (?)", sq.Expr(parts, args...)).From("table_events")
	rows, e := tableRows[tableEventRow](ctx, tx, q, "events", req)
	if e != nil {
		return out, e
	}
	out.Keys = rows.Keys
	out.HasMore = rows.HasMore
	for _, v := range rows.Items {
		record := domain.EventRecord{Key: domain.EventKey{OccurredAt: v.OccurredAt, SourceRank: v.SourceRank, TargetKey: v.TargetKey, Revision: v.Revision}, Locator: domain.EventLocator{Source: v.Source, Target: v.TargetKey, Revision: v.Revision}, Well: domain.Well{ID: v.WellID, WellNumber: v.WellNumber, Active: v.Active, ArchivedAt: v.ArchivedAt}, ActorSnapshot: &domain.EventActorSnapshot{ID: v.ActorID, Name: v.ActorName, Authenticated: v.ActorAuthenticated}, Verdict: v.Verdict, ReviewStatus: v.ReviewStatus, Conclusion: v.Conclusion}
		if v.ChangeKind != nil {
			record.ChangeKind = *v.ChangeKind
		}
		out.Items = append(out.Items, record)
	}
	return out, tx.Commit(ctx)
}
