package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type jobStoreFake struct {
	FileJobStore
	accepted int
	prepared domain.FileJobPrepared
	lease    *domain.FileJobLease
	claimErr error
	publish  domain.FileJobPublishOutcome
}

func (f *jobStoreFake) AcceptFileJob(_ context.Context, v domain.FileJobPrepared) (domain.FileJobStatus, error) {
	f.accepted++
	f.prepared = v
	return domain.FileJobStatus{ID: v.Input.ID, State: domain.FileJobPendingDispatch}, nil
}
func (f *jobStoreFake) ClaimFileJob(context.Context, domain.FileJobMessage, string) (*domain.FileJobLease, error) {
	return f.lease, f.claimErr
}
func (f *jobStoreFake) ClaimFileJobPublish(context.Context) (*domain.FileJobPublishLease, error) {
	return &domain.FileJobPublishLease{}, nil
}
func (f *jobStoreFake) FinishFileJobPublish(_ context.Context, _ domain.FileJobPublishLease, v domain.FileJobPublishOutcome) error {
	f.publish = v
	return nil
}

type rejectingJobGate struct{}

func (rejectingJobGate) ValidateRaw(context.Context, int64, string, domain.FileParseSpec) error {
	return domain.NewFileJobProblem(domain.Invalid, "synthetic_input_required")
}
func (rejectingJobGate) ValidatePrepared(context.Context, domain.FileJobPrepared) error { return nil }

type passingJobGate struct{}

func (passingJobGate) ValidateRaw(context.Context, int64, string, domain.FileParseSpec) error {
	return nil
}
func (passingJobGate) ValidatePrepared(context.Context, domain.FileJobPrepared) error { return nil }
func jobChecks(t *testing.T) (*FileChecks, *fileRows, domain.Account) {
	t.Helper()
	a := domain.Account{ID: 1, Active: true, Role: "technologist", Scope: domain.AccessScope{Funds: []string{"operational"}}}
	rows := &fileRows{}
	raw := []byte("test")
	s, e := newFileChecksFixture(t, &fileStore{}, &fileAccounts{a}, &fileScope{domain.Well{ID: 2, Active: true, FundType: "operational"}}, rows, fileScorer{}, domain.TelemetrySchemaSnapshot{Bytes: raw, SHA256: domain.FileSHA256(raw), Channels: []domain.TelemetryChannel{{Canonical: "F_out", Scale: 1}}}, testFilePin(), domain.DefaultFileInputLimits())
	if e != nil {
		t.Fatal(e)
	}
	return s, rows, a
}
func jobSpec() domain.FileParseSpec {
	return domain.FileParseSpec{Version: "file_input_v1", Format: "csv", Delimiter: ",", HeaderRow: 1, TimeColumn: "time", TimestampLayout: "rfc3339", Timezone: "UTC", Mappings: []domain.Mapping{{Field: "f", Parameter: "F_out", Scale: 1}}}
}
func TestFileJobsRejectForgedContextAndClientActor(t *testing.T) {
	checks, _, a := jobChecks(t)
	store := &jobStoreFake{}
	s, e := NewFileJobs(store, checks, passingJobGate{})
	if e != nil {
		t.Fatal(e)
	}
	for _, ctx := range []context.Context{context.Background(), domain.WithPrincipal(context.Background(), domain.Principal{Authenticated: true, AccountID: &a.ID, Role: "admin", Mechanism: "worker"})} {
		if _, e = s.Submit(ctx, 2, "abcdefghijklmnop", jobSpec(), strings.NewReader("raw")); e == nil {
			t.Fatal("forged context accepted")
		}
	}
	if store.accepted != 0 {
		t.Fatal("accepted unauthorized input")
	}
}
func TestFileJobsManifestRejectsUnlistedRawBeforeParse(t *testing.T) {
	checks, rows, a := jobChecks(t)
	store := &jobStoreFake{}
	s, e := NewFileJobs(store, checks, rejectingJobGate{})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Submit(domain.WithPrincipal(context.Background(), domain.AccountPrincipal(a)), 2, "abcdefghijklmnop", jobSpec(), strings.NewReader("real data"))
	if e == nil || rows.calls != 0 || store.accepted != 0 {
		t.Fatal(e, rows.calls, store.accepted)
	}
}
func TestFileJobsSingleRowCoverageIsHonest(t *testing.T) {
	checks, _, a := jobChecks(t)
	store := &jobStoreFake{}
	s, _ := NewFileJobs(store, checks, passingJobGate{})
	_, e := s.Submit(domain.WithPrincipal(context.Background(), domain.AccountPrincipal(a)), 2, "abcdefghijklmnop", jobSpec(), strings.NewReader("raw"))
	if e != nil {
		t.Fatal(e)
	}
	if store.prepared.PointsCount != 1 || store.prepared.End.Sub(store.prepared.Start).Microseconds() != 1 {
		t.Fatal(store.prepared)
	}
}
func TestFileJobWorkerDuplicateHasNoRPC(t *testing.T) {
	checks, _, _ := jobChecks(t)
	w, e := NewFileJobWorker(&jobStoreFake{}, checks, "11111111111111111111111111111111")
	if e != nil {
		t.Fatal(e)
	}
	if e = w.HandleDelivery(context.Background(), domain.FileJobMessage{}); e != nil {
		t.Fatal(e)
	}
}
func TestFileJobWorkerDBFailureIsNotAckSafe(t *testing.T) {
	checks, _, _ := jobChecks(t)
	want := errors.New("db failed")
	w, _ := NewFileJobWorker(&jobStoreFake{claimErr: want}, checks, "11111111111111111111111111111111")
	if !errors.Is(w.HandleDelivery(context.Background(), domain.FileJobMessage{}), want) {
		t.Fatal("lost DB failure")
	}
}

type jobWorkerStore struct {
	jobStoreFake
	input        domain.PreparedFileInput
	renewErr     error
	executionErr error
	finishErr    error
	finished     int
	failure      *domain.FileJobFailure
}

func (f *jobWorkerStore) ExecutionFileJob(context.Context, domain.FileJobLease) (domain.FileJobExecution, error) {
	return domain.FileJobExecution{Input: f.input}, f.executionErr
}
func (f *jobWorkerStore) RenewFileJob(context.Context, domain.FileJobLease) error { return f.renewErr }
func (f *jobWorkerStore) FinishFileJob(_ context.Context, _ domain.FileJobLease, r *domain.StoredFileResult, failure *domain.FileJobFailure) (domain.FileJobDisposition, error) {
	f.finished++
	f.failure = failure
	return domain.FileJobDisposition{Kind: "ack"}, f.finishErr
}

type cancelledJobScorer struct{ cancelled chan struct{} }

func (s cancelledJobScorer) UploadPinned(ctx context.Context, _ domain.ExecutionPin, _ domain.PinnedInputIdentity, _ io.Reader) (domain.UploadResult, error) {
	<-ctx.Done()
	close(s.cancelled)
	return domain.UploadResult{}, ctx.Err()
}
func TestFileJobWorkerLeaseFailureCancelsRPCWithoutAck(t *testing.T) {
	checks, _, a := jobChecks(t)
	input, e := checks.PrepareFileInput(domain.WithPrincipal(context.Background(), domain.AccountPrincipal(a)), 2, "abcdefghijklmnop", jobSpec(), strings.NewReader("raw"))
	if e != nil {
		t.Fatal(e)
	}
	stopped := make(chan struct{})
	checks.scorer = cancelledJobScorer{stopped}
	want := errors.New("database uncertain")
	store := &jobWorkerStore{jobStoreFake: jobStoreFake{lease: &domain.FileJobLease{}}, input: input, renewErr: want}
	worker, _ := NewFileJobWorker(store, checks, "11111111111111111111111111111111")
	worker.heartbeat = time.Millisecond
	if e = worker.HandleDelivery(context.Background(), domain.FileJobMessage{}); !errors.Is(e, want) {
		t.Fatal(e)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("RPC not cancelled")
	}
	if store.finished != 0 {
		t.Fatal("uncertain DB error finalized")
	}
}
func TestFileJobWorkerCancelFinalizesBeforeAck(t *testing.T) {
	checks, _, a := jobChecks(t)
	input, e := checks.PrepareFileInput(domain.WithPrincipal(context.Background(), domain.AccountPrincipal(a)), 2, "abcdefghijklmnop", jobSpec(), strings.NewReader("raw"))
	if e != nil {
		t.Fatal(e)
	}
	checks.scorer = cancelledJobScorer{make(chan struct{})}
	store := &jobWorkerStore{jobStoreFake: jobStoreFake{lease: &domain.FileJobLease{}}, input: input, renewErr: domain.NewError(domain.Duplicate, "cancel_requested")}
	worker, _ := NewFileJobWorker(store, checks, "11111111111111111111111111111111")
	worker.heartbeat = time.Millisecond
	if e = worker.HandleDelivery(context.Background(), domain.FileJobMessage{}); e != nil {
		t.Fatal(e)
	}
	if store.finished != 1 || store.failure == nil {
		t.Fatal("no persisted cancellation resolution")
	}
}

func TestFileJobWorkerExecutionLoadErrorBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		finish bool
	}{
		{"unknown_database", errors.New("temporary PostgreSQL read failure"), false},
		{"unknown_io", io.ErrUnexpectedEOF, false},
		{"context_deadline", context.DeadlineExceeded, false},
		{"unclassified_domain", domain.NewError(domain.Unavailable, "input_unavailable"), false},
		{"unknown_typed", domain.NewFileJobProblem(domain.Unavailable, "database_unavailable"), false},
		{"missing_payload", domain.NewFileJobProblem(domain.Unavailable, "input_unavailable"), true},
		{"corrupt_payload", domain.NewFileJobProblem(domain.Unavailable, "input_corrupt"), true},
		{"persisted_cancel", domain.NewFileJobProblem(domain.Duplicate, "cancelled"), true},
		{"persisted_revoke", domain.NewFileJobProblem(domain.Duplicate, "access_revoked"), true},
		{"persisted_deadline", domain.NewFileJobProblem(domain.Duplicate, "job_deadline"), true},
		{"stale_lease", domain.NewFileJobProblem(domain.Duplicate, "stale_lease"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checks, _, _ := jobChecks(t)
			store := &jobWorkerStore{jobStoreFake: jobStoreFake{lease: &domain.FileJobLease{}}, executionErr: tc.err}
			worker, _ := NewFileJobWorker(store, checks, "11111111111111111111111111111111")
			err := worker.HandleDelivery(context.Background(), domain.FileJobMessage{})
			if tc.finish {
				if err != nil || store.finished != 1 || store.failure == nil {
					t.Fatalf("known semantic load failure not resolved: err=%v finished=%d", err, store.finished)
				}
			} else {
				if !errors.Is(err, tc.err) || store.finished != 0 {
					t.Fatalf("unknown load error became ack-safe: err=%v finished=%d", err, store.finished)
				}
			}
		})
	}
}
func TestFileJobWorkerExecutionSemanticFailureStillRequiresDurableFinish(t *testing.T) {
	checks, _, _ := jobChecks(t)
	want := errors.New("uncertain finalize commit")
	store := &jobWorkerStore{jobStoreFake: jobStoreFake{lease: &domain.FileJobLease{}}, executionErr: domain.NewFileJobProblem(domain.Unavailable, "input_corrupt"), finishErr: want}
	worker, _ := NewFileJobWorker(store, checks, "11111111111111111111111111111111")
	if err := worker.HandleDelivery(context.Background(), domain.FileJobMessage{}); !errors.Is(err, want) {
		t.Fatal("finalize error acknowledged", err)
	}
}
