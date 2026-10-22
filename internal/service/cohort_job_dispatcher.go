package service

import (
	"context"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type CohortJobDispatcher struct {
	store     CohortJobStore
	publisher CohortPublisher
	owner     string
}

func NewCohortJobDispatcher(store CohortJobStore, publisher CohortPublisher, owner string) (*CohortJobDispatcher, error) {
	if store == nil || publisher == nil || owner == "" {
		return nil, domain.NewError(domain.Invalid, "invalid_cohort_job_dispatcher")
	}
	return &CohortJobDispatcher{store: store, publisher: publisher, owner: owner}, nil
}
func (d *CohortJobDispatcher) DispatchOnce(ctx context.Context) error {
	lease, err := d.store.ClaimCohortOutbox(ctx, d.owner)
	if err != nil || lease == nil {
		return err
	}
	outcome, publishErr := d.publisher.Publish(ctx, lease.Message)
	if publishErr != nil {
		outcome = "unknown"
	}
	switch outcome {
	case "confirmed", "returned", "nack", "unknown":
	default:
		outcome = "unknown"
	}
	return d.store.CompleteCohortPublish(ctx, *lease, outcome)
}
func (d *CohortJobDispatcher) ReconcileOnce(ctx context.Context) error {
	_, err := d.store.ReconcileCohortJobs(ctx, 8)
	return err
}
