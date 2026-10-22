package service

import (
	"context"
	"io"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type CohortFixtureCatalogue interface {
	Fixture(id string) (domain.PreparedCohortJob, error) //deep copy; actor/key empty
}
type CohortPinnedScorer interface {
	UploadCohortPinned(context.Context, domain.ExecutionPin, domain.PinnedInputIdentity, io.Reader) (domain.UploadResult, error)
}
type CohortJobClaim interface {
	Lease() domain.CohortJobLease
	Alive(context.Context) error
	Close()
}
type CohortJobStore interface {
	AcceptCohortJob(context.Context, domain.PreparedCohortJob) (domain.CohortJobStatus, error)
	CohortJobStatus(context.Context, string) (domain.CohortJobStatus, error)
	CancelCohortJob(context.Context, string, int64) (domain.CohortJobStatus, error)
	ClaimCohortJob(context.Context, domain.CohortJobMessage, string) (CohortJobClaim, error)
	NextCohortMember(context.Context, domain.CohortJobLease) (*domain.CohortMemberExecution, error)
	CheckpointCohortMember(context.Context, domain.CohortJobLease, domain.CohortCheckpoint) error
	RenewCohortJob(context.Context, domain.CohortJobLease) error
	FinishCohortJob(context.Context, domain.CohortJobLease, *domain.CohortJobFailure) (domain.CohortJobDisposition, error)
	ReconcileCohortJobs(context.Context, int) (int, error)
	ClaimCohortOutbox(context.Context, string) (*domain.CohortPublishLease, error)
	CompleteCohortPublish(context.Context, domain.CohortPublishLease, domain.CohortPublishOutcome) error
}
type CohortPublisher interface {
	Publish(context.Context, domain.CohortJobMessage) (domain.CohortPublishOutcome, error)
}
