package service

import (
	"context"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type CollectionJobClaim interface {
	Lease() domain.CollectionLease
	Close()
}
type CollectionJobStore interface {
	AcceptCollectionJob(context.Context, domain.CollectionSubmit) (domain.CollectionJobStatus, error)
	CollectionJobStatus(context.Context, string) (domain.CollectionJobStatus, error)
	CancelCollectionJob(context.Context, string, int64) (domain.CollectionJobStatus, error)
	ClaimCollectionJob(context.Context, domain.CollectionMessage, string) (CollectionJobClaim, error)
	LoadCollectionWork(context.Context, domain.CollectionLease) (domain.CollectionWork, error)
	SaveCollectionCapture(context.Context, domain.CollectionLease, domain.CollectionPreparedCapture) error
	ApplyCollectionCapture(context.Context, domain.CollectionLease) (domain.CollectionDisposition, error)
	RenewCollectionJob(context.Context, domain.CollectionLease) error
	FinishCollectionFailure(context.Context, domain.CollectionLease, domain.CollectionFailure) (domain.CollectionDisposition, error)
	ReconcileCollectionJobs(context.Context, int) (int, error)
	ClaimCollectionOutbox(context.Context, string) (*domain.CollectionPublishLease, error)
	CompleteCollectionPublish(context.Context, domain.CollectionPublishLease, domain.CollectionPublishOutcome) error
}
type CollectionSource interface {
	FetchCollection(context.Context, domain.CollectionIntent) (domain.CollectionRawCapture, error)
}
type CollectionPublisher interface {
	Publish(context.Context, domain.CollectionMessage) (domain.CollectionPublishOutcome, error)
}
type CollectionDeliveryHandler interface {
	HandleDelivery(context.Context, domain.CollectionMessage) error
}
