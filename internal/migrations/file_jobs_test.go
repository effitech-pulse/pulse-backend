package migrations

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFileJobUpgradeTwelveAndRepeat(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("q1_upgrade_%d", time.Now().UnixNano())
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
	for _, sql := range []string{initialSchema, `CREATE TABLE backend_schema_migration(version integer PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now());INSERT INTO backend_schema_migration(version) VALUES(1)`, integrationSchema, wellIdentitySchema, manualChecksSchema, reviewsSchema, fundTypeSchema, accessSchema, wellLifecycleSchema, cohortSchema, snapshotSchema, runScopeSchema, fileInputsSchema} {
		if _, e = pool.Exec(ctx, sql); e != nil {
			t.Fatal(e)
		}
	}
	var version int
	if e = pool.QueryRow(ctx, "SELECT max(version) FROM backend_schema_migration").Scan(&version); e != nil || version != 12 {
		t.Fatal("not schema12", version, e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO well(ngdu,department,active) VALUES(0,0,true);INSERT INTO manual_check(id,well_id,request_id,start_at,end_at,created_at,input_sha256,points_count,resolution,result) VALUES('legacy',1,'legacy-key','2026-01-01','2026-02-01',now(),'old',1,'raw','{"verdict":"norm"}');INSERT INTO case_review(kind,target_id,check_id,well_id,state) VALUES('check','legacy','legacy',1,'{"status":"new"}')`); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e = Apply(ctx, pool); e != nil {
			t.Fatal(e)
		}
	}
	if e = pool.QueryRow(ctx, "SELECT max(version) FROM backend_schema_migration").Scan(&version); e != nil || version != CurrentVersion {
		t.Fatal(version, e)
	}
	var n int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM file_job_pilot_guard`).Scan(&n); e != nil || n != 0 {
		t.Fatal("migration enabled synthetic guard", n, e)
	}
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM case_review WHERE kind='check' AND target_id='legacy' AND file_job_id IS NULL`).Scan(&n); e != nil || n != 1 {
		t.Fatal("legacy review lost", n, e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO case_review(kind,target_id,check_id,well_id,state) VALUES('file_check','legacy','legacy',1,'{}')`); e == nil {
		t.Fatal("private typed target constraint missing")
	}
}
