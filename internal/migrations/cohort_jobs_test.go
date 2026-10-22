package migrations

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCohortMigration14PreservesSnapshots(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("q2_upgrade_%d", time.Now().UnixNano())
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
	for _, sql := range []string{initialSchema, `CREATE TABLE backend_schema_migration(version integer PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now());INSERT INTO backend_schema_migration(version) VALUES(1)`, integrationSchema, wellIdentitySchema, manualChecksSchema, reviewsSchema, fundTypeSchema, accessSchema, wellLifecycleSchema, cohortSchema, snapshotSchema, runScopeSchema, fileInputsSchema, fileJobsSchema} {
		if _, e = pool.Exec(ctx, sql); e != nil {
			t.Fatal(e)
		}
	}
	var version int
	if e = pool.QueryRow(ctx, "SELECT max(version) FROM backend_schema_migration").Scan(&version); e != nil || version != 13 {
		t.Fatal(version, e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO app_cohort(version,manifest_sha256) VALUES('historic',repeat('a',64));INSERT INTO ml_run(status,trigger,started_at,profile,cohort_version) VALUES('done','manual',now(),'cohort','historic');INSERT INTO ml_run_snapshot(run_id,snapshot_id,format_version,cohort_version,as_of,provenance_status,output_sha256,snapshot) VALUES(1,'old',1,'historic',now(),'partial',repeat('b',64),'{"old":false,"zero":0,"absent":null}')`); e != nil {
		t.Fatal(e)
	}
	var before, after, digest string
	if e = pool.QueryRow(ctx, "SELECT snapshot::text FROM ml_run_snapshot WHERE run_id=1").Scan(&before); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e = Apply(ctx, pool); e != nil {
			t.Fatal(e)
		}
	}
	if e = pool.QueryRow(ctx, "SELECT snapshot::text,output_sha256 FROM ml_run_snapshot WHERE run_id=1").Scan(&after, &digest); e != nil || before != after || digest != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatal("history changed", e)
	}
	var n int
	if e = pool.QueryRow(ctx, "SELECT count(*) FROM cohort_job_guard").Scan(&n); e != nil || n != 0 {
		t.Fatal("guard seeded", n, e)
	}
	if e = pool.QueryRow(ctx, "SELECT max(version) FROM backend_schema_migration").Scan(&version); e != nil || version != 14 {
		t.Fatal(version, e)
	}
}
