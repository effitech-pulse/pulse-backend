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
)

func cohortWithFilePolicy(t *testing.T) (*pgxpool.Pool, context.Context, *postgres.Repository, domain.PreparedCohortJob, domain.FileJobPrepared) {
	t.Helper()
	pool, ctx, r, v := cohortJobSetup(t)
	if _, e := pool.Exec(ctx, "UPDATE well SET unitek_id=9007199254740993,armits_id=42 WHERE id=1"); e != nil {
		t.Fatal(e)
	}
	checks := savedService(t, pool, &savedScorer{expectedPin: savedPin()}, savedPin(), domain.DefaultFileInputLimits())
	input := prepareSaved(t, checks, ctx, "q1-with-q2-000001")
	spec := savedSpec()
	sort.Slice(spec.Mappings, func(i, j int) bool { return spec.Mappings[i].Field < spec.Mappings[j].Field })
	specJSON, _ := json.Marshal(spec)
	p := domain.DefaultFileJobPolicy()
	p.TestSchema = pool.Config().ConnConfig.RuntimeParams["search_path"]
	p.RunID = "q1-with-q2"
	p.ManifestSHA256 = domain.FileSHA256([]byte("file-guard"))
	p.CohortVersion = v.Fixture.CohortVersion
	p.CohortManifestSHA256 = v.Fixture.CohortManifestSHA256
	p.AllowedAccountIDs = []int64{1}
	p.AllowedInputs = []domain.FileJobAllowedInput{{WellID: 1, RawSHA256: input.RawSHA256, Format: "csv", ClientSpecJSON: specJSON, ClientSpecSHA256: domain.FileSHA256(specJSON), ExecutionPin: savedPin()}}
	if _, e := pool.Exec(ctx, `INSERT INTO file_job_pilot_guard(id,run_id,manifest_sha256,synthetic_only) VALUES(true,$1,$2,true)`, p.RunID, p.ManifestSHA256); e != nil {
		t.Fatal(e)
	}
	r, e := r.WithFileJobPolicy(p)
	if e != nil {
		t.Fatal(e)
	}
	f := domain.FileJobPrepared{Input: input, Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 1, 2, 0, 0, 0, 1000, time.UTC), PointsCount: 2}
	return pool, ctx, r, v, f
}
func TestCohortQ1GateBoundedWaitNoEffects(t *testing.T) {
	pool, ctx, r, _, f := cohortWithFilePolicy(t)
	tx, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(74019323)"); e != nil {
		t.Fatal(e)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, e = r.AcceptFileJob(short, f)
	assertFileJobProblem(t, e, domain.Limited, "file_job_capacity")
	if time.Since(started) > 2*time.Second {
		t.Fatal("unbounded gate")
	}
	var n int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM file_job").Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
	var retained int
	_ = pool.QueryRow(ctx, "SELECT retained_count FROM file_input_budget WHERE budget_key='global'").Scan(&retained)
	if retained != 0 {
		t.Fatal("quota leaked", retained)
	}
}
func TestCohortQ1TerminalDuplicateAllowed(t *testing.T) {
	pool, ctx, r, v, f := cohortWithFilePolicy(t)
	job, e := r.AcceptFileJob(ctx, f)
	if e != nil {
		t.Fatal(e)
	}
	message := fileJobMessage(t, pool, job.ID)
	if _, e = r.CancelFileJob(ctx, 1, job.ID, job.Revision); e != nil {
		t.Fatal(e)
	}
	if _, e = r.AcceptCohortJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	claim, e := r.ClaimFileJob(ctx, message, strings.Repeat("1", 32))
	if e != nil || claim != nil {
		t.Fatal("terminal duplicate churn", claim, e)
	}
	var receipt string
	if e = pool.QueryRow(ctx, "SELECT state FROM file_job_inbox WHERE event_id=$1", message.EventID).Scan(&receipt); e != nil || receipt != "superseded" {
		t.Fatal(receipt, e)
	}
}
func TestCohortQ1AcceptBlockedNoQuota(t *testing.T) {
	pool, ctx, r, v, f := cohortWithFilePolicy(t)
	if _, e := r.AcceptCohortJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	_, e := r.AcceptFileJob(ctx, f)
	assertFileJobProblem(t, e, domain.Limited, "file_job_capacity")
	var n int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM file_job").Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
}
func TestCohortQ1QuarantineBlocksAdmission(t *testing.T) {
	pool, ctx, r, v, f := cohortWithFilePolicy(t)
	j, e := r.AcceptFileJob(ctx, f)
	if e != nil {
		t.Fatal(e)
	}
	m := fileJobMessage(t, pool, j.ID)
	m.Version = 2
	if claim, e := r.ClaimFileJob(ctx, m, strings.Repeat("1", 32)); e != nil || claim != nil {
		t.Fatal(claim, e)
	}
	status, e := r.FileJob(ctx, 1, j.ID)
	if e != nil || status.State != domain.FileJobQuarantined {
		t.Fatal(status, e)
	}
	if _, e = r.AcceptCohortJob(ctx, v); e == nil {
		t.Fatal("quarantined Q1 reservation bypassed")
	}
	var n int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM cohort_job").Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
}
