package integration

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestCohortSnapshotAtomicImmutableAndRestartRead(t *testing.T) {
	ctx := legacyResearchFixtureContext()
	pool := testDB(t)
	r := postgres.New(pool)
	for i := 0; i < 100; i++ {
		u := int64(-900000 - i)
		if _, err := r.CreateWell(ctx, domain.Well{UnitekID: &u, FundType: "presentation", NGDU: -900, Department: -900, Active: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.CaptureCohort(ctx, "v1"); err != nil {
		t.Fatal(err)
	}
	lease, acquired, err := r.Reserve(ctx)
	if err != nil || !acquired {
		t.Fatal(err)
	}
	defer lease.Close()
	id, err := lease.(interface {
		BeginCohort(context.Context, string, string) (int64, error)
	}).BeginCohort(ctx, "test", "v1")
	if err != nil {
		t.Fatal(err)
	}
	_, members, err := r.CohortMembers(ctx, "v1")
	if err != nil {
		t.Fatal(err)
	}
	sid := "test-snapshot"
	asof := time.Now().UTC()
	s := domain.CohortSnapshot{Profile: "cohort", CohortVersion: "v1", RunID: &id, SnapshotID: &sid, AsOf: &asof, ProvenanceStatus: "partial", OutcomeCompleteness: "complete"}
	for i, w := range members {
		o := domain.CohortOutcome{AppWellID: w.ID, Metadata: w, Status: "insufficient", ReasonCode: "no_data"}
		if i == 0 {
			o.Status = "scored"
			o.Result = &domain.UploadResult{Verdict: "watch", LatestRisk: .9, Timeline: []domain.TimelinePoint{{WindowEnd: "2026-10-01T00:00:00Z", Risk: .9}}}
		}
		s.Outcomes = append(s.Outcomes, o)
	}
	s.Recount()
	writer := lease.(service.CohortRunLease)
	invalid := s
	invalid.Outcomes = append([]domain.CohortOutcome(nil), s.Outcomes...)
	invalid.Outcomes[1].Status = "unknown"
	if err = writer.PersistCohort(ctx, id, invalid); err == nil {
		t.Fatal("invalid terminal outcome committed")
	}
	var partialAlerts int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM ml_alert WHERE run_id=$1", id).Scan(&partialAlerts); err != nil || partialAlerts != 0 {
		t.Fatal("failed snapshot left partial alerts", partialAlerts, err)
	}
	if err = writer.PersistCohort(ctx, id, s); err != nil {
		t.Fatal(err)
	}
	if err = writer.PersistCohort(ctx, id, s); err != nil {
		t.Fatal("idempotency", err)
	}
	s.Outcomes[0].Result.LatestRisk = .1
	if err = writer.PersistCohort(ctx, id, s); err == nil {
		t.Fatal("immutable result rewritten")
	}
	saved, err := postgres.New(pool).CohortSnapshot(ctx, "v1", &id)
	if err != nil || len(saved.Outcomes) != 100 || saved.Outcomes[0].Result.LatestRisk != .9 {
		t.Fatal(saved, err)
	}
	var alerts, scored int
	var modelID *int64
	var appID int64
	if err = pool.QueryRow(ctx, "SELECT alerts_count,wells_scored FROM ml_run WHERE id=$1", id).Scan(&alerts, &scored); err != nil {
		t.Fatal(err)
	}
	if alerts != 1 || scored != 1 {
		t.Fatal(alerts, scored)
	}
	if err = pool.QueryRow(ctx, "SELECT ml_well_id,app_well_id FROM ml_alert WHERE run_id=$1", id).Scan(&modelID, &appID); err != nil || modelID != nil || appID != members[0].ID {
		t.Fatal(modelID, appID, err)
	}
}

type cohortOnlyML struct {
	fakeML
	scoreCalls  int
	uploadCalls int
}

func (f *cohortOnlyML) Score(context.Context) (domain.Snapshot, error) {
	f.scoreCalls++
	return domain.Snapshot{}, fmt.Errorf("research Score forbidden")
}
func (f *cohortOnlyML) Upload(_ context.Context, _ string, r io.Reader) (domain.UploadResult, error) {
	f.uploadCalls++
	_, err := io.ReadAll(r)
	return domain.UploadResult{Verdict: "norm", ModelVersion: ptr("same-bundle"), Timeline: []domain.TimelinePoint{}, Contributions: []domain.Contribution{}}, err
}
func TestCohortScheduledRunUsesUploadAndNeverManualChecks(t *testing.T) {
	ctx := domain.WithSystemPrincipal(context.Background()) // intentional scheduler fixture
	pool := testDB(t)
	r := postgres.New(pool)
	points := []domain.Measurement{}
	now := time.Now().UTC()
	v := float64(50)
	for i := 0; i < 100; i++ {
		u := int64(-900000 - i)
		w, err := r.CreateWell(ctx, domain.Well{UnitekID: &u, FundType: "presentation", NGDU: -900, Department: -900, Active: true})
		if err != nil {
			t.Fatal(err)
		}
		points = append(points, domain.Measurement{WellID: service.PulseTelemetryID(w.ID), Timestamp: now.Add(-time.Hour), Values: map[string]*float64{"F_out": &v}})
	}
	if _, err := r.CaptureCohort(ctx, "v1"); err != nil {
		t.Fatal(err)
	}
	scope, err := service.NewFundScope(ctx, r, "v1")
	if err != nil {
		t.Fatal(err)
	}
	client := &cohortOnlyML{}
	data := service.NewIntegrations(r, nil, nil, client).WithScope(scope)
	if _, err = data.Import(domain.WithSchedulerPrincipal(ctx, domain.ExecutionSystemCollect, domain.AccessScope{Funds: []string{"operational", "presentation"}}), points); err != nil {
		t.Fatal(err)
	}
	ml := service.NewML(r, r, client, service.RunOptions{Profile: "cohort", CohortVersion: "v1", Timeout: 10 * time.Second}).WithCohort(scope, data, r)
	started, err := ml.StartRun(domain.WithSchedulerPrincipal(ctx, domain.ExecutionSystemML, domain.AccessScope{Funds: []string{"operational", "presentation"}}), "scheduled")
	if err != nil || !started {
		t.Fatal(started, err)
	}
	ml.Wait()
	saved, err := r.CohortSnapshot(ctx, "v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Count != 100 || saved.Coverage.ScoredCount != 100 || client.scoreCalls != 0 || client.uploadCalls != 100 {
		t.Fatal(saved.Coverage, client.scoreCalls, client.uploadCalls)
	}
	var checks int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM manual_check").Scan(&checks); err != nil || checks != 0 {
		t.Fatal("scheduled run created manual checks", checks, err)
	}
}
