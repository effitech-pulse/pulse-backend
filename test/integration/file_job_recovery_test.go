package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestFileJobPublisherCrashAfterExhaustionRecoversQuarantine(t *testing.T) {
	pool, ctx, store, _, v := fileJobSetup(t)
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	// Exact crash window: outbox exhaustion durable, job quarantine commit lost.
	if _, e := pool.Exec(ctx, `UPDATE file_job_outbox SET status='exhausted',publish_attempts=8,last_error_code='publish_exhausted' WHERE job_id=$1`, v.Input.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := store.ReconcileFileJobs(context.Background(), 8); e != nil {
		t.Fatal(e)
	}
	got, e := store.FileJob(ctx, 1, v.Input.ID)
	if e != nil || got.State != domain.FileJobQuarantined {
		t.Fatal("publish exhaustion stuck", got.State, e)
	}
}
func TestFileJobAuthLocksSerializeAccountWellUpdate(t *testing.T) {
	for _, target := range []string{"account", "well"} {
		t.Run(target, func(t *testing.T) {
			pool, ctx, store, checks, v := fileJobSetup(t)
			if _, e := store.AcceptFileJob(ctx, v); e != nil {
				t.Fatal(e)
			}
			lease, e := store.ClaimFileJob(context.Background(), fileJobMessage(t, pool, v.Input.ID), strings.Repeat("c", 32))
			if e != nil {
				t.Fatal(e)
			}
			result, e := checks.ComputeStoredInput(ctx, v.Input.ID)
			if e != nil {
				t.Fatal(e)
			}
			revoke, e := pool.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer revoke.Rollback(ctx)
			query := `UPDATE auth_user SET active=false WHERE id=1`
			if target == "well" {
				query = `UPDATE well SET active=false WHERE id=1`
			}
			if _, e = revoke.Exec(ctx, query); e != nil {
				t.Fatal(e)
			}
			done := make(chan error, 1)
			go func() { _, e := store.FinishFileJob(context.Background(), *lease, &result, nil); done <- e }()
			// DB lock barrier: wait until finalize is demonstrably blocked on our writer.
			deadline := time.Now().Add(5 * time.Second)
			blocked := false
			for time.Now().Before(deadline) {
				var n int
				if e = pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%FOR SHARE%'`).Scan(&n); e != nil {
					t.Fatal(e)
				}
				if n > 0 {
					blocked = true
					break
				}
				time.Sleep(time.Millisecond)
			}
			if !blocked {
				t.Fatal("finalize did not wait for auth writer")
			}
			if e = revoke.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			select {
			case e = <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("finalize blocked after commit")
			}
			var state string
			var n int
			if e = pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM file_check_result) FROM file_job WHERE id=$1`, v.Input.ID).Scan(&state, &n); e != nil || state != "revoked" || n != 0 {
				t.Fatal(state, n, e)
			}
		})
	}
}
func TestFileJobPublishConfirmDoesNotRegressRunning(t *testing.T) {
	pool, ctx, store, _, v := fileJobSetup(t)
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	pub, e := store.ClaimFileJobPublish(context.Background())
	if e != nil || pub == nil {
		t.Fatal(pub, e)
	}
	lease, e := store.ClaimFileJob(context.Background(), pub.Message, strings.Repeat("c", 32))
	if e != nil || lease == nil {
		t.Fatal(lease, e)
	}
	if e = store.FinishFileJobPublish(context.Background(), *pub, domain.FileJobPublishConfirmed); e != nil {
		t.Fatal(e)
	}
	got, e := store.FileJob(ctx, 1, v.Input.ID)
	if e != nil || got.State != domain.FileJobRunning {
		t.Fatal(got, e)
	}
	var count int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM file_job_attempt`).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
}

func TestFileJobQuarantineDoesNotResumeButExpires(t *testing.T) {
	pool, ctx, store, _, v := fileJobSetup(t)
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	msg := fileJobMessage(t, pool, v.Input.ID)
	msg.JobID = strings.Repeat("d", 32)
	if lease, e := store.ClaimFileJob(context.Background(), msg, strings.Repeat("c", 32)); e != nil || lease != nil {
		t.Fatal(lease, e)
	}
	if _, e := store.ReconcileFileJobs(context.Background(), 8); e != nil {
		t.Fatal(e)
	}
	got, e := store.FileJob(ctx, 1, v.Input.ID)
	if e != nil || got.State != domain.FileJobQuarantined {
		t.Fatal(got, e)
	}
	if _, e = pool.Exec(ctx, `UPDATE file_job SET created_at=created_at-interval '21 minutes',deadline_at=deadline_at-interval '21 minutes' WHERE id=$1`, v.Input.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = store.ReconcileFileJobs(context.Background(), 8); e != nil {
		t.Fatal(e)
	}
	got, e = store.FileJob(ctx, 1, v.Input.ID)
	if e != nil || got.State != domain.FileJobExpired {
		t.Fatal("quarantine never expired", got, e)
	}
}

func TestFileJobResultInboxAtomicRollback(t *testing.T) {
	pool, ctx, store, checks, v := fileJobSetup(t)
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	lease, e := store.ClaimFileJob(context.Background(), fileJobMessage(t, pool, v.Input.ID), strings.Repeat("c", 32))
	if e != nil {
		t.Fatal(e)
	}
	result, e := checks.ComputeStoredInput(ctx, v.Input.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `CREATE FUNCTION fail_q1_inbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected inbox boundary'; END $$;CREATE TRIGGER fail_q1_inbox BEFORE UPDATE ON file_job_inbox FOR EACH ROW EXECUTE FUNCTION fail_q1_inbox()`); e != nil {
		t.Fatal(e)
	}
	if _, e = store.FinishFileJob(context.Background(), *lease, &result, nil); e == nil {
		t.Fatal("success acknowledged without inbox")
	}
	var n int
	var state string
	if e = pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM file_check_result) FROM file_job WHERE id=$1`, v.Input.ID).Scan(&state, &n); e != nil || state != "running" || n != 0 {
		t.Fatal("half finalized", state, n, e)
	}
	if _, e = pool.Exec(ctx, `DROP TRIGGER fail_q1_inbox ON file_job_inbox`); e != nil {
		t.Fatal(e)
	}
	if _, e = store.FinishFileJob(context.Background(), *lease, &result, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `ALTER TABLE file_input_payload RENAME TO hidden_payload_for_metadata_proof`); e != nil {
		t.Fatal(e)
	}
	if _, e = store.FileJob(ctx, 1, v.Input.ID); e != nil {
		t.Fatal("metadata touched payload", e)
	}
	if _, e = store.FileJobs(ctx, 1, ""); e != nil {
		t.Fatal("list touched payload", e)
	}
	if _, e = store.FileJobResult(ctx, 1, v.Input.ID); e != nil {
		t.Fatal("result touched payload", e)
	}
}
func TestFileJobRetryBudgetAndRedrive(t *testing.T) {
	t.Run("retry_budget", func(t *testing.T) {
		pool, ctx, store, _, v := fileJobSetup(t)
		if _, e := store.AcceptFileJob(ctx, v); e != nil {
			t.Fatal(e)
		}
		for attempt := 1; attempt <= 3; attempt++ {
			lease, e := store.ClaimFileJob(context.Background(), fileJobMessage(t, pool, v.Input.ID), strings.Repeat("c", 32))
			if e != nil || lease == nil {
				t.Fatal(lease, e)
			}
			out, e := store.FinishFileJob(context.Background(), *lease, nil, &domain.FileJobFailure{Code: "ml_unavailable", Retryable: true})
			if e != nil {
				t.Fatal(e)
			}
			want := domain.FileJobRetryWait
			if attempt == 3 {
				want = domain.FileJobFailed
			}
			if out.Status.State != want || out.Status.AttemptCount != attempt {
				t.Fatal(out)
			}
			if attempt < 3 {
				if _, e = pool.Exec(ctx, `UPDATE file_job SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE id=$1`, v.Input.ID); e != nil {
					t.Fatal(e)
				}
				if _, e = store.ReconcileFileJobs(context.Background(), 8); e != nil {
					t.Fatal(e)
				}
			}
		}
	})
	t.Run("owner_admin_once", func(t *testing.T) {
		pool, ctx, store, _, v := fileJobSetup(t)
		if _, e := store.AcceptFileJob(ctx, v); e != nil {
			t.Fatal(e)
		}
		msg := fileJobMessage(t, pool, v.Input.ID)
		msg.Version = 99
		if _, e := store.ClaimFileJob(context.Background(), msg, strings.Repeat("c", 32)); e != nil {
			t.Fatal(e)
		}
		status, e := store.FileJob(ctx, 1, v.Input.ID)
		if e != nil || status.State != domain.FileJobQuarantined {
			t.Fatal(status, e)
		}
		if _, e = store.RedriveFileJob(ctx, 1, v.Input.ID, status.Revision, "operator_retry"); e == nil {
			t.Fatal("technologist redrive")
		}
		if _, e = pool.Exec(ctx, `UPDATE auth_user SET role='admin' WHERE id=1`); e != nil {
			t.Fatal(e)
		}
		got, e := store.RedriveFileJob(ctx, 1, v.Input.ID, status.Revision, "operator_retry")
		if e != nil || got.State != domain.FileJobPendingDispatch {
			t.Fatal(got, e)
		}
		if _, e = store.RedriveFileJob(ctx, 1, v.Input.ID, status.Revision, "operator_retry"); e == nil {
			t.Fatal("repeated redrive")
		}
		var n int
		if e = pool.QueryRow(ctx, `SELECT redrive_count FROM file_job WHERE id=$1`, v.Input.ID).Scan(&n); e != nil || n != 1 {
			t.Fatal(n, e)
		}
	})
}

func TestFileJobMalformedKnownReferenceInvalidatesLease(t *testing.T) {
	pool, ctx, store, _, v := fileJobSetup(t)
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	msg := fileJobMessage(t, pool, v.Input.ID)
	lease, e := store.ClaimFileJob(context.Background(), msg, strings.Repeat("c", 32))
	if e != nil || lease == nil {
		t.Fatal(lease, e)
	}
	msg.JobID = strings.Repeat("d", 32)
	if got, e := store.ClaimFileJob(context.Background(), msg, strings.Repeat("c", 32)); e != nil || got != nil {
		t.Fatal(got, e)
	}
	status, e := store.FileJob(ctx, 1, v.Input.ID)
	if e != nil || status.State != domain.FileJobQuarantined {
		t.Fatal("known malformed reference kept running", status, e)
	}
	if e = store.RenewFileJob(context.Background(), *lease); e == nil {
		t.Fatal("quarantined lease renewed")
	}
}
