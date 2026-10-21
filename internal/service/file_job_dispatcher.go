package service

import (
	"context"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type FileJobPublisher interface {
	Publish(context.Context, domain.FileJobMessage) (domain.FileJobPublishOutcome, error)
}
type FileJobDispatcher struct {
	store     FileJobStore
	publisher FileJobPublisher
}

func NewFileJobDispatcher(store FileJobStore, publisher FileJobPublisher) (*FileJobDispatcher, error) {
	if store == nil || publisher == nil {
		return nil, fileInvalid("invalid_file_job_dispatcher")
	}
	return &FileJobDispatcher{store, publisher}, nil
}
func (d *FileJobDispatcher) DispatchOnce(ctx context.Context) error {
	lease, e := d.store.ClaimFileJobPublish(ctx)
	if e != nil || lease == nil {
		return e
	}
	outcome, publishErr := d.publisher.Publish(ctx, lease.Message)
	if publishErr != nil {
		outcome = domain.FileJobPublishUnknown
	}
	return d.store.FinishFileJobPublish(ctx, *lease, outcome)
}
