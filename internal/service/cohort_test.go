package service

import (
	"context"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type cohortFixture struct {
	Store
	wells   []domain.Well
	cohort  domain.Cohort
	members []domain.Well
}

func (f *cohortFixture) Wells(context.Context, bool) ([]domain.Well, error) { return f.wells, nil }
func (f *cohortFixture) Well(_ context.Context, id int64) (domain.Well, error) {
	for _, w := range f.wells {
		if w.ID == id {
			return w, nil
		}
	}
	return domain.Well{}, domain.NewError(domain.Missing, "missing")
}
func (f *cohortFixture) CohortMembers(context.Context, string) (domain.Cohort, []domain.Well, error) {
	if f.members != nil {
		return f.cohort, f.members, nil
	}
	return f.cohort, f.wells, nil
}
func TestCohortGuardAdminCannotEscapeMembership(t *testing.T) {
	f := &cohortFixture{cohort: domain.Cohort{Version: "v1"}}
	for i := int64(1); i <= 101; i++ {
		u := -900000 - i
		f.wells = append(f.wells, domain.Well{ID: i, UnitekID: &u, FundType: "presentation", Active: true})
	}
	f.members = f.wells[:100]
	scope, err := NewFundScope(context.Background(), f, "v1")
	if err != nil {
		t.Fatal(err)
	}
	c := NewCatalog(f).WithScope(scope)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Role: "admin"})
	rows, err := c.Wells(ctx, false)
	if err != nil || len(rows) != 100 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	if _, err = c.Well(ctx, 101); err == nil {
		t.Fatal("admin escaped cohort")
	}
	if _, err = c.CreateWell(ctx, domain.WellInput{}); err == nil {
		t.Fatal("cohort grew through create")
	}
}
func TestCohortRejectsIncompleteAndDuplicateMembership(t *testing.T) {
	for _, n := range []int{0, 99, 101} {
		f := &cohortFixture{cohort: domain.Cohort{Version: "v1"}, wells: make([]domain.Well, n)}
		for i := range f.wells {
			f.wells[i].ID = int64(i + 1)
		}
		if _, err := NewFundScope(context.Background(), f, "v1"); err == nil {
			t.Fatalf("accepted %d members", n)
		}
	}
	f := &cohortFixture{cohort: domain.Cohort{Version: "v1"}, wells: make([]domain.Well, 100)}
	if _, err := NewFundScope(context.Background(), f, "v1"); err == nil {
		t.Fatal("duplicate membership accepted")
	}
}
