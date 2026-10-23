package service

import (
	"context"
	"regexp"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

var collectionOpaque = regexp.MustCompile(`^[a-f0-9]{32}$`)
var collectionSelector = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var collectionReplay = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

type CollectionJobs struct {
	store   CollectionJobStore
	enabled bool
}

func NewCollectionJobs(store CollectionJobStore, enabled bool) (*CollectionJobs, error) {
	if store == nil {
		return nil, domain.NewError(domain.Invalid, "invalid_collection_request")
	}
	return &CollectionJobs{store, enabled}, nil
}
func collectionActor(ctx context.Context) error {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || !p.Authenticated || p.Mechanism != "session" || p.AccountID == nil || *p.AccountID <= 0 {
		return domain.NewError(domain.Unauthorized, "session_required")
	}
	if p.Role != "admin" {
		return domain.NewError(domain.Forbidden, "collection_access_denied")
	}
	return nil
}
func (s *CollectionJobs) Submit(ctx context.Context, input domain.CollectionSubmit) (domain.CollectionJobStatus, error) {
	if e := collectionActor(ctx); e != nil {
		return domain.CollectionJobStatus{}, e
	}
	if !s.enabled {
		return domain.CollectionJobStatus{}, domain.NewError(domain.Unavailable, "collection_jobs_disabled")
	}
	if !collectionReplay.MatchString(input.ReplayKey) || !collectionSelector.MatchString(input.SourceID) || !collectionSelector.MatchString(input.FixtureID) {
		return domain.CollectionJobStatus{}, domain.NewError(domain.Invalid, "invalid_collection_request")
	}
	return s.store.AcceptCollectionJob(ctx, input)
}
func (s *CollectionJobs) Status(ctx context.Context, id string) (domain.CollectionJobStatus, error) {
	if e := collectionActor(ctx); e != nil {
		return domain.CollectionJobStatus{}, e
	}
	if !collectionOpaque.MatchString(id) {
		return domain.CollectionJobStatus{}, domain.NewError(domain.Missing, "collection_job_missing")
	}
	return s.store.CollectionJobStatus(ctx, id)
}
func (s *CollectionJobs) Cancel(ctx context.Context, id string, revision int64) (domain.CollectionJobStatus, error) {
	if e := collectionActor(ctx); e != nil {
		return domain.CollectionJobStatus{}, e
	}
	if !s.enabled {
		return domain.CollectionJobStatus{}, domain.NewError(domain.Unavailable, "collection_jobs_disabled")
	}
	if !collectionOpaque.MatchString(id) {
		return domain.CollectionJobStatus{}, domain.NewError(domain.Missing, "collection_job_missing")
	}
	if revision < 1 || revision > 9007199254740991 {
		return domain.CollectionJobStatus{}, domain.NewError(domain.Invalid, "invalid_collection_request")
	}
	return s.store.CancelCollectionJob(ctx, id, revision)
}
