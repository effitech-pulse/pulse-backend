package postgres

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"io"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func fileJobID() (string, error) {
	var b [16]byte
	_, e := rand.Read(b[:])
	return hex.EncodeToString(b[:]), e
}
func jobStatusQuery() sq.SelectBuilder {
	return sqlBuilder.Select("j.id", "j.well_id", "j.state", "j.revision", "j.attempt_count", "j.created_at", "j.updated_at", "j.deadline_at", "j.started_at", "j.finished_at", "j.next_attempt_at", "j.cancel_requested_at", "j.check_id", "j.error_code", `json_build_object('format',i.parse_spec->>'format','raw_bytes',i.raw_length,'canonical_bytes',i.canonical_length,'rows',i.rows_count,'raw_sha256',i.raw_sha256,'canonical_sha256',i.canonical_sha256,'fingerprint_sha256',i.fingerprint_sha256) AS input`, `json_build_object('model_name',i.execution_pin->>'model_name','bundle_sha256',i.execution_pin->>'bundle_sha256','context_version',i.execution_pin->>'context_version') AS execution`).From("file_job j").Join("file_input i ON i.id=j.input_id")
}
func jobStatus(ctx context.Context, db db, id string) (domain.FileJobStatus, error) {
	return one[domain.FileJobStatus](ctx, db, jobStatusQuery().Where(sq.Eq{"j.id": id}))
}
func dbJobNow(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	e := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now)
	return now, e
}
func lockJobBudget(ctx context.Context, tx pgx.Tx) error {
	_, e := one[struct {
		Key string `json:"budget_key"`
	}](ctx, tx, sqlBuilder.Select("budget_key").From("file_input_budget").Where(sq.Eq{"budget_key": "global"}).Suffix("FOR UPDATE"))
	return e
}
func jobOutbox(ctx context.Context, tx pgx.Tx, id string, generation int, due time.Time) error {
	event, e := fileJobID()
	if e != nil {
		return e
	}
	_, e = execute(ctx, tx, sqlBuilder.Insert("file_job_outbox").Columns("event_id", "job_id", "generation", "status", "next_publish_at", "created_at").Values(event, id, generation, "pending", due, due))
	return e
}
func validJobBounds(v domain.FileJobPrepared) bool {
	if v.PointsCount != v.Input.Rows || v.Start.IsZero() || !v.End.After(v.Start) {
		return false
	}
	reader := csv.NewReader(bytes.NewReader(v.Input.Canonical))
	header, e := reader.Read()
	if e != nil || len(header) == 0 {
		return false
	}
	n := 0
	var min, max time.Time
	for {
		row, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil || len(row) == 0 {
			return false
		}
		t, e := time.Parse(time.RFC3339Nano, row[0])
		if e != nil {
			return false
		}
		if n == 0 || t.Before(min) {
			min = t
		}
		if n == 0 || t.After(max) {
			max = t
		}
		n++
	}
	return n == v.PointsCount && v.Start.Equal(min.Truncate(time.Microsecond)) && v.End.Equal(max.Truncate(time.Microsecond).Add(time.Microsecond))
}
func (r *Repository) AcceptFileJob(ctx context.Context, v domain.FileJobPrepared) (domain.FileJobStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var zero domain.FileJobStatus
	actor, e := fileJobSession(ctx)
	if e != nil {
		return zero, e
	}
	if actor != v.Input.ActorAccountID {
		return zero, missingFileJob()
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	if e = fileJobCapacityGate(ctx, tx); e != nil {
		return zero, e
	}
	allowed, e := r.authorizeFileJobTx(ctx, tx, actor, v.Input.PulseWellID, nil, "filechecks.run", false)
	if e != nil {
		return zero, e
	}
	if !allowed {
		return zero, missingFileJob()
	}
	acceptedWell, e := one[domain.Well](ctx, tx, sqlBuilder.Select(wellColumns).From("well").Where(sq.Eq{"id": v.Input.PulseWellID}).Suffix("FOR SHARE"))
	if e != nil {
		return zero, e
	}
	wellAuthority, e := domain.FileJobWellAuthorityFrom(acceptedWell)
	if e != nil {
		return zero, e
	}
	if e = lockJobBudget(ctx, tx); e != nil {
		return zero, e
	}
	previous, e := one[domain.FileInputSummary](ctx, tx, fileInputQuery().Where(sq.Eq{"actor_account_id": actor, "operation_kind": "file_check", "request_key": v.Input.RequestKey}))
	if e == nil {
		if previous.PulseWellID != v.Input.PulseWellID || previous.RequestCommitmentSHA256 != v.Input.RequestCommitmentSHA256 {
			return zero, domain.NewFileJobProblem(domain.Duplicate, "request_key_conflict")
		}
		oldJob, jobErr := one[jobRecord](ctx, tx, jobQuery().Where(sq.Eq{"id": previous.ID}))
		if isMissing(jobErr) {
			return zero, domain.NewFileJobProblem(domain.Duplicate, "input_not_job")
		}
		if jobErr != nil {
			return zero, jobErr
		}
		if !r.matchesJobPolicy(oldJob) {
			return zero, missingFileJob()
		}
		got, e := jobStatus(ctx, tx, previous.ID)
		if isMissing(e) {
			return zero, domain.NewFileJobProblem(domain.Duplicate, "input_not_job")
		}
		if e != nil {
			return zero, e
		}
		return got, tx.Commit(ctx)
	}
	if !isMissing(e) {
		return zero, e
	}
	if busy, e := activeCohort(ctx, tx); e != nil {
		return zero, e
	} else if busy {
		return zero, domain.NewFileJobProblem(domain.Limited, "file_job_capacity")
	}
	if !r.allowsPrepared(v.Input) {
		return zero, domain.NewFileJobProblem(domain.Forbidden, "synthetic_input_required")
	}
	if e = domain.ValidateFileInput(v.Input); e != nil {
		return zero, e
	}
	if !validJobBounds(v) {
		return zero, domain.NewFileJobProblem(domain.Invalid, "invalid_file_job_bounds")
	}
	counts, e := one[struct {
		Global  int `json:"global"`
		Account int `json:"account"`
	}](ctx, tx, sqlBuilder.Select("count(*) AS global").Column(sq.Expr("count(*) FILTER (WHERE actor_account_id=?) AS account", actor)).From("file_job").Where(sq.NotEq{"state": []string{"succeeded", "failed", "cancelled", "expired", "revoked"}}))
	if e != nil {
		return zero, e
	}
	if counts.Global >= r.fileJobPolicy.ActiveGlobal || counts.Account >= r.fileJobPolicy.ActivePerAccount {
		return zero, domain.NewFileJobProblem(domain.Limited, "file_job_capacity")
	}
	input, e := insertFileInputTx(ctx, tx, v.Input)
	if e != nil {
		return zero, e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return zero, e
	}
	p := r.fileJobPolicy
	snapshot, e := admissionAuthorization(ctx, tx, actor, "filechecks.run")
	if e != nil {
		return zero, e
	}
	_, e = execute(ctx, tx, sqlBuilder.Insert("file_job").Columns("well_authority", "actor_authorization", "id", "input_id", "actor_account_id", "well_id", "cohort_version", "cohort_manifest_sha256", "pilot_run_id", "state", "created_at", "updated_at", "deadline_at", "start_at", "end_at", "points_count").Values(wellAuthority, snapshot, input.ID, input.ID, actor, input.PulseWellID, p.CohortVersion, p.CohortManifestSHA256, p.RunID, "pending_dispatch", now, now, now.Add(20*time.Minute), v.Start, v.End, v.PointsCount))
	if e != nil {
		return zero, e
	}
	if e = jobOutbox(ctx, tx, input.ID, 1, now); e != nil {
		return zero, e
	}
	out, e := jobStatus(ctx, tx, input.ID)
	if e != nil {
		return zero, e
	}
	return out, tx.Commit(ctx)
}

// publicFileJobTx требует owner даже для admin и линеаризует auth/job guards.
func (r *Repository) publicFileJobTx(ctx context.Context, tx pgx.Tx, well int64, id string, actions ...domain.Permission) (jobRecord, error) {
	actor, e := fileJobSession(ctx)
	if e != nil {
		return jobRecord{}, e
	}
	row, e := one[jobRecord](ctx, tx, jobQuery().Where(sq.Eq{"id": id, "well_id": well, "actor_account_id": actor}))
	if e != nil {
		return row, e
	}
	permission := domain.Permission("filechecks.read")
	if len(actions) > 0 {
		permission = actions[0]
	}
	allowed, e := r.authorizeFileJobTx(ctx, tx, actor, well, nil, permission, false)
	if e != nil {
		return row, e
	}
	if !allowed {
		return row, missingFileJob()
	}
	row, e = one[jobRecord](ctx, tx, jobQuery().Where(sq.Eq{"id": id, "well_id": well, "actor_account_id": actor}).Suffix("FOR SHARE"))
	if e == nil && !r.matchesJobPolicy(row) {
		e = missingFileJob()
	}
	return row, e
}
func (r *Repository) FileJob(ctx context.Context, well int64, id string) (domain.FileJobStatus, error) {
	var zero domain.FileJobStatus
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	if _, e = r.publicFileJobTx(ctx, tx, well, id); e != nil {
		return zero, e
	}
	v, e := jobStatus(ctx, tx, id)
	if e != nil {
		return zero, e
	}
	return v, tx.Commit(ctx)
}
func (r *Repository) FileJobs(ctx context.Context, well int64, before string) (domain.FileJobPage, error) {
	zero := domain.FileJobPage{Items: []domain.FileJobStatus{}}
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
	filter := sq.Eq{"j.actor_account_id": actor, "j.well_id": well, "j.pilot_run_id": p.RunID, "j.cohort_version": p.CohortVersion, "j.cohort_manifest_sha256": p.CohortManifestSHA256}
	q := jobStatusQuery().Where(filter)
	if before != "" {
		cursor, e := one[struct {
			CreatedAt time.Time `json:"created_at"`
			ID        string    `json:"id"`
		}](ctx, tx, sqlBuilder.Select("j.created_at", "j.id").From("file_job j").Where(filter).Where(sq.Eq{"j.id": before}))
		if e != nil {
			if isMissing(e) {
				return zero, missingFileJob()
			}
			return zero, e
		}
		q = q.Where(sq.Expr("(j.created_at,j.id)<(?,?)", cursor.CreatedAt, cursor.ID))
	}
	items, e := list[domain.FileJobStatus](ctx, tx, q.OrderBy("j.created_at DESC", "j.id DESC").Limit(26))
	if e != nil {
		return zero, e
	}
	if len(items) > 25 {
		cursor := items[24].ID
		zero.NextBefore = &cursor
		items = items[:25]
	}
	zero.Items = items
	return zero, tx.Commit(ctx)
}
func (r *Repository) FileJobResult(ctx context.Context, well int64, id string) (domain.FileSavedCheck, error) {
	var zero domain.FileSavedCheck
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	row, e := r.publicFileJobTx(ctx, tx, well, id)
	if e != nil {
		return zero, e
	}
	switch row.State {
	case domain.FileJobSucceeded:
	case domain.FileJobPendingDispatch, domain.FileJobQueued, domain.FileJobRunning, domain.FileJobRetryWait, domain.FileJobCancelRequested:
		return zero, domain.NewFileJobProblem(domain.Duplicate, "result_not_ready")
	case domain.FileJobFailed, domain.FileJobQuarantined:
		return zero, domain.NewFileJobProblem(domain.Duplicate, "result_unavailable")
	case domain.FileJobCancelled:
		return zero, domain.NewFileJobProblem(domain.Missing, "result_cancelled")
	case domain.FileJobExpired:
		return zero, domain.NewFileJobProblem(domain.Missing, "result_expired")
	default:
		return zero, missingFileJob()
	}
	out, e := one[domain.FileSavedCheck](ctx, tx, sqlBuilder.Select("j.check_id AS id", "j.id AS job_id", "j.well_id", "i.source_kind", "j.start_at AS start", "j.end_at AS end", "j.finished_at AS created_at", "substring(i.canonical_sha256 from 8) AS input_sha256", "j.points_count", "'file'::text AS resolution", "r.result").From("file_job j").Join("file_input i ON i.id=j.input_id").Join("file_check_result r ON r.input_id=j.input_id").Where(sq.Eq{"j.id": id}))
	if e != nil {
		return zero, e
	}
	return out, tx.Commit(ctx)
}

// JSON equality helper keeps pin comparisons typed at the adapter boundary.
func equalJobJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// fileJobCapacityGate сохраняет Q1 same-key replay при ограниченном ожидании общего gate.
func fileJobCapacityGate(ctx context.Context, tx pgx.Tx) error {
	_, e := execute(ctx, tx, sq.Expr("SELECT pg_advisory_xact_lock(?)", cohortCapacityGate))
	if e != nil && ctx.Err() == context.DeadlineExceeded {
		return domain.NewFileJobProblem(domain.Limited, "file_job_capacity")
	}
	return e
}
