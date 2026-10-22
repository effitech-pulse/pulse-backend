package postgres

import (
	"context"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type cohortOutboxRow struct {
	EventID    string     `json:"event_id"`
	JobID      string     `json:"job_id"`
	Generation int        `json:"generation"`
	Status     string     `json:"status"`
	Attempts   int        `json:"publish_attempts"`
	Token      *string    `json:"claim_token_sha256"`
	Until      *time.Time `json:"claim_until"`
}

func cohortOutboxQuery() sq.SelectBuilder { return sqlBuilder.Select("*").From("cohort_job_outbox") }
func cohortOutbox(ctx context.Context, tx pgx.Tx, id string, generation int, due time.Time) error {
	event, e := fileJobID()
	if e != nil {
		return e
	}
	_, e = execute(ctx, tx, sqlBuilder.Insert("cohort_job_outbox").Columns("event_id", "job_id", "generation", "status", "next_publish_at", "created_at").Values(event, id, generation, "pending", due, due))
	return e
}
func (r *Repository) ClaimCohortOutbox(ctx context.Context, worker string) (*domain.CohortPublishLease, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if r.cohortJobPolicy == nil || !fileJobOpaque(worker) {
		return nil, cohortError(domain.Invalid, "invalid_cohort_publisher")
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if e = cohortGate(ctx, tx); e != nil {
		return nil, e
	}
	o, e := one[cohortOutboxRow](ctx, tx, cohortOutboxQuery().Where(sq.Or{sq.And{sq.Eq{"status": "pending"}, sq.Expr("next_publish_at<=clock_timestamp()")}, sq.And{sq.Eq{"status": "leased"}, sq.Expr("claim_until<=clock_timestamp()")}}).OrderBy("next_publish_at", "event_id").Limit(1).Suffix("FOR UPDATE SKIP LOCKED"))
	if isMissing(e) {
		return nil, tx.Commit(ctx)
	}
	if e != nil {
		return nil, e
	}
	if o.Attempts >= 6 {
		if _, e = execute(ctx, tx, sqlBuilder.Update("cohort_job_outbox").SetMap(sq.Eq{"status": "exhausted", "claim_token_sha256": nil, "claim_until": nil}).Where(sq.Eq{"event_id": o.EventID})); e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	token, e := fileJobID()
	if e != nil {
		return nil, e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return nil, e
	}
	until := now.Add(15 * time.Second)
	if _, e = execute(ctx, tx, sqlBuilder.Update("cohort_job_outbox").SetMap(sq.Eq{"status": "leased", "publish_attempts": o.Attempts + 1, "claim_token_sha256": domain.FileSHA256([]byte(token)), "claim_until": until}).Where(sq.Eq{"event_id": o.EventID})); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return &domain.CohortPublishLease{Message: domain.CohortJobMessage{Version: 1, Kind: "cohort.ready.v1", EventID: o.EventID, JobID: o.JobID, Generation: o.Generation}, Token: token, LeaseUntil: until, Attempt: o.Attempts + 1}, nil
}
func (r *Repository) CompleteCohortPublish(ctx context.Context, l domain.CohortPublishLease, outcome domain.CohortPublishOutcome) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	switch outcome {
	case "confirmed", "returned", "nack", "unknown":
	default:
		return cohortError(domain.Invalid, "invalid_publish_outcome")
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = cohortGate(ctx, tx); e != nil {
		return e
	}
	j, _, allowed, e := r.lockCohort(ctx, tx, l.Message.JobID)
	if e != nil {
		return e
	}
	o, e := one[cohortOutboxRow](ctx, tx, cohortOutboxQuery().Where(sq.Eq{"event_id": l.Message.EventID}).Suffix("FOR UPDATE"))
	if e != nil {
		return e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return e
	}
	if o.JobID != j.ID || o.Generation != l.Message.Generation || o.Status != "leased" || o.Token == nil || *o.Token != domain.FileSHA256([]byte(l.Token)) || o.Until == nil || !o.Until.After(now) {
		return tx.Commit(ctx)
	}
	if j.State.Terminal() || j.Generation != o.Generation {
		return tx.Commit(ctx)
	}
	if state, code := cohortDenied(j, allowed, now); state != "" {
		if e = cohortTerminal(ctx, tx, j, state, code, now); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	values := sq.Eq{"claim_token_sha256": nil, "claim_until": nil}
	if outcome == "confirmed" {
		values["status"] = "confirmed"
		values["confirmed_at"] = now
		values["last_error_code"] = nil
		if j.State == domain.CohortJobPendingDispatch {
			if e = cohortUpdate(ctx, tx, j, sq.Eq{"state": "queued"}, now); e != nil {
				return e
			}
		}
	} else {
		values["status"] = "pending"
		values["last_error_code"] = "publish_" + string(outcome)
		backoff := []int{1, 2, 5, 10, 30, 30}
		if o.Attempts < 1 || o.Attempts > 6 {
			return cohortError(domain.Invalid, "invalid_publish_attempt")
		}
		values["next_publish_at"] = now.Add(time.Duration(backoff[o.Attempts-1]) * time.Second)
		if o.Attempts >= 6 {
			values["status"] = "exhausted"
		}
	}
	if _, e = execute(ctx, tx, sqlBuilder.Update("cohort_job_outbox").SetMap(values).Where(sq.Eq{"event_id": o.EventID})); e != nil {
		return e
	}
	if outcome != "confirmed" && o.Attempts >= 6 && (j.State == domain.CohortJobPendingDispatch || j.State == domain.CohortJobQueued) {
		if e = cohortQuarantine(ctx, tx, j, "publish_exhausted", now); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
