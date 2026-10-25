package integration

import (
	"errors"
	"strings"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestCohortCancelAfterSuccessReturnsActualTerminalDTO(t *testing.T) {
	pool, ctx, store, input := cohortJobSetup(t)
	accepted, err := store.AcceptCohortJob(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimCohortJob(ctx, cohortMessage(t, pool, accepted.ID), strings.Repeat("a", 32))
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	defer claim.Close()
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
	completed, err := store.FinishCohortJob(ctx, claim.Lease(), nil)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := store.CancelCohortJob(ctx, accepted.ID, completed.Status.Revision)
	var conflict *domain.Error
	if !errors.As(err, &conflict) || conflict.Kind != domain.Duplicate || conflict.Message != "job_terminal" {
		t.Fatal("expected terminal conflict", err)
	}
	if actual.ID != accepted.ID || actual.RunID != accepted.RunID || actual.State != domain.CohortJobSucceeded || actual.SnapshotID != accepted.SnapshotID || actual.CompletedCount != 100 || actual.Revision != completed.Status.Revision || actual.FinishedAt == nil {
		t.Fatalf("terminal status lost: %+v", actual)
	}
	persisted, err := store.CohortJobStatus(ctx, accepted.ID)
	if err != nil || persisted.State != domain.CohortJobSucceeded || persisted.Revision != actual.Revision {
		t.Fatal("cancel changed completed job", persisted, err)
	}
}

func TestCohortResultCapacityFailureKeepsSafeReason(t *testing.T) {
	pool, ctx, store, input := cohortJobSetup(t)
	accepted, err := store.AcceptCohortJob(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimCohortJob(ctx, cohortMessage(t, pool, accepted.ID), strings.Repeat("a", 32))
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	defer claim.Close()
	disposition, err := store.FinishCohortJob(ctx, claim.Lease(), &domain.CohortJobFailure{Code: "cohort_result_capacity", Retryable: false})
	if err != nil || disposition.Kind != "ack" || disposition.Status.State != domain.CohortJobFailed || disposition.Status.ErrorCode == nil || *disposition.Status.ErrorCode != "cohort_result_capacity" || disposition.Status.NextAttemptAt != nil {
		t.Fatal("deterministic safe reason lost", disposition, err)
	}
	var state, reason string
	if err = pool.QueryRow(ctx, "SELECT status,error FROM ml_run WHERE id=$1", accepted.RunID).Scan(&state, &reason); err != nil || state != "error" || reason != "cohort_result_capacity" {
		t.Fatal(state, reason, err)
	}
	var snapshots int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM ml_run_snapshot WHERE run_id=$1", accepted.RunID).Scan(&snapshots); err != nil || snapshots != 0 {
		t.Fatal(snapshots, err)
	}
}
