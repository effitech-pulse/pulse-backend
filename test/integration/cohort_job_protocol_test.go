package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestCohortPhysicalLeaseFence(t *testing.T) {
	pool, ctx, r, v := cohortJobSetup(t)
	j, e := r.AcceptCohortJob(ctx, v)
	if e != nil {
		t.Fatal(e)
	}
	claim, e := r.ClaimCohortJob(ctx, cohortMessage(t, pool, j.ID), strings.Repeat("1", 32))
	if e != nil || claim == nil {
		t.Fatal(e)
	}
	x, e := r.NextCohortMember(ctx, claim.Lease())
	if e != nil {
		claim.Close()
		t.Fatal(e)
	}
	checkpoint := cohortMarkerCheckpoint(t, *x)
	claim.Close()
	if e = r.CheckpointCohortMember(ctx, claim.Lease(), checkpoint); e == nil {
		t.Fatal("physical lock loss accepted checkpoint")
	}
	var n int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM cohort_job_checkpoint WHERE job_id=$1", j.ID).Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
	if e = claim.Alive(ctx); e == nil {
		t.Fatal("closed reservation alive")
	}
	d, e := r.FinishCohortJob(ctx, claim.Lease(), nil)
	if e != nil || d.Kind != "stale" {
		t.Fatal(d, e)
	}
}
func TestCohortCurrentAuthorityRevocation(t *testing.T) {
	for _, change := range []string{`UPDATE auth_user SET active=false WHERE id=1`, `UPDATE auth_user SET role='viewer' WHERE id=1`, `UPDATE auth_user SET scope='{"funds":[],"ngdus":null,"departments":null}' WHERE id=1`, `UPDATE well SET active=false WHERE id=100`, `DELETE FROM cohort_job_guard`} {
		t.Run(change[:6]+strings.Split(change, " ")[1], func(t *testing.T) {
			pool, ctx, r, v := cohortJobSetup(t)
			j, e := r.AcceptCohortJob(ctx, v)
			if e != nil {
				t.Fatal(e)
			}
			claim, e := r.ClaimCohortJob(ctx, cohortMessage(t, pool, j.ID), strings.Repeat("1", 32))
			if e != nil || claim == nil {
				t.Fatal(e)
			}
			defer claim.Close()
			if _, e = pool.Exec(ctx, change); e != nil {
				t.Fatal(e)
			}
			if _, e = r.NextCohortMember(ctx, claim.Lease()); e == nil {
				t.Fatal("revoked actor/lifecycle accessed input")
			}
			var state string
			if e = pool.QueryRow(ctx, "SELECT state FROM cohort_job WHERE id=$1", j.ID).Scan(&state); e != nil || state != "revoked" {
				t.Fatal(state, e)
			}
		})
	}
}
func TestCohortCheckpointResumeAndFence(t *testing.T) {
	pool, ctx, r, v := cohortJobSetup(t)
	j, e := r.AcceptCohortJob(ctx, v)
	if e != nil {
		t.Fatal(e)
	}
	first, e := r.ClaimCohortJob(ctx, cohortMessage(t, pool, j.ID), strings.Repeat("1", 32))
	if e != nil || first == nil {
		t.Fatal(e)
	}
	x, e := r.NextCohortMember(ctx, first.Lease())
	if e != nil {
		t.Fatal(e)
	}
	c := cohortMarkerCheckpoint(t, *x)
	if e = r.CheckpointCohortMember(ctx, first.Lease(), c); e != nil {
		t.Fatal(e)
	}
	if e = r.CheckpointCohortMember(ctx, first.Lease(), c); e != nil {
		t.Fatal("same current digest not idempotent", e)
	}
	d, e := r.FinishCohortJob(ctx, first.Lease(), &domain.CohortJobFailure{Code: "ml_unavailable", Retryable: true})
	first.Close()
	if e != nil || d.Status.State != domain.CohortJobRetryWait {
		t.Fatal(d, e)
	}
	if e = r.CheckpointCohortMember(ctx, first.Lease(), c); e == nil {
		t.Fatal("old fence accepted duplicate")
	}
	time.Sleep(10100 * time.Millisecond)
	if _, e = r.ReconcileCohortJobs(ctx, 8); e != nil {
		t.Fatal(e)
	}
	second, e := r.ClaimCohortJob(ctx, cohortMessage(t, pool, j.ID), strings.Repeat("2", 32))
	if e != nil || second == nil {
		t.Fatal(e)
	}
	defer second.Close()
	x, e = r.NextCohortMember(ctx, second.Lease())
	if e != nil || x == nil || x.Member.Well.ID == c.WellID {
		t.Fatal("checkpoint recomputed", e)
	}
	if second.Lease().Fence <= first.Lease().Fence {
		t.Fatal("fence not monotonic")
	}
}
func TestCohortCancelFinalizeRace(t *testing.T) {
	pool, ctx, r, v := cohortJobSetup(t)
	j, e := r.AcceptCohortJob(ctx, v)
	if e != nil {
		t.Fatal(e)
	}
	claim, e := r.ClaimCohortJob(ctx, cohortMessage(t, pool, j.ID), strings.Repeat("1", 32))
	if e != nil || claim == nil {
		t.Fatal(e)
	}
	defer claim.Close()
	s, e := r.CohortJobStatus(ctx, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	s, e = r.CancelCohortJob(ctx, j.ID, s.Revision)
	if e != nil || s.State != domain.CohortJobCancelRequested {
		t.Fatal(s, e)
	}
	if _, e = r.NextCohortMember(ctx, claim.Lease()); e == nil {
		t.Fatal("cancel allowed input")
	}
	d, e := r.FinishCohortJob(ctx, claim.Lease(), nil)
	if e != nil || d.Status.State != domain.CohortJobCancelled {
		t.Fatal(d, e)
	}
	var n int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM ml_run_snapshot WHERE run_id=$1", j.RunID).Scan(&n)
	if n != 0 {
		t.Fatal("partial publication", n)
	}
}
func TestCohortQuotaAndCrossKindCapacity(t *testing.T) {
	pool, ctx, r, v := cohortJobSetup(t)
	held, ok, e := r.Reserve(ctx)
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	if _, e = r.AcceptCohortJob(ctx, v); e == nil {
		held.Close()
		t.Fatal("legacy lock overtaken")
	}
	held.Close()
	for i := 0; i < 4; i++ {
		v.RequestKey = "q2-retained-key-000" + string(rune('1'+i))
		j, e := r.AcceptCohortJob(ctx, v)
		if e != nil {
			t.Fatal(i, e)
		}
		if held, ok, e := r.Reserve(ctx); e != nil || ok {
			if held != nil {
				held.Close()
			}
			t.Fatal("legacy accepted active Q2", ok, e)
		}
		if _, e = r.CancelCohortJob(ctx, j.ID, j.Revision); e != nil {
			t.Fatal(e)
		}
	}
	v.RequestKey = "q2-retained-key-0005"
	if _, e = r.AcceptCohortJob(ctx, v); e == nil {
		t.Fatal("retained account cap bypassed")
	}
	var n int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM cohort_job").Scan(&n)
	if n != 4 {
		t.Fatal(n)
	}
}
func TestCohortLegacyFailAndCleanupExcluded(t *testing.T) {
	pool, ctx, r, v := cohortJobSetup(t)
	j, e := r.AcceptCohortJob(ctx, v)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.CancelCohortJob(ctx, j.ID, j.Revision); e != nil {
		t.Fatal(e)
	}
	held, ok, e := r.Reserve(ctx)
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	defer held.Close()
	if e = held.Fail(ctx, j.RunID, "forged legacy overwrite"); e != nil {
		t.Fatal(e)
	}
	var reason string
	if e = pool.QueryRow(ctx, "SELECT error FROM ml_run WHERE id=$1", j.RunID).Scan(&reason); e != nil || reason != "cancelled" {
		t.Fatal(reason, e)
	}

	if _, e = held.BeginCohort(context.Background(), "manual", v.Fixture.CohortVersion); e != nil {
		t.Fatal(e)
	}
}
func TestCohortRetryAndPublishBounds(t *testing.T) {
	pool, ctx, r, v := cohortJobSetup(t)
	j, e := r.AcceptCohortJob(ctx, v)
	if e != nil {
		t.Fatal(e)
	}
	publisher := strings.Repeat("3", 32)
	l, e := r.ClaimCohortOutbox(ctx, publisher)
	if e != nil || l == nil {
		t.Fatal(e)
	}
	wrong := *l
	wrong.Token = strings.Repeat("f", 32)
	if e = r.CompleteCohortPublish(ctx, wrong, "confirmed"); e != nil {
		t.Fatal(e)
	}
	s, e := r.CohortJobStatus(ctx, j.ID)
	if e != nil || s.State != domain.CohortJobPendingDispatch {
		t.Fatal(s, e)
	}
	if e = r.CompleteCohortPublish(ctx, *l, "confirmed"); e != nil {
		t.Fatal(e)
	}
	s, e = r.CohortJobStatus(ctx, j.ID)
	if e != nil || s.State != domain.CohortJobQueued {
		t.Fatal(s, e)
	}
	claim, e := r.ClaimCohortJob(ctx, cohortMessage(t, pool, j.ID), strings.Repeat("1", 32))
	if e != nil || claim == nil {
		t.Fatal(e)
	}
	defer claim.Close()
	d, e := r.FinishCohortJob(ctx, claim.Lease(), &domain.CohortJobFailure{Code: "arbitrary SQL secret", Retryable: true})
	if e != nil || d.Status.State != domain.CohortJobFailed || d.Status.ErrorCode == nil || *d.Status.ErrorCode != "execution_failed" {
		t.Fatal(d, e)
	}
	var receipts int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM cohort_job_inbox WHERE job_id=$1 AND state='completed'", j.ID).Scan(&receipts)
	if receipts != 1 {
		t.Fatal(receipts)
	}
}
func TestCohortFrozenStorageImmutable(t *testing.T) {
	pool, ctx, r, v := cohortJobSetup(t)
	j, e := r.AcceptCohortJob(ctx, v)
	if e != nil {
		t.Fatal(e)
	}
	for _, query := range []string{`UPDATE cohort_job_manifest SET fixture_json=fixture_json WHERE job_id=$1`, `DELETE FROM cohort_job_member WHERE job_id=$1`} {
		if _, e = pool.Exec(ctx, query, j.ID); e == nil {
			t.Fatal("immutable mutation accepted")
		}
	}
	var count int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM cohort_job_member WHERE job_id=$1", j.ID).Scan(&count)
	if count != 100 {
		t.Fatal(count)
	}
}
func TestCohortRunIDOverflowRollsBack(t *testing.T) {
	pool, ctx, r, v := cohortJobSetup(t)
	if _, e := pool.Exec(ctx, `SELECT setval(pg_get_serial_sequence('ml_run','id'),2147483647,true)`); e != nil {
		t.Fatal(e)
	}
	if _, e := r.AcceptCohortJob(ctx, v); e == nil {
		t.Fatal("overflow accepted")
	}
	var n int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM cohort_job").Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM ml_run").Scan(&n)
	if n != 0 {
		t.Fatal("orphan run", n)
	}
}
func TestCohortAdmissionDeniesForgedScopeAndActor(t *testing.T) {
	pool, ctx, r, v := cohortJobSetup(t)
	if _, e := pool.Exec(ctx, `UPDATE auth_user SET scope='{"funds":[],"ngdus":null,"departments":null}' WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	id := int64(1)
	forged := domain.WithPrincipal(ctx, domain.Principal{AccountID: &id, Authenticated: true, Mechanism: "session", Role: "admin", Scope: domain.AccessScope{Funds: []string{"presentation"}}})
	if _, e := r.AcceptCohortJob(forged, v); e == nil {
		t.Fatal("forged client scope granted authority")
	}
	v.ActorAccountID = 2
	if _, e := r.AcceptCohortJob(ctx, v); e == nil {
		t.Fatal("forged actor accepted")
	}
}
func TestCohortUnknownDatabaseFailureNotRevocation(t *testing.T) {
	pool, ctx, r, v := cohortJobSetup(t)
	j, e := r.AcceptCohortJob(ctx, v)
	if e != nil {
		t.Fatal(e)
	}
	claim, e := r.ClaimCohortJob(ctx, cohortMessage(t, pool, j.ID), strings.Repeat("1", 32))
	if e != nil || claim == nil {
		t.Fatal(e)
	}
	defer claim.Close()
	if _, e = pool.Exec(ctx, "ALTER TABLE cohort_job_guard RENAME TO unavailable_guard"); e != nil {
		t.Fatal(e)
	}
	if _, e = r.NextCohortMember(ctx, claim.Lease()); e == nil {
		t.Fatal("unknown DB outage hidden")
	}
	var state string
	if e = pool.QueryRow(ctx, "SELECT state FROM cohort_job WHERE id=$1", j.ID).Scan(&state); e != nil || state != "running" {
		t.Fatal("unknown DB outage guessed revoked", state, e)
	}
	if _, e = pool.Exec(ctx, "ALTER TABLE unavailable_guard RENAME TO cohort_job_guard"); e != nil {
		t.Fatal(e)
	}
	if _, e = r.NextCohortMember(ctx, claim.Lease()); e != nil {
		t.Fatal("recovery failed", e)
	}
}
func TestCohortPhysicalSessionLossAbortsTransaction(t *testing.T) {
	for _, stage := range []string{"checkpoint", "final"} {
		t.Run(stage, func(t *testing.T) {
			pool, ctx, r, v := cohortInputJobSetup(t)
			j, e := r.AcceptCohortJob(ctx, v)
			if e != nil {
				t.Fatal(e)
			}
			claim, e := r.ClaimCohortJob(ctx, cohortMessage(t, pool, j.ID), strings.Repeat("1", 32))
			if e != nil || claim == nil {
				t.Fatal(e)
			}
			defer claim.Close()
			var checkpoint domain.CohortCheckpoint
			if stage == "final" {
				for i := 0; i < 100; i++ {
					x, e := r.NextCohortMember(ctx, claim.Lease())
					if e != nil || x == nil {
						t.Fatal(e)
					}
					var accepted domain.CohortCheckpoint
					if x.Member.Input != nil {
						accepted = cohortScoredCheckpoint(t, *x)
					} else {
						accepted = cohortMarkerCheckpoint(t, *x)
					}
					if e = r.CheckpointCohortMember(ctx, claim.Lease(), accepted); e != nil {
						t.Fatal(e)
					}
				}
			} else {
				x, e := r.NextCohortMember(ctx, claim.Lease())
				if e != nil {
					t.Fatal(e)
				}
				if x.Member.Input != nil {
					checkpoint = cohortScoredCheckpoint(t, *x)
				} else {
					checkpoint = cohortMarkerCheckpoint(t, *x)
				}
			}
			blocker, e := pool.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer blocker.Rollback(ctx)
			if _, e = blocker.Exec(ctx, "SELECT pg_advisory_xact_lock(74019444)"); e != nil {
				t.Fatal(e)
			}
			target := "cohort_job_checkpoint"
			if stage == "final" {
				target = "ml_run_snapshot"
			}
			_, e = pool.Exec(ctx, `CREATE FUNCTION q2_physical_barrier() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM pg_advisory_xact_lock(74019444);RETURN NEW;END$$;CREATE TRIGGER q2_physical_pause BEFORE INSERT ON `+target+` FOR EACH ROW EXECUTE FUNCTION q2_physical_barrier()`)
			if e != nil {
				t.Fatal(e)
			}
			result := make(chan error, 1)
			go func() {
				if stage == "final" {
					_, e := r.FinishCohortJob(ctx, claim.Lease(), nil)
					result <- e
				} else {
					result <- r.CheckpointCohortMember(ctx, claim.Lease(), checkpoint)
				}
			}()
			deadline := time.Now().Add(3 * time.Second)
			blocked := false
			for time.Now().Before(deadline) {
				var waiting bool
				if e = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock' AND wait_event='advisory')`, claim.Lease().PhysicalBackendPID).Scan(&waiting); e != nil {
					t.Fatal(e)
				}
				if waiting {
					blocked = true
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !blocked {
				t.Fatal("claim backend did not own in-flight transaction")
			}
			var killed bool
			if e = pool.QueryRow(ctx, "SELECT pg_terminate_backend($1)", claim.Lease().PhysicalBackendPID).Scan(&killed); e != nil || !killed {
				t.Fatal(killed, e)
			}
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("terminated transaction reported success")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("terminated transaction did not abort")
			}
			var snapshots, alerts int
			_ = pool.QueryRow(ctx, "SELECT count(*) FROM ml_run_snapshot WHERE run_id=$1", j.RunID).Scan(&snapshots)
			_ = pool.QueryRow(ctx, "SELECT count(*) FROM ml_alert WHERE run_id=$1", j.RunID).Scan(&alerts)
			if snapshots != 0 || alerts != 0 {
				t.Fatal("partial publication", snapshots, alerts)
			}
			if stage == "checkpoint" {
				var n int
				_ = pool.QueryRow(ctx, "SELECT count(*) FROM cohort_job_checkpoint WHERE job_id=$1", j.ID).Scan(&n)
				if n != 0 {
					t.Fatal("stale checkpoint", n)
				}
			}
		})
	}
}
