package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/dailyflow"
	"github.com/effitech-pulse/pulse-backend/internal/adapter/spreadsheet"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestDailyFlowCurrentSessionScopeAndPinnedExport(t *testing.T) {
	for _, mode := range []domain.RBACMode{domain.RBACCompat, domain.RBACEnforce} {
		t.Run(string(mode), func(t *testing.T) { dailyFlowCurrentSession(t, mode) })
	}
}
func dailyFlowCurrentSession(t *testing.T, mode domain.RBACMode) {
	pool := testDB(t)
	ctx := context.Background()
	app := New(testConfig(), pool, &fakeML{})
	p, e := dailyflow.New(time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC))
	if e != nil {
		t.Fatal(e)
	}
	profile := p.Profile()
	// Isolated test schema only: exact observed profile, never runtime/PREP writes.
	for _, m := range profile.Members {
		if _, e = pool.Exec(ctx, `INSERT INTO well(id,unitek_id,armits_id,ngdu,department,fund_type,active) OVERRIDING SYSTEM VALUE VALUES($1,$2,$3,-900,-900,'presentation',true)`, m.WellID, m.UnitekID, m.ArmitsID); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = pool.Exec(ctx, `INSERT INTO app_cohort(version,manifest_sha256) VALUES($1,$2)`, profile.CohortVersion, profile.CohortManifestSHA256); e != nil {
		t.Fatal(e)
	}
	for _, m := range profile.Members {
		if _, e = pool.Exec(ctx, `INSERT INTO app_cohort_member(version,well_id,unitek_id,armits_id,revision) VALUES($1,$2,$3,$4,1)`, profile.CohortVersion, m.WellID, m.UnitekID, m.ArmitsID); e != nil {
			t.Fatal(e)
		}
	}
	scope, e := service.NewFundScope(ctx, app.store, profile.CohortVersion)
	if e != nil {
		t.Fatal(e)
	}
	app.http.Catalog.WithScope(scope)
	app.http.DailyFlow = service.NewDailyFlow(app.http.Catalog, scope, p, spreadsheet.DailyFlowWriter{}, "synthetic")
	access, e := service.NewAccess(app.store, true)
	if e != nil {
		t.Fatal(e)
	}
	in := accountInput("daily-admin", "admin")
	in.Scope.Funds = []string{"presentation"}
	admin, e := access.Bootstrap(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	adminCtx := domain.WithPrincipal(ctx, domain.AccountPrincipal(admin))
	for _, login := range []string{"daily-viewer", "daily-denied"} {
		in = accountInput(login, "viewer")
		in.Scope.Funds = []string{"presentation"}
		if login == "daily-denied" {
			in.Scope.NGDUs = ptr([]int64{0})
		}
		if _, e = access.Create(adminCtx, in); e != nil {
			t.Fatal(e)
		}
	}
	access.WithRBACMode(mode)
	app.http.Access = access
	h := app.Handler()
	cookie := loginCookie(t, h, "daily-viewer")
	denied := loginCookie(t, h, "daily-denied")
	path := "/api/v1/wells/1001/daily-flow"
	authRequest(t, h, "GET", path, "", nil, 401)
	authRequest(t, h, "GET", path, "", denied, 404)
	authRequest(t, h, "GET", "/api/v1/wells/9999/daily-flow?dataset_version=bad", "", cookie, 404)
	read := authRequest(t, h, "GET", path, "", cookie, 200)
	if read.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(read.Header())
	}
	var view domain.DailyFlowView
	if e = json.Unmarshal(read.Body.Bytes(), &view); e != nil {
		t.Fatal(e)
	}
	again := authRequest(t, h, "GET", path, "", cookie, 200)
	if !bytes.Equal(read.Body.Bytes(), again.Body.Bytes()) {
		t.Fatal("request identity drift")
	}
	export := fmt.Sprintf("%s/export?start_date=%s&end_date=%s&dataset_version=%s&view_id=%s", path, view.StartDate, view.EndDate, view.DatasetVersion, view.ViewID)
	file := authRequest(t, h, "GET", export, "", cookie, 200)
	f, e := excelize.OpenReader(bytes.NewReader(file.Body.Bytes()))
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	rows, e := f.GetRows("Суточный дебит")
	if e != nil || len(rows) != 31 {
		t.Fatal(len(rows), e)
	}
	authRequest(t, h, "GET", export+"bad", "", cookie, 409)
	authRequest(t, h, "GET", path+"?start_date=2026-10-24&start%5Fdate=2026-10-23", "", cookie, 422)
	if _, e = pool.Exec(ctx, `UPDATE well SET unitek_id=-42 WHERE id=1001`); e != nil {
		t.Fatal(e)
	}
	authRequest(t, h, "GET", export, "", cookie, 503)
	if _, e = pool.Exec(ctx, `UPDATE well SET unitek_id=-900000 WHERE id=1001`); e != nil {
		t.Fatal(e)
	}
	if mode == domain.RBACEnforce {
		if _, e = pool.Exec(ctx, `DELETE FROM auth_user_role WHERE user_id=(SELECT id FROM auth_user WHERE login='daily-viewer')`); e != nil {
			t.Fatal(e)
		}
		denied := authRequest(t, h, "GET", export, "", cookie, 403)
		if denied.Header().Get("Content-Disposition") != "" {
			t.Fatal("denied attachment")
		}
	}
	authRequest(t, h, "POST", "/api/v1/auth/logout", "", cookie, 204)
	authRequest(t, h, "GET", export, "", cookie, 401)
	for _, table := range []string{"telemetry_measurement", "manual_check", "ml_run", "telemetry_source"} {
		var n int
		if e = pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
}
