package service

import (
	"context"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type CollectionJobDispatcher struct {
	store     CollectionJobStore
	publisher CollectionPublisher
	owner     string
}

func NewCollectionJobDispatcher(store CollectionJobStore, publisher CollectionPublisher, id string) (*CollectionJobDispatcher, error) {
	if store == nil || publisher == nil || !collectionOpaque.MatchString(id) {
		return nil, domain.NewError(domain.Invalid, "invalid_collection_request")
	}
	return &CollectionJobDispatcher{store, publisher, id}, nil
}
func (d *CollectionJobDispatcher) DispatchOnce(ctx context.Context) error {
	lease, e := d.store.ClaimCollectionOutbox(ctx, d.owner)
	if e != nil || lease == nil {
		return e
	}
	out, e := d.publisher.Publish(ctx, lease.Message)
	if e != nil {
		out = domain.CollectionPublishUnknown
	}
	switch out {
	case domain.CollectionPublishConfirmed, domain.CollectionPublishReturned, domain.CollectionPublishNack, domain.CollectionPublishUnknown:
	default:
		out = domain.CollectionPublishUnknown
	}
	return d.store.CompleteCollectionPublish(ctx, *lease, out)
}
func (d *CollectionJobDispatcher) ReconcileOnce(ctx context.Context) error {
	_, e := d.store.ReconcileCollectionJobs(ctx, 8)
	return e
}
