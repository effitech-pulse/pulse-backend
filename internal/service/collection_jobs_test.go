package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type collectionTestStore struct {
	CollectionJobStore
	capture  *domain.CollectionPreparedCapture
	applied  bool
	saved    bool
	saveErr  error
	finished bool
	outcome  domain.CollectionPublishOutcome
	loadErr  error
	work     domain.CollectionWork
	failure  domain.CollectionFailure
}

func (s *collectionTestStore) ClaimCollectionJob(context.Context, domain.CollectionMessage, string) (CollectionJobClaim, error) {
	return collectionTestClaim{}, nil
}

type collectionTestClaim struct{}

func (collectionTestClaim) Lease() domain.CollectionLease {
	return domain.CollectionLease{JobID: strings.Repeat("a", 32), AttemptDeadline: time.Now().Add(time.Minute)}
}
func (collectionTestClaim) Close() {}
func (s *collectionTestStore) LoadCollectionWork(context.Context, domain.CollectionLease) (domain.CollectionWork, error) {
	if s.capture != nil {
		return domain.CollectionWork{Capture: s.capture}, s.loadErr
	}
	return s.work, s.loadErr
}
func (s *collectionTestStore) ApplyCollectionCapture(context.Context, domain.CollectionLease) (domain.CollectionDisposition, error) {
	s.applied = true
	return domain.CollectionDisposition{Kind: "applied"}, nil
}
func (s *collectionTestStore) SaveCollectionCapture(context.Context, domain.CollectionLease, domain.CollectionPreparedCapture) error {
	s.saved = true
	return s.saveErr
}
func (s *collectionTestStore) FinishCollectionFailure(_ context.Context, _ domain.CollectionLease, failure domain.CollectionFailure) (domain.CollectionDisposition, error) {
	s.finished = true
	s.failure = failure
	return domain.CollectionDisposition{Kind: "terminal"}, nil
}
func (s *collectionTestStore) CollectionJobStatus(context.Context, string) (domain.CollectionJobStatus, error) {
	return domain.CollectionJobStatus{ID: "visible"}, nil
}

type collectionNeverSource struct{}

func (collectionNeverSource) FetchCollection(context.Context, domain.CollectionIntent) (domain.CollectionRawCapture, error) {
	return domain.CollectionRawCapture{}, errors.New("unexpected_refetch")
}
func TestCollectionWorkerNeverRereadsCaptured(t *testing.T) {
	s := &collectionTestStore{capture: &domain.CollectionPreparedCapture{}}
	w, e := NewCollectionJobWorker(s, collectionNeverSource{}, strings.Repeat("a", 32))
	if e != nil {
		t.Fatal(e)
	}
	if e = w.HandleDelivery(context.Background(), domain.CollectionMessage{}); e != nil || !s.applied || s.finished || s.saved {
		t.Fatalf("capture resume: applied=%v saved=%v finished=%v err=%v", s.applied, s.saved, s.finished, e)
	}
}
func TestCollectionOffStatusPreserved(t *testing.T) {
	s, _ := NewCollectionJobs(&collectionTestStore{}, false)
	id := int64(1)
	ctx := domain.WithPrincipal(context.Background(), domain.TrustedCompatibilityPrincipal(domain.Principal{Authenticated: true, Mechanism: "session", Role: "admin", AccountID: &id}))
	if _, e := s.Submit(ctx, domain.CollectionSubmit{}); e == nil {
		t.Fatal("off submit accepted")
	}
	if _, e := s.Cancel(ctx, strings.Repeat("a", 32), 1); e == nil {
		t.Fatal("off cancel accepted")
	}
	if v, e := s.Status(ctx, strings.Repeat("a", 32)); e != nil || v.ID != "visible" {
		t.Fatalf("off status %v %v", v, e)
	}
}

func TestCollectionCapturedCorruptionTerminalWithoutRefetch(t *testing.T) {
	s := &collectionTestStore{loadErr: domain.NewError(domain.Invalid, "capture_corrupt")}
	w, _ := NewCollectionJobWorker(s, collectionNeverSource{}, strings.Repeat("a", 32))
	e := w.HandleDelivery(context.Background(), domain.CollectionMessage{})
	if e != nil || !s.finished || s.failure.Code != domain.CollectionCodeCaptureCorrupt || s.applied || s.saved {
		t.Fatalf("corruption must persist terminal disposition: finished=%v err=%v", s.finished, e)
	}
}

type collectionRawSource struct{ raw domain.CollectionRawCapture }

func (s collectionRawSource) FetchCollection(context.Context, domain.CollectionIntent) (domain.CollectionRawCapture, error) {
	return s.raw, nil
}
func TestCollectionLateIOAfterAuthorityChange(t *testing.T) {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	intent := domain.CollectionIntent{Start: start, End: start.Add(time.Minute), Binding: domain.CollectionBinding{SourceID: "source", WellID: 1, ExternalID: "-1"}, Fixture: domain.CollectionFixture{ID: "dense", Protocol: "collection-fixture-v1", Horizon: start.Add(time.Hour), RetentionStart: start.Add(-30 * 24 * time.Hour), Channels: []domain.CollectionChannel{{Name: "F_out", Minimum: 0, Maximum: 100}}, Mappings: []domain.Mapping{{Field: "f", Parameter: "F_out", Scale: 1}}}, CanonicalVersion: "collection-canonical-v1", SchemaVersion: "collection-measurement-v1"}
	raw := []byte(`{"protocol":"collection-fixture-v1","fixture_id":"dense","well_id":"-1","start":"2026-10-19T00:00:00Z","end":"2026-10-19T00:01:00Z","data_revision":"a","complete":true,"empty":false,"expected_slots":1,"records":[{"well_id":"-1","timestamp":"2026-10-19T00:00:00Z","values":{"f":0}}]}`)
	s := &collectionTestStore{work: domain.CollectionWork{Intent: intent}, saveErr: domain.NewError(domain.Forbidden, "access_revoked")}
	w, _ := NewCollectionJobWorker(s, collectionRawSource{domain.CollectionRawCapture{Body: raw}}, strings.Repeat("a", 32))
	e := w.HandleDelivery(context.Background(), domain.CollectionMessage{})
	if e == nil || !s.saved || s.applied || s.finished {
		t.Fatalf("late IO must stop at denied Save: saved=%v applied=%v finish=%v err=%v", s.saved, s.applied, s.finished, e)
	}
}

type collectionDispatchStore struct {
	CollectionJobStore
	outcome        domain.CollectionPublishOutcome
	reconcileLimit int
}

func (s *collectionDispatchStore) ClaimCollectionOutbox(context.Context, string) (*domain.CollectionPublishLease, error) {
	return &domain.CollectionPublishLease{Message: domain.CollectionMessage{Version: 1}}, nil
}
func (s *collectionDispatchStore) CompleteCollectionPublish(_ context.Context, _ domain.CollectionPublishLease, out domain.CollectionPublishOutcome) error {
	s.outcome = out
	return nil
}
func (s *collectionDispatchStore) ReconcileCollectionJobs(_ context.Context, limit int) (int, error) {
	s.reconcileLimit = limit
	return 0, nil
}

type collectionUnknownPublisher struct{}

func (collectionUnknownPublisher) Publish(context.Context, domain.CollectionMessage) (domain.CollectionPublishOutcome, error) {
	return domain.CollectionPublishConfirmed, errors.New("confirm_unknown")
}
func TestCollectionDispatcherPersistsUncertainty(t *testing.T) {
	s := &collectionDispatchStore{}
	d, _ := NewCollectionJobDispatcher(s, collectionUnknownPublisher{}, strings.Repeat("b", 32))
	if e := d.DispatchOnce(context.Background()); e != nil || s.outcome != domain.CollectionPublishUnknown {
		t.Fatal("uncertain publish marked delivered", s.outcome, e)
	}
	if e := d.ReconcileOnce(context.Background()); e != nil || s.reconcileLimit != 8 {
		t.Fatal("unbounded reconcile", s.reconcileLimit, e)
	}
}

type collectionLostRenewStore struct{ collectionTestStore }

func (s *collectionLostRenewStore) RenewCollectionJob(context.Context, domain.CollectionLease) error {
	return domain.NewError(domain.Forbidden, "stale_lease")
}

type collectionAwaitSource struct{}

func (collectionAwaitSource) FetchCollection(ctx context.Context, _ domain.CollectionIntent) (domain.CollectionRawCapture, error) {
	<-ctx.Done()
	return domain.CollectionRawCapture{}, ctx.Err()
}
func TestCollectionWorkerRenewLossCancelsIO(t *testing.T) {
	s := &collectionLostRenewStore{}
	w, _ := NewCollectionJobWorker(s, collectionAwaitSource{}, strings.Repeat("a", 32))
	w.heartbeat = time.Millisecond
	e := w.HandleDelivery(context.Background(), domain.CollectionMessage{})
	if e == nil || e.Error() != "stale_lease" || s.saved || s.finished || s.applied {
		t.Fatalf("renew loss allowed effect: err=%v", e)
	}
}
