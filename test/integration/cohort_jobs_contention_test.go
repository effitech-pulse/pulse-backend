package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestCohortOwnedProgressSurvivesBriefCapacityGateContention(t *testing.T) {
	for _, operation := range []string{"next", "renew", "checkpoint", "final"} {
		t.Run(operation, func(t *testing.T) {
			pool, ctx, store, prepared := cohortJobSetup(t)
			accepted, err := store.AcceptCohortJob(ctx, prepared)
			if err != nil {
				t.Fatal(err)
			}
			claim, err := store.ClaimCohortJob(ctx, cohortMessage(t, pool, accepted.ID), strings.Repeat("c", 32))
			if err != nil || claim == nil {
				t.Fatal(err)
			}
			defer claim.Close()
			var checkpoint domain.CohortCheckpoint
			if operation == "checkpoint" {
				execution, err := store.NextCohortMember(ctx, claim.Lease())
				if err != nil {
					t.Fatal(err)
				}
				checkpoint = cohortMarkerCheckpoint(t, *execution)
			}
			if operation == "final" {
				for i := 0; i < 100; i++ {
					renewCohortFixture(t, ctx, store, claim.Lease(), i)
					execution, err := store.NextCohortMember(ctx, claim.Lease())
					if err != nil || execution == nil {
						t.Fatal(i, err)
					}
					if err = store.CheckpointCohortMember(ctx, claim.Lease(), cohortMarkerCheckpoint(t, *execution)); err != nil {
						t.Fatal(i, err)
					}
				}
			}
			holder, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback(ctx)
			if _, err = holder.Exec(ctx, "SELECT pg_advisory_xact_lock(74019323)"); err != nil {
				t.Fatal(err)
			}
			released := make(chan error, 1)
			go func() { time.Sleep(50 * time.Millisecond); released <- holder.Commit(ctx) }()
			switch operation {
			case "next":
				_, err = store.NextCohortMember(ctx, claim.Lease())
			case "renew":
				err = store.RenewCohortJob(ctx, claim.Lease())
			case "checkpoint":
				err = store.CheckpointCohortMember(ctx, claim.Lease(), checkpoint)
			case "final":
				_, err = store.FinishCohortJob(ctx, claim.Lease(), nil)
			}
			if releaseErr := <-released; releaseErr != nil {
				t.Fatal(releaseErr)
			}
			if err != nil {
				actual, getErr := store.CohortJobStatus(ctx, accepted.ID)
				t.Fatalf("healthy owned %s aborted by brief gate contention: err=%v state=%s attempt=%d checkpoints=%d statusErr=%v", operation, err, actual.State, actual.AttemptCount, actual.CompletedCount, getErr)
			}
			actual, err := store.CohortJobStatus(ctx, accepted.ID)
			wantState := domain.CohortJobRunning
			wantCompleted := 0
			if operation == "final" {
				wantState = domain.CohortJobSucceeded
				wantCompleted = 100
			} else if operation == "checkpoint" {
				wantCompleted = 1
			}
			if err != nil || actual.State != wantState || actual.AttemptCount != 1 || actual.CompletedCount != wantCompleted || actual.NextAttemptAt != nil {
				t.Fatal("gate contention changed claim generation/attempt/progress", actual, err)
			}
			if err = claim.Alive(ctx); err != nil {
				t.Fatal("healthy claim closed", err)
			}
			var starts int
			if err = pool.QueryRow(ctx, "SELECT count(*) FROM cohort_job_member_attempt WHERE job_id=$1", accepted.ID).Scan(&starts); err != nil || starts != 0 {
				t.Fatal("gate wait spent compute attempts", starts, err)
			}

		})
	}
}

func TestCohortOwnedGateDeadlineDoesNotChangeDurableState(t *testing.T) {
	pool, ctx, store, prepared := cohortJobSetup(t)
	accepted, err := store.AcceptCohortJob(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimCohortJob(ctx, cohortMessage(t, pool, accepted.ID), strings.Repeat("d", 32))
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	defer claim.Close()
	execution, err := store.NextCohortMember(ctx, claim.Lease())
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := cohortMarkerCheckpoint(t, *execution)
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err = holder.Exec(ctx, "SELECT pg_advisory_xact_lock(74019323)"); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = store.CheckpointCohortMember(bounded, claim.Lease(), checkpoint)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("expected bounded gate deadline", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("owned gate exceeded caller deadline")
	}
	if err = holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	actual, err := store.CohortJobStatus(ctx, accepted.ID)
	if err != nil || actual.State != domain.CohortJobRunning || actual.AttemptCount != 1 || actual.CompletedCount != 0 || actual.NextAttemptAt != nil {
		t.Fatal("deadline guessed a durable disposition", actual, err)
	}
	var fence int64
	if err = pool.QueryRow(ctx, "SELECT fence FROM cohort_job WHERE id=$1", accepted.ID).Scan(&fence); err != nil || fence != claim.Lease().Fence {
		t.Fatal("timeout modified fence", fence, err)
	}
}

func TestCohortOwnedGateWaitRechecksAuthorityWithoutLockCycle(t *testing.T) {
	pool, ctx, store, prepared := cohortJobSetup(t)
	accepted, err := store.AcceptCohortJob(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimCohortJob(ctx, cohortMessage(t, pool, accepted.ID), strings.Repeat("e", 32))
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	defer claim.Close()
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err = holder.Exec(ctx, "SELECT pg_advisory_xact_lock(74019323)"); err != nil {
		t.Fatal(err)
	}
	outcome := make(chan error, 1)
	go func() { _, err := store.NextCohortMember(ctx, claim.Lease()); outcome <- err }()
	waiting := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event_type='Lock' AND wait_event='advisory')`, claim.Lease().PhysicalBackendPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !waiting {
		t.Fatal("owned runtime did not wait before row locks")
	}
	var physical bool
	started := time.Now()
	if err = holder.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(74019321)").Scan(&physical); err != nil || physical || time.Since(started) > 100*time.Millisecond {
		t.Fatal("gate holder blocked on physical claim", physical, err)
	}
	if _, err = pool.Exec(ctx, "UPDATE auth_user SET active=false,revision=revision+1 WHERE id=1"); err != nil {
		t.Fatal("owner took authority rows before gate", err)
	}
	if err = holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	err = <-outcome
	var denied *domain.Error
	if !errors.As(err, &denied) || denied.Message != "access_revoked" {
		t.Fatal("post-wait authority not checked", err)
	}
	var state string
	var checkpoints int
	if err = pool.QueryRow(ctx, "SELECT state,(SELECT count(*) FROM cohort_job_checkpoint WHERE job_id=$1) FROM cohort_job WHERE id=$1", accepted.ID).Scan(&state, &checkpoints); err != nil || state != "revoked" || checkpoints != 0 {
		t.Fatal(state, checkpoints, err)
	}
}

func TestCohortOwnedNextGateWaitRecordsOnlyIntendedStart(t *testing.T) {
	pool, ctx, store, prepared := cohortInputJobSetup(t)
	accepted, err := store.AcceptCohortJob(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimCohortJob(ctx, cohortMessage(t, pool, accepted.ID), strings.Repeat("f", 32))
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	defer claim.Close()
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err = holder.Exec(ctx, "SELECT pg_advisory_xact_lock(74019323)"); err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	go func() { time.Sleep(50 * time.Millisecond); released <- holder.Commit(ctx) }()
	execution, err := store.NextCohortMember(ctx, claim.Lease())
	if releaseErr := <-released; releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if err != nil || execution == nil || execution.Member.Input == nil || execution.Attempt != 1 {
		t.Fatal(execution, err)
	}
	var attempts, starts, claims int
	if err = pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM cohort_job_member_attempt WHERE job_id=$1),compute_starts,attempt_count FROM cohort_job WHERE id=$1", accepted.ID).Scan(&attempts, &starts, &claims); err != nil || attempts != 1 || starts != 1 || claims != 1 {
		t.Fatal(attempts, starts, claims, err)
	}
	if err = claim.Alive(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCohortOwnedGateWaitCannotRenewExpiredLease(t *testing.T) {
	pool, ctx, store, prepared := cohortJobSetup(t)
	accepted, err := store.AcceptCohortJob(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimCohortJob(ctx, cohortMessage(t, pool, accepted.ID), strings.Repeat("a", 32))
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	defer claim.Close()
	var seconds float64
	var until time.Time
	if err = pool.QueryRow(ctx, "SELECT lease_until,extract(epoch FROM (lease_until-clock_timestamp()))::double precision FROM cohort_job WHERE id=$1", accepted.ID).Scan(&until, &seconds); err != nil {
		t.Fatal(err)
	}
	// Observe real server lease expiry: no SQL clock/state rewriting and no renewal.
	remaining := time.Duration(seconds * float64(time.Second))
	if remaining > 2*time.Second {
		time.Sleep(remaining - 2*time.Second)
	}
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(ctx)
	if _, err = holder.Exec(ctx, "SELECT pg_advisory_xact_lock(74019323)"); err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	go func() { time.Sleep(3 * time.Second); released <- holder.Commit(ctx) }()
	err = store.RenewCohortJob(ctx, claim.Lease())
	if releaseErr := <-released; releaseErr != nil {
		t.Fatal(releaseErr)
	}
	var denied *domain.Error
	if !errors.As(err, &denied) || denied.Message != "stale_lease" {
		t.Fatal("expired lease renewed after gate wait", err)
	}
	var actualUntil time.Time
	var state string
	var attempt int
	if err = pool.QueryRow(ctx, "SELECT lease_until,state,attempt_count FROM cohort_job WHERE id=$1", accepted.ID).Scan(&actualUntil, &state, &attempt); err != nil || !until.Equal(actualUntil) || state != "running" || attempt != 1 {
		t.Fatal("expired claim mutated", actualUntil, state, attempt, err)
	}
}
