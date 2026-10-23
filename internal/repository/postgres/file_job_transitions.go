package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type jobRecord struct {
	WellAuthority *domain.FileJobWellAuthority `json:"well_authority"`
	domain.FileJobStatus
	Authorization        *domain.JobAuthorization `json:"actor_authorization"`
	ActorAccountID       int64                    `json:"actor_account_id"`
	InputID              string                   `json:"input_id"`
	CohortVersion        string                   `json:"cohort_version"`
	CohortManifestSHA256 string                   `json:"cohort_manifest_sha256"`
	PilotRunID           string                   `json:"pilot_run_id"`
	Generation           int                      `json:"generation"`
	RedriveCount         int                      `json:"redrive_count"`
	Fence                int64                    `json:"fence"`
	LeaseTokenSHA256     *string                  `json:"lease_token_sha256"`
	LeaseUntil           *time.Time               `json:"lease_until"`
	StartAt              time.Time                `json:"start_at"`
	EndAt                time.Time                `json:"end_at"`
}

func jobQuery() sq.SelectBuilder { return sqlBuilder.Select("*").From("file_job") }
func (r *Repository) matchesJobPolicy(j jobRecord) bool {
	p := r.fileJobPolicy
	return p != nil && j.PilotRunID == p.RunID && j.CohortVersion == p.CohortVersion && j.CohortManifestSHA256 == p.CohortManifestSHA256
}

type runtimeRecord struct {
	JobID      *string    `json:"job_id"`
	Fence      *int64     `json:"fence"`
	LeaseUntil *time.Time `json:"lease_until"`
}

func lockJobRuntime(ctx context.Context, tx pgx.Tx) (runtimeRecord, error) {
	return one[runtimeRecord](ctx, tx, sqlBuilder.Select("job_id", "fence", "lease_until").From("file_job_runtime").Where(sq.Eq{"key": "execution"}).Suffix("FOR UPDATE"))
}
func (r *Repository) lockRuntimeJob(ctx context.Context, tx pgx.Tx, id string, actions ...domain.Permission) (jobRecord, runtimeRecord, bool, error) {
	j, e := one[jobRecord](ctx, tx, jobQuery().Where(sq.Eq{"id": id}))
	if e != nil {
		return j, runtimeRecord{}, false, e
	}
	permission := domain.Permission("filechecks.run")
	saved := j.Authorization
	execution := true
	if len(actions) > 0 {
		permission = actions[0]
		saved = nil
		execution = false
	}
	ok, e := r.authorizeFileJobTx(ctx, tx, j.ActorAccountID, j.WellID, saved, permission, execution)
	if e != nil {
		return j, runtimeRecord{}, false, e
	}
	if execution {
		if j.WellAuthority == nil {
			ok = ok && !domain.RBACEnforced(ctx)
		} else {
			w, err := one[domain.Well](ctx, tx, sqlBuilder.Select(wellColumns).From("well").Where(sq.Eq{"id": j.WellID}).Suffix("FOR SHARE"))
			if isMissing(err) {
				ok = false
			} else if err != nil {
				return j, runtimeRecord{}, false, err
			} else {
				ok = ok && j.WellAuthority.Matches(w)
			}
		}
	}
	slot, e := lockJobRuntime(ctx, tx)
	if e != nil {
		return j, slot, false, e
	}
	j, e = one[jobRecord](ctx, tx, jobQuery().Where(sq.Eq{"id": id}).Suffix("FOR UPDATE"))
	return j, slot, ok && r.matchesJobPolicy(j), e
}
func slotMatches(s runtimeRecord, j jobRecord) bool {
	return s.JobID != nil && *s.JobID == j.ID && s.Fence != nil && *s.Fence == j.Fence
}
func leaseMatches(j jobRecord, l domain.FileJobLease, now time.Time) bool {
	return j.ID == l.JobID && j.Generation == l.Generation && j.Fence == l.Fence && j.LeaseTokenSHA256 != nil && *j.LeaseTokenSHA256 == domain.FileSHA256([]byte(l.Token)) && j.LeaseUntil != nil && j.LeaseUntil.After(now) && (j.State == domain.FileJobRunning || j.State == domain.FileJobCancelRequested)
}
func staleLease() error { return domain.NewFileJobProblem(domain.Duplicate, "stale_lease") }
func updateJob(ctx context.Context, tx pgx.Tx, j jobRecord, values sq.Eq, now time.Time) error {
	values["revision"] = sq.Expr("revision+1")
	values["updated_at"] = now
	_, e := execute(ctx, tx, sqlBuilder.Update("file_job").SetMap(values).Where(sq.Eq{"id": j.ID}))
	return e
}
func clearRuntime(ctx context.Context, tx pgx.Tx, j jobRecord) error {
	_, e := execute(ctx, tx, sqlBuilder.Update("file_job_runtime").SetMap(sq.Eq{"job_id": nil, "fence": nil, "lease_until": nil}).Where(sq.Eq{"key": "execution", "job_id": j.ID, "fence": j.Fence}))
	return e
}
func finishAttempt(ctx context.Context, tx pgx.Tx, j jobRecord, outcome, code string, now time.Time) error {
	var safe any
	if code != "" {
		safe = code
	}
	if _, e := execute(ctx, tx, sqlBuilder.Update("file_job_attempt").SetMap(sq.Eq{"ended_at": now, "outcome": outcome, "error_code": safe}).Where(sq.Eq{"job_id": j.ID, "fence": j.Fence, "ended_at": nil})); e != nil {
		return e
	}
	_, e := execute(ctx, tx, sqlBuilder.Update("file_job_inbox").SetMap(sq.Eq{"state": "completed", "outcome": outcome, "updated_at": now}).Where(sq.Eq{"job_id": j.ID, "fence": j.Fence, "state": "processing"}))
	return e
}
func supersedeOutbox(ctx context.Context, tx pgx.Tx, j jobRecord) error {
	_, e := execute(ctx, tx, sqlBuilder.Update("file_job_outbox").SetMap(sq.Eq{"status": "superseded", "claim_token_sha256": nil, "claim_until": nil}).Where(sq.Eq{"job_id": j.ID}).Where(sq.NotEq{"status": "superseded"}))
	return e
}
func terminalJob(ctx context.Context, tx pgx.Tx, j jobRecord, state domain.FileJobState, code string, now time.Time) error {
	var safe any
	if code != "" {
		safe = code
	}
	values := sq.Eq{"state": state, "finished_at": now, "lease_token_sha256": nil, "lease_until": nil, "next_attempt_at": nil, "error_code": safe, "fence": sq.Expr("fence+1")}
	if state == domain.FileJobSucceeded {
		values["check_id"] = "q1_" + j.ID
	}
	if e := updateJob(ctx, tx, j, values, now); e != nil {
		return e
	}
	if j.LeaseUntil != nil {
		if e := finishAttempt(ctx, tx, j, string(state), code, now); e != nil {
			return e
		}
	}
	if e := clearRuntime(ctx, tx, j); e != nil {
		return e
	}
	return supersedeOutbox(ctx, tx, j)
}
func deniedState(j jobRecord, allowed bool, now time.Time) (domain.FileJobState, string) {
	if !allowed {
		return domain.FileJobRevoked, "access_revoked"
	}
	if !j.DeadlineAt.After(now) {
		return domain.FileJobExpired, "job_deadline"
	}
	if j.State == domain.FileJobCancelRequested {
		return domain.FileJobCancelled, "cancelled"
	}
	return "", ""
}
func jobReceipt(ctx context.Context, tx pgx.Tx, j jobRecord, event, state string, now time.Time) error {
	_, e := execute(ctx, tx, sqlBuilder.Insert("file_job_inbox").Columns("event_id", "job_id", "generation", "fence", "state", "first_seen_at", "updated_at").Values(event, j.ID, j.Generation, j.Fence, state, now, now).Suffix("ON CONFLICT (event_id) DO NOTHING"))
	return e
}
func jobAction(ctx context.Context, tx pgx.Tx, j jobRecord, kind, code string, actor any, now time.Time) error {
	_, e := execute(ctx, tx, sqlBuilder.Insert("file_job_action").Columns("job_id", "revision", "kind", "reason_code", "actor_account_id", "created_at").Values(j.ID, j.Revision+1, kind, code, actor, now))
	return e
}
func quarantineJob(ctx context.Context, tx pgx.Tx, j jobRecord, code string, now time.Time) error {
	if j.LeaseUntil != nil {
		if e := finishAttempt(ctx, tx, j, "failed", code, now); e != nil {
			return e
		}
	}
	if e := updateJob(ctx, tx, j, sq.Eq{"state": "quarantined", "error_code": code, "lease_token_sha256": nil, "lease_until": nil, "next_attempt_at": nil, "fence": sq.Expr("fence+1")}, now); e != nil {
		return e
	}
	if e := jobAction(ctx, tx, j, "quarantine", code, nil, now); e != nil {
		return e
	}
	if e := clearRuntime(ctx, tx, j); e != nil {
		return e
	}
	return supersedeOutbox(ctx, tx, j)
}
func nextGeneration(ctx context.Context, tx pgx.Tx, j jobRecord, due, now time.Time) error {
	if j.Generation >= 6 {
		return quarantineJob(ctx, tx, j, "dispatch_exhausted", now)
	}
	if e := supersedeOutbox(ctx, tx, j); e != nil {
		return e
	}
	if e := updateJob(ctx, tx, j, sq.Eq{"generation": j.Generation + 1, "state": "pending_dispatch", "next_attempt_at": nil, "error_code": nil}, now); e != nil {
		return e
	}
	return jobOutbox(ctx, tx, j.ID, j.Generation+1, due)
}
func (r *Repository) ClaimFileJob(ctx context.Context, m domain.FileJobMessage, worker string) (*domain.FileJobLease, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !fileJobOpaque(worker) {
		return nil, domain.NewFileJobProblem(domain.Invalid, "invalid_worker_identity")
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if e = fileJobCapacityGate(ctx, tx); e != nil {
		return nil, e
	}
	event, e := one[outboxRecord](ctx, tx, outboxQuery().Where(sq.Eq{"event_id": m.EventID}))
	if isMissing(e) {
		return nil, tx.Commit(ctx)
	}
	if e != nil {
		return nil, e
	}
	j, slot, allowed, e := r.lockRuntimeJob(ctx, tx, event.JobID)
	if e != nil {
		return nil, e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return nil, e
	}
	if m.Version != 1 || m.JobID != event.JobID || m.Generation != event.Generation {
		if !j.State.Terminal() && j.State != domain.FileJobQuarantined {
			e = quarantineJob(ctx, tx, j, "invalid_envelope", now)
		}
		if e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	if j.State.Terminal() || j.Generation != m.Generation || j.State == domain.FileJobQuarantined || j.State == domain.FileJobRetryWait {
		j.Generation = event.Generation
		if e = jobReceipt(ctx, tx, j, m.EventID, "superseded", now); e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	if j.State == domain.FileJobRunning || j.State == domain.FileJobCancelRequested {
		return nil, tx.Commit(ctx)
	}
	if state, code := deniedState(j, allowed, now); state != "" {
		if e = jobReceipt(ctx, tx, j, m.EventID, "processing", now); e != nil {
			return nil, e
		}
		if e = terminalJob(ctx, tx, j, state, code, now); e != nil {
			return nil, e
		}
		if _, e = execute(ctx, tx, sqlBuilder.Update("file_job_inbox").SetMap(sq.Eq{"state": "completed", "outcome": state, "updated_at": now}).Where(sq.Eq{"event_id": m.EventID})); e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	if busy, e := activeCohort(ctx, tx); e != nil {
		return nil, e
	} else if busy {
		return nil, domain.NewFileJobProblem(domain.Limited, "file_job_capacity")
	}
	// A stale occupied slot must be reconciled before granting another physical lease.
	if slot.JobID != nil {
		if e = nextGeneration(ctx, tx, j, now.Add(10*time.Second), now); e != nil {
			return nil, e
		}
		if e = jobReceipt(ctx, tx, j, m.EventID, "superseded", now); e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	if j.AttemptCount >= 3 || j.DeadlineAt.Sub(now) <= 10*time.Second {
		if e = terminalJob(ctx, tx, j, domain.FileJobFailed, "attempts_exhausted", now); e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	token, e := fileJobID()
	if e != nil {
		return nil, e
	}
	lease := domain.FileJobLease{JobID: j.ID, Generation: j.Generation, Fence: j.Fence + 1, Token: token}
	until := now.Add(45 * time.Second)
	values := sq.Eq{"state": "running", "attempt_count": j.AttemptCount + 1, "fence": lease.Fence, "lease_token_sha256": domain.FileSHA256([]byte(token)), "lease_until": until, "heartbeat_at": now, "started_at": sq.Expr("COALESCE(started_at,?)", now), "error_code": nil}
	if e = updateJob(ctx, tx, j, values, now); e != nil {
		return nil, e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Update("file_job_runtime").SetMap(sq.Eq{"job_id": j.ID, "fence": lease.Fence, "lease_until": until}).Where(sq.Eq{"key": "execution"})); e != nil {
		return nil, e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Insert("file_job_attempt").Columns("job_id", "attempt", "generation", "fence", "worker_instance", "started_at").Values(j.ID, j.AttemptCount+1, j.Generation, lease.Fence, worker, now)); e != nil {
		return nil, e
	}
	j.Fence = lease.Fence
	if e = jobReceipt(ctx, tx, j, m.EventID, "processing", now); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return &lease, nil
}
func fileJobOpaque(v string) bool { return len(v) == 32 && strings.Trim(v, "0123456789abcdef") == "" }
func (r *Repository) ExecutionFileJob(ctx context.Context, l domain.FileJobLease) (domain.FileJobExecution, error) {
	var zero domain.FileJobExecution
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	j, slot, allowed, e := r.lockRuntimeJob(ctx, tx, l.JobID)
	if e != nil {
		return zero, e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return zero, e
	}
	if !slotMatches(slot, j) || !leaseMatches(j, l, now) {
		return zero, staleLease()
	}
	if state, code := deniedState(j, allowed, now); state != "" {
		if e = terminalJob(ctx, tx, j, state, code, now); e != nil {
			return zero, e
		}
		if e = tx.Commit(ctx); e != nil {
			return zero, e
		}
		return zero, domain.NewFileJobProblem(domain.Duplicate, code)
	}
	summary, e := one[domain.FileInputSummary](ctx, tx, fileInputQuery().Where(sq.Eq{"id": j.InputID}))
	if e != nil {
		return zero, e
	}
	v := domain.PreparedFileInput{FileInputSummary: summary, Limits: summary.ParseRecipe.Limits}
	sql, args, e := build(sqlBuilder.Select("raw_bytes", "canonical_bytes").From("file_input_payload").Where(sq.Eq{"input_id": j.InputID}))
	if e != nil {
		return zero, e
	}
	if e = tx.QueryRow(ctx, sql, args...).Scan(&v.Raw, &v.Canonical); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return zero, domain.NewFileJobProblem(domain.Unavailable, "input_unavailable")
		}
		return zero, e
	}
	if e = domain.ValidateFileInput(v); e != nil {
		return zero, domain.NewFileJobProblem(domain.Unavailable, "input_corrupt")
	}
	if e = tx.Commit(ctx); e != nil {
		return zero, e
	}
	return domain.FileJobExecution{Lease: l, Input: v}, nil
}
func (r *Repository) RenewFileJob(ctx context.Context, l domain.FileJobLease) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	j, slot, allowed, e := r.lockRuntimeJob(ctx, tx, l.JobID)
	if e != nil {
		return e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return e
	}
	if !slotMatches(slot, j) || !leaseMatches(j, l, now) {
		return staleLease()
	}
	if state, code := deniedState(j, allowed, now); state != "" {
		if e = terminalJob(ctx, tx, j, state, code, now); e != nil {
			return e
		}
		if e = tx.Commit(ctx); e != nil {
			return e
		}
		return domain.NewFileJobProblem(domain.Duplicate, code)
	}
	until := now.Add(45 * time.Second)
	if _, e = execute(ctx, tx, sqlBuilder.Update("file_job").SetMap(sq.Eq{"lease_until": until, "heartbeat_at": now}).Where(sq.Eq{"id": j.ID})); e != nil {
		return e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Update("file_job_runtime").Set("lease_until", until).Where(sq.Eq{"key": "execution", "job_id": j.ID, "fence": j.Fence})); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func retryableJobFailure(f *domain.FileJobFailure) bool {
	return f != nil && f.Retryable && (f.Code == "ml_unavailable" || f.Code == "ml_deadline" || f.Code == "execution_busy")
}
func jobFailureCode(f *domain.FileJobFailure) string {
	if f == nil {
		return "execution_failed"
	}
	switch f.Code {
	case "ml_unavailable", "ml_deadline", "execution_busy", "input_corrupt", "input_unavailable", "pinned_input_unavailable", "bundle_unavailable", "result_identity_mismatch", "parser_identity_unavailable", "rpc_incompatible", "execution_failed":
		return f.Code
	}
	return "execution_failed"
}
func retryJob(ctx context.Context, tx pgx.Tx, j jobRecord, code, outcome string, now time.Time) error {
	delay := 10 * time.Second
	if j.AttemptCount >= 2 {
		delay = 30 * time.Second
	}
	if j.AttemptCount >= 3 || !j.DeadlineAt.After(now.Add(delay+10*time.Second)) {
		return terminalJob(ctx, tx, j, domain.FileJobFailed, "attempts_exhausted", now)
	}
	if e := updateJob(ctx, tx, j, sq.Eq{"state": "retry_wait", "next_attempt_at": now.Add(delay), "lease_token_sha256": nil, "lease_until": nil, "fence": sq.Expr("fence+1"), "error_code": code}, now); e != nil {
		return e
	}
	if e := finishAttempt(ctx, tx, j, outcome, code, now); e != nil {
		return e
	}
	if e := clearRuntime(ctx, tx, j); e != nil {
		return e
	}
	return supersedeOutbox(ctx, tx, j)
}
func (r *Repository) FinishFileJob(ctx context.Context, l domain.FileJobLease, result *domain.StoredFileResult, failure *domain.FileJobFailure) (domain.FileJobDisposition, error) {
	var zero domain.FileJobDisposition
	if (result == nil) == (failure == nil) {
		return zero, domain.NewFileJobProblem(domain.Invalid, "invalid_file_job_outcome")
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	j, slot, allowed, e := r.lockRuntimeJob(ctx, tx, l.JobID)
	if e != nil {
		return zero, e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return zero, e
	}
	if !slotMatches(slot, j) || !leaseMatches(j, l, now) {
		if j.State.Terminal() {
			status, e := jobStatus(ctx, tx, j.ID)
			if e != nil {
				return zero, e
			}
			return domain.FileJobDisposition{Kind: "stale", Status: status}, tx.Commit(ctx)
		}
		return zero, staleLease()
	}
	if state, code := deniedState(j, allowed, now); state != "" {
		e = terminalJob(ctx, tx, j, state, code, now)
	} else if result != nil {
		if result.InputID != j.InputID {
			return zero, domain.NewFileJobProblem(domain.Duplicate, "result_identity_conflict")
		}
		if _, e = insertFileResultTx(ctx, tx, *result); e != nil {
			return zero, e
		}
		now, e = dbJobNow(ctx, tx)
		if e != nil {
			return zero, e
		}
		if !leaseMatches(j, l, now) || !j.DeadlineAt.After(now) {
			return zero, staleLease()
		}
		e = terminalJob(ctx, tx, j, domain.FileJobSucceeded, "", now)
	} else if retryableJobFailure(failure) {
		e = retryJob(ctx, tx, j, jobFailureCode(failure), "retry", now)
	} else {
		e = terminalJob(ctx, tx, j, domain.FileJobFailed, jobFailureCode(failure), now)
	}
	if e != nil {
		return zero, e
	}
	status, e := jobStatus(ctx, tx, j.ID)
	if e != nil {
		return zero, e
	}
	if e = tx.Commit(ctx); e != nil {
		return zero, e
	}
	return domain.FileJobDisposition{Kind: "ack", Status: status}, nil
}
func (r *Repository) CancelFileJob(ctx context.Context, well int64, id string, revision int64) (domain.FileJobStatus, error) {
	var zero domain.FileJobStatus
	actor, e := fileJobSession(ctx)
	if e != nil {
		return zero, e
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	initial, e := one[jobRecord](ctx, tx, jobQuery().Where(sq.Eq{"id": id, "well_id": well, "actor_account_id": actor}))
	if e != nil {
		return zero, e
	}
	j, _, allowed, e := r.lockRuntimeJob(ctx, tx, initial.ID, "filechecks.cancel")
	if e != nil {
		return zero, e
	}
	if !allowed {
		return zero, missingFileJob()
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return zero, e
	}
	if j.State == domain.FileJobCancelled || j.State == domain.FileJobCancelRequested {
		out, e := jobStatus(ctx, tx, id)
		if e != nil {
			return zero, e
		}
		return out, tx.Commit(ctx)
	}
	if j.State.Terminal() {
		return zero, domain.NewFileJobProblem(domain.Duplicate, "job_terminal")
	}
	if revision > 0 && revision != j.Revision {
		return zero, domain.NewFileJobProblem(domain.Duplicate, "job_revision_conflict")
	}
	if j.State == domain.FileJobRunning {
		e = updateJob(ctx, tx, j, sq.Eq{"state": "cancel_requested", "cancel_requested_at": now}, now)
	} else {
		e = terminalJob(ctx, tx, j, domain.FileJobCancelled, "cancelled", now)
	}
	if e != nil {
		return zero, e
	}
	if e = jobAction(ctx, tx, j, "cancel", "owner_cancel", actor, now); e != nil {
		return zero, e
	}
	out, e := jobStatus(ctx, tx, id)
	if e != nil {
		return zero, e
	}
	return out, tx.Commit(ctx)
}
func (r *Repository) RedriveFileJob(ctx context.Context, well int64, id string, revision int64, reason string) (domain.FileJobStatus, error) {
	var zero domain.FileJobStatus
	actor, e := fileJobSession(ctx)
	if e != nil {
		return zero, e
	}
	if reason != "operator_retry" && reason != "broker_recovered" {
		return zero, domain.NewFileJobProblem(domain.Invalid, "invalid_redrive_reason")
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	initial, e := one[jobRecord](ctx, tx, jobQuery().Where(sq.Eq{"id": id, "well_id": well, "actor_account_id": actor}))
	if e != nil {
		return zero, e
	}
	j, _, allowed, e := r.lockRuntimeJob(ctx, tx, initial.ID)
	if e != nil {
		return zero, e
	}
	if !allowed {
		return zero, missingFileJob()
	}
	auth, e := jobAuthority(ctx, tx, actor, j.Authorization, "filechecks.run", true)
	if e != nil {
		return zero, e
	}
	if !auth.FullApplicationScope() {
		return zero, missingFileJob()
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return zero, e
	}
	if j.State != domain.FileJobQuarantined || revision != j.Revision || j.RedriveCount != 0 || j.Generation >= 6 || j.AttemptCount >= 3 || !j.DeadlineAt.After(now.Add(10*time.Second)) {
		return zero, domain.NewFileJobProblem(domain.Duplicate, "redrive_unavailable")
	}
	if e = nextGeneration(ctx, tx, j, now, now); e != nil {
		return zero, e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Update("file_job").Set("redrive_count", j.RedriveCount+1).Where(sq.Eq{"id": id})); e != nil {
		return zero, e
	}
	if e = jobAction(ctx, tx, j, "redrive", reason, actor, now); e != nil {
		return zero, e
	}
	out, e := jobStatus(ctx, tx, id)
	if e != nil {
		return zero, e
	}
	return out, tx.Commit(ctx)
}
func (r *Repository) ReconcileFileJobs(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 8 {
		limit = 8
	}
	rows, e := list[jobRecord](ctx, r.pool, jobQuery().Where(sq.NotEq{"state": []string{"succeeded", "failed", "cancelled", "expired", "revoked"}}).OrderBy("updated_at", "id").Limit(uint64(limit)))
	if e != nil {
		return 0, e
	}
	count := 0
	for _, j := range rows {
		changed, e := r.reconcileFileJob(ctx, j.ID)
		if e != nil {
			return count, e
		}
		if changed {
			count++
		}
	}
	return count, nil
}
func (r *Repository) reconcileFileJob(ctx context.Context, id string) (bool, error) {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return false, e
	}
	defer tx.Rollback(ctx)
	j, _, allowed, e := r.lockRuntimeJob(ctx, tx, id)
	if e != nil {
		return false, e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return false, e
	}
	if j.State.Terminal() {
		return false, tx.Commit(ctx)
	}
	changed := true
	publishExhausted := false
	if j.State == domain.FileJobPendingDispatch || j.State == domain.FileJobQueued {
		rows, e2 := list[outboxRecord](ctx, tx, outboxQuery().Where(sq.Eq{"job_id": j.ID, "generation": j.Generation, "status": "exhausted"}))
		if e2 != nil {
			return false, e2
		}
		publishExhausted = len(rows) > 0
	}
	if !allowed {
		e = terminalJob(ctx, tx, j, domain.FileJobRevoked, "access_revoked", now)
	} else if !j.DeadlineAt.After(now) {
		e = terminalJob(ctx, tx, j, domain.FileJobExpired, "job_deadline", now)
	} else if publishExhausted {
		e = quarantineJob(ctx, tx, j, "publish_exhausted", now)
	} else if j.LeaseUntil != nil && !j.LeaseUntil.After(now) {
		if j.State == domain.FileJobCancelRequested {
			e = terminalJob(ctx, tx, j, domain.FileJobCancelled, "cancelled", now)
		} else {
			e = retryJob(ctx, tx, j, "lease_expired", "lease_expired", now)
		}
	} else if j.State == domain.FileJobRetryWait && j.NextAttemptAt != nil && !j.NextAttemptAt.After(now) {
		e = nextGeneration(ctx, tx, j, now, now)
	} else if j.State == domain.FileJobQueued && now.Sub(j.UpdatedAt) >= 60*time.Second {
		e = nextGeneration(ctx, tx, j, now, now)

	} else {
		changed = false
	}
	if e != nil {
		return false, e
	}
	return changed, tx.Commit(ctx)
}
