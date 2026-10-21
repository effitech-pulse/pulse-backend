package migrations

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFileInputUpgradeElevenPreservesLegacyReads(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("q0_upgrade_%d", time.Now().UnixNano())
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
	// Exact existing schema1..11 migrations, before additive Q0 schema12.
	for _, sql := range []string{initialSchema, `CREATE TABLE backend_schema_migration(version integer PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now());INSERT INTO backend_schema_migration(version) VALUES(1)`, integrationSchema, wellIdentitySchema, manualChecksSchema, reviewsSchema, fundTypeSchema, accessSchema, wellLifecycleSchema, cohortSchema, snapshotSchema, runScopeSchema} {
		if _, e = pool.Exec(ctx, sql); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = pool.Exec(ctx, `INSERT INTO well(ngdu,department,active) VALUES(0,0,true);INSERT INTO manual_check(id,well_id,request_id,start_at,end_at,created_at,input_sha256,points_count,resolution,result) VALUES('old-check',1,'old-request','2026-01-01','2026-02-01',now(),'old',1,'raw','{"verdict":"norm"}')`); e != nil {
		t.Fatal(e)
	}
	var before int
	if e = pool.QueryRow(ctx, "SELECT max(version) FROM backend_schema_migration").Scan(&before); e != nil || before != 11 {
		t.Fatal("not baseline11", before, e)
	}
	if e = Apply(ctx, pool); e != nil {
		t.Fatal(e)
	}
	if e = Apply(ctx, pool); e != nil {
		t.Fatal(e)
	}
	// Unchanged old application SELECT projections still read the pre-upgrade row.
	var verdict string
	var id int64
	if e = pool.QueryRow(ctx, "SELECT c.result->>'verdict',w.id FROM manual_check c JOIN well w ON w.id=c.well_id WHERE c.id='old-check'").Scan(&verdict, &id); e != nil || verdict != "norm" || id != 1 {
		t.Fatal("legacy row changed", verdict, id, e)
	}
	var version int
	if e = pool.QueryRow(ctx, "SELECT max(version) FROM backend_schema_migration").Scan(&version); e != nil || version != CurrentVersion {
		t.Fatal(version, e)
	}
	var immutable int
	if e = pool.QueryRow(ctx, "SELECT count(*) FROM pg_trigger WHERE tgname='file_input_immutable' AND tgrelid IN ('file_input'::regclass,'file_input_payload'::regclass,'file_check_result'::regclass)").Scan(&immutable); e != nil || immutable != 3 {
		t.Fatal("trigger duplication/missing", immutable, e)
	}
}
