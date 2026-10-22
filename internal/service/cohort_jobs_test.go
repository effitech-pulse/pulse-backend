package service

import (
	"context"
	"strings"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type cohortAdmissionStore struct {
	CohortJobStore
	calls    int
	prepared domain.PreparedCohortJob
}

func (s *cohortAdmissionStore) AcceptCohortJob(_ context.Context, p domain.PreparedCohortJob) (domain.CohortJobStatus, error) {
	s.calls++
	s.prepared = p
	return domain.CohortJobStatus{ID: strings.Repeat("a", 32)}, nil
}

type cohortCatalogueFake struct{ calls int }

func (c *cohortCatalogueFake) Fixture(string) (domain.PreparedCohortJob, error) {
	c.calls++
	return domain.PreparedCohortJob{}, domain.NewError(domain.Missing, "fixture_unavailable")
}
func TestCohortJobsRequireSessionAdminBeforeFixture(t *testing.T) {
	for _, p := range []domain.Principal{{}, {Role: "admin"}, {Role: "admin", AccountID: ptr(int64(1)), Authenticated: true, Mechanism: "api_key"}, {Role: "viewer", AccountID: ptr(int64(1)), Authenticated: true, Mechanism: "session"}} {
		store := &cohortAdmissionStore{}
		catalogue := &cohortCatalogueFake{}
		jobs, err := NewCohortJobs(store, catalogue)
		if err != nil {
			t.Fatal(err)
		}
		_, err = jobs.Submit(domain.WithPrincipal(context.Background(), p), strings.Repeat("k", 16), "all100")
		if err == nil || catalogue.calls != 0 || store.calls != 0 {
			t.Fatal("forged authority reached fixture/store")
		}
	}
}
func TestCohortJobsRejectMalformedRequestBeforeFixture(t *testing.T) {
	catalogue := &cohortCatalogueFake{}
	jobs, _ := NewCohortJobs(&cohortAdmissionStore{}, catalogue)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{AccountID: ptr(int64(1)), Authenticated: true, Mechanism: "session", Role: "admin"})
	for _, key := range []string{"", strings.Repeat("x", 15), strings.Repeat("x", 81), strings.Repeat("x", 16) + "/"} {
		if _, err := jobs.Submit(ctx, key, "all100"); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
	if catalogue.calls != 0 {
		t.Fatal("invalid request reached catalogue")
	}
}
