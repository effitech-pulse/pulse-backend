package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func assertFileJobProblem(t *testing.T, err error, kind domain.Kind, code string) {
	t.Helper()
	var problem *domain.FileJobProblem
	var de *domain.Error
	if !errors.As(err, &problem) || problem.Code != code || !errors.As(err, &de) || de.Kind != kind {
		t.Fatalf("want %s/%s got %v", kind, code, err)
	}
}
func TestFileJobResultStateContract(t *testing.T) {
	for _, state := range []string{"pending_dispatch", "queued", "running", "retry_wait", "cancel_requested", "quarantined", "failed", "cancelled", "expired", "revoked"} {
		t.Run(state, func(t *testing.T) {
			pool, ctx, store, _, v := fileJobSetup(t)
			if _, e := store.AcceptFileJob(ctx, v); e != nil {
				t.Fatal(e)
			}
			if _, e := pool.Exec(ctx, `UPDATE file_job SET state=$2,finished_at=CASE WHEN $2 IN ('failed','cancelled','expired','revoked') THEN clock_timestamp() END,lease_until=CASE WHEN $2 IN ('running','cancel_requested') THEN clock_timestamp()+interval '45 seconds' END,lease_token_sha256=CASE WHEN $2 IN ('running','cancel_requested') THEN 'synthetic-token-hash' END WHERE id=$1`, v.Input.ID, state); e != nil {
				t.Fatal(e)
			}
			_, e := store.FileJobResult(ctx, 1, v.Input.ID)
			kind, code := domain.Duplicate, "result_not_ready"
			switch state {
			case "failed", "quarantined":
				code = "result_unavailable"
			case "cancelled":
				kind, code = domain.Missing, "result_cancelled"
			case "expired":
				kind, code = domain.Missing, "result_expired"
			case "revoked":
				kind, code = domain.Missing, "file_job_not_found"
			}
			assertFileJobProblem(t, e, kind, code)
		})
	}
}
func TestFileJobInvalidCursorContract(t *testing.T) {
	_, ctx, store, _, v := fileJobSetup(t)
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	if _, e := store.FileJobs(ctx, 1, strings.Repeat("f", 32)); e == nil {
		t.Fatal("unknown cursor returned empty success")
	} else {
		var de *domain.Error
		if !errors.As(e, &de) || de.Kind != domain.Missing {
			t.Fatal(e)
		}
	}
	page, e := store.FileJobs(ctx, 1, v.Input.ID)
	if e != nil || len(page.Items) != 0 || page.NextBefore != nil {
		t.Fatal("valid end cursor", page, e)
	}
	page, e = store.FileJobs(ctx, 1, "")
	if e != nil || len(page.Items) != 1 {
		t.Fatal("normal list", page, e)
	}
}
func TestFileJobPinnedInputUnavailablePersisted(t *testing.T) {
	pool, ctx, store, _, v := fileJobSetup(t)
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	lease, e := store.ClaimFileJob(context.Background(), fileJobMessage(t, pool, v.Input.ID), strings.Repeat("c", 32))
	if e != nil || lease == nil {
		t.Fatal(lease, e)
	}
	disposition, e := store.FinishFileJob(context.Background(), *lease, nil, &domain.FileJobFailure{Code: "pinned_input_unavailable"})
	if e != nil || disposition.Status.State != domain.FileJobFailed {
		t.Fatal(disposition, e)
	}
	persisted, e := store.FileJob(ctx, 1, v.Input.ID)
	if e != nil || persisted.ErrorCode == nil || *persisted.ErrorCode != "pinned_input_unavailable" {
		t.Fatal("pinned failure classification lost", persisted.ErrorCode, e)
	}
	var attemptCode string
	if e = pool.QueryRow(ctx, `SELECT error_code FROM file_job_attempt WHERE job_id=$1 AND attempt=1`, v.Input.ID).Scan(&attemptCode); e != nil || attemptCode != "pinned_input_unavailable" {
		t.Fatal(attemptCode, e)
	}
}

func TestFileJobExecutionUnknownPayloadReadRemainsRecoverable(t *testing.T) {
	pool, ctx, store, _, v := fileJobSetup(t)
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	lease, e := store.ClaimFileJob(context.Background(), fileJobMessage(t, pool, v.Input.ID), strings.Repeat("c", 32))
	if e != nil || lease == nil {
		t.Fatal(lease, e)
	}
	if _, e = pool.Exec(ctx, `ALTER TABLE file_input_payload RENAME TO unavailable_payload_for_execution`); e != nil {
		t.Fatal(e)
	}
	_, e = store.ExecutionFileJob(context.Background(), *lease)
	var pg *pgconn.PgError
	var problem *domain.FileJobProblem
	if !errors.As(e, &pg) || pg.Code != "42P01" || errors.As(e, &problem) {
		t.Fatalf("unknown PG error was classified as terminal: %T", e)
	}
	var state, inbox string
	var fence int64
	var token string
	var slot string
	if e = pool.QueryRow(ctx, `SELECT j.state,j.fence,j.lease_token_sha256,i.state,r.job_id FROM file_job j JOIN file_job_inbox i ON i.job_id=j.id JOIN file_job_runtime r ON r.job_id=j.id WHERE j.id=$1`, v.Input.ID).Scan(&state, &fence, &token, &inbox, &slot); e != nil || state != "running" || inbox != "processing" || fence != lease.Fence || token != domain.FileSHA256([]byte(lease.Token)) || slot != lease.JobID {
		t.Fatal("execution error consumed durable intent", state, inbox, fence, e)
	}
	if _, e = pool.Exec(ctx, `ALTER TABLE unavailable_payload_for_execution RENAME TO file_input_payload`); e != nil {
		t.Fatal(e)
	}
	execution, e := store.ExecutionFileJob(context.Background(), *lease)
	if e != nil || len(execution.Input.Raw) == 0 {
		t.Fatal("same lease did not recover", e)
	}
}
func TestFileJobExecutionMissingPayloadIsKnown(t *testing.T) {
	pool, ctx, store, _, v := fileJobSetup(t)
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	lease, e := store.ClaimFileJob(context.Background(), fileJobMessage(t, pool, v.Input.ID), strings.Repeat("c", 32))
	if e != nil || lease == nil {
		t.Fatal(lease, e)
	}
	if _, e = pool.Exec(ctx, `ALTER TABLE file_input_payload DISABLE TRIGGER file_input_immutable;DELETE FROM file_input_payload;ALTER TABLE file_input_payload ENABLE TRIGGER file_input_immutable`); e != nil {
		t.Fatal(e)
	}
	_, e = store.ExecutionFileJob(context.Background(), *lease)
	assertFileJobProblem(t, e, domain.Unavailable, "input_unavailable")
}

func TestFileJobExecutionCorruptPayloadIsKnown(t *testing.T) {
	pool, ctx, store, _, v := fileJobSetup(t)
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	lease, e := store.ClaimFileJob(context.Background(), fileJobMessage(t, pool, v.Input.ID), strings.Repeat("c", 32))
	if e != nil || lease == nil {
		t.Fatal(lease, e)
	}
	if _, e = pool.Exec(ctx, `ALTER TABLE file_input_payload DISABLE TRIGGER file_input_immutable;UPDATE file_input_payload SET raw_bytes=set_byte(raw_bytes,0,0);ALTER TABLE file_input_payload ENABLE TRIGGER file_input_immutable`); e != nil {
		t.Fatal(e)
	}
	_, e = store.ExecutionFileJob(context.Background(), *lease)
	assertFileJobProblem(t, e, domain.Unavailable, "input_corrupt")
}
