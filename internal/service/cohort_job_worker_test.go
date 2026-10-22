package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type cohortWorkerClaim struct {
	closed atomic.Bool
	alive  func(context.Context) error
}

func (c *cohortWorkerClaim) Lease() domain.CohortJobLease {
	return domain.CohortJobLease{JobID: strings.Repeat("a", 32), DeadlineAt: time.Now().Add(30 * time.Minute)}
}
func (c *cohortWorkerClaim) Alive(ctx context.Context) error {
	if c.alive != nil {
		return c.alive(ctx)
	}
	return nil
}
func (c *cohortWorkerClaim) Close() { c.closed.Store(true) }

type cohortWorkerStore struct {
	CohortJobStore
	claim                                                 CohortJobClaim
	claimErr, nextErr, checkpointErr, finishErr, renewErr error
	members                                               []*domain.CohortMemberExecution
	checkpoints                                           []domain.CohortCheckpoint
	failure                                               *domain.CohortJobFailure
	finishes                                              int
}

func (s *cohortWorkerStore) ClaimCohortJob(context.Context, domain.CohortJobMessage, string) (CohortJobClaim, error) {
	return s.claim, s.claimErr
}
func (s *cohortWorkerStore) NextCohortMember(context.Context, domain.CohortJobLease) (*domain.CohortMemberExecution, error) {
	if s.nextErr != nil {
		return nil, s.nextErr
	}
	if len(s.members) == 0 {
		return nil, nil
	}
	m := s.members[0]
	s.members = s.members[1:]
	return m, nil
}
func (s *cohortWorkerStore) CheckpointCohortMember(_ context.Context, _ domain.CohortJobLease, c domain.CohortCheckpoint) error {
	if s.checkpointErr != nil {
		return s.checkpointErr
	}
	s.checkpoints = append(s.checkpoints, c)
	return nil
}
func (s *cohortWorkerStore) RenewCohortJob(context.Context, domain.CohortJobLease) error {
	return s.renewErr
}
func (s *cohortWorkerStore) FinishCohortJob(_ context.Context, _ domain.CohortJobLease, f *domain.CohortJobFailure) (domain.CohortJobDisposition, error) {
	s.finishes++
	s.failure = f
	return domain.CohortJobDisposition{Kind: "ack"}, s.finishErr
}

type cohortWorkerScorer struct {
	calls  int
	result domain.UploadResult
	err    error
	score  func(context.Context) (domain.UploadResult, error)
}

func (s *cohortWorkerScorer) UploadCohortPinned(ctx context.Context, _ domain.ExecutionPin, _ domain.PinnedInputIdentity, _ io.Reader) (domain.UploadResult, error) {
	s.calls++
	if s.score != nil {
		return s.score(ctx)
	}
	return s.result, s.err
}
func TestCohortWorkerDuplicateAndUnknownDatabaseDoNotCompute(t *testing.T) {
	for _, name := range []string{"duplicate", "claim", "next", "final", "physical"} {
		t.Run(name, func(t *testing.T) {
			claim := &cohortWorkerClaim{}
			store := &cohortWorkerStore{claim: claim}
			scorer := &cohortWorkerScorer{}
			unknown := errors.New("database uncertain")
			switch name {
			case "duplicate":
				store.claim = nil
			case "claim":
				store.claimErr = unknown
			case "next":
				store.nextErr = unknown
			case "final":
				store.finishErr = unknown
			case "physical":
				claim.alive = func(context.Context) error { return unknown }
			}
			worker, e := NewCohortJobWorker(store, scorer, strings.Repeat("b", 32))
			if e != nil {
				t.Fatal(e)
			}
			e = worker.HandleDelivery(context.Background(), domain.CohortJobMessage{})
			if name == "duplicate" {
				if e != nil {
					t.Fatal(e)
				}
			} else if !errors.Is(e, unknown) {
				t.Fatalf("unknown must stay unacked: %v", e)
			}
			if scorer.calls != 0 || len(store.checkpoints) != 0 {
				t.Fatal("computed without authority")
			}
			if name != "duplicate" && name != "claim" && !claim.closed.Load() {
				t.Fatal("physical connection leaked")
			}
		})
	}
}
func TestCohortWorkerNoComputeCheckpointAndResume(t *testing.T) {
	marker := "archived"
	pin := testFilePin()
	pinSHA, _ := domain.CohortExecutionPinSHA256(pin)
	member := domain.CohortFrozenMember{Ordinal: 1, Well: domain.Well{ID: 17, Active: true, ArchivedAt: ptr(time.Now())}, NoComputeReason: &marker}
	execution := &domain.CohortMemberExecution{JobID: strings.Repeat("a", 32), Member: member, ExecutionPin: pin, ExecutionPinSHA256: pinSHA, InputManifestSHA256: domain.FileSHA256([]byte("manifest"))}
	store := &cohortWorkerStore{claim: &cohortWorkerClaim{}, members: []*domain.CohortMemberExecution{execution}}
	scorer := &cohortWorkerScorer{}
	worker, _ := NewCohortJobWorker(store, scorer, strings.Repeat("b", 32))
	if err := worker.HandleDelivery(context.Background(), domain.CohortJobMessage{}); err != nil {
		t.Fatal(err)
	}
	if scorer.calls != 0 || len(store.checkpoints) != 1 || store.checkpoints[0].Outcome.Result != nil || store.checkpoints[0].Outcome.Status != "archived" || store.failure != nil || store.finishes != 1 {
		t.Fatalf("marker/resume lost: %+v", store)
	}
	if err := domain.ValidateCohortCheckpoint(*execution, store.checkpoints[0]); err != nil {
		t.Fatal(err)
	}
}

func cohortScoredExecution(t *testing.T) (domain.CohortMemberExecution, domain.UploadResult) {
	t.Helper()
	pin := testFilePin()
	pinSHA, err := domain.CohortExecutionPinSHA256(pin)
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("canonical")
	input := &domain.CohortFrozenInput{Identity: domain.PinnedInputIdentity{InputSnapshotID: strings.Repeat("c", 32), CanonicalSHA256: domain.FileSHA256(raw), CanonicalBytes: int64(len(raw)), FingerprintSHA256: domain.FileSHA256([]byte("fingerprint")), SourceKind: "cohort_snapshot"}, Canonical: raw, Resolution: "supplied_canonical"}
	e := domain.CohortMemberExecution{JobID: strings.Repeat("a", 32), Member: domain.CohortFrozenMember{Ordinal: 1, Well: domain.Well{ID: 1, Active: true}, Input: input}, ExecutionPin: pin, ExecutionPinSHA256: pinSHA, InputManifestSHA256: domain.FileSHA256([]byte("manifest"))}
	result := domain.UploadResult{Verdict: "norm", ModelName: &pin.ModelName, ModelVersion: &pin.BundleSHA256, DecisionRule: pin.DecisionRule, Provenance: &domain.Provenance{InputKind: "cohort_snapshot", InputSnapshotID: &input.Identity.InputSnapshotID, InputSHA256: &input.Identity.CanonicalSHA256, ContextVersion: &pin.ContextVersion, FeatureRecipeSHA256: &pin.FeatureRecipeSHA256, FeatureSchemaSHA256: &pin.FeatureSchemaSHA256, SourceManifestSHA256: &pin.SourceManifestSHA256, RuntimeManifestSHA256: &pin.RuntimeManifestSHA256}}
	return e, result
}
func TestCohortWorkerChecksPinBeforeCompute(t *testing.T) {
	e, result := cohortScoredExecution(t)
	e.ExecutionPinSHA256 = domain.FileSHA256([]byte("wrong"))
	store := &cohortWorkerStore{claim: &cohortWorkerClaim{}, members: []*domain.CohortMemberExecution{&e}}
	scorer := &cohortWorkerScorer{result: result}
	worker, _ := NewCohortJobWorker(store, scorer, strings.Repeat("b", 32))
	if err := worker.HandleDelivery(context.Background(), domain.CohortJobMessage{}); err != nil {
		t.Fatal(err)
	}
	if scorer.calls != 0 || len(store.checkpoints) != 0 || store.failure == nil {
		t.Fatal("pin mismatch computed/checkpointed")
	}
}

func TestCohortWorkerResultIdentityAndTransientFailures(t *testing.T) {
	for _, name := range []string{"success", "insufficient", "file_kind", "bundle", "policy", "verdict", "busy", "near_busy", "unavailable", "unknown_db"} {
		t.Run(name, func(t *testing.T) {
			execution, result := cohortScoredExecution(t)
			store := &cohortWorkerStore{claim: &cohortWorkerClaim{}, members: []*domain.CohortMemberExecution{&execution}}
			scorer := &cohortWorkerScorer{result: result}
			switch name {
			case "insufficient":
				scorer.result.Verdict = "insufficient"
			case "file_kind":
				scorer.result.Provenance.InputKind = "uploaded_file"
			case "bundle":
				scorer.result.ModelVersion = ptr("wrong")
			case "policy":
				rule := *scorer.result.DecisionRule
				rule.HighEnabled = true
				scorer.result.DecisionRule = &rule
			case "verdict":
				scorer.result.Verdict = "unknown"
			case "busy":
				scorer.err = domain.NewError(domain.Limited, "execution_busy")
			case "near_busy":
				scorer.err = domain.NewError(domain.Limited, "pinned_resource_limit")
			case "unavailable":
				scorer.err = domain.NewError(domain.Unavailable, "ml_unavailable")
			case "unknown_db":
				store.checkpointErr = errors.New("checkpoint uncertain")
			}
			worker, _ := NewCohortJobWorker(store, scorer, strings.Repeat("b", 32))
			err := worker.HandleDelivery(context.Background(), domain.CohortJobMessage{})
			if name == "unknown_db" {
				if !errors.Is(err, store.checkpointErr) || store.finishes != 0 {
					t.Fatal("DB uncertainty finalized", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if name == "success" || name == "insufficient" {
				if len(store.checkpoints) != 1 || store.failure != nil {
					t.Fatal("valid result not checkpointed")
				}
				cp := store.checkpoints[0]
				if err = domain.ValidateCohortCheckpoint(execution, cp); err != nil {
					t.Fatal(err)
				}
				if name == "insufficient" && (cp.Outcome.Result != nil || cp.Outcome.Status != "insufficient") {
					t.Fatal("invented insufficient risk")
				}
			} else {
				if len(store.checkpoints) != 0 || store.failure == nil {
					t.Fatal("invalid result checkpointed")
				}
				if name == "verdict" && store.failure.Code != "unsupported_verdict" {
					t.Fatalf("unsupported verdict reason lost: %+v", store.failure)
				}
				wantRetry := name == "busy" || name == "unavailable"
				if store.failure.Retryable != wantRetry {
					t.Fatalf("wrong retry %+v", store.failure)
				}
			}
		})
	}
}
func TestCohortWorkerHeartbeatUnknownFailureCancelsWithoutAck(t *testing.T) {
	execution, _ := cohortScoredExecution(t)
	unknown := errors.New("database renewal unknown")
	store := &cohortWorkerStore{claim: &cohortWorkerClaim{}, members: []*domain.CohortMemberExecution{&execution}, renewErr: unknown}
	scorer := &cohortWorkerScorer{score: func(ctx context.Context) (domain.UploadResult, error) {
		<-ctx.Done()
		return domain.UploadResult{}, ctx.Err()
	}}
	worker, _ := NewCohortJobWorker(store, scorer, strings.Repeat("b", 32))
	worker.heartbeat = time.Millisecond
	if err := worker.HandleDelivery(context.Background(), domain.CohortJobMessage{}); !errors.Is(err, unknown) {
		t.Fatal("unknown renewal acknowledged", err)
	}
	if store.finishes != 0 || len(store.checkpoints) != 0 {
		t.Fatal("renewal failure accepted result")
	}
}

func TestCohortWorkerDeterministicCheckpointCapacityRequiresDurableFinish(t *testing.T) {
	for _, name := range []string{"capacity", "finish_uncertain", "unknown_sql", "wrong_kind", "other_limit"} {
		t.Run(name, func(t *testing.T) {
			execution, result := cohortScoredExecution(t)
			rejected := domain.NewError(domain.Limited, "cohort_result_capacity")
			switch name {
			case "unknown_sql":
				rejected = errors.New("checkpoint commit unknown")
			case "wrong_kind":
				rejected = domain.NewError(domain.Unavailable, "cohort_result_capacity")
			case "other_limit":
				rejected = domain.NewError(domain.Limited, "unclassified_limit")
			}
			store := &cohortWorkerStore{claim: &cohortWorkerClaim{}, members: []*domain.CohortMemberExecution{&execution}, checkpointErr: rejected}
			if name == "finish_uncertain" {
				store.finishErr = errors.New("final commit unknown")
			}
			scorer := &cohortWorkerScorer{result: result}
			worker, err := NewCohortJobWorker(store, scorer, strings.Repeat("b", 32))
			if err != nil {
				t.Fatal(err)
			}
			err = worker.HandleDelivery(context.Background(), domain.CohortJobMessage{})
			if scorer.calls != 1 || len(store.checkpoints) != 0 {
				t.Fatal("capacity rejection changed accepted compute/checkpoint behavior")
			}
			switch name {
			case "capacity", "finish_uncertain":
				if store.finishes != 1 || store.failure == nil || store.failure.Code != "cohort_result_capacity" || store.failure.Retryable {
					t.Fatalf("deterministic capacity not finalized: err=%v finishes=%d failure=%+v", err, store.finishes, store.failure)
				}
				if name == "capacity" && err != nil {
					t.Fatalf("durable terminal disposition not ack-safe: %v", err)
				}
				if name == "finish_uncertain" && !errors.Is(err, store.finishErr) {
					t.Fatal("unknown Finish commit acknowledged", err)
				}
			default:
				if !errors.Is(err, rejected) || store.finishes != 0 {
					t.Fatalf("unknown checkpoint error finalized: %v %+v", err, store.failure)
				}
			}
		})
	}
}
