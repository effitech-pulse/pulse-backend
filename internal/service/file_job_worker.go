package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type FileJobWorker struct {
	store     FileJobStore
	checks    *FileChecks
	workerID  string
	heartbeat time.Duration
}

func NewFileJobWorker(store FileJobStore, checks *FileChecks, workerID string) (*FileJobWorker, error) {
	if store == nil || checks == nil || len(workerID) != 32 || strings.Trim(workerID, "0123456789abcdef") != "" {
		return nil, fileInvalid("invalid_file_job_worker")
	}
	return &FileJobWorker{store, checks, workerID, 10 * time.Second}, nil
}
func fileJobFailure(err error) *domain.FileJobFailure {
	if errors.Is(err, context.DeadlineExceeded) {
		return &domain.FileJobFailure{Code: "ml_deadline", Retryable: true}
	}
	var de *domain.Error
	if errors.As(err, &de) {
		switch de.Message {
		case "execution_busy":
			return &domain.FileJobFailure{Code: "execution_busy", Retryable: true}
		case "ml_unavailable", "ml_deadline":
			return &domain.FileJobFailure{Code: de.Message, Retryable: true}
		}
	}
	return &domain.FileJobFailure{Code: "pinned_input_unavailable"}
}
func (w *FileJobWorker) HandleDelivery(ctx context.Context, msg domain.FileJobMessage) error {
	lease, e := w.store.ClaimFileJob(ctx, msg, w.workerID)
	if e != nil || lease == nil {
		return e
	}
	execution, e := w.store.ExecutionFileJob(ctx, *lease)
	if e != nil {
		// Unknown DB/IO/commit outcomes do not establish immutable-input failure.
		// Only explicit store semantics may enter fenced durable finalization.
		var problem *domain.FileJobProblem
		if !errors.As(e, &problem) {
			return e
		}
		switch problem.Code {
		case "input_unavailable", "input_corrupt":
			return w.finish(ctx, *lease, nil, &domain.FileJobFailure{Code: "pinned_input_unavailable"})
		case "cancelled", "access_revoked", "job_deadline", "stale_lease":
			return w.finish(ctx, *lease, nil, &domain.FileJobFailure{Code: "execution_interrupted"})
		default:
			return e
		}
	}
	// Admission deadline is immutable; reserve ten seconds for durable finalization.
	deadline := execution.Input.CreatedAt.Add(20*time.Minute - 10*time.Second)
	computeCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	stop := make(chan struct{})
	renewed := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(w.heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				renewed <- nil
				return
			case <-computeCtx.Done():
				renewed <- nil
				return
			case <-ticker.C:
				heartbeatCtx, done := context.WithTimeout(computeCtx, 5*time.Second)
				err := w.store.RenewFileJob(heartbeatCtx, *lease)
				done()
				if err != nil {
					cancel()
					renewed <- err
					return
				}
			}
		}
	}()
	result, computeErr := w.checks.computePreparedInput(computeCtx, execution.Input)
	close(stop)
	renewErr := <-renewed
	if renewErr != nil {
		// A known fence/cancel/rights rejection can be durably resolved by Finish.
		// An unknown DB failure must leave the delivery unacked.
		var de *domain.Error
		if !errors.As(renewErr, &de) {
			return renewErr
		}
		return w.finish(ctx, *lease, nil, &domain.FileJobFailure{Code: "execution_interrupted"})
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if computeErr != nil {
		return w.finish(ctx, *lease, nil, fileJobFailure(computeErr))
	}
	return w.finish(ctx, *lease, &result, nil)
}
func (w *FileJobWorker) finish(ctx context.Context, lease domain.FileJobLease, result *domain.StoredFileResult, failure *domain.FileJobFailure) error {
	finalize, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	disposition, e := w.store.FinishFileJob(finalize, lease, result, failure)
	if e != nil {
		return e
	}
	if disposition.Kind != "ack" && disposition.Kind != "stale" {
		return domain.NewError(domain.Unavailable, "job_disposition_unknown")
	}
	return nil
}
