package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

func cohortJobSetup(t *testing.T) (*pgxpool.Pool, context.Context, *postgres.Repository, domain.PreparedCohortJob) {
	t.Helper()
	pool := testDB(t)
	ctx := context.Background()
	r := postgres.New(pool)
	if _, e := pool.Exec(ctx, `INSERT INTO auth_user(login,name,password_hash,role,scope) VALUES('cohort-admin','Synthetic','unused','admin','{"funds":["presentation"],"ngdus":null,"departments":null}')`); e != nil {
		t.Fatal(e)
	}
	f := domain.CohortFixtureManifest{Version: "cohort_fixture_v1", FixtureID: "markers", CohortVersion: "q2-storage", CohortManifestSHA256: strings.Repeat("e", 64), AsOf: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), ExecutionPin: savedPin(), CaptureRecipe: domain.CohortCaptureRecipe{Version: "cohort_fixture_capture_v1", Normalization: "none_supplied_canonical", BackendBuildSHA256: domain.FileSHA256([]byte("build"))}}
	reason := "insufficient_telemetry"
	v := domain.PreparedCohortJob{ActorAccountID: 1, RequestKey: "q2-storage-key-0001"}
	for i := 0; i < 100; i++ {
		w, e := r.CreateWell(ctx, domain.Well{NGDU: 0, Department: 0, FundType: "presentation", Active: true})
		if e != nil {
			t.Fatal(e)
		}
		f.Members = append(f.Members, domain.CohortFixtureMember{WellID: w.ID, NoComputeReason: &reason})
		v.Members = append(v.Members, domain.CohortFrozenMember{Ordinal: i + 1, Well: w, NoComputeReason: &reason})
	}
	v.Fixture = f
	v.FixtureJSON, _ = domain.CanonicalCohortFixture(f)
	v.FixtureSHA256 = domain.FileSHA256(v.FixtureJSON)
	v.RequestCommitmentSHA256, _ = domain.CohortRequestCommitment(1, f.FixtureID, v.FixtureSHA256)
	if _, e := pool.Exec(ctx, `INSERT INTO app_cohort(version,manifest_sha256) VALUES($1,$2);`, f.CohortVersion, f.CohortManifestSHA256); e != nil {
		t.Fatal(e)
	}
	for _, m := range v.Members {
		if _, e := pool.Exec(ctx, `INSERT INTO app_cohort_member(version,well_id,revision) VALUES($1,$2,$3)`, f.CohortVersion, m.Well.ID, m.Well.Revision); e != nil {
			t.Fatal(e)
		}
	}
	p := domain.CohortJobPolicy{RunID: "q2-storage", TestSchema: pool.Config().ConnConfig.RuntimeParams["search_path"], ManifestSHA256: domain.FileSHA256([]byte("descriptor")), CohortVersion: f.CohortVersion, CohortManifestSHA256: f.CohortManifestSHA256, AllowedAccountIDs: []int64{1}, Fixtures: []domain.CohortFixturePermit{{FixtureID: f.FixtureID, FixtureSHA256: v.FixtureSHA256}}}
	if _, e := pool.Exec(ctx, `INSERT INTO cohort_job_guard(id,run_id,manifest_sha256,synthetic_only) VALUES(true,$1,$2,true)`, p.RunID, p.ManifestSHA256); e != nil {
		t.Fatal(e)
	}
	r, e := r.WithCohortJobPolicy(p)
	if e != nil {
		t.Fatal(e)
	}
	actor := int64(1)
	ctx = domain.WithPrincipal(ctx, domain.Principal{Authenticated: true, AccountID: &actor, Mechanism: "session", Role: "viewer"})
	return pool, ctx, r, v
}
func TestCohortAdmissionReplay(t *testing.T) {
	pool, ctx, r, v := cohortJobSetup(t)
	first, e := r.AcceptCohortJob(ctx, v)
	if e != nil {
		t.Fatal(e)
	}
	again, e := r.AcceptCohortJob(ctx, v)
	if e != nil || again.ID != first.ID || again.RunID != first.RunID {
		t.Fatal(again, e)
	}
	var n int
	if e = pool.QueryRow(ctx, `SELECT count(*) FROM cohort_job_member WHERE job_id=$1`, first.ID).Scan(&n); e != nil || n != 100 {
		t.Fatal(n, e)
	}
	v.RequestCommitmentSHA256 = domain.FileSHA256([]byte("different"))
	if _, e = r.AcceptCohortJob(ctx, v); e == nil {
		t.Fatal("changed commitment accepted")
	}
	if _, ok, e := r.Reserve(ctx); e != nil || ok {
		t.Fatal("legacy overtook Q2", ok, e)
	}
}
func TestCohortAdmissionRejectsIncompleteFreeze(t *testing.T) {
	pool, ctx, r, v := cohortJobSetup(t)
	v.Members = v.Members[:99]
	if _, e := r.AcceptCohortJob(ctx, v); e == nil {
		t.Fatal("incomplete freeze accepted")
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM cohort_job`).Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
}
func TestCohortAdmissionAtomicRollback(t *testing.T) {
	pool, ctx, r, v := cohortJobSetup(t)
	_, e := pool.Exec(ctx, `CREATE FUNCTION fail_q2_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected';END $$;CREATE TRIGGER fail_q2 BEFORE INSERT ON cohort_job_outbox FOR EACH ROW EXECUTE FUNCTION fail_q2_outbox()`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.AcceptCohortJob(ctx, v); e == nil {
		t.Fatal("accepted without outbox")
	}
	for _, table := range []string{"cohort_job", "cohort_job_member", "cohort_job_manifest", "ml_run"} {
		var n int
		_ = pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n)
		if n != 0 {
			t.Fatal(table, n)
		}
	}
}
func cohortMessage(t *testing.T, pool *pgxpool.Pool, id string) domain.CohortJobMessage {
	t.Helper()
	m := domain.CohortJobMessage{Version: 1, Kind: "cohort.ready.v1", JobID: id}
	if e := pool.QueryRow(context.Background(), `SELECT event_id,generation FROM cohort_job_outbox WHERE job_id=$1 ORDER BY generation DESC LIMIT 1`, id).Scan(&m.EventID, &m.Generation); e != nil {
		t.Fatal(e)
	}
	return m
}
func cohortMarkerCheckpoint(t *testing.T, x domain.CohortMemberExecution) domain.CohortCheckpoint {
	t.Helper()
	reason := *x.Member.NoComputeReason
	status := map[string]string{"insufficient_telemetry": "insufficient", "archived": "archived", "inactive": "inactive", "synthetic_source_unavailable": "error"}[reason]
	c := domain.CohortCheckpoint{JobID: x.JobID, WellID: x.Member.Well.ID, ExecutionPinSHA256: x.ExecutionPinSHA256, InputManifestSHA256: x.InputManifestSHA256, Outcome: domain.CohortOutcome{AppWellID: x.Member.Well.ID, Metadata: x.Member.Well, ReasonCode: reason, Status: status}}
	var e error
	c.DigestSHA256, e = domain.CohortCheckpointDigest(c)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestCohortAtomicPublicationReplay(t *testing.T) {
	pool, ctx, r, v := cohortJobSetup(t)
	j, e := r.AcceptCohortJob(ctx, v)
	if e != nil {
		t.Fatal(e)
	}
	claim, e := r.ClaimCohortJob(ctx, cohortMessage(t, pool, j.ID), strings.Repeat("1", 32))
	if e != nil || claim == nil {
		t.Fatal(e)
	}
	defer claim.Close()
	for i := 0; i < 100; i++ {
		x, e := r.NextCohortMember(ctx, claim.Lease())
		if e != nil || x == nil {
			t.Fatal(i, e)
		}
		if e = r.CheckpointCohortMember(ctx, claim.Lease(), cohortMarkerCheckpoint(t, *x)); e != nil {
			t.Fatal(i, e)
		}
	}
	x, e := r.NextCohortMember(ctx, claim.Lease())
	if e != nil || x != nil {
		t.Fatal(x, e)
	}
	_, e = pool.Exec(ctx, `CREATE FUNCTION fail_q2_final() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected';END $$;CREATE TRIGGER fail_q2 BEFORE INSERT ON ml_run_snapshot FOR EACH ROW EXECUTE FUNCTION fail_q2_final()`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.FinishCohortJob(ctx, claim.Lease(), nil); e == nil {
		t.Fatal("publication accepted before commit")
	}
	_, _ = pool.Exec(ctx, `DROP TRIGGER fail_q2 ON ml_run_snapshot`)
	d, e := r.FinishCohortJob(ctx, claim.Lease(), nil)
	if e != nil || d.Status.State != domain.CohortJobSucceeded {
		t.Fatal(d, e)
	}
	d, e = r.FinishCohortJob(ctx, claim.Lease(), nil)
	if e != nil || d.Status.State != domain.CohortJobSucceeded {
		t.Fatal(d, e)
	}
	snapshot, e := r.CohortSnapshot(ctx, v.Fixture.CohortVersion, &j.RunID)
	if e != nil || snapshot.Execution == nil || len(snapshot.Outcomes) != 100 || snapshot.Coverage.InsufficientCount != 100 {
		t.Fatal(snapshot.Count, e)
	}
	raw, _ := json.Marshal(snapshot)
	if len(raw) == 0 {
		t.Fatal("empty")
	}
}
