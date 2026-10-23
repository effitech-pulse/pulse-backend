package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/migrations"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func reviewCohort(t *testing.T) (*application, service.FundScope, []domain.Well) {
	t.Helper()
	ctx := legacyResearchFixtureContext()
	app := New(testConfig(), testDB(t), &fakeML{})
	t.Cleanup(app.Wait)
	for i := 0; i < 100; i++ {
		u := int64(-900000 - i)
		if _, err := app.store.CreateWell(ctx, domain.Well{UnitekID: &u, FundType: "presentation", NGDU: -900, Department: -900, Active: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.store.CaptureCohort(ctx, "senior-v1"); err != nil {
		t.Fatal(err)
	}
	scope, err := service.NewFundScope(ctx, app.store, "senior-v1")
	if err != nil {
		t.Fatal(err)
	}
	_, members, err := scope.Members(ctx)
	if err != nil {
		t.Fatal(err)
	}
	app.http.Catalog.WithScope(scope)
	app.ml.WithCohort(scope, nil, app.store)
	return app, scope, members
}

func TestReviewFixForeignReferenceMutation(t *testing.T) {
	app, _, _ := reviewCohort(t)
	ctx := legacyResearchFixtureContext() // intentional legacy execution fixture
	const foreign = int64(717171)
	if _, err := app.store.CreateReference(ctx, domain.NGDUReference, foreign, "outside-private"); err != nil {
		t.Fatal(err)
	}
	refs, err := app.http.Catalog.References(ctx, domain.NGDUReference)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range refs {
		if r.ID == foreign {
			t.Fatal("precondition: outsider visible")
		}
	}
	// Correct scope behavior rejects this mutation before store access.
	call(t, app.Handler(), "PUT", fmt.Sprintf("/api/v1/ngdu/%d", foreign), `{"name":"modified-outside"}`, 403)
}

func TestReviewFixLatestReviewResetMustSurviveNextRun(t *testing.T) {
	app, _, members := reviewCohort(t)
	ctx := legacyResearchFixtureContext() // intentional legacy execution fixture
	makeRun := func() (int64, int64) {
		lease, ok, err := app.store.Reserve(ctx)
		if err != nil || !ok {
			t.Fatal(err)
		}
		defer lease.Close()
		id, err := lease.Begin(ctx, "senior-repro")
		if err != nil {
			t.Fatal(err)
		}
		sid := fmt.Sprintf("senior-snapshot-%d", id)
		now := time.Now().UTC()
		snapshot := domain.CohortSnapshot{Profile: "cohort", CohortVersion: "senior-v1", RunID: &id, SnapshotID: &sid, AsOf: &now, ProvenanceStatus: "partial", OutcomeCompleteness: "complete"}
		for i, w := range members {
			o := domain.CohortOutcome{AppWellID: w.ID, Metadata: w, Status: "insufficient", ReasonCode: "no_data"}
			if i == 0 {
				o.Status = "scored"
				o.Result = &domain.UploadResult{Verdict: "watch", LatestRisk: .8}
			}
			snapshot.Outcomes = append(snapshot.Outcomes, o)
		}
		if err = lease.(service.CohortRunLease).PersistCohort(ctx, id, snapshot); err != nil {
			t.Fatal(err)
		}
		alerts, err := app.store.CohortAlerts(ctx, id)
		if err != nil || len(alerts) != 1 {
			t.Fatal(alerts, err)
		}
		return id, alerts[0].ID
	}
	_, first := makeRun()
	reviews := service.NewReviews(app.store).WithCatalog(app.store)
	if _, err := reviews.LegacyAlert(ctx, fmt.Sprint(first), "false_positive", domain.ReviewActor{}); err != nil {
		t.Fatal(err)
	}
	_, second := makeRun()
	if _, err := reviews.LegacyAlert(ctx, fmt.Sprint(second), "new", domain.ReviewActor{}); err != nil {
		t.Fatal(err)
	}
	third, _ := makeRun()
	alerts, err := app.store.CohortAlerts(ctx, third)
	if err != nil {
		t.Fatal(err)
	}
	if alerts[0].Status != "new" {
		t.Fatalf("explicit latest review reset lost: got %q, want new", alerts[0].Status)
	}
	for _, status := range []string{"acknowledged", "confirmed", "false_positive"} {
		_, current := makeRun()
		if _, err = app.store.UpdateAlert(ctx, current, status); err != nil {
			t.Fatal(err)
		}
		next, _ := makeRun()
		inherited, err := app.store.CohortAlerts(ctx, next)
		if err != nil || len(inherited) != 1 || inherited[0].Status != status {
			t.Fatal("latest status transfer failed", status, inherited, err)
		}
	}

}

func TestReviewFixFailedCohortRunVisibleInHistory(t *testing.T) {
	app, _, _ := reviewCohort(t)
	ctx := legacyResearchFixtureContext()
	lease, ok, err := app.store.Reserve(ctx)
	if err != nil || !ok {
		t.Fatal(err)
	}
	beginner, ok := lease.(interface {
		BeginCohort(context.Context, string, string) (int64, error)
	})
	if !ok {
		lease.Close()
		t.Fatal("cohort identity creation is unavailable")
	}
	id, err := beginner.BeginCohort(ctx, "manual", "senior-v1")
	if err != nil {
		lease.Close()
		t.Fatal(err)
	}
	if err = lease.Fail(ctx, id, "injected cohort persistence failure"); err != nil {
		lease.Close()
		t.Fatal(err)
	}
	lease.Close()
	runs, err := app.ml.Runs(domain.WithSystemPrincipal(ctx), 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range runs.Runs {
		if r.ID == id && r.Status == "error" {
			return
		}
	}
	t.Fatalf("latest failed cohort run %d disappeared: %+v", id, runs)
}

func TestReviewFixAllReferenceWritesDeniedBeforeValidation(t *testing.T) {
	app, _, _ := reviewCohort(t)
	ctx := legacyResearchFixtureContext()
	for _, kind := range []domain.ReferenceKind{domain.NGDUReference, domain.DepartmentReference} {
		if _, err := app.store.CreateReference(ctx, kind, 717171, "outside-private"); err != nil {
			t.Fatal(err)
		}
		before, err := app.store.References(ctx, kind)
		if err != nil {
			t.Fatal(err)
		}
		route := "ngdu"
		if kind == domain.DepartmentReference {
			route = "departments"
		}
		for _, target := range []int64{-900, 717171, 818181} {
			for _, p := range []domain.Principal{{Role: "admin"}, {Role: "viewer", Scope: domain.AccessScope{Funds: []string{"presentation"}}}} {
				c := domain.WithPrincipal(ctx, p)
				_, err = app.http.Catalog.UpdateReference(c, kind, target, nil)
				assertForbiddenReference(t, err)
				err = app.http.Catalog.DeleteReference(c, kind, target)
				assertForbiddenReference(t, err)
				_, err = app.http.Catalog.CreateReference(c, kind, domain.ReferenceInput{})
				assertForbiddenReference(t, err)
			}
			call(t, app.Handler(), "PUT", fmt.Sprintf("/api/v1/%s/%d", route, target), `{"name":"modified"}`, 403)
			call(t, app.Handler(), "DELETE", fmt.Sprintf("/api/v1/%s/%d", route, target), "", 403)
		}
		call(t, app.Handler(), "POST", "/api/v1/"+route, `{"id":919191,"name":"new"}`, 403)
		after, err := app.store.References(ctx, kind)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("denied writes changed global reference", kind, before, after, err)
		}
		// Явный research Catalog сохраняет прежние create/update/delete.
		research := service.NewCatalog(app.store)
		name := "research"
		id := int64(919191)
		if _, err = research.CreateReference(domain.WithSystemPrincipal(ctx), kind, domain.ReferenceInput{ID: &id, Name: &name}); err != nil {
			t.Fatal(err)
		}
		if _, err = research.UpdateReference(domain.WithSystemPrincipal(ctx), kind, id, &name); err != nil {
			t.Fatal(err)
		}
		if err = research.DeleteReference(domain.WithSystemPrincipal(ctx), kind, id); err != nil {
			t.Fatal(err)
		}
	}
}
func assertForbiddenReference(t *testing.T, err error) {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) || de.Kind != domain.Forbidden {
		t.Fatalf("want generic forbidden before validation/lookup, got %v", err)
	}
}

func TestReviewFixRunHistoryIdentifiesPendingFailureAndSuppressesPrivateMetadata(t *testing.T) {
	app, scope, members := reviewCohort(t)
	ctx := legacyResearchFixtureContext()
	makeAttempt := func(version string, fail bool) int64 {
		lease, ok, err := app.store.Reserve(ctx)
		if err != nil || !ok {
			t.Fatal(err)
		}
		id, err := lease.BeginCohort(ctx, "manual", version)
		if err != nil {
			lease.Close()
			t.Fatal(err)
		}
		if fail {
			if err = lease.Fail(ctx, id, "SECRET-SQL-SOURCE-717171"); err != nil {
				t.Fatal(err)
			}
		}
		lease.Close()
		return id
	}
	failed := makeAttempt("senior-v1", true)
	assertFailure := func(ml *service.ML) {
		t.Helper()
		out, err := ml.Runs(domain.WithSystemPrincipal(ctx), 20)
		if err != nil || len(out.Runs) != 1 || out.Runs[0].ID != failed || out.Runs[0].Status != "error" {
			t.Fatal(out, err)
		}
		r := out.Runs[0]
		if r.SnapshotID != nil || r.Provenance != nil || r.ProvenanceStatus != nil || r.WellsScored != nil || r.AlertsCount != nil || r.DriftVerdict != nil || r.Error == nil || r.FailureReasonCode == nil || *r.FailureReasonCode != "run_failed" || strings.Contains(*r.Error, "SECRET") {
			t.Fatal("failed run leaked private/computed metadata", r)
		}
	}
	assertFailure(app.ml)
	restarted := service.NewML(app.store, app.store, &fakeML{}, service.RunOptions{Profile: "cohort", CohortVersion: "senior-v1", Timeout: time.Second}).WithCohort(scope, nil, app.store)
	assertFailure(restarted)
	h := app.Handler()
	raw := call(t, h, "GET", "/api/v1/ml/runs", "", 200)
	encoded, _ := json.Marshal(raw)
	if strings.Contains(string(encoded), "SECRET-SQL-SOURCE") {
		t.Fatal("private error leaked HTTP")
	}
	lease, _, err := app.store.Reserve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	research, err := lease.Begin(ctx, "research")
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Fail(ctx, research, "private research"); err != nil {
		t.Fatal(err)
	}
	lease.Close()
	if _, err = app.store.PatchWell(ctx, members[0].ID, domain.WellPatch{WellNumber: domain.Field[string]{Present: true, Value: "new-capture"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = app.store.CaptureCohort(ctx, "other-v2"); err != nil {
		t.Fatal(err)
	}
	makeAttempt("other-v2", true)
	assertFailure(app.ml)
	running := makeAttempt("senior-v1", false)
	out, err := app.ml.Runs(domain.WithSystemPrincipal(ctx), 20)
	if err != nil || !out.Running || len(out.Runs) != 2 || out.Runs[0].ID != running || out.Runs[0].Status != "running" || out.Runs[0].Error != nil || out.Runs[0].FailureReasonCode != nil {
		t.Fatal(out, err)
	}
	empty := domain.WithPrincipal(ctx, domain.TrustedCompatibilityPrincipal(domain.Principal{Role: "viewer", Scope: domain.AccessScope{Funds: []string{}}}))
	none, err := app.ml.Runs(empty, 20)
	if err != nil || none.Running || len(none.Runs) != 0 {
		t.Fatal("empty scope leaked orchestration", none, err)
	}
	// Абандон прежнего running сохраняет исходную identity и делает его видимым error.
	next := makeAttempt("senior-v1", true)
	out, err = app.ml.Runs(domain.WithSystemPrincipal(ctx), 20)
	if err != nil || out.Running || len(out.Runs) != 3 || out.Runs[0].ID != next || out.Runs[1].ID != running || out.Runs[1].Status != "error" {
		t.Fatal(out, err)
	}
}

type reviewCaptureFailure struct {
	service.FundScope
	fail bool
}

func (s *reviewCaptureFailure) Members(ctx context.Context) (domain.Cohort, []domain.Well, error) {
	if s.fail {
		return domain.Cohort{}, nil, fmt.Errorf("private early capture failure")
	}
	return s.FundScope.Members(ctx)
}
func TestReviewFixCaptureFailureStillHasAttemptIdentity(t *testing.T) {
	app, scope, _ := reviewCohort(t)
	ctx := legacyResearchFixtureContext()
	failing := &reviewCaptureFailure{FundScope: scope, fail: true}
	ml := service.NewML(app.store, app.store, &fakeML{}, service.RunOptions{Profile: "cohort", CohortVersion: "senior-v1", Timeout: time.Second}).WithCohort(failing, nil, app.store)
	started, err := ml.StartRun(ctx, "manual")
	if err != nil || !started {
		t.Fatal(started, err)
	}
	ml.Wait()
	failing.fail = false
	out, err := ml.Runs(domain.WithSystemPrincipal(ctx), 20)
	if err != nil || len(out.Runs) != 1 || out.Runs[0].Status != "error" || out.Runs[0].Profile == nil || *out.Runs[0].Profile != "cohort" || out.Runs[0].CohortVersion == nil || *out.Runs[0].CohortVersion != "senior-v1" {
		t.Fatal(out, err)
	}
}

func TestReviewFixRunScopeMigrationPreservesHistoricalNullAndRejectsInvalidPairs(t *testing.T) {
	ctx := context.Background()
	// Изолированная схема возвращается к форме v10 до additive migration.
	// Доступ к pool нужен только тесту, не production service.
	pool := testDB(t)
	if _, err := pool.Exec(ctx, "ALTER TABLE ml_run DROP COLUMN profile; ALTER TABLE ml_run DROP COLUMN cohort_version; DELETE FROM backend_schema_migration WHERE version=11; INSERT INTO ml_run(status,trigger,started_at,error) VALUES('error','historical',now(),'private historical')"); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := pool.QueryRow(ctx, "SELECT row_to_json(r)::text FROM ml_run r").Scan(&before); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := migrations.Apply(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	var profile, version *string
	var after string
	if err := pool.QueryRow(ctx, "SELECT profile,cohort_version,(to_jsonb(r)-'profile'-'cohort_version')::text FROM ml_run r").Scan(&profile, &version, &after); err != nil {
		t.Fatal(err)
	}
	var first, second map[string]any
	_ = json.Unmarshal([]byte(before), &first)
	_ = json.Unmarshal([]byte(after), &second)
	if profile != nil || version != nil || !reflect.DeepEqual(first, second) {
		t.Fatal("historical run backfilled/changed", profile, version, before, after)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO app_cohort(version,manifest_sha256) VALUES('v1',repeat('a',64))"); err != nil {
		t.Fatal(err)
	}
	for _, values := range []string{"NULL,'v1'", "'cohort',NULL", "'research','v1'", "'all',NULL"} {
		if _, err := pool.Exec(ctx, "INSERT INTO ml_run(status,trigger,started_at,profile,cohort_version) VALUES('running','invalid',now(),"+values+")"); err == nil {
			t.Fatal("invalid identity accepted", values)
		}
	}
}
