package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestCohortCaptureExistingImmutableMembership(t *testing.T) {
	pool := testDB(t)
	ctx := legacyResearchFixtureContext()
	r := postgres.New(pool)
	if _, err := r.CaptureCohort(ctx, "prep-v1"); err == nil {
		t.Fatal("captured incomplete cohort")
	}
	for i := 0; i < 100; i++ {
		u := int64(-900000 - i)
		_, err := r.CreateWell(ctx, domain.Well{UnitekID: &u, NGDU: -900, Department: -900, FundType: "presentation", Active: i != 0})
		if err != nil {
			t.Fatal(err)
		}
	}
	c, err := r.CaptureCohort(ctx, "prep-v1")
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != "prep-v1" || len(c.ManifestSHA256) != 64 {
		t.Fatal(c)
	}
	if _, err = r.CaptureCohort(ctx, "prep-v1"); err == nil {
		t.Fatal("rewrote version")
	}
	scope, err := service.NewFundScope(ctx, r, "prep-v1")
	if err != nil {
		t.Fatal(err)
	}
	_, w, err := scope.Members(ctx)
	if err != nil || len(w) != 100 {
		t.Fatal(len(w), err)
	}
	if w[0].Active {
		t.Fatal("capture restored inactive member")
	}
	outsider, err := r.CreateWell(ctx, domain.Well{NGDU: 0, Department: 0, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.RequireWell(ctx, outsider.ID); err == nil {
		t.Fatal("outsider allowed")
	}
	if _, err = pool.Exec(ctx, fmt.Sprintf("UPDATE app_cohort SET manifest_sha256='%064d'", 1)); err == nil {
		t.Fatal("mutable cohort")
	}
}

func TestCohortHTTPAdminScopeRawAndJournalIsolation(t *testing.T) {
	ctx := legacyResearchFixtureContext()
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	defer app.ml.Wait()
	for i := 0; i < 100; i++ {
		u := int64(-900000 - i)
		if _, err := app.store.CreateWell(ctx, domain.Well{UnitekID: &u, NGDU: -900, Department: -900, FundType: "presentation", Active: true}); err != nil {
			t.Fatal(err)
		}
	}
	outsider, err := app.store.CreateWell(ctx, domain.Well{UnitekID: ptr(int64(42)), NGDU: 1, Department: 4, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.store.CaptureCohort(ctx, "v1"); err != nil {
		t.Fatal(err)
	}
	scope, err := service.NewFundScope(ctx, app.store, "v1")
	if err != nil {
		t.Fatal(err)
	}
	app.http.Catalog.WithScope(scope)
	app.http.Integrations = service.NewIntegrations(app.store, nil, nil).WithScope(scope)
	app.http.Checks = service.NewChecks(app.store, app.http.Integrations).WithScope(scope)
	app.http.Reviews = service.NewReviews(app.store).WithCatalog(app.store).WithScope(scope)
	app.ml.WithCohort(scope, app.http.Integrations, app.store)
	h := app.http.Handler()
	for _, p := range []string{"/wells/%d", "/wells/%d/predictions", "/wells/%d/predictions/export", "/wells/%d/complications", "/wells/%d/classification", "/wells/%d/incidents", "/wells/%d/history", "/wells/%d/risk", "/wells/%d/risk/history", "/wells/%d/checks", "/wells/%d/telemetry/bounds"} {
		t.Run(p, func(t *testing.T) { call(t, h, "GET", fmt.Sprintf("/api/v1"+p, outsider.ID), "", 404) })
	}
	call(t, h, "GET", "/api/v1/telemetry/bounds?well_id=42", "", 404)
	call(t, h, "GET", "/api/v1/telemetry?well_id=42&start=2026-01-01T00:00:00Z&end=2026-01-02T00:00:00Z", "", 404)
	call(t, h, "POST", "/api/v1/wells", `{"ngdu":1,"department":4}`, 403)
	call(t, h, "POST", "/api/v1/wells/bulk", `[]`, 403)
	call(t, h, "POST", "/api/v1/telemetry/import", fmt.Sprintf(`{"points":[{"well_id":"1","timestamp":"2026-01-01T00:00:00Z","values":{"F_out":50}},{"well_id":"%d","timestamp":"2026-01-01T00:00:00Z","values":{"F_out":50}}]}`, outsider.ID), 404)
	var count int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM telemetry_measurement").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("partial mixed import", count)
	}
	source, err := app.store.SaveSource(ctx, domain.TelemetrySource{ID: "mixed-source", Kind: "json_push", Status: "running", WellField: "well_id", TimeField: "timestamp", Mappings: []domain.Mapping{{Field: "f", Parameter: "F_out", Scale: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	call(t, h, "POST", "/api/v1/integrations/"+source.ID+"/ingest", `{"records":[{"well_id":-900000,"timestamp":"2026-01-01T00:00:00Z","f":50},{"well_id":42,"timestamp":"bad","f":50}]}`, 404)
	rows := call(t, h, "GET", "/api/v1/wells", "", 200).([]any)
	if len(rows) != 100 {
		t.Fatal(len(rows))
	}
	envelope := call(t, h, "GET", "/api/v1/ml/wells", "", 200).(map[string]any)
	if envelope["profile"] != "cohort" || envelope["count"] != float64(100) {
		t.Fatal(envelope)
	}
	call(t, h, "GET", "/api/v1/ml/wells/123/history", "", 404)
}

func TestCohortScopedSnapshotAndReviewsDoNotExposeHiddenMembers(t *testing.T) {
	ctx := legacyResearchFixtureContext()
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	defer app.Wait()
	var members []domain.Well
	for i := 0; i < 100; i++ {
		u := int64(-900000 - i)
		w, err := app.store.CreateWell(ctx, domain.Well{UnitekID: &u, NGDU: -900, Department: -900, FundType: "presentation", Active: true})
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, w)
	}
	if _, err := app.store.CaptureCohort(ctx, "v1"); err != nil {
		t.Fatal(err)
	}
	hidden, err := app.store.PatchWell(ctx, members[99].ID, domain.WellPatch{Department: domain.Field[int64]{Present: true, Value: -901}, WellNumber: domain.Field[string]{Present: true, Value: "private-hidden-well"}})
	if err != nil {
		t.Fatal(err)
	}
	members[99] = hidden
	scope, err := service.NewFundScope(ctx, app.store, "v1")
	if err != nil {
		t.Fatal(err)
	}
	app.http.Catalog.WithScope(scope)
	app.ml.WithCohort(scope, nil, app.store)
	app.http.Reviews = service.NewReviews(app.store).WithCatalog(app.store).WithScope(scope)
	lease, acquired, err := app.store.Reserve(ctx)
	if err != nil || !acquired {
		t.Fatal(err)
	}
	id, err := lease.BeginCohort(ctx, "test", "v1")
	if err != nil {
		t.Fatal(err)
	}
	sid := "private-full-snapshot"
	asof := time.Now().UTC()
	snapshot := domain.CohortSnapshot{Profile: "cohort", CohortVersion: "v1", RunID: &id, SnapshotID: &sid, AsOf: &asof, ProvenanceStatus: "partial", OutcomeCompleteness: "complete"}
	for i, w := range members {
		o := domain.CohortOutcome{AppWellID: w.ID, Metadata: w, Status: "insufficient", ReasonCode: "no_data"}
		if i == 99 {
			o.Status = "scored"
			o.Result = &domain.UploadResult{Verdict: "watch", LatestRisk: .9}
		}
		snapshot.Outcomes = append(snapshot.Outcomes, o)
	}
	if err = lease.(service.CohortRunLease).PersistCohort(ctx, id, snapshot); err != nil {
		t.Fatal(err)
	}
	lease.Close()
	access, err := service.NewAccess(app.store, true)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := access.Bootstrap(ctx, accountInput("admin", "admin"))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		name, role string
		dept       int64
	}{{"viewer", "viewer", -900}, {"tech", "technologist", -901}} {
		in := accountInput(v.name, v.role)
		in.Scope = domain.AccessScope{Funds: []string{"presentation"}, Departments: ptr([]int64{v.dept})}
		if _, err = access.Create(domain.WithPrincipal(ctx, domain.AccountPrincipal(admin)), in); err != nil {
			t.Fatal(err)
		}
	}
	app.http.Access = access
	h := app.Handler()
	viewer := loginCookie(t, h, "viewer")
	tech := loginCookie(t, h, "tech")
	adminCookie := loginCookie(t, h, "admin")
	for _, v := range []struct {
		cookie  *http.Cookie
		visible int
	}{{viewer, 99}, {tech, 1}, {adminCookie, 100}} {
		w := authRequest(t, h, "GET", fmt.Sprintf("/api/v1/ml/runs/%d/snapshot", id), "", v.cookie, 200)
		var out domain.CohortSnapshot
		if err = json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Count != v.visible || out.Coverage.MemberCount != v.visible || out.Coverage.VisibleCount != v.visible {
			t.Fatal(out.Coverage)
		}
		summaryResponse := authRequest(t, h, "GET", "/api/v1/ml/runs", "", v.cookie, 200)
		var summaries domain.RunList
		if err = json.Unmarshal(summaryResponse.Body.Bytes(), &summaries); err != nil || len(summaries.Runs) != 1 {
			t.Fatal(summaries, err)
		}
		expectedScored := int64(1)
		if v.cookie == viewer {
			expectedScored = 0
		}
		if summaries.Runs[0].WellsScored == nil || *summaries.Runs[0].WellsScored != expectedScored || summaries.Runs[0].AlertsCount == nil || *summaries.Runs[0].AlertsCount != expectedScored {
			t.Fatal("hidden completed counts leaked", summaries)
		}
		if v.cookie == viewer && strings.Contains(w.Body.String(), "private-hidden-well") {
			t.Fatal("hidden metadata leaked")
		}
	}
	var alertID int64
	if err = pool.QueryRow(ctx, "SELECT id FROM ml_alert WHERE run_id=$1", id).Scan(&alertID); err != nil {
		t.Fatal(err)
	}
	authRequest(t, h, "GET", fmt.Sprintf("/api/v1/ml/alerts/%d/review", alertID), "", viewer, 404)
	authRequest(t, h, "GET", fmt.Sprintf("/api/v1/ml/alerts/%d/review", alertID), "", tech, 200)
	authRequest(t, h, "GET", fmt.Sprintf("/api/v1/wells/%d/risk?run_id=%d", hidden.ID, id), "", viewer, 404)
	before := authRequest(t, h, "GET", fmt.Sprintf("/api/v1/wells/%d/risk?run_id=%d", hidden.ID, id), "", tech, 200).Body.String()
	// Новый процесс и новый снимок не меняют выбранный прежний результат.
	app.ml = service.NewML(app.store, app.store, &fakeML{}, service.RunOptions{}).WithCohort(scope, nil, app.store)
	app.http.ML = app.ml
	after := authRequest(t, app.Handler(), "GET", fmt.Sprintf("/api/v1/wells/%d/risk?run_id=%d", hidden.ID, id), "", tech, 200).Body.String()
	if before != after {
		t.Fatal("selected run changed after restart")
	}
	// Новые права чтения cohort attempts не расширяют старый research RBAC.
	app.http.ML = service.NewML(app.store, app.store, &fakeML{}, service.RunOptions{Profile: "research"})
	authRequest(t, app.Handler(), "GET", "/api/v1/ml/runs", "", viewer, 403)
	app.http.ML = app.ml
	authRequest(t, h, "POST", "/api/v1/auth/logout", "", viewer, 204)
	authRequest(t, h, "GET", "/api/v1/ml/wells", "", viewer, 401)
}

func TestCohortFilteredViewKeepsCoverageAndLiveReviewLink(t *testing.T) {
	ctx := legacyResearchFixtureContext()
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	defer app.Wait()
	members := []domain.Well{}
	for i := 0; i < 100; i++ {
		u := int64(-900000 - i)
		w, err := app.store.CreateWell(ctx, domain.Well{UnitekID: &u, NGDU: -900, Department: -900, FundType: "presentation", Active: true})
		if err != nil {
			t.Fatal(err)
		}
		members = append(members, w)
	}
	if _, err := app.store.CaptureCohort(ctx, "v1"); err != nil {
		t.Fatal(err)
	}
	scope, err := service.NewFundScope(ctx, app.store, "v1")
	if err != nil {
		t.Fatal(err)
	}
	app.http.Catalog.WithScope(scope)
	app.ml.WithCohort(scope, nil, app.store)
	lease, _, err := app.store.Reserve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id, err := lease.BeginCohort(ctx, "test", "v1")
	if err != nil {
		t.Fatal(err)
	}
	sid := "review-link"
	asof := time.Now().UTC()
	s := domain.CohortSnapshot{Profile: "cohort", CohortVersion: "v1", RunID: &id, SnapshotID: &sid, AsOf: &asof, ProvenanceStatus: "partial", OutcomeCompleteness: "complete"}
	for i, w := range members {
		o := domain.CohortOutcome{AppWellID: w.ID, Metadata: w, Status: "insufficient", ReasonCode: "no_data"}
		if i == 0 {
			o.Status = "scored"
			o.Result = &domain.UploadResult{Verdict: "watch", LatestRisk: .2}
		}
		s.Outcomes = append(s.Outcomes, o)
	}
	if err = lease.(service.CohortRunLease).PersistCohort(ctx, id, s); err != nil {
		t.Fatal(err)
	}
	lease.Close()
	h := app.Handler()
	first := call(t, h, "GET", "/api/v1/ml/wells?min_risk=0.9&alert_only=true", "", 200).(map[string]any)
	if len(first["outcomes"].([]any)) != 100 || len(first["scored"].([]any)) != 0 {
		t.Fatal("filter changed membership or ignored", first)
	}
	o := first["outcomes"].([]any)[0].(map[string]any)
	if o["alert_id"] == nil || o["alert_status"] != "new" {
		t.Fatal("missing live review linkage", o)
	}
	if _, err = app.store.UpdateAlert(ctx, int64(o["alert_id"].(float64)), "acknowledged"); err != nil {
		t.Fatal(err)
	}
	last := call(t, h, "GET", fmt.Sprintf("/api/v1/wells/%d/risk?run_id=%d", members[0].ID, id), "", 200).(map[string]any)
	if last["alert_status"] != "acknowledged" || last["result"].(map[string]any)["latest_risk"] != .2 {
		t.Fatal(last)
	}
	retry, _, err := app.store.Reserve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	overlay, err := app.ml.CohortWells(domain.WithSystemPrincipal(ctx), &id)
	if err != nil {
		t.Fatal(err)
	}
	if err = retry.(service.CohortRunLease).PersistCohort(ctx, id, overlay); err != nil {
		t.Fatal("read overlay changed canonical digest", err)
	}
	retry.Close()
	saved, err := app.store.CohortSnapshot(ctx, "v1", &id)
	if err != nil || saved.Outcomes[0].AlertStatus != nil {
		t.Fatal("review rewrote immutable fact", err)
	}
}
