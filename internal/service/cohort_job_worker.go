package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type CohortJobWorker struct {
	store     CohortJobStore
	scorer    CohortPinnedScorer
	workerID  string
	heartbeat time.Duration
}

func NewCohortJobWorker(store CohortJobStore, scorer CohortPinnedScorer, workerID string) (*CohortJobWorker, error) {
	if store == nil || scorer == nil || !cohortOpaqueID.MatchString(workerID) {
		return nil, domain.NewError(domain.Invalid, "invalid_cohort_job_worker")
	}
	return &CohortJobWorker{store: store, scorer: scorer, workerID: workerID, heartbeat: 10 * time.Second}, nil
}
func (w *CohortJobWorker) finish(ctx context.Context, lease domain.CohortJobLease, failure *domain.CohortJobFailure) error {
	final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	disposition, err := w.store.FinishCohortJob(final, lease, failure)
	if err != nil {
		return err
	}
	if disposition.Kind != "ack" && disposition.Kind != "stale" {
		return domain.NewError(domain.Unavailable, "job_disposition_unknown")
	}
	return nil
}
func (w *CohortJobWorker) storeError(ctx context.Context, lease domain.CohortJobLease, err error) error {
	var de *domain.Error
	if !errors.As(err, &de) {
		return err
	}
	if de.Kind == domain.Limited && de.Message == "cohort_result_capacity" {
		return w.finish(ctx, lease, &domain.CohortJobFailure{Code: "cohort_result_capacity"})
	}
	switch de.Message {
	case "input_unavailable", "input_corrupt":
		return w.finish(ctx, lease, &domain.CohortJobFailure{Code: de.Message})
	case "cancelled", "access_revoked", "job_deadline", "stale_lease":
		return w.finish(ctx, lease, &domain.CohortJobFailure{Code: "execution_interrupted"})
	default:
		return err
	}
}
func cohortComputeFailure(err error) *domain.CohortJobFailure {
	if errors.Is(err, context.DeadlineExceeded) {
		return &domain.CohortJobFailure{Code: "ml_deadline", Retryable: true}
	}
	var de *domain.Error
	if errors.As(err, &de) {
		switch de.Message {
		case "ml_unavailable", "ml_deadline", "execution_busy":
			return &domain.CohortJobFailure{Code: de.Message, Retryable: true}
		case "unsupported_verdict", "bundle_unavailable", "bundle_corrupt", "pin_mismatch", "policy_mismatch", "recipe_mismatch", "runtime_mismatch", "schema_mismatch", "unsupported_context_version", "unsupported_feature_recipe", "pinned_protocol_unavailable", "invalid_pinned_input", "pinned_resource_limit", "result_identity_mismatch", "input_corrupt":
			return &domain.CohortJobFailure{Code: de.Message}
		}
	}
	return &domain.CohortJobFailure{Code: "pinned_execution_unavailable"}
}
func (w *CohortJobWorker) HandleDelivery(ctx context.Context, msg domain.CohortJobMessage) error {
	claim, err := w.store.ClaimCohortJob(ctx, msg, w.workerID)
	if err != nil || claim == nil {
		return err
	}
	defer claim.Close()
	lease := claim.Lease()
	deadline := time.Now().Add(20 * time.Minute)
	if d := lease.DeadlineAt.Add(-10 * time.Second); d.Before(deadline) {
		deadline = d
	}
	compute, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	stopped := make(chan struct{})
	renewed := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(w.heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-stopped:
				renewed <- nil
				return
			case <-compute.Done():
				renewed <- nil
				return
			case <-ticker.C:
				renewal, done := context.WithTimeout(compute, 5*time.Second)
				e := claim.Alive(renewal)
				if e == nil {
					e = w.store.RenewCohortJob(renewal, lease)
				}
				done()
				if e != nil {
					cancel()
					renewed <- e
					return
				}
			}
		}
	}()
	loopErr := w.execute(compute, claim, lease)
	close(stopped)
	renewErr := <-renewed
	if renewErr != nil {
		return w.storeError(ctx, lease, renewErr)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if loopErr != nil {
		var computeErr *cohortInferenceError
		if errors.As(loopErr, &computeErr) {
			return w.finish(ctx, lease, cohortComputeFailure(computeErr.err))
		}
		return w.storeError(ctx, lease, loopErr)
	}
	return w.finish(ctx, lease, nil)
}

// cohortInferenceError отделяет RPC/validation failure от неизвестного DB outcome.
type cohortInferenceError struct{ err error }

func (e *cohortInferenceError) Error() string { return e.err.Error() }
func (e *cohortInferenceError) Unwrap() error { return e.err }
func (w *CohortJobWorker) execute(ctx context.Context, claim CohortJobClaim, lease domain.CohortJobLease) error {
	for {
		if err := ctx.Err(); err != nil {
			return &cohortInferenceError{err}
		}
		alive, done := context.WithTimeout(ctx, 5*time.Second)
		err := claim.Alive(alive)
		done()
		if err != nil {
			return err
		}
		execution, err := w.store.NextCohortMember(ctx, lease)
		if err != nil {
			return err
		}
		if execution == nil {
			return nil
		}
		if execution.JobID != lease.JobID {
			return &cohortInferenceError{domain.NewError(domain.Invalid, "input_corrupt")}
		}
		checkpoint, err := w.computeMember(ctx, *execution)
		if err != nil {
			return &cohortInferenceError{err}
		}
		alive, done = context.WithTimeout(ctx, 5*time.Second)
		err = claim.Alive(alive)
		done()
		if err != nil {
			return err
		}
		if err = w.store.CheckpointCohortMember(ctx, lease, checkpoint); err != nil {
			return err
		}
	}
}
func (w *CohortJobWorker) computeMember(ctx context.Context, e domain.CohortMemberExecution) (domain.CohortCheckpoint, error) {
	pinSHA, pinErr := domain.CohortExecutionPinSHA256(e.ExecutionPin)
	if pinErr != nil || pinSHA != e.ExecutionPinSHA256 || !domain.ValidFileSHA256(e.InputManifestSHA256) {
		return domain.CohortCheckpoint{}, domain.NewError(domain.Invalid, "input_corrupt")
	}
	member := e.Member
	out := domain.CohortOutcome{AppWellID: member.Well.ID, Metadata: member.Well}
	fingerprint := ""
	if member.NoComputeReason != nil {
		if member.Input != nil {
			return domain.CohortCheckpoint{}, domain.NewError(domain.Invalid, "input_corrupt")
		}
		out.ReasonCode = *member.NoComputeReason
		switch out.ReasonCode {
		case "archived":
			out.Status = "archived"
		case "inactive":
			out.Status = "inactive"
		case "insufficient_telemetry":
			out.Status = "insufficient"
		case "synthetic_source_unavailable":
			out.Status = "error"
		default:
			return domain.CohortCheckpoint{}, domain.NewError(domain.Invalid, "input_corrupt")
		}
	} else {
		input := member.Input
		if input == nil || input.Identity.SourceKind != "cohort_snapshot" || !domain.ValidFileSHA256(input.Identity.FingerprintSHA256) || !cohortOpaqueID.MatchString(input.Identity.InputSnapshotID) || int64(len(input.Canonical)) != input.Identity.CanonicalBytes || len(input.Canonical) == 0 || len(input.Canonical) > 512<<10 || domain.FileSHA256(input.Canonical) != input.Identity.CanonicalSHA256 {
			return domain.CohortCheckpoint{}, domain.NewError(domain.Invalid, "input_corrupt")
		}
		if err := e.ExecutionPin.Validate(); err != nil {
			return domain.CohortCheckpoint{}, err
		}
		fingerprint = input.Identity.FingerprintSHA256
		call, cancel := context.WithTimeout(ctx, 10*time.Second)
		result, err := w.scorer.UploadCohortPinned(call, e.ExecutionPin, input.Identity, bytes.NewReader(input.Canonical))
		cancel()
		if err != nil {
			return domain.CohortCheckpoint{}, err
		}
		if err = domain.ValidateCohortPinnedResult(*input, e.ExecutionPin, result); err != nil {
			return domain.CohortCheckpoint{}, err
		}
		out.InputSHA256 = strings.TrimPrefix(input.Identity.CanonicalSHA256, "sha256:")
		out.Bounds = &input.Bounds
		out.InputWatermark = input.Watermark
		out.Resolution = input.Resolution
		out.Provenance = result.Provenance
		out.DecisionRule = result.DecisionRule
		out.Diagnostics = &domain.CohortDiagnostics{Verdict: result.Verdict, NWindows: result.NWindows, WindowsRunning: result.WindowsRunning, ChannelsUsed: result.ChannelsUsed, FeaturesUnavailable: result.FeaturesUnavailable}
		if result.Verdict == "insufficient" {
			out.Status = "insufficient"
			out.ReasonCode = "insufficient_running_windows"
		} else {
			out.Status = "scored"
			out.ReasonCode = "evaluated"
			out.Result = &result
		}
	}
	cp := domain.CohortCheckpoint{JobID: e.JobID, WellID: member.Well.ID, InputFingerprintSHA256: fingerprint, ExecutionPinSHA256: e.ExecutionPinSHA256, InputManifestSHA256: e.InputManifestSHA256, Outcome: out}
	var err error
	cp.DigestSHA256, err = domain.CohortCheckpointDigest(cp)
	if err != nil {
		return cp, err
	}
	if err = domain.ValidateCohortCheckpoint(e, cp); err != nil {
		return cp, err
	}
	return cp, nil
}
