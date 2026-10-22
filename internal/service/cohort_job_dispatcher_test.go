package service

import (
	"context"
	"errors"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type cohortDispatchStore struct {
	CohortJobStore
	lease       *domain.CohortPublishLease
	outcome     domain.CohortPublishOutcome
	completeErr error
	owner       string
}

func (s *cohortDispatchStore) ClaimCohortOutbox(_ context.Context, id string) (*domain.CohortPublishLease, error) {
	s.owner = id
	return s.lease, nil
}
func (s *cohortDispatchStore) CompleteCohortPublish(_ context.Context, _ domain.CohortPublishLease, out domain.CohortPublishOutcome) error {
	s.outcome = out
	return s.completeErr
}

type cohortPublisherFake struct {
	out   domain.CohortPublishOutcome
	err   error
	calls int
}

func (p *cohortPublisherFake) Publish(context.Context, domain.CohortJobMessage) (domain.CohortPublishOutcome, error) {
	p.calls++
	return p.out, p.err
}
func TestCohortDispatcherPersistsPublishUncertainty(t *testing.T) {
	for _, name := range []string{"confirmed", "returned", "nack", "uncertain", "nil", "commit_error"} {
		t.Run(name, func(t *testing.T) {
			store := &cohortDispatchStore{lease: &domain.CohortPublishLease{}}
			publisher := &cohortPublisherFake{out: domain.CohortPublishOutcome(name)}
			if name == "uncertain" {
				publisher.err = errors.New("private")
			}
			if name == "nil" {
				store.lease = nil
			}
			if name == "commit_error" {
				store.completeErr = errors.New("commit uncertain")
			}
			d, err := NewCohortJobDispatcher(store, publisher, "dispatcher")
			if err != nil {
				t.Fatal(err)
			}
			err = d.DispatchOnce(context.Background())
			if !errors.Is(err, store.completeErr) {
				t.Fatal(err)
			}
			if name == "nil" {
				if publisher.calls != 0 {
					t.Fatal("published without lease")
				}
				return
			}
			want := domain.CohortPublishOutcome(name)
			if name == "uncertain" || name == "commit_error" {
				want = "unknown"
			}
			if store.outcome != want || store.owner != "dispatcher" {
				t.Fatalf("lost outcome %s", store.outcome)
			}
		})
	}
}
