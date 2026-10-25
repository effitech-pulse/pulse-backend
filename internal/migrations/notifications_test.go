package migrations

import (
	"context"
	"testing"
)

func TestNotificationMigrationConstraints(t *testing.T) {
	p := rbacMigrationDB(t)
	ctx := context.Background()
	if e := Apply(ctx, p); e != nil {
		t.Fatal(e)
	}
	exec := func(sql string) {
		t.Helper()
		if _, e := p.Exec(ctx, sql); e != nil {
			t.Fatal(e)
		}
	}
	reject := func(sql string) {
		t.Helper()
		if _, e := p.Exec(ctx, sql); e == nil {
			t.Fatal("invalid mutation accepted", sql)
		}
	}
	exec(`UPDATE notification_activation SET activation_id='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',activated_at=clock_timestamp();
 INSERT INTO auth_user(login,name,password_hash,role,scope)VALUES('reader','reader','hash','viewer','{"funds":["operational"]}');
 INSERT INTO well(ngdu,department)VALUES(1,1);
 INSERT INTO ml_run(status,trigger)VALUES('done','test');
 INSERT INTO ml_alert(run_id,app_well_id,risk,status)SELECT r.id,w.id,0,'new' FROM ml_run r,well w;
 INSERT INTO notification_fact(activation_id,kind,alert_id,run_id,well_id,code)SELECT 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','alert.created.v1',id,run_id,app_well_id,'alert_created' FROM ml_alert;
 INSERT INTO notification_read(account_id,notification_id)SELECT u.id,f.id FROM auth_user u,notification_fact f;`)
	reject(`UPDATE notification_fact SET code='alert_created'`)
	reject(`DELETE FROM notification_activation`)
	reject(`UPDATE notification_activation SET activation_id='bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'`)
	reject(`INSERT INTO telemetry_collection_log(source_id,accepted,duplicates,rejected,message,outcome)VALUES('source',0,0,0,'failed','failed')`)
	exec(`INSERT INTO telemetry_collection_log(source_id,accepted,duplicates,rejected,message,outcome,attempt_profile_version,attempt_generation,attempt_token_sha256)VALUES('source',0,0,0,'failed','failed',1,1,repeat('a',64))`)
	reject(`INSERT INTO telemetry_collection_log(source_id,accepted,duplicates,rejected,message,outcome,attempt_profile_version,attempt_generation,attempt_token_sha256)VALUES('source',0,0,0,'completed','completed',1,1,repeat('a',64))`)
	exec(`DELETE FROM well`)
	var facts, reads int
	if e := p.QueryRow(ctx, `SELECT (SELECT count(*) FROM notification_fact),(SELECT count(*) FROM notification_read)`).Scan(&facts, &reads); e != nil || facts != 0 || reads != 0 {
		t.Fatal(facts, reads, e)
	}
}
