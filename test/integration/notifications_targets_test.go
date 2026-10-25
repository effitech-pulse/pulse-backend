package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

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
