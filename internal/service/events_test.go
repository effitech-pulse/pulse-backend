package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type eventsStore struct {
	records []domain.EventRecord
	query   domain.EventReadQuery
	err     error
}

func (f *eventsStore) ReadEventPage(_ context.Context, q domain.EventReadQuery) ([]domain.EventRecord, error) {
	f.query = q
	return f.records, f.err
}
func (f *eventsStore) ReadEvent(_ context.Context, _ domain.EventReadScope, _ domain.EventLocator) (domain.EventRecord, error) {
	if len(f.records) == 0 {
		return domain.EventRecord{}, domain.NewError(domain.Missing, "")
	}
	return f.records[0], f.err
}
func eventsContext() context.Context {
	return domain.WithPrincipal(context.Background(), domain.TrustedLegacyPrincipal("test"))
}
func eventsFixture() domain.EventRecord {
	return domain.EventRecord{Key: domain.EventKey{OccurredAt: time.Date(2026, 10, 22, 12, 0, 0, 123456000, time.UTC), SourceRank: 1, TargetKey: "saved"}, Locator: domain.EventLocator{Source: "mc", Target: "saved"}, Well: domain.Well{ID: 9223372036854775807}, Verdict: eventPtr("insufficient")}
}
func eventPtr[T any](v T) *T { return &v }
func TestEventsScopeAndProjection(t *testing.T) {
	f := &eventsStore{records: []domain.EventRecord{eventsFixture()}}
	s, e := NewEvents(f, nil, EventsOptions{Mode: "research", Prefix: "/api/v1"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.List(context.Background(), domain.EventFilter{}, ""); e == nil {
		t.Fatal("missing principal admitted")
	}
	for _, role := range []string{"viewer", "technologist", "admin"} {
		ctx := domain.WithPrincipal(context.Background(), domain.TrustedCompatibilityPrincipal(domain.Principal{AccountID: eventPtr(int64(41)), Role: role, Authenticated: true, Mechanism: "session"}))
		p, e := s.List(ctx, domain.EventFilter{}, "")
		if e != nil || len(p.Items) != 1 || *p.Items[0].Outcome.Verdict != "insufficient" || p.Items[0].Actor.Kind != "unknown" {
			t.Fatalf("%+v %v", p, e)
		}
	}
	if f.query.Filter.Limit != 25 {
		t.Fatal("default limit")
	}
	for _, n := range []int{-1, 101} {
		if _, e = s.List(eventsContext(), domain.EventFilter{Limit: n}, ""); e == nil {
			t.Fatal("bad limit")
		}
	}
}
func TestEventsActorNormalization(t *testing.T) {
	for _, v := range []struct {
		a    *domain.EventActorSnapshot
		kind string
	}{{nil, "unknown"}, {&domain.EventActorSnapshot{ID: eventPtr("user:41")}, "unknown"}, {&domain.EventActorSnapshot{ID: eventPtr("user:41"), Authenticated: eventPtr(true)}, "person"}, {&domain.EventActorSnapshot{ID: eventPtr("shared"), Authenticated: eventPtr(false)}, "shared"}, {&domain.EventActorSnapshot{ID: eventPtr("service"), Authenticated: eventPtr(false)}, "service"}, {&domain.EventActorSnapshot{ID: eventPtr("user:41"), Authenticated: eventPtr(false)}, "unknown"}} {
		if got := eventActor(v.a); got.Kind != v.kind {
			t.Fatalf("%+v => %+v", v.a, got)
		}
	}
}
func TestEventsCursorRejectsChangedContext(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	r := eventsFixture()
	f := &eventsStore{records: []domain.EventRecord{r, r}}
	s, _ := NewEvents(f, nil, EventsOptions{Mode: "research", Prefix: "/api/v1", Now: func() time.Time { return now }})
	p, e := s.List(eventsContext(), domain.EventFilter{Limit: 1}, "")
	if e != nil || p.NextCursor == nil {
		t.Fatalf("%+v %v", p, e)
	}
	token := *p.NextCursor
	if _, e = s.List(eventsContext(), domain.EventFilter{Limit: 1}, token); e != nil {
		t.Fatal(e)
	}
	other, _ := NewEvents(f, nil, EventsOptions{Mode: "research", Prefix: "/api/v1"})
	checks := []func() error{func() error { _, e := s.List(eventsContext(), domain.EventFilter{Limit: 2}, token); return e }, func() error { _, e := other.List(eventsContext(), domain.EventFilter{Limit: 1}, token); return e }, func() error { _, e := s.List(eventsContext(), domain.EventFilter{Limit: 1}, token+"x"); return e }, func() error {
		now = now.Add(time.Hour)
		_, e := s.List(eventsContext(), domain.EventFilter{Limit: 1}, token)
		return e
	}}
	for _, fn := range checks {
		var de *domain.EventReadError
		if !errors.As(fn(), &de) || de.Detail != "cursor_restart_required" {
			t.Fatal("cursor rejection not generic")
		}
	}
}
func TestEventsEmissionBudgets(t *testing.T) {
	if !EventURLFits(strings.Repeat("x", 4096)) || EventURLFits(strings.Repeat("x", 4097)) {
		t.Fatal("URL boundary")
	}
	f := &eventsStore{records: []domain.EventRecord{eventsFixture()}}
	f.records[0].Locator.Target = strings.Repeat("x", 2000)
	s, _ := NewEvents(f, nil, EventsOptions{Mode: "research", Prefix: "/api/v1"})
	_, e := s.List(eventsContext(), domain.EventFilter{}, "")
	var de *domain.Error
	if !errors.As(e, &de) || de.Kind != domain.Unavailable {
		t.Fatal(e)
	}
}

func TestEventsUnknownVerdictIsNotNullOrNorm(t *testing.T) {
	for _, tc := range []struct {
		raw  *string
		want *string
	}{{nil, nil}, {eventPtr(""), eventPtr("unknown")}, {eventPtr("future"), eventPtr("unknown")}, {eventPtr("insufficient"), eventPtr("insufficient")}} {
		r := eventsFixture()
		r.Verdict = tc.raw
		s, _ := NewEvents(&eventsStore{records: []domain.EventRecord{r}}, nil, EventsOptions{Mode: "research", Prefix: "/api/v1"})
		p, e := s.List(eventsContext(), domain.EventFilter{}, "")
		if e != nil {
			t.Fatal(e)
		}
		got := p.Items[0].Outcome.Verdict
		if tc.want == nil {
			if got != nil {
				t.Fatal("missing verdict not null")
			}
		} else if got == nil || *got != *tc.want {
			t.Fatalf("raw=%v got=%v want=%s", tc.raw, got, *tc.want)
		}
	}
}

func TestEventsRejectsRawLegacyAndMissingEnforcementSnapshot(t *testing.T) {
	s, e := NewEvents(&eventsStore{}, nil, EventsOptions{Mode: "research", Prefix: "/api/v1"})
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range []domain.Principal{{Role: "admin", Mechanism: "legacy"}, {Role: "admin", AccountID: eventPtr(int64(1)), Authenticated: true, Mechanism: "session"}} {
		ctx := domain.WithRBACMode(domain.WithPrincipal(context.Background(), p), domain.RBACEnforce)
		if _, e := s.List(ctx, domain.EventFilter{}, ""); e == nil {
			t.Fatal("missing snapshot admitted")
		}
	}
}
