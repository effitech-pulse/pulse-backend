package postgres

import (
	"context"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type outboxRecord struct {
	EventID          string     `json:"event_id"`
	JobID            string     `json:"job_id"`
	Generation       int        `json:"generation"`
	Status           string     `json:"status"`
	PublishAttempts  int        `json:"publish_attempts"`
	ClaimTokenSHA256 *string    `json:"claim_token_sha256"`
	ClaimUntil       *time.Time `json:"claim_until"`
}

func outboxQuery() sq.SelectBuilder {
	return sqlBuilder.Select("event_id", "job_id", "generation", "status", "publish_attempts", "claim_token_sha256", "claim_until").From("file_job_outbox")
}
func (r *Repository) ClaimFileJobPublish(ctx context.Context) (*domain.FileJobPublishLease, error) {
	if r.fileJobPolicy == nil {
		return nil, domain.NewFileJobProblem(domain.Forbidden, "file_jobs_disabled")
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	row, e := one[outboxRecord](ctx, tx, outboxQuery().Where(sq.Or{sq.And{sq.Eq{"status": "pending"}, sq.Expr("next_publish_at<=clock_timestamp()")}, sq.And{sq.Eq{"status": "leased"}, sq.Expr("claim_until<=clock_timestamp()")}}).OrderBy("next_publish_at", "event_id").Limit(1).Suffix("FOR UPDATE SKIP LOCKED"))
	if isMissing(e) {
		return nil, tx.Commit(ctx)
	}
	if e != nil {
		return nil, e
	}
	// Publisher claims only an outbox lock; exhaustion is reconciled after commit,
	// so it never holds outbox while acquiring account/well/job locks.
	if row.PublishAttempts >= 8 {
		if _, e = execute(ctx, tx, sqlBuilder.Update("file_job_outbox").SetMap(sq.Eq{"status": "exhausted", "claim_token_sha256": nil, "claim_until": nil, "last_error_code": "publish_exhausted"}).Where(sq.Eq{"event_id": row.EventID})); e != nil {
			return nil, e
		}
		if e = tx.Commit(ctx); e != nil {
			return nil, e
		}
		return nil, r.quarantinePublish(ctx, row)
	}
	token, e := fileJobID()
	if e != nil {
		return nil, e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return nil, e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Update("file_job_outbox").SetMap(sq.Eq{"status": "leased", "publish_attempts": row.PublishAttempts + 1, "claim_token_sha256": domain.FileSHA256([]byte(token)), "claim_until": now.Add(15 * time.Second)}).Where(sq.Eq{"event_id": row.EventID})); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return &domain.FileJobPublishLease{Message: domain.FileJobMessage{Version: 1, EventID: row.EventID, JobID: row.JobID, Generation: row.Generation}, Token: token}, nil
}
func (r *Repository) quarantinePublish(ctx context.Context, row outboxRecord) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	j, _, allowed, e := r.lockRuntimeJob(ctx, tx, row.JobID)
	if e != nil {
		return e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return e
	}
	if j.Generation == row.Generation && (j.State == domain.FileJobPendingDispatch || j.State == domain.FileJobQueued) {
		if state, code := deniedState(j, allowed, now); state != "" {
			e = terminalJob(ctx, tx, j, state, code, now)
		} else {
			e = quarantineJob(ctx, tx, j, "publish_exhausted", now)
		}
		if e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}
func (r *Repository) FinishFileJobPublish(ctx context.Context, l domain.FileJobPublishLease, outcome domain.FileJobPublishOutcome) error {
	if outcome != domain.FileJobPublishConfirmed && outcome != domain.FileJobPublishReturned && outcome != domain.FileJobPublishNack && outcome != domain.FileJobPublishUnknown {
		return domain.NewFileJobProblem(domain.Invalid, "invalid_publish_outcome")
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	j, e := one[jobRecord](ctx, tx, jobQuery().Where(sq.Eq{"id": l.Message.JobID}).Suffix("FOR UPDATE"))
	if e != nil {
		return e
	}
	row, e := one[outboxRecord](ctx, tx, outboxQuery().Where(sq.Eq{"event_id": l.Message.EventID}).Suffix("FOR UPDATE"))
	if e != nil {
		return e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return e
	}
	if row.JobID != j.ID || row.Generation != l.Message.Generation || row.Status != "leased" || row.ClaimTokenSHA256 == nil || *row.ClaimTokenSHA256 != domain.FileSHA256([]byte(l.Token)) || row.ClaimUntil == nil || !row.ClaimUntil.After(now) {
		return tx.Commit(ctx)
	}
	values := sq.Eq{"claim_token_sha256": nil, "claim_until": nil}
	if outcome == domain.FileJobPublishConfirmed {
		values["status"] = "confirmed"
		values["confirmed_at"] = now
		values["last_error_code"] = nil
		if j.Generation == row.Generation && j.State == domain.FileJobPendingDispatch {
			if e = updateJob(ctx, tx, j, sq.Eq{"state": "queued"}, now); e != nil {
				return e
			}
		}
	} else {
		values["status"] = "pending"
		values["last_error_code"] = "publish_" + string(outcome)
		backoffs := []int{1, 2, 4, 8, 16, 30, 30, 30}
		values["next_publish_at"] = now.Add(time.Duration(backoffs[row.PublishAttempts-1]) * time.Second)
		if row.PublishAttempts >= 8 {
			values["status"] = "exhausted"
		}
	}
	if _, e = execute(ctx, tx, sqlBuilder.Update("file_job_outbox").SetMap(values).Where(sq.Eq{"event_id": row.EventID})); e != nil {
		return e
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	if outcome != domain.FileJobPublishConfirmed && row.PublishAttempts >= 8 {
		return r.quarantinePublish(ctx, row)
	}
	return nil
}
