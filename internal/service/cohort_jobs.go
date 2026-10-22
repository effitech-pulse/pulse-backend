package service

import (
	"context"
	"regexp"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

var cohortRequestKey = regexp.MustCompile(`^[A-Za-z0-9_-]{16,80}$`)
var cohortFixtureID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
var cohortOpaqueID = regexp.MustCompile(`^[0-9a-f]{32}$`)

type CohortJobs struct {
	store     CohortJobStore
	catalogue CohortFixtureCatalogue
}

func NewCohortJobs(store CohortJobStore, catalogue CohortFixtureCatalogue) (*CohortJobs, error) {
	if store == nil || catalogue == nil {
		return nil, domain.NewError(domain.Invalid, "invalid_cohort_jobs_configuration")
	}
	return &CohortJobs{store: store, catalogue: catalogue}, nil
}
func cohortJobActor(ctx context.Context) (int64, error) {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || !p.Authenticated || p.Mechanism != "session" || p.AccountID == nil || *p.AccountID <= 0 {
		return 0, domain.NewError(domain.Unauthorized, "session_required")
	}
	if p.Role != "admin" {
		return 0, domain.NewError(domain.Forbidden, "cohort_job_forbidden")
	}
	return *p.AccountID, nil
}
func (s *CohortJobs) Submit(ctx context.Context, key, fixtureID string) (domain.CohortJobStatus, error) {
	actor, err := cohortJobActor(ctx)
	if err != nil {
		return domain.CohortJobStatus{}, err
	}
	if !cohortRequestKey.MatchString(key) || !cohortFixtureID.MatchString(fixtureID) {
		return domain.CohortJobStatus{}, domain.NewError(domain.Invalid, "invalid_cohort_job_request")
	}
	p, err := s.catalogue.Fixture(fixtureID)
	if err != nil {
		return domain.CohortJobStatus{}, err
	}
	p.ActorAccountID = actor
	p.RequestKey = key
	p.RequestCommitmentSHA256, err = domain.CohortRequestCommitment(actor, fixtureID, p.FixtureSHA256)
	if err != nil {
		return domain.CohortJobStatus{}, err
	}
	if err = domain.ValidatePreparedCohortJob(p); err != nil {
		return domain.CohortJobStatus{}, err
	}
	return s.store.AcceptCohortJob(ctx, p)
}
func (s *CohortJobs) Get(ctx context.Context, id string) (domain.CohortJobStatus, error) {
	if _, err := cohortJobActor(ctx); err != nil {
		return domain.CohortJobStatus{}, err
	}
	if !cohortOpaqueID.MatchString(id) {
		return domain.CohortJobStatus{}, domain.NewError(domain.Missing, "cohort_job_unavailable")
	}
	return s.store.CohortJobStatus(ctx, id)
}
func (s *CohortJobs) Cancel(ctx context.Context, id string, revision int64) (domain.CohortJobStatus, error) {
	if _, err := cohortJobActor(ctx); err != nil {
		return domain.CohortJobStatus{}, err
	}
	if !cohortOpaqueID.MatchString(id) {
		return domain.CohortJobStatus{}, domain.NewError(domain.Missing, "cohort_job_unavailable")
	}
	if revision < 1 || revision > 9007199254740991 {
		return domain.CohortJobStatus{}, domain.NewError(domain.Invalid, "invalid_revision")
	}
	return s.store.CancelCohortJob(ctx, id, revision)
}
