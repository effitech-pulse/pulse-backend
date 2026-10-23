package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type cohortTelemetryFixture struct {
	calls int
	ends  []time.Time
}

func (f *cohortTelemetryFixture) ReadApp(_ context.Context, w domain.Well, start, end time.Time) (domain.TelemetryResult, error) {
	f.calls++
	f.ends = append(f.ends, end)
	if w.ID == 3 {
		return domain.TelemetryResult{Truncated: true}, nil
	}
	v := float64(50)
	return domain.TelemetryResult{Points: []domain.Measurement{{Timestamp: end.Add(-time.Hour), Values: map[string]*float64{"F_out": &v}}}, Resolution: "hour"}, nil
}

type cohortMLFixture struct {
	MLClient
	calls   int
	result  *domain.UploadResult
	failure error
}

func (f *cohortMLFixture) Upload(_ context.Context, _ string, r io.Reader) (domain.UploadResult, error) {
	f.calls++
	_, _ = io.ReadAll(r)
	if f.failure != nil {
		return domain.UploadResult{}, f.failure
	}
	if f.result != nil {
		return *f.result, nil
	}
	return domain.UploadResult{Verdict: "watch", ModelVersion: ptr("v1"), LatestRisk: .7, Timeline: []domain.TimelinePoint{{WindowEnd: "2026-10-01T00:00:00Z", Risk: .7}}}, nil
}
func TestCohortScoringPreservesAllNullableOutcomes(t *testing.T) {
	f := &cohortFixture{cohort: domain.Cohort{Version: "v1"}}
	for i := int64(1); i <= 100; i++ {
		u := -900000 - i
		f.wells = append(f.wells, domain.Well{ID: i, UnitekID: &u, Active: true})
	}
	f.wells[0].ArchivedAt = ptr(time.Now())
	f.wells[1].Active = false
	scope, err := NewFundScope(internalFixtureContext(), f, "v1")
	if err != nil {
		t.Fatal(err)
	}
	source := &cohortTelemetryFixture{}
	ml := &cohortMLFixture{}
	s, err := ScoreCohort(internalFixtureContext(), scope, source, ml, 19, time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Outcomes) != 100 || s.Coverage.ScoredCount != 97 || s.Coverage.InsufficientCount != 1 || s.Coverage.ArchivedCount != 1 || s.Coverage.InactiveCount != 1 {
		t.Fatal(s.Coverage)
	}
	for _, o := range s.Outcomes {
		if o.Status != "scored" && o.Result != nil {
			t.Fatal("non-scored became norm")
		}
	}
	if source.calls != 98 || ml.calls != 97 {
		t.Fatal(source.calls, ml.calls)
	}
	for _, e := range source.ends {
		if !e.Equal(*s.AsOf) {
			t.Fatal("asof changed")
		}
	}
	if s.ProvenanceStatus != "partial" {
		t.Fatal("missing input bytes declared replayable")
	}
}

func TestCohortTimeoutStillReturnsAllTerminalOutcomes(t *testing.T) {
	f := &cohortFixture{cohort: domain.Cohort{Version: "v1"}}
	for i := int64(1); i <= 100; i++ {
		f.wells = append(f.wells, domain.Well{ID: i, Active: true})
	}
	scope, err := NewFundScope(internalFixtureContext(), f, "v1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(internalFixtureContext())
	cancel()
	source := &cohortTelemetryFixture{}
	out, err := ScoreCohort(ctx, scope, source, &cohortMLFixture{}, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if out.Coverage.ErrorCount != 100 || source.calls != 0 {
		t.Fatal(out.Coverage, source.calls)
	}
	for _, o := range out.Outcomes {
		if o.Result != nil || o.ReasonCode != "run_timeout" {
			t.Fatal(o)
		}
	}
}

func TestCohortInsufficientAndUnsupportedRemainNullable(t *testing.T) {
	for _, tc := range []struct {
		name           string
		result         *domain.UploadResult
		failure        error
		status, reason string
	}{
		{name: "running", result: &domain.UploadResult{Verdict: "insufficient", NWindows: 12, WindowsRunning: 1}, status: "insufficient", reason: "insufficient_running_windows"},
		{name: "unsupported", failure: domain.NewError(domain.Invalid, "unsupported_feature_recipe"), status: "error", reason: "unsupported_feature_recipe"},
		{name: "digest", result: &domain.UploadResult{Verdict: "norm", Provenance: &domain.Provenance{InputSHA256: ptr("sha256:wrong")}}, status: "error", reason: "input_digest_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, identity := scoreCohortMember(internalFixtureContext(), domain.CohortOutcome{AppWellID: 1, Status: "error"}, domain.Well{ID: 1}, &cohortTelemetryFixture{}, &cohortMLFixture{result: tc.result, failure: tc.failure}, time.Now().Add(-24*time.Hour), time.Now())
			if o.Status != tc.status || o.ReasonCode != tc.reason || o.Result != nil {
				t.Fatal(o)
			}
			if tc.name == "running" && (identity == nil || o.Diagnostics == nil || o.Diagnostics.NWindows != 12 || o.InputWatermark == nil) {
				t.Fatal("insufficient diagnostics dropped", o)
			}
		})
	}
}

type cohortProjectionStore struct{ snapshot domain.CohortSnapshot }

func (s *cohortProjectionStore) CohortSnapshot(context.Context, string, *int64) (domain.CohortSnapshot, error) {
	return s.snapshot, nil
}
func (s *cohortProjectionStore) CohortAlerts(context.Context, int64) ([]domain.CohortAlert, error) {
	return nil, nil
}
func (s *cohortProjectionStore) CohortRuns(context.Context, string, int) ([]domain.MLRun, error) {
	return nil, nil
}
func TestCohortRestrictedProjectionClearsExecution(t *testing.T) {
	fixture := &cohortFixture{cohort: domain.Cohort{Version: "v1"}}
	snapshot := domain.CohortSnapshot{Execution: &domain.CohortExecutionIdentity{SourceKind: "cohort_snapshot"}}
	for i := int64(1); i <= 100; i++ {
		w := domain.Well{ID: i, FundType: "presentation", NGDU: i}
		fixture.wells = append(fixture.wells, w)
		snapshot.Outcomes = append(snapshot.Outcomes, domain.CohortOutcome{AppWellID: i, Metadata: w, Status: "inactive"})
	}
	scope, err := NewFundScope(internalFixtureContext(), fixture, "v1")
	if err != nil {
		t.Fatal(err)
	}
	store := &cohortProjectionStore{snapshot: snapshot}
	beforeBytes, err := json.Marshal(store.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	m := &ML{scope: scope, snapshots: store}
	for _, tc := range []struct {
		name  string
		p     domain.Principal
		count int
	}{
		{"full", domain.Principal{Role: "viewer", Scope: domain.AccessScope{Funds: []string{"presentation"}}}, 100},
		{"restricted", domain.Principal{Role: "viewer", Scope: domain.AccessScope{Funds: []string{"presentation"}, NGDUs: ptr([]int64{1})}}, 1},
		{"empty", domain.Principal{Role: "viewer", Scope: domain.AccessScope{Funds: []string{"operational"}}}, 0},
	} {
		for _, run := range []*int64{nil, ptr(int64(7))} {
			t.Run(tc.name, func(t *testing.T) {
				a := domain.AuthorizationSnapshot{Active: true, Permissions: domain.BuiltinAccessPermissions("viewer"), LegacyScope: tc.p.Scope}
				ctx := domain.WithRBACMode(domain.WithPrincipal(context.Background(), domain.Principal{Authenticated: true, Authorization: &a}), domain.RBACEnforce)
				out, err := m.CohortWells(ctx, run)
				if err != nil {
					t.Fatal(err)
				}
				if len(out.Outcomes) != tc.count || (tc.count == 100) != (out.Execution != nil) {
					t.Fatalf("scope execution leak: count=%d execution=%+v", len(out.Outcomes), out.Execution)
				}
				afterBytes, e := json.Marshal(store.snapshot)
				if e != nil || !bytes.Equal(beforeBytes, afterBytes) {
					t.Fatal("read mutated immutable snapshot bytes", e)
				}
				if store.snapshot.Execution == nil {
					t.Fatal("read mutated stored identity")
				}
			})
		}
	}
}
