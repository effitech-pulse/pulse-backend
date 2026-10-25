package integration

import (
	"context"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/migrations"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

func TestNotificationMigration18PreservesHistory(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	// Reconstruct schema18 in this disposable schema, leaving versions1–18 SQL untouched.
	eventsExec(t, p, `DROP TABLE notification_read,notification_fact,notification_activation;
 DROP FUNCTION notification_fact_immutable(),notification_activation_immutable();
 DROP INDEX ml_alert_notification_identity,telemetry_collection_log_identity,telemetry_collection_terminal_attempt;
 ALTER TABLE telemetry_collection_log DROP CONSTRAINT telemetry_collection_attempt,DROP COLUMN outcome,DROP COLUMN attempt_profile_version,DROP COLUMN attempt_generation,DROP COLUMN attempt_token_sha256;
 ALTER TABLE ml_run_snapshot ALTER COLUMN run_id TYPE integer;
 DELETE FROM backend_schema_migration WHERE version=19`)
	eventsWell(t, p, 9007199254740993, 1)
	eventsExec(t, p, `INSERT INTO ml_run(id,status,trigger) OVERRIDING SYSTEM VALUE VALUES(9007199254740995,'done','old');
 INSERT INTO ml_alert(id,run_id,app_well_id,risk,status) OVERRIDING SYSTEM VALUE VALUES(9007199254740997,9007199254740995,9007199254740993,0,'confirmed');
 INSERT INTO telemetry_collection_log(source_id,accepted,duplicates,rejected,message) VALUES('old',0,0,0,'failed-looking historical message')`)
	var before, after string
	const fingerprint = `SELECT md5((SELECT string_agg(row_to_json(a)::text,',' ORDER BY id) FROM ml_alert a)||(SELECT string_agg(row_to_json(r)::text,',' ORDER BY id) FROM ml_run r)||COALESCE((SELECT string_agg(row_to_json(p)::text,',' ORDER BY role_id,permission_key) FROM auth_role_permission p),'empty'))`
	if e := p.QueryRow(ctx, fingerprint).Scan(&before); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e := migrations.Apply(ctx, p); e != nil {
			t.Fatal(e)
		}
	}
	if e := p.QueryRow(ctx, fingerprint).Scan(&after); e != nil || before != after {
		t.Fatal(before, after, e)
	}
	var n int
	var outcome, typ string
	if e := p.QueryRow(ctx, `SELECT outcome FROM telemetry_collection_log`).Scan(&outcome); e != nil || outcome != "historical_unknown" {
		t.Fatal(outcome, e)
	}
	if e := p.QueryRow(ctx, `SELECT count(*) FROM notification_fact`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	if e := p.QueryRow(ctx, `SELECT data_type FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='ml_run_snapshot' AND column_name='run_id'`).Scan(&typ); e != nil || typ != "bigint" {
		t.Fatal(typ, e)
	}
	if e := postgres.New(p).ValidateNotifications(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e := postgres.New(p).ActivateNotifications(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `UPDATE notification_activation SET activated_at=clock_timestamp()`); e == nil {
		t.Fatal("mutable boundary")
	}
}
