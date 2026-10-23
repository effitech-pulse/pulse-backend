package migrations

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Additive миграция сохраняет legacy values/null/quality и не создаёт guard.
func TestCollectionMigration15PreservesTelemetryAndConstrainsWriter(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("collection_upgrade_%d", time.Now().UnixNano())
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { pool.Close(); _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	for _, sql := range []string{initialSchema, `CREATE TABLE backend_schema_migration(version integer PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now());INSERT INTO backend_schema_migration(version)VALUES(1)`, integrationSchema, wellIdentitySchema, manualChecksSchema, reviewsSchema, fundTypeSchema, accessSchema, wellLifecycleSchema, cohortSchema, snapshotSchema, runScopeSchema, fileInputsSchema, fileJobsSchema, cohortJobsSchema} {
		if _, e = pool.Exec(ctx, sql); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = pool.Exec(ctx, `INSERT INTO telemetry_source(id,config)VALUES('historic','{"profile_version":1}');INSERT INTO telemetry_measurement(well_id,measured_at,values,quality,source_id)VALUES('old','2026-01-01T00:00:00Z','{"F_out":0,"R_izol":null}','old-quality','historic')`); e != nil {
		t.Fatal(e)
	}
	var before, after string
	if e = pool.QueryRow(ctx, `SELECT json_build_array(values,quality,source_id)::text FROM telemetry_measurement`).Scan(&before); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e = Apply(ctx, pool); e != nil {
			t.Fatal(e)
		}
	}
	if e = pool.QueryRow(ctx, `SELECT json_build_array(values,quality,source_id)::text FROM telemetry_measurement`).Scan(&after); e != nil || before != after {
		t.Fatal("legacy telemetry changed", e)
	}
	var unsafeFK int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE contype='f' AND conrelid IN (SELECT oid FROM pg_class WHERE relnamespace=current_schema()::regnamespace AND relname LIKE 'collection_job%') AND confdeltype<>'r'`).Scan(&unsafeFK); e != nil || unsafeFK != 0 {
		t.Fatal("collection foreign key deletion not restricted", unsafeFK, e)
	}
	var n int
	if e = pool.QueryRow(ctx, "SELECT count(*) FROM collection_job_guard").Scan(&n); e != nil || n != 0 {
		t.Fatal("migration seeded authority", n, e)
	}
	if _, e = pool.Exec(ctx, "UPDATE telemetry_source SET writer_token_sha256=repeat('a',64) WHERE id='historic'"); e == nil {
		t.Fatal("half writer token accepted")
	}
	if _, e = pool.Exec(ctx, `INSERT INTO collection_job_guard(id,run_id,manifest_sha256,test_schema,fixture_origin,synthetic_only,accepting,expires_at)VALUES(true,'run',repeat('a',64),'synthetic','http://fixture:8080',true,true,now()+interval '25 hours')`); e == nil {
		t.Fatal("unbounded guard lifetime accepted")
	}
	if e = pool.QueryRow(ctx, "SELECT count(*) FROM telemetry_measurement WHERE collection_provenance IS NULL").Scan(&n); e != nil || n != 1 {
		t.Fatal("invented old provenance", n, e)
	}
}
