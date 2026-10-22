package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

func cohortInputJobSetup(t *testing.T) (*pgxpool.Pool, context.Context, *postgres.Repository, domain.PreparedCohortJob) {
	t.Helper()
	pool, ctx, r, v := cohortJobSetup(t)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * 24 * time.Hour).Add(time.Nanosecond)
	raw := []byte("timestamp,CosF,F_out,Ia,Motor_winding_temperature,Ped_load,Pump_intake_pressure_kgs,qg\n2026-01-01T00:00:00Z,0.87,50,22,92,55,45,0\n2026-01-31T00:00:00Z,0.87,50,22,92,55,45,0\n")
	input := domain.CohortFrozenInput{Identity: domain.PinnedInputIdentity{CanonicalSHA256: domain.FileSHA256(raw), CanonicalBytes: int64(len(raw)), SourceKind: "cohort_snapshot"}, Canonical: raw, Bounds: domain.TelemetryBounds{Start: &start, End: &end}, Rows: 2, Columns: 7, Resolution: "file"}
	v.Fixture.FixtureID = "inputs"
	v.Fixture.Members[0].NoComputeReason = nil
	v.Fixture.Members[0].CanonicalSHA256 = input.Identity.CanonicalSHA256
	v.Fixture.Members[0].CanonicalBytes = input.Identity.CanonicalBytes
	v.Members[0].NoComputeReason = nil
	v.Members[0].Input = &input
	v.FixtureJSON, _ = domain.CanonicalCohortFixture(v.Fixture)
	v.FixtureSHA256 = domain.FileSHA256(v.FixtureJSON)
	v.RequestCommitmentSHA256, _ = domain.CohortRequestCommitment(1, v.Fixture.FixtureID, v.FixtureSHA256)
	var e error
	r, e = r.WithCohortJobPolicy(domain.CohortJobPolicy{RunID: "q2-storage", TestSchema: pool.Config().ConnConfig.RuntimeParams["search_path"], ManifestSHA256: domain.FileSHA256([]byte("descriptor")), CohortVersion: v.Fixture.CohortVersion, CohortManifestSHA256: v.Fixture.CohortManifestSHA256, AllowedAccountIDs: []int64{1}, Fixtures: []domain.CohortFixturePermit{{FixtureID: v.Fixture.FixtureID, FixtureSHA256: v.FixtureSHA256}}})
	if e != nil {
		t.Fatal(e)
	}
	return pool, ctx, r, v
}
func cohortScoredCheckpoint(t *testing.T, x domain.CohortMemberExecution) domain.CohortCheckpoint {
	t.Helper()
	in := x.Member.Input
	p := x.ExecutionPin
	str := func(s string) *string { return &s }
	result := domain.UploadResult{ModelName: &p.ModelName, ModelVersion: &p.BundleSHA256, DecisionRule: p.DecisionRule, Verdict: "watch", LatestRisk: 0, Provenance: &domain.Provenance{InputKind: "cohort_snapshot", InputSnapshotID: str(in.Identity.InputSnapshotID), InputSHA256: str(in.Identity.CanonicalSHA256), ContextVersion: &p.ContextVersion, FeatureRecipeSHA256: &p.FeatureRecipeSHA256, FeatureSchemaSHA256: &p.FeatureSchemaSHA256, SourceManifestSHA256: &p.SourceManifestSHA256, RuntimeManifestSHA256: &p.RuntimeManifestSHA256}}
	c := domain.CohortCheckpoint{JobID: x.JobID, WellID: x.Member.Well.ID, InputFingerprintSHA256: in.Identity.FingerprintSHA256, ExecutionPinSHA256: x.ExecutionPinSHA256, InputManifestSHA256: x.InputManifestSHA256, Outcome: domain.CohortOutcome{AppWellID: x.Member.Well.ID, Metadata: x.Member.Well, Status: "scored", ReasonCode: "evaluated", Result: &result, Provenance: result.Provenance, DecisionRule: result.DecisionRule, InputSHA256: strings.TrimPrefix(in.Identity.CanonicalSHA256, "sha256:"), Bounds: &in.Bounds, InputWatermark: in.Watermark, Resolution: in.Resolution}}
	var e error
	c.DigestSHA256, e = domain.CohortCheckpointDigest(c)
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestCohortPublicationPreservesAlertReset(t *testing.T) {
	pool, ctx, r, v := cohortInputJobSetup(t)
	var lastAlert int64
	for iteration, want := range []string{"new", "confirmed", "new"} {
		if iteration > 0 {
			if _, e := r.UpdateAlert(ctx, lastAlert, want); e != nil {
				t.Fatal(e)
			}
		}
		v.RequestKey = "q2-alert-reset-key-" + string(rune('1'+iteration))
		j, e := r.AcceptCohortJob(ctx, v)
		if e != nil {
			t.Fatal(e)
		}
		claim, e := r.ClaimCohortJob(ctx, cohortMessage(t, pool, j.ID), strings.Repeat("1", 32))
		if e != nil || claim == nil {
			t.Fatal(e)
		}
		for i := 0; i < 100; i++ {
			x, e := r.NextCohortMember(ctx, claim.Lease())
			if e != nil || x == nil {
				claim.Close()
				t.Fatal(e)
			}
			var c domain.CohortCheckpoint
			if x.Member.Input != nil {
				c = cohortScoredCheckpoint(t, *x)
			} else {
				c = cohortMarkerCheckpoint(t, *x)
			}
			if e = r.CheckpointCohortMember(ctx, claim.Lease(), c); e != nil {
				claim.Close()
				t.Fatal(e)
			}
		}
		if iteration == 0 {
			if _, e = pool.Exec(ctx, `CREATE FUNCTION q2_fail_after_alert() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'after-alert';END$$;CREATE TRIGGER q2_fault BEFORE INSERT ON ml_run_snapshot FOR EACH ROW EXECUTE FUNCTION q2_fail_after_alert()`); e != nil {
				t.Fatal(e)
			}
			if _, e = r.FinishCohortJob(ctx, claim.Lease(), nil); e == nil {
				claim.Close()
				t.Fatal("fault accepted")
			}
			var count int
			_ = pool.QueryRow(ctx, "SELECT count(*) FROM ml_alert WHERE run_id=$1", j.RunID).Scan(&count)
			if count != 0 {
				t.Fatal("partial alert leak", count)
			}
			_, _ = pool.Exec(ctx, "DROP TRIGGER q2_fault ON ml_run_snapshot")
		}
		if _, e = r.FinishCohortJob(ctx, claim.Lease(), nil); e != nil {
			claim.Close()
			t.Fatal(e)
		}
		claim.Close()
		alerts, e := r.CohortAlerts(ctx, j.RunID)
		if e != nil || len(alerts) != 1 || alerts[0].Status != want {
			t.Fatal(alerts, e)
		}
		lastAlert = alerts[0].ID
	}
}
func TestCohortInputCannotHideCorruptionBehindArchived(t *testing.T) {
	pool, ctx, r, v := cohortInputJobSetup(t)
	if _, e := pool.Exec(ctx, "UPDATE well SET archived_at=clock_timestamp() WHERE id=$1", v.Members[0].Well.ID); e != nil {
		t.Fatal(e)
	}
	v.Members[0].Input.Canonical[0] = 'X'
	if _, e := r.AcceptCohortJob(ctx, v); e == nil {
		t.Fatal("archived source corruption hidden")
	}
	var n int
	_ = pool.QueryRow(ctx, "SELECT count(*) FROM cohort_job").Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
}
