package service

import (
	"context"
	"errors"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type CollectionJobWorker struct {
	store     CollectionJobStore
	source    CollectionSource
	workerID  string
	heartbeat time.Duration
}

func NewCollectionJobWorker(store CollectionJobStore, source CollectionSource, id string) (*CollectionJobWorker, error) {
	if store == nil || source == nil || !collectionOpaque.MatchString(id) {
		return nil, domain.NewError(domain.Invalid, "invalid_collection_request")
	}
	return &CollectionJobWorker{store, source, id, 5 * time.Second}, nil
}
func collectionDisposition(d domain.CollectionDisposition, e error) error {
	if e != nil {
		return e
	}
	switch d.Kind {
	case "applied", "retry", "terminal", "stale", "ignored":
		return nil
	default:
		return domain.NewError(domain.Unavailable, "collection_store_unavailable")
	}
}
func (w *CollectionJobWorker) finish(ctx context.Context, l domain.CollectionLease, f domain.CollectionFailure) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	d, e := w.store.FinishCollectionFailure(bounded, l, f)
	return collectionDisposition(d, e)
}
func collectionSourceFailure(e error) domain.CollectionFailure {
	if errors.Is(e, context.DeadlineExceeded) {
		return domain.CollectionFailure{Code: domain.CollectionCodeSourceTimeout, Retryable: true}
	}
	var de *domain.Error
	if errors.As(e, &de) {
		switch domain.CollectionCode(de.Message) {
		case domain.CollectionCodeInputLimit, domain.CollectionCodeInvalidCapture, domain.CollectionCodeInputConflict:
			return domain.CollectionFailure{Code: domain.CollectionCode(de.Message)}
		case domain.CollectionCodeSourceUnavailable, domain.CollectionCodeSourceTimeout:
			return domain.CollectionFailure{Code: domain.CollectionCode(de.Message), Retryable: de.Kind == domain.Unavailable}
		}
	}
	return domain.CollectionFailure{Code: domain.CollectionCodeSourceUnavailable, Retryable: true}
}
func (w *CollectionJobWorker) HandleDelivery(ctx context.Context, msg domain.CollectionMessage) error {
	claim, e := w.store.ClaimCollectionJob(ctx, msg, w.workerID)
	if e != nil || claim == nil {
		return e
	}
	defer claim.Close()
	lease := claim.Lease()
	deadline := time.Now().Add(60 * time.Second)
	if !lease.AttemptDeadline.IsZero() && lease.AttemptDeadline.Before(deadline) {
		deadline = lease.AttemptDeadline
	}
	run, cancel := context.WithDeadline(ctx, deadline)
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
			case <-run.Done():
				renewed <- nil
				return
			case <-ticker.C:
				bounded, done := context.WithTimeout(run, 5*time.Second)
				e := w.store.RenewCollectionJob(bounded, lease)
				done()
				if e != nil {
					cancel()
					renewed <- e
					return
				}
			}
		}
	}()
	result := w.execute(run, lease)
	close(stop)
	renewErr := <-renewed
	if renewErr != nil {
		return renewErr
	}
	return result
}
func (w *CollectionJobWorker) execute(ctx context.Context, l domain.CollectionLease) error {
	work, e := w.store.LoadCollectionWork(ctx, l)
	if e != nil {
		var de *domain.Error
		if errors.As(e, &de) && de.Message == "capture_corrupt" {
			return w.finish(ctx, l, domain.CollectionFailure{Code: domain.CollectionCodeCaptureCorrupt})
		}
		return e
	}
	if work.Capture == nil {
		raw, e := w.source.FetchCollection(ctx, work.Intent)
		if e != nil {
			return w.finish(ctx, l, collectionSourceFailure(e))
		}
		capture, e := domain.PrepareCollectionCapture(work.Intent, raw)
		if e != nil {
			return w.finish(ctx, l, collectionSourceFailure(e))
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if e = w.store.SaveCollectionCapture(ctx, l, capture); e != nil {
			return e
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	d, e := w.store.ApplyCollectionCapture(ctx, l)
	if e != nil {
		var de *domain.Error
		if errors.As(e, &de) {
			switch domain.CollectionCode(de.Message) {
			case domain.CollectionCodeMeasurementConflict, domain.CollectionCodeUnverifiableExisting, domain.CollectionCodeCaptureCorrupt:
				return w.finish(ctx, l, domain.CollectionFailure{Code: domain.CollectionCode(de.Message)})
			}
		}
		return e
	}
	return collectionDisposition(d, nil)
}
