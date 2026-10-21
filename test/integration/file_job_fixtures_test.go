package integration

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func fileJobSetup(t *testing.T) (*pgxpool.Pool, context.Context, *postgres.Repository, *service.FileChecks, domain.FileJobPrepared) {
	t.Helper()
	return fileJobSetupWithAccounts(t, []int64{1})
}
func fileJobSetupWithAccounts(t *testing.T, accounts []int64) (*pgxpool.Pool, context.Context, *postgres.Repository, *service.FileChecks, domain.FileJobPrepared) {
	t.Helper()
	pool, ctx, checks, _ := savedSetup(t)
	v := prepareSaved(t, checks, ctx, "q1-accept-request01")
	spec := savedSpec()
	sort.Slice(spec.Mappings, func(i, j int) bool { return spec.Mappings[i].Field < spec.Mappings[j].Field })
	raw, _ := json.Marshal(spec)
	p := domain.DefaultFileJobPolicy()
	p.TestSchema = pool.Config().ConnConfig.RuntimeParams["search_path"]
	p.AllowedAccountIDs = accounts
	p.RunID = "q1-integration"
	p.ManifestSHA256 = domain.FileSHA256([]byte("q1-test-guard"))
	p.CohortVersion = "q1-test"
	p.CohortManifestSHA256 = strings.Repeat("a", 64)
	p.AllowedInputs = []domain.FileJobAllowedInput{{WellID: 1, RawSHA256: v.RawSHA256, Format: "csv", ClientSpecJSON: raw, ClientSpecSHA256: domain.FileSHA256(raw), ExecutionPin: savedPin()}}
	if _, e := pool.Exec(ctx, `INSERT INTO app_cohort(version,manifest_sha256) VALUES($1,$2);`, p.CohortVersion, p.CohortManifestSHA256); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO app_cohort_member(version,well_id,revision) VALUES($1,1,1)`, p.CohortVersion); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO file_job_pilot_guard(id,run_id,manifest_sha256,synthetic_only) VALUES(true,$1,$2,true)`, p.RunID, p.ManifestSHA256); e != nil {
		t.Fatal(e)
	}
	store, e := postgres.New(pool).WithFileJobPolicy(p)
	if e != nil {
		t.Fatal(e)
	}
	return pool, ctx, store, checks, domain.FileJobPrepared{Input: v, Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 1, 2, 0, 0, 0, 1000, time.UTC), PointsCount: 2}
}
func fileJobMessage(t *testing.T, pool *pgxpool.Pool, id string) domain.FileJobMessage {
	t.Helper()
	m := domain.FileJobMessage{Version: 1, JobID: id}
	if e := pool.QueryRow(context.Background(), `SELECT event_id,generation FROM file_job_outbox WHERE job_id=$1 ORDER BY generation DESC LIMIT 1`, id).Scan(&m.EventID, &m.Generation); e != nil {
		t.Fatal(e)
	}
	return m
}
