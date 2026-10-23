package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func scopeContext() context.Context {
	ngdu, department := int64(1), int64(3)
	a := domain.AuthorizationSnapshot{Active: true, Permissions: domain.BuiltinAccessPermissions("admin"), Organization: &domain.OrganizationScope{Kind: "department", NGDUID: &ngdu, DepartmentID: &department}, LegacyScope: domain.AccessScope{Funds: []string{"operational"}}}
	return domain.WithRBACMode(domain.WithPrincipal(context.Background(), domain.Principal{Authenticated: true, Authorization: &a}), domain.RBACEnforce)
}
func TestRBACScopeSurfaces(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	ctx := context.Background()
	setupCtx := domain.WithPrincipal(ctx, domain.TrustedLegacyPrincipal("scope fixture"))
	for _, body := range []string{`{"ngdu":1,"department":3}`, `{"ngdu":1,"department":4}`, `{"ngdu":2,"department":3}`, `{"ngdu":1,"department":3,"fund_type":"presentation"}`} {
		call(t, app.Handler(), "POST", "/api/v1/wells", body, 201)
	}
	c := service.NewCatalog(app.store)
	scoped := scopeContext()
	wells, err := c.Wells(scoped, false)
	if err != nil || len(wells) != 1 || wells[0].ID != 1 {
		t.Fatalf("visible wells=%v err=%v", wells, err)
	}
	if _, err = c.Well(scoped, 2); err == nil {
		t.Fatal("foreign well disclosed")
	}
	for _, id := range []int64{1, 2} {
		_, err = app.store.CreateIncident(setupCtx, domain.Incident{WellID: id, FailureType: "salt", FailureDate: "2026-01-01", DaysBefore: 60})
		if err != nil {
			t.Fatal(err)
		}
	}
	stats, err := c.IncidentStats(scoped)
	if err != nil || stats.Total != 1 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	zero := float64(0)
	_, err = app.store.CommitTelemetry(setupCtx, domain.TelemetryWrite{SourceID: "manual", Points: []domain.Measurement{{WellID: "pulse:1", Timestamp: start, Values: map[string]*float64{"F_out": &zero, "CosF": nil}}, {WellID: "pulse:2", Timestamp: start, Values: map[string]*float64{"F_out": &zero}}}})
	if err != nil {
		t.Fatal(err)
	}
	integrations := service.NewIntegrations(app.store, nil, nil)
	data, err := integrations.ReadApp(scoped, wells[0], start, end)
	if err != nil || len(data.Points) != 1 || data.Points[0].Values["F_out"] == nil || *data.Points[0].Values["F_out"] != 0 || data.Points[0].Values["CosF"] != nil {
		t.Fatalf("zero/null telemetry changed %+v %v", data, err)
	}
	if _, err = integrations.Read(scoped, "999", start, end); err == nil {
		t.Fatal("unmapped raw telemetry accepted")
	}
	check := domain.ManualCheck{ID: "scope-check-00000001", WellID: 1, RequestID: "scope-request-0001", Start: start, End: end, CreatedAt: start, PointsCount: 1, InputSHA256: strings.Repeat("a", 64), Resolution: "raw", Result: domain.UploadResult{Verdict: "norm"}}
	if _, err = app.store.SaveManualCheck(setupCtx, check); err != nil {
		t.Fatal(err)
	}
	checks := service.NewChecks(app.store, integrations)
	if rows, e := checks.List(scoped, 1, ""); e != nil || len(rows) != 1 {
		t.Fatalf("visible checks %+v %v", rows, e)
	}
	if _, e := checks.List(scoped, 2, ""); e == nil {
		t.Fatal("foreign checks accepted")
	}
	reviews := service.NewReviews(app.store).WithCatalog(app.store)
	if _, e := reviews.Get(scoped, "check", check.ID, ptr(int64(1)), 0); e != nil {
		t.Fatal(e)
	}
	if _, e := reviews.Get(scoped, "check", check.ID, ptr(int64(2)), 0); e == nil {
		t.Fatal("cross-well review binding accepted")
	}
	if _, err = c.Predictions(scoped, 2, 10); err == nil {
		t.Fatal("foreign predictions accepted")
	}
	if _, err = c.Wells(domain.WithRBACMode(ctx, domain.RBACEnforce), false); err == nil {
		t.Fatal("missing snapshot accepted")
	}
}
func TestRBACWellTransfer(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	h := app.Handler()
	call(t, h, "POST", "/api/v1/wells", `{"ngdu":1,"department":3}`, 201)
	c := service.NewCatalog(app.store)
	var patch domain.WellPatch
	if err := json.Unmarshal([]byte(`{"revision":1,"department":4}`), &patch); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PatchWell(scopeContext(), 1, patch); err == nil {
		t.Fatal("foreign proposed scope accepted")
	}
	w, err := app.store.Well(domain.WithSystemPrincipal(context.Background()), 1)
	if err != nil || w.Department != 3 || w.Revision != 1 {
		t.Fatalf("partial write %+v %v", w, err)
	}
	rows := []domain.WellInput{{NGDU: ptr(int64(1)), Department: ptr(int64(3))}, {NGDU: ptr(int64(2)), Department: ptr(int64(3))}}
	if _, err = c.ImportWells(scopeContext(), rows); err == nil {
		t.Fatal("mixed import accepted")
	}
	wells, err := app.store.Wells(domain.WithSystemPrincipal(context.Background()), false)
	if err != nil || len(wells) != 1 {
		t.Fatalf("partial import %v %v", wells, err)
	}
}
func TestRBACGlobalArtifacts(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	if _, err := service.NewPresentation(app.store).Scenario(scopeContext()); err == nil {
		t.Fatal("scoped global scenario accepted")
	}
	if _, err := service.NewCatalog(app.store).Audit(scopeContext(), 10); err == nil {
		t.Fatal("scoped global audit accepted")
	}
}

func TestRBACScopeSurfacesHTTP(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	ctx := context.Background()
	for _, body := range []string{`{"ngdu":1,"department":3,"unitek_id":42}`, `{"ngdu":1,"department":4,"unitek_id":43}`} {
		call(t, app.Handler(), "POST", "/api/v1/wells", body, 201)
	}
	a, err := service.NewAccess(app.store, true)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := a.Bootstrap(ctx, accountInput("admin", "admin"))
	if err != nil {
		t.Fatal(err)
	}
	user, err := a.Create(domain.WithPrincipal(ctx, domain.AccountPrincipal(admin)), accountInput("scoped", "admin"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE auth_user SET organization_scope='{"kind":"department","ngdu_id":1,"department_id":3}',scope='{"funds":["operational"],"ngdus":null,"departments":null}' WHERE id=$1`, user.ID); err != nil {
		t.Fatal(err)
	}
	a.WithRBACMode(domain.RBACEnforce)
	app.http.Access = a
	h := app.Handler()
	cookie := loginCookie(t, h, "scoped")
	for _, path := range []string{"/wells/1", "/wells/1/predictions", "/wells/1/complications", "/wells/1/classification", "/wells/1/incidents", "/ml/wells/by-unitek/42", "/ml/wells/123/explain", "/ml/wells/123/history"} {
		authRequest(t, h, "GET", "/api/v1"+path, "", cookie, 200)
	}
	for _, path := range []string{"/wells/2", "/wells/2/predictions", "/wells/2/incidents", "/ml/wells/by-unitek/43", "/ml/wells/999/explain", "/ml/wells/999/history"} {
		authRequest(t, h, "GET", "/api/v1"+path, "", cookie, 404)
	}
	authRequest(t, h, "PATCH", "/api/v1/wells/1", `{"revision":1,"department":4}`, cookie, 404)
	authRequest(t, h, "PATCH", "/api/v1/wells/2", `{"revision":1,"department":3}`, cookie, 404)
	authRequest(t, h, "PATCH", "/api/v1/wells/1", `{"revision":1,"well_number":null,"active":false}`, cookie, 200)
	authRequest(t, h, "GET", "/api/v1/audit", "", cookie, 403)
	authRequest(t, h, "GET", "/api/v1/analytics/dashboards/1/embed", "", cookie, 403)
	authRequest(t, h, "POST", "/api/v1/ml/wells/refresh", "", cookie, 403)
	export := authRequest(t, h, "GET", "/api/v1/wells/export?identity=pulse", "", cookie, 200)
	book, e := excelize.OpenReader(bytes.NewReader(export.Body.Bytes()))
	if e != nil {
		t.Fatal(e)
	}
	exportRows, e := book.GetRows(book.GetSheetName(0))
	book.Close()
	if e != nil || len(exportRows) != 2 || exportRows[0][0] != "PULSE_ID" || exportRows[1][0] != "1" {
		t.Fatalf("scope export rows=%v err=%v", exportRows, e)
	}
	// A stale persisted actor cannot commit, even when a request snapshot still grants the operation.
	snapshot, e := app.store.Authorization(ctx, user.ID)
	if e != nil {
		t.Fatal(e)
	}
	stale := domain.WithRBACMode(domain.WithPrincipal(ctx, domain.Principal{Authenticated: true, AccountID: &user.ID, Authorization: &snapshot}), domain.RBACEnforce)
	if _, e = pool.Exec(ctx, "UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=$1", user.ID); e != nil {
		t.Fatal(e)
	}
	var stalePatch domain.WellPatch
	if e = json.Unmarshal([]byte(`{"revision":2,"department":3}`), &stalePatch); e != nil {
		t.Fatal(e)
	}
	if _, e = app.store.PatchWell(stale, 1, stalePatch); e == nil {
		t.Fatal("revoked actor committed")
	}
	current, e := app.store.Well(domain.WithSystemPrincipal(ctx), 1)
	if e != nil || current.Revision != 2 || current.Department != 3 || current.Active {
		t.Fatalf("unexpected mutation %+v %v", current, e)
	}
	cookie = loginCookie(t, h, "scoped")
	refs := authRequest(t, h, "GET", "/api/v1/ngdu", "", cookie, 200)
	if strings.Contains(refs.Body.String(), `"id":2`) {
		t.Fatal(refs.Body.String())
	}
}

type scopeQueryTrace struct {
	mu         sync.Mutex
	statements []pgx.TraceQueryStartData
}

func (s *scopeQueryTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	s.mu.Lock()
	s.statements = append(s.statements, d)
	s.mu.Unlock()
	return ctx
}
func (*scopeQueryTrace) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func TestRBACScopeSurfaces1700(t *testing.T) {
	base := testDB(t)
	ctx := context.Background()
	if _, err := base.Exec(ctx, `INSERT INTO well(ngdu,department,active,fund_type) SELECT CASE WHEN n%4 IN (0,1) THEN 1 ELSE 2 END,CASE WHEN n%4 IN (0,2) THEN 3 ELSE 4 END,true,'operational' FROM generate_series(1,1700) n`); err != nil {
		t.Fatal(err)
	}
	trace := &scopeQueryTrace{}
	cfg := base.Config()
	cfg.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	c := service.NewCatalog(postgres.New(pool))
	rows, err := c.Wells(scopeContext(), false)
	if err != nil || len(rows) != 425 {
		t.Fatalf("1700 projection count=%d err=%v", len(rows), err)
	}
	trace.mu.Lock()
	queries := append([]pgx.TraceQueryStartData(nil), trace.statements...)
	trace.mu.Unlock()
	if len(queries) != 1 {
		t.Fatalf("1700 wells used %d queries, want 1", len(queries))
	}
	t.Logf("1700 well list SQL round trips=%d visible=%d", len(queries), len(rows))
	plan, err := base.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+queries[0].SQL, queries[0].Args...)
	if err != nil {
		t.Fatal(err)
	}
	for plan.Next() {
		var line string
		if err = plan.Scan(&line); err != nil {
			t.Fatal(err)
		}
		t.Log(line)
	}
	plan.Close()
	if err = plan.Err(); err != nil {
		t.Fatal(err)
	}
	stats, err := c.IncidentStats(scopeContext())
	if err != nil || stats.Total != 0 {
		t.Fatalf("empty aggregate %+v %v", stats, err)
	}
	trace.mu.Lock()
	n := len(trace.statements)
	trace.mu.Unlock()
	if n != 3 {
		t.Fatalf("list+two aggregates used %d queries, want 3", n)
	}
	t.Logf("1700 wells list+stats SQL round trips=%d (independent of row count)", n)
}
