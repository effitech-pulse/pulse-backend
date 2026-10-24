package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

// Matches live outside the legacy last-200 page; ties cross every page boundary.
func TestTableLogsGlobalFiltersMultisortAndStableCursor(t *testing.T) {
	pool := testDB(t)
	ctx := domain.WithSystemPrincipal(context.Background())
	store := postgres.New(pool)
	svc := service.NewIntegrations(store, nil, nil)
	for i := 0; i < 230; i++ {
		message := "new"
		if i < 27 {
			message = "ӘӨҮҖҢҺ %_"
		}
		if _, err := pool.Exec(ctx, `INSERT INTO telemetry_collection_log(timestamp,source_id,accepted,duplicates,rejected,message) VALUES ('2026-10-24T10:00:00Z','fixture',$1,$2,0,$3)`, i%3, i%2, message); err != nil {
			t.Fatal(err)
		}
	}
	raw := `{"version":1,"filters":[{"field":"message","op":"contains","value":"әөүҗңһ %_"}],"sort":[{"field":"accepted","direction":"asc"},{"field":"duplicates","direction":"desc"}]}`
	seen := map[int64]bool{}
	cursor := ""
	lastAccepted := -1
	lastDuplicates := 2
	var lastID int64 = 999
	for {
		page, err := svc.LogsTable(ctx, raw, 4, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Items {
			if seen[row.ID] {
				t.Fatal("duplicate", row.ID)
			}
			seen[row.ID] = true
			if row.Accepted < lastAccepted || row.Accepted == lastAccepted && row.Duplicates > lastDuplicates || row.Accepted == lastAccepted && row.Duplicates == lastDuplicates && row.ID >= lastID {
				t.Fatalf("wrong lexicographic order: %+v after %d/%d/%d", row, lastAccepted, lastDuplicates, lastID)
			}
			lastAccepted = row.Accepted
			lastDuplicates = row.Duplicates
			lastID = row.ID
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 27 {
		t.Fatalf("global matches=%d want27", len(seen))
	}
	legacy, err := svc.Logs(ctx)
	if err != nil || len(legacy) != 200 {
		t.Fatal("legacy", len(legacy), err)
	}
	if _, err := svc.LogsTable(ctx, strings.Replace(raw, "asc", "desc", 1), 4, cursor); err == nil {
		t.Fatal("reused cursor after sort change")
	}
}

func TestTableChecksNullLastOverlapAndWellIsolation(t *testing.T) {
	pool := testDB(t)
	ctx := domain.WithSystemPrincipal(context.Background())
	store := postgres.New(pool)
	svc := service.NewChecks(store, nil)
	for _, well := range []int64{11, 12} {
		eventsWell(t, pool, well, 0)
	}
	for i := 0; i < 61; i++ {
		model := any("Модель")
		if i%3 == 0 {
			model = nil
		}
		raw, _ := json.Marshal(map[string]any{"model_name": model, "verdict": "normal"})
		if _, err := pool.Exec(ctx, `INSERT INTO manual_check(id,well_id,request_id,start_at,end_at,created_at,input_sha256,points_count,resolution,result) VALUES ($1,$2,$1,'2026-10-01T00:00:00Z','2026-10-10T00:00:00Z','2026-10-24T10:00:00Z','hash',1,'raw',$3)`, fmt.Sprintf("check-%02d", i), int64(11+i%2), raw); err != nil {
			t.Fatal(err)
		}
	}
	raw := `{"version":1,"filters":[{"field":"period","op":"overlap","from":"2026-10-10T03:00:00+03:00","to":"2026-10-11T00:00:00Z"}],"sort":[{"field":"model_name","direction":"desc"},{"field":"created_at","direction":"asc"}]}`
	cursor := ""
	seen := map[string]bool{}
	nullSeen := false
	for {
		page, err := svc.ListTable(ctx, 11, raw, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Items {
			if row.WellID != 11 || seen[row.ID] {
				t.Fatal("cross-parent/duplicate", row)
			}
			seen[row.ID] = true
			if row.Result.ModelName == nil {
				nullSeen = true
			} else if nullSeen {
				t.Fatal("nonnull follows null")
			}
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 31 {
		t.Fatalf("overlap inclusive count=%d", len(seen))
	}
	if _, err := svc.ListTable(ctx, 12, raw, 2, cursor); err == nil {
		t.Fatal("cross-parent cursor accepted")
	}
}

func TestTableHistoryDerivedChangesAndLegacyEnvelope(t *testing.T) {
	pool := testDB(t)
	ctx := domain.WithSystemPrincipal(context.Background())
	store := postgres.New(pool)
	catalog := service.NewCatalog(store)
	eventsWell(t, pool, 11, 0)
	if _, err := pool.Exec(ctx, `INSERT INTO well_event(well_id,revision,kind,actor,reason,before_state,after_state) VALUES (11,2,'metadata','{"name":"Технолог","id":"user:1","authenticated":true}','test','{"active":false,"well_number":"001A"}','{"active":true,"well_number":"001A"}')`); err != nil {
		t.Fatal(err)
	}
	raw := `{"version":1,"filters":[{"field":"changes","op":"contains","value":"Активна: Нет → Да"}],"sort":[{"field":"changes","direction":"asc"}]}`
	page, err := catalog.WellHistoryTable(ctx, 11, raw, 25, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Changes != "Активна: Нет → Да" {
		t.Fatalf("derived changes %+v", page)
	}
	app := New(testConfig(), pool, &fakeML{})
	h := app.Handler()
	for _, path := range []string{"/api/v1/audit?table_query=" + url.QueryEscape(`{"version":1,"filters":[],"sort":[]}`) + "&limit=10", "/api/v1/audit?table_query=" + url.QueryEscape(`{"version":1,"filters":[],"sort":[]}`) + "&cursor=forged"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestTableFileJobsRechecksPrivateActorAndCurrentGuard(t *testing.T) {
	pool, ctx, store, checks, v := fileJobSetupWithAccounts(t, []int64{1, 2, 3, 4})
	// Historical metadata read fixture exceeds the current synthetic retained-input quota.
	// Clone terminal metadata only; no payload/queue/compute or admission limit is changed.
	old := v
	old.Input = prepareSaved(t, checks, ctx, "table-old-request-000")
	job, err := store.AcceptFileJob(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CancelFileJob(ctx, 1, job.ID, job.Revision); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 30; i++ {
		oldID := fmt.Sprintf("%032x", i)
		if _, err := pool.Exec(ctx, `INSERT INTO file_input SELECT (jsonb_populate_record(NULL::file_input,to_jsonb(i)||jsonb_build_object('id',$1::text,'request_key',$2::text))).* FROM file_input i WHERE i.id=$3`, oldID, fmt.Sprintf("table-old-metadata-%03d", i), job.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO file_job SELECT (jsonb_populate_record(NULL::file_job,to_jsonb(j)||jsonb_build_object('id',$1::text,'input_id',$1::text))).* FROM file_job j WHERE j.id=$2`, oldID, job.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.AcceptFileJob(ctx, v); err != nil {
		t.Fatal(err)
	}
	ownID := v.Input.ID
	other, err := postgres.New(pool).CreateAccount(rbacFixtureManagerContext(t, pool), domain.Account{Login: "table-other", Name: "Other admin", Role: "admin", Scope: domain.AccessScope{Funds: []string{"operational"}}, Active: true}, "unused", false)
	if err != nil {
		t.Fatal(err)
	}
	otherCtx := fileJobFixtureSession(t, ctx, store, other)
	second := v
	second.Input = prepareSaved(t, checks, otherCtx, "table-other-request01")
	if _, err := store.AcceptFileJob(otherCtx, second); err != nil {
		t.Fatal(err)
	}
	own := v
	own.Input = prepareSaved(t, checks, ctx, "table-own-request002")
	if _, err := store.AcceptFileJob(ctx, own); err != nil {
		t.Fatal(err)
	}
	req := domain.TableRequest{PageSize: 1, Query: domain.TableQuery{Version: 1, Filters: []domain.TableFilter{{Field: "state", Op: "in", Values: []json.RawMessage{json.RawMessage(`"pending_dispatch"`)}}}, Sort: []domain.TableSort{{Field: "model_name", Direction: "asc"}, {Field: "created_at", Direction: "desc"}}}}
	first, err := store.FileJobsTable(ctx, 1, req)
	if err != nil || len(first.Items) != 1 || !first.HasMore {
		t.Fatalf("first %+v %v", first, err)
	}
	req.After = first.Keys
	last, err := store.FileJobsTable(ctx, 1, req)
	if err != nil || len(last.Items) != 1 || last.HasMore || last.Items[0].ID == first.Items[0].ID {
		t.Fatalf("last %+v %v", last, err)
	}
	for _, id := range []string{first.Items[0].ID, last.Items[0].ID} {
		if id != ownID && id != own.Input.ID {
			t.Fatal("private data leaked", id)
		}
	}
	req.After = nil
	others, err := store.FileJobsTable(otherCtx, 1, req)
	if err != nil || len(others.Items) != 1 || others.Items[0].ID != second.Input.ID || others.HasMore {
		t.Fatalf("other admin projection %+v %v", others, err)
	}
	history := domain.TableRequest{PageSize: 7, Query: domain.TableQuery{Version: 1, Filters: []domain.TableFilter{{Field: "state", Op: "in", Values: []json.RawMessage{json.RawMessage(`"cancelled"`)}}}}}
	terminal := map[string]bool{}
	for {
		part, err := store.FileJobsTable(ctx, 1, history)
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range part.Items {
			if terminal[job.ID] {
				t.Fatal("duplicate cancelled")
			}
			terminal[job.ID] = true
		}
		if !part.HasMore {
			break
		}
		history.After = part.Keys
	}
	if len(terminal) != 30 {
		t.Fatal("terminal history capped", len(terminal))
	}
	if _, err := pool.Exec(ctx, `UPDATE file_job_pilot_guard SET run_id='revoked'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FileJobsTable(ctx, 1, req); err == nil {
		t.Fatal("query bypassed current guard")
	}
}

func TestTableAuditGlobalWallClockNullAndExactLastPage(t *testing.T) {
	pool := testDB(t)
	ctx := domain.WithSystemPrincipal(context.Background())
	svc := service.NewCatalog(postgres.New(pool))
	for i := 0; i < 230; i++ {
		code := 200
		var actor any = "new"
		if i < 25 {
			code = 503
			actor = "Технолог"
			if i%3 == 0 {
				actor = nil
			}
		}
		if _, err := pool.Exec(ctx, `INSERT INTO action_log(ts,actor,method,path,status_code) VALUES ('2026-10-24T10:00:00.123456',$1,'GET','/fixture',$2)`, actor, code); err != nil {
			t.Fatal(err)
		}
	}
	raw := `{"version":1,"filters":[{"field":"status_code","op":"range","from":500.5},{"field":"ts","op":"range","from":"2026-10-24T10:00:00.123456","to":"2026-10-24T10:00:00.123456"}],"sort":[{"field":"actor","direction":"desc"},{"field":"ts","direction":"asc"}]}`
	page, err := svc.AuditTable(ctx, raw, 25, "")
	if err != nil || len(page.Items) != 25 || page.NextCursor != nil {
		t.Fatalf("exact page %+v %v", page, err)
	}
	nullSeen := false
	for _, row := range page.Items {
		if row.Actor == nil {
			nullSeen = true
		} else if nullSeen {
			t.Fatal("nonnull after null")
		}
		if row.Ts != "2026-10-24T10:00:00.123456" {
			t.Fatal("invented zone", row.Ts)
		}
	}
	cursor := ""
	seen := map[int64]bool{}
	for {
		part, err := svc.AuditTable(ctx, raw, 4, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range part.Items {
			if seen[row.ID] {
				t.Fatal("duplicate", row.ID)
			}
			seen[row.ID] = true
		}
		if part.NextCursor == nil {
			break
		}
		cursor = *part.NextCursor
	}
	if len(seen) != 25 {
		t.Fatal("not global", len(seen))
	}
	nulls, err := svc.AuditTable(ctx, `{"version":1,"filters":[{"field":"actor","op":"in","values":[null]}],"sort":[]}`, 25, "")
	if err != nil || len(nulls.Items) != 9 {
		t.Fatal("null selection", len(nulls.Items), err)
	}
}

func TestTableHistoryChangesPreservesStringBooleanTypesAndLF(t *testing.T) {
	pool := testDB(t)
	ctx := domain.WithSystemPrincipal(context.Background())
	eventsWell(t, pool, 11, 0)
	if _, err := pool.Exec(ctx, `INSERT INTO well_event(well_id,revision,kind,actor,reason,before_state,after_state) VALUES (11,2,'metadata','{}','fixture','{"well_number":"false","active":false,"responsible":null}','{"well_number":"true","active":true,"responsible":"true"}')`); err != nil {
		t.Fatal(err)
	}
	svc := service.NewCatalog(postgres.New(pool))
	raw := `{"version":1,"filters":[],"sort":[{"field":"changes","direction":"asc"}]}`
	page, err := svc.WellHistoryTable(ctx, 11, raw, 25, "")
	if err != nil {
		t.Fatal(err)
	}
	want := "№ скважины: false → true\nАктивна: Нет → Да\nОтветственный: — → true"
	if len(page.Items) != 1 || page.Items[0].Changes != want {
		t.Fatalf("typed display text: got%+v want%q", page.Items, want)
	}
	filtered, err := svc.WellHistoryTable(ctx, 11, `{"version":1,"filters":[{"field":"changes","op":"contains","value":"false → true"}],"sort":[]}`, 25, "")
	if err != nil || len(filtered.Items) != 1 {
		t.Fatal("literal string filter diverged", filtered, err)
	}
}

func TestTableBoundsRejectSubmicrosecondsBeforeSQLAndAcceptDecimalStrings(t *testing.T) {
	pool := testDB(t)
	ctx := domain.WithSystemPrincipal(context.Background())
	eventsWell(t, pool, 11, 0)
	for i, stamp := range []string{"2026-10-24T10:00:00.123456Z", "2026-10-24T10:00:00.123457Z"} {
		if _, err := pool.Exec(ctx, `INSERT INTO manual_check(id,well_id,request_id,start_at,end_at,created_at,input_sha256,points_count,resolution,result) VALUES ($1,11,$1,'2026-10-01','2026-10-10',$2,'hash',1,'raw','{}')`, fmt.Sprint(i), stamp); err != nil {
			t.Fatal(err)
		}
	}
	app := New(testConfig(), pool, &fakeML{})
	app.http.Checks = service.NewChecks(app.store, nil)
	h := app.Handler()
	for _, part := range []string{`"from":"2026-10-24T10:00:00.1234561Z"`, `"to":"2026-10-24T10:00:00.1234569Z"`} {
		raw := `{"version":1,"filters":[{"field":"created_at","op":"range",` + part + `}],"sort":[]}`
		call(t, h, "GET", "/api/v1/wells/11/checks?table_query="+url.QueryEscape(raw), "", 400)
	}
	checks := service.NewChecks(app.store, nil)
	exact, err := checks.ListTable(ctx, 11, `{"version":1,"filters":[{"field":"created_at","op":"range","from":"2026-10-24T13:00:00.123456000+03:00","to":"2026-10-24T10:00:00.123456Z"}],"sort":[]}`, 25, "")
	if err != nil || len(exact.Items) != 1 || exact.Items[0].ID != "0" {
		t.Fatal("exact inclusive microsecond", exact, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO telemetry_collection_log(source_id,accepted,duplicates,rejected,message) VALUES ('fixture',2,0,0,'fixture')`); err != nil {
		t.Fatal(err)
	}
	logs := service.NewIntegrations(app.store, nil, nil)
	for _, test := range []struct {
		from, to string
		count    int
	}{{"1.5", "2", 1}, {"9007199254740993", "9007199254740994", 0}} {
		raw := `{"version":1,"filters":[{"field":"accepted","op":"range","from":"` + test.from + `","to":"` + test.to + `"}],"sort":[]}`
		page, err := logs.LogsTable(ctx, raw, 25, "")
		if err != nil || len(page.Items) != test.count {
			t.Fatal("exact numeric string", test, page, err)
		}
	}
}
