package integration

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

func TestCohortPreexistingLifecycleAdmissionRemainsClaimable(t *testing.T) {
	for _, scenario := range []struct{ name, zone, change string }{
		{"inactive_revision2", "Europe/Moscow", `UPDATE well SET active=false,revision=revision+1 WHERE id=98`},
		{"archived_utc_revision2", "UTC", `UPDATE well SET archived_at='2026-01-31T12:34:56.123456+03:00',revision=revision+1 WHERE id=97`},
		{"archived_moscow_revision2", "Europe/Moscow", `UPDATE well SET archived_at='2026-01-31T12:34:56.123456+03:00',revision=revision+1 WHERE id=97`},
		{"mixed_revision2", "Europe/Moscow", `UPDATE well SET archived_at='2026-01-31T12:34:56.123456+03:00',revision=revision+1 WHERE id=97;UPDATE well SET active=false,revision=revision+1 WHERE id=98`},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			base, ctx, _, prepared := cohortJobSetup(t)
			cfg := base.Config().Copy()
			cfg.ConnConfig.RuntimeParams["timezone"] = scenario.zone
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			store, err := postgres.New(pool).WithCohortJobPolicy(domain.CohortJobPolicy{RunID: "q2-storage", TestSchema: pool.Config().ConnConfig.RuntimeParams["search_path"], ManifestSHA256: domain.FileSHA256([]byte("descriptor")), CohortVersion: prepared.Fixture.CohortVersion, CohortManifestSHA256: prepared.Fixture.CohortManifestSHA256, AllowedAccountIDs: []int64{1}, Fixtures: []domain.CohortFixturePermit{{FixtureID: prepared.Fixture.FixtureID, FixtureSHA256: prepared.FixtureSHA256}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = pool.Exec(ctx, scenario.change); err != nil {
				t.Fatal(err)
			}
			var captured, current int64
			wellID := int64(97)
			if strings.HasPrefix(scenario.name, "inactive") {
				wellID = 98
			}
			if err = pool.QueryRow(ctx, `SELECT m.revision,w.revision FROM app_cohort_member m JOIN well w ON w.id=m.well_id WHERE m.version=$1 AND m.well_id=$2`, prepared.Fixture.CohortVersion, wellID).Scan(&captured, &current); err != nil || captured != 1 || current != 2 {
				t.Fatal("preexisting lifecycle setup", captured, current, err)
			}
			accepted, err := store.AcceptCohortJob(ctx, prepared)
			if err != nil {
				t.Fatal("preexisting lifecycle rejected admission", err)
			}
			claim, err := store.ClaimCohortJob(ctx, cohortMessage(t, pool, accepted.ID), strings.Repeat("b", 32))
			if err != nil || claim == nil {
				actual, _ := store.CohortJobStatus(ctx, accepted.ID)
				t.Fatalf("admitted lifecycle immediately rejected: state=%s reason=%v error=%v", actual.State, actual.ErrorCode, err)
			}
			defer claim.Close()
			if err = store.RenewCohortJob(ctx, claim.Lease()); err != nil {
				t.Fatal("unchanged lifecycle failed renewal", err)
			}
			if scenario.name == "mixed_revision2" {
				for i := 0; i < 100; i++ {
					execution, err := store.NextCohortMember(ctx, claim.Lease())
					if err != nil || execution == nil {
						t.Fatal(i, err)
					}
					if execution.Member.Well.ID == 97 || execution.Member.Well.ID == 98 {
						want := "archived"
						if execution.Member.Well.ID == 98 {
							want = "inactive"
						}
						if execution.Member.NoComputeReason == nil || *execution.Member.NoComputeReason != want || execution.Member.Input != nil || execution.Member.Well.Revision != 2 {
							t.Fatal("frozen lifecycle overlay lost")
						}
					}
					if err = store.CheckpointCohortMember(ctx, claim.Lease(), cohortMarkerCheckpoint(t, *execution)); err != nil {
						t.Fatal(i, err)
					}
				}
				finished, err := store.FinishCohortJob(ctx, claim.Lease(), nil)
				if err != nil || finished.Status.State != domain.CohortJobSucceeded || finished.Status.Counts.ArchivedCount != 1 || finished.Status.Counts.InactiveCount != 1 || finished.Status.Counts.InsufficientCount != 98 {
					t.Fatal("mixed lifecycle publication rejected", finished, err)
				}
			} else {
				change := `UPDATE well SET archived_at=NULL,revision=revision+1 WHERE id=97`
				if strings.HasPrefix(scenario.name, "inactive") {
					change = `UPDATE well SET active=true,revision=revision+1 WHERE id=98`
				}
				if _, err = pool.Exec(ctx, change); err != nil {
					t.Fatal(err)
				}
				if _, err = store.NextCohortMember(ctx, claim.Lease()); err == nil {
					t.Fatal("real lifecycle drift accepted")
				}
				var state string
				if err = pool.QueryRow(ctx, "SELECT state FROM cohort_job WHERE id=$1", accepted.ID).Scan(&state); err != nil || state != "revoked" {
					t.Fatal("real lifecycle drift not revoked", state, err)
				}
			}

		})
	}
}
