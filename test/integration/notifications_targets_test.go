package integration

import (
	"context"
	"encoding/json"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

func TestNotificationExactInt64AndDeletedAnchor(t *testing.T) {
	p := testDB(t)
	r := postgres.New(p)
	ctx := context.Background()
	s := notificationScope(t, p, r, `{"kind":"organization","ngdu_id":null,"department_id":null}`, "alerts.read", "risks.read", "wells.read")
	notificationSeed(t, p)
	target := domain.NotificationAlertLocator{WellID: 9007199254740993, RunID: 9007199254740995, AlertID: 9007199254740997}
	v, e := r.ReadNotificationAlert(ctx, s, target)
	if e != nil || v.Target.RunID != target.RunID || v.Target.AlertID != target.AlertID || v.Target.Risk == nil || *v.Target.Risk != 0 || v.SavedResult != nil {
		t.Fatal(v, e)
	}
	target.RunID = 1
	if _, e = r.ReadNotificationAlert(ctx, s, target); e == nil {
		t.Fatal("wrong tuple accepted")
	}
	eventsExec(t, p, `INSERT INTO ml_run(id,status,trigger) OVERRIDING SYSTEM VALUE VALUES(11,'done','second');INSERT INTO ml_alert(id,run_id,app_well_id,risk,status) OVERRIDING SYSTEM VALUE VALUES(12,11,9007199254740993,0,'new');
 INSERT INTO notification_fact(activation_id,kind,alert_id,run_id,well_id,code,recorded_at) SELECT activation_id,kind,12,11,well_id,code,recorded_at FROM notification_fact`)
	page, e := r.ReadNotificationPage(ctx, domain.NotificationQuery{Scope: s, State: domain.NotificationStateAll, Limit: 1})
	if e != nil || len(page.Items) != 2 {
		t.Fatal(page, e)
	}
	anchor := page.Items[0]
	eventsExec(t, p, `DELETE FROM ml_alert WHERE id=12`)
	next, e := r.ReadNotificationPage(ctx, domain.NotificationQuery{Scope: s, State: domain.NotificationStateAll, Limit: 1, Before: &domain.NotificationPosition{Key: anchor.Key, Boundary: page.Boundary}})
	if e != nil || len(next.Items) != 1 || next.Items[0].Alert.AlertID != 9007199254740997 {
		t.Fatal(next, e)
	}
}
func TestNotificationSafeSourceCanary(t *testing.T) {
	p := testDB(t)
	r := postgres.New(p)
	ctx := context.Background()
	s := notificationScope(t, p, r, `{"kind":"organization","ngdu_id":null,"department_id":null}`, "integrations.read")
	eventsExec(t, p, `UPDATE auth_user SET scope='{"funds":["operational","presentation"]}' WHERE id=$1`, *s.Principal.AccountID)
	eventsExec(t, p, `INSERT INTO telemetry_source(id,config,error) VALUES('opaque / ? source','{"name":"Safe","kind":"postgres","status":"running","interval_seconds":60,"config":{"password":"CANARY"},"unknown":"CANARY"}','CANARY')`)
	v, e := r.ReadNotificationSource(ctx, s, "opaque / ? source")
	if e != nil || v.Name != "Safe" || v.Status != "error" || v.ErrorCode == nil {
		t.Fatal(v, e)
	}
	raw, e := json.Marshal(v)
	if e != nil || strings.Contains(string(raw), "CANARY") {
		t.Fatal(string(raw), e)
	}
	eventsExec(t, p, `UPDATE auth_user SET organization_scope='{"kind":"ngdu","ngdu_id":1,"department_id":null}' WHERE id=$1`, *s.Principal.AccountID)
	if _, e = r.ReadNotificationSource(ctx, s, "opaque / ? source"); e == nil {
		t.Fatal("scoped source target leaked")
	}
}

func TestNotificationCohortExactSavedOutcome(t *testing.T) {
	p := testDB(t)
	r := postgres.New(p)
	ctx := context.Background()
	s := notificationScope(t, p, r, `{"kind":"organization","ngdu_id":null,"department_id":null}`, "alerts.read", "risks.read", "wells.read")
	notificationSeed(t, p)
	eventsExec(t, p, `INSERT INTO app_cohort(version,manifest_sha256)VALUES('exact',repeat('a',64));
 UPDATE ml_run SET profile='cohort',cohort_version='exact' WHERE id=9007199254740995;
 INSERT INTO ml_run_snapshot(run_id,snapshot_id,format_version,cohort_version,as_of,provenance_status,output_sha256,snapshot) VALUES(9007199254740995,'exact',1,'exact',now(),'partial',repeat('a',64),'{"outcomes":[{"app_well_id":9007199254740993,"status":"scored","result":{"verdict":"watch","model_name":"Saved","model_version":"v1"}},{"app_well_id":2,"status":"scored","result":{"verdict":"alert","secret":"CANARY"}}]}')`)
	target := domain.NotificationAlertLocator{WellID: 9007199254740993, RunID: 9007199254740995, AlertID: 9007199254740997}
	got, e := r.ReadNotificationAlert(ctx, s, target)
	if e != nil || got.SnapshotID == nil || *got.SnapshotID != "exact" || got.SavedResult == nil || got.SavedResult.Verdict != "watch" || got.SavedResult.ModelName != "Saved" {
		t.Fatal(got, e)
	}
	target.WellID = 2
	if _, e = r.ReadNotificationAlert(ctx, s, target); e == nil {
		t.Fatal("wrong cohort well accepted")
	}
}

// Oversized opaque identities must fail in the repository, before service/DTO projection.
func TestNotificationOversizedPGProjections(t *testing.T) {
	p := testDB(t)
	r := postgres.New(p)
	ctx := context.Background()
	s := notificationScope(t, p, r, `{"kind":"organization","ngdu_id":null,"department_id":null}`, "integrations.read")
	eventsExec(t, p, `UPDATE auth_user SET scope='{"funds":["operational","presentation"]}' WHERE id=$1`, *s.Principal.AccountID)
	eventsExec(t, p, `UPDATE notification_activation SET activation_id='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',activated_at=clock_timestamp();
 INSERT INTO telemetry_source(id,config,error)VALUES(repeat('x',65536),'{"name":"safe","kind":"postgres","status":"running","interval_seconds":60,"config":{"secret":"CANARY"}}','CANARY');
 INSERT INTO telemetry_collection_log(source_id,accepted,duplicates,rejected,message,outcome,attempt_profile_version,attempt_generation,attempt_token_sha256) SELECT repeat('x',65536),0,0,0,'CANARY','failed',1,g,repeat('a',64)FROM generate_series(1,26)g;
 INSERT INTO notification_fact(activation_id,kind,collection_log_id,source_id,code)SELECT 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','collection.failed.v1',id,source_id,'collection_failed'FROM telemetry_collection_log`)
	t.Run("list fails before retaining IDs", func(t *testing.T) {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		page, e := r.ReadNotificationPage(ctx, domain.NotificationQuery{Scope: s, State: domain.NotificationStateAll, Limit: 25})
		runtime.ReadMemStats(&after)
		t.Logf("retained records=%d total allocated bytes=%d", len(page.Items), after.TotalAlloc-before.TotalAlloc)
		if after.TotalAlloc-before.TotalAlloc > 1024*1024 {
			t.Errorf("oversized ID allocation reached %d bytes", after.TotalAlloc-before.TotalAlloc)
		}
		if e == nil {
			t.Fatalf("repository retained %d oversized IDs", len(page.Items))
		}
		rbacReadErrorKind(t, e, domain.Unavailable)
		if len(page.Items) != 0 {
			t.Fatal("partial list on resource failure")
		}
	})
	t.Run("safe source fails before projection", func(t *testing.T) {
		v, e := r.ReadNotificationSource(ctx, s, strings.Repeat("x", 65536))
		if e == nil {
			t.Fatal("repository loaded oversized source", len(v.ID))
		}
		rbacReadErrorKind(t, e, domain.Unavailable)
		if v.ID != "" {
			t.Fatal("partial source projection")
		}
	})
	t.Run("count keeps oversized facts", func(t *testing.T) {
		count, e := r.CountUnreadNotifications(ctx, s)
		if e != nil || count.Value != 26 {
			t.Fatal(count, e)
		}
	})
	t.Run("mark needs no opaque payload", func(t *testing.T) {
		var id int64
		if e := p.QueryRow(ctx, `SELECT min(id) FROM notification_fact`).Scan(&id); e != nil {
			t.Fatal(e)
		}
		if _, e := r.MarkNotificationRead(ctx, s, id); e != nil {
			t.Fatal(e)
		}
	})
}

func TestNotificationPageProjectionBudget(t *testing.T) {
	p := testDB(t)
	r := postgres.New(p)
	ctx := context.Background()
	s := notificationScope(t, p, r, `{"kind":"organization","ngdu_id":null,"department_id":null}`, "integrations.read")
	eventsExec(t, p, `UPDATE auth_user SET scope='{"funds":["operational","presentation"]}' WHERE id=$1`, *s.Principal.AccountID)
	eventsExec(t, p, `UPDATE notification_activation SET activation_id='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',activated_at=clock_timestamp();
 INSERT INTO telemetry_source(id,config)VALUES(repeat('x',4000),'{"name":"safe","kind":"postgres","status":"running","interval_seconds":60}');
 INSERT INTO telemetry_collection_log(source_id,accepted,duplicates,rejected,message,outcome,attempt_profile_version,attempt_generation,attempt_token_sha256) SELECT repeat('x',4000),0,0,0,'failed','failed',1,g,repeat('a',64)FROM generate_series(1,101)g;
 INSERT INTO notification_fact(activation_id,kind,collection_log_id,source_id,code)SELECT 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','collection.failed.v1',id,source_id,'collection_failed'FROM telemetry_collection_log`)
	page, e := r.ReadNotificationPage(ctx, domain.NotificationQuery{Scope: s, State: domain.NotificationStateAll, Limit: 100})
	if e == nil {
		t.Fatal("page retained more than256KiB IDs", len(page.Items))
	}
	rbacReadErrorKind(t, e, domain.Unavailable)
	if len(page.Items) != 0 {
		t.Fatal("partial oversized page")
	}
	small, e := r.ReadNotificationPage(ctx, domain.NotificationQuery{Scope: s, State: domain.NotificationStateAll, Limit: 1})
	if e != nil || len(small.Items) != 2 || small.Items[0].Collection.SourceID != strings.Repeat("x", 4000) {
		t.Fatal("opaque identity changed or hidden", len(small.Items), e)
	}
}

func TestNotificationOversizedSnapshotProjection(t *testing.T) {
	p := testDB(t)
	r := postgres.New(p)
	ctx := context.Background()
	s := notificationScope(t, p, r, `{"kind":"organization","ngdu_id":null,"department_id":null}`, "alerts.read", "risks.read", "wells.read")
	notificationSeed(t, p)
	eventsExec(t, p, `ALTER TABLE ml_run_snapshot ALTER COLUMN snapshot_id SET COMPRESSION lz4;
 INSERT INTO app_cohort(version,manifest_sha256)VALUES('oversized',repeat('a',64));
 UPDATE ml_run SET profile='cohort',cohort_version='oversized' WHERE id=9007199254740995;
 INSERT INTO ml_run_snapshot(run_id,snapshot_id,format_version,cohort_version,as_of,provenance_status,output_sha256,snapshot) VALUES(9007199254740995,repeat('x',300000),1,'oversized',now(),'partial',repeat('a',64),'{"outcomes":[{"app_well_id":9007199254740993,"status":"scored","result":{"verdict":"watch"}}]}')`)
	got, e := r.ReadNotificationAlert(ctx, s, domain.NotificationAlertLocator{WellID: 9007199254740993, RunID: 9007199254740995, AlertID: 9007199254740997})
	if e == nil {
		t.Fatal("repository retained oversized snapshot identity", len(*got.SnapshotID))
	}
	rbacReadErrorKind(t, e, domain.Unavailable)
	if got.SnapshotID != nil || got.SavedResult != nil {
		t.Fatal("partial oversized saved context")
	}
}

// Actual server-generated run times must describe the same instant as the fact,
// while the ML window timestamp keeps its separate UTC provenance.
func TestNotificationRunWriterTimezone(t *testing.T) {
	for _, zone := range []string{"Europe/Moscow", "UTC"} {
		t.Run(zone, func(t *testing.T) {
			fixture := testDB(t)
			ctx := legacyResearchFixtureContext()
			location, e := time.LoadLocation(zone)
			if e != nil {
				t.Fatal(e)
			}
			dsn, e := url.Parse(fixture.Config().ConnString())
			if e != nil {
				t.Fatal(e)
			}
			query := dsn.Query()
			query.Set("search_path", fixture.Config().ConnConfig.RuntimeParams["search_path"])
			dsn.RawQuery = query.Encode()
			pool, e := postgres.Open(ctx, config.Config{DatabaseURL: dsn.String(), Location: location, PoolSize: 4})
			if e != nil {
				t.Fatal(e)
			}
			defer pool.Close()
			r := postgres.New(pool)
			var actualZone string
			if e = pool.QueryRow(ctx, `SHOW TimeZone`).Scan(&actualZone); e != nil || actualZone != zone {
				t.Fatal(actualZone, e)
			}
			scope := notificationScope(t, pool, r, `{"kind":"organization","ngdu_id":null,"department_id":null}`, "alerts.read", "risks.read", "wells.read")
			eventsExec(t, pool, `UPDATE notification_activation SET activation_id='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',activated_at=clock_timestamp()`)
			external := int64(77881)
			well, e := r.CreateWell(ctx, domain.Well{UnitekID: &external, NGDU: 1, Department: 1, FundType: "operational", Active: true})
			if e != nil {
				t.Fatal(e)
			}
			lease, ok, e := r.Reserve(ctx)
			if e != nil || !ok {
				t.Fatal(ok, e)
			}
			defer lease.Close()
			before := time.Now().UTC()
			run, e := lease.Begin(ctx, "timezone")
			if e != nil {
				t.Fatal(e)
			}
			window := "2026-10-25T12:00:00Z"
			if e = lease.Persist(ctx, run, domain.Snapshot{Wells: []domain.WellRisk{{UnitekID: &external, Alert: true, WindowEnd: &window}}}); e != nil {
				t.Fatal(e)
			}
			after := time.Now().UTC()
			var alert int64
			var fact time.Time
			if e = pool.QueryRow(ctx, `SELECT alert_id,recorded_at FROM notification_fact WHERE run_id=$1`, run).Scan(&alert, &fact); e != nil {
				t.Fatal(e)
			}
			locator := domain.NotificationAlertLocator{WellID: well.ID, RunID: run, AlertID: alert}
			got, e := r.ReadNotificationAlert(ctx, scope, locator)
			if e != nil {
				t.Fatal(e)
			}
			t.Logf("writer_zone=%s before=%s run_started=%s run_finished=%v fact=%s after=%s", zone, before.Format(time.RFC3339Nano), got.RunStartedAt.UTC().Format(time.RFC3339Nano), got.RunFinishedAt, fact.UTC().Format(time.RFC3339Nano), after.Format(time.RFC3339Nano))
			if got.RunStartedAt.Before(before.Add(-time.Second)) || got.RunStartedAt.After(fact.Add(time.Second)) || got.RunStartedAt.After(after.Add(time.Second)) {
				t.Fatal("run start is not the actual writer instant", got.RunStartedAt, fact)
			}
			if got.RunFinishedAt == nil || got.RunFinishedAt.Before(got.RunStartedAt) || got.RunFinishedAt.After(fact.Add(time.Second)) || got.RunFinishedAt.After(after.Add(time.Second)) {
				t.Fatal("run finish is not the actual writer instant", got.RunFinishedAt, fact)
			}
			expectedWindow := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
			if got.Target.WindowEnd == nil || !got.Target.WindowEnd.Equal(expectedWindow) {
				t.Fatal("ML UTC window provenance changed", got.Target.WindowEnd)
			}
			eventsExec(t, pool, `UPDATE ml_run SET finished_at=NULL WHERE id=$1`, run)
			nullable, e := r.ReadNotificationAlert(ctx, scope, locator)
			if e != nil || nullable.RunFinishedAt != nil || !nullable.RunStartedAt.Equal(got.RunStartedAt) {
				t.Fatal("nullable legacy finish changed", nullable.RunFinishedAt, e)
			}
		})
	}
}
