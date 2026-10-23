package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

// Explicit legacy research is the sole compatibility path for an absent binding.
func TestRBACLegacyResearchCompatibility(t *testing.T) {
	pool := testDB(t)
	legacy := domain.TrustedLegacyPrincipal("verified legacy research fixture")
	accountID := rbacReadUser(t, pool, "raw-account", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "runs.start", "runs.read", "risks.read", "wells.read", "telemetry.import", "telemetry.read")
	if _, err := pool.Exec(context.Background(), `UPDATE auth_user SET scope='{"funds":["operational","presentation"]}' WHERE id=$1`, accountID); err != nil {
		t.Fatal(err)
	}
	base := postgres.New(pool)
	account, err := base.Account(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	a, err := base.Authorization(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	accountPrincipal := domain.AccountPrincipal(account)
	accountPrincipal.Authorization = &a
	accountPrincipal.Mechanism = "legacy"
	partial := legacy
	pa := *legacy.Authorization
	pa.LegacyScope.Funds = []string{"operational"}
	partial.Authorization = &pa
	noPermission := legacy
	na := *legacy.Authorization
	na.Permissions = []domain.Permission{}
	noPermission.Authorization = &na
	for i, tc := range []struct {
		name, authMode, appScope string
		ctx                      context.Context
		want                     bool
	}{
		{"verified", "legacy", "research", domain.WithPrincipal(context.Background(), legacy), true},
		{"missing", "legacy", "research", context.Background(), false},
		{"forged", "legacy", "research", domain.WithPrincipal(context.Background(), domain.Principal{Authenticated: true, Mechanism: "legacy", Authorization: legacy.Authorization}), false},
		{"account", "legacy", "research", domain.WithPrincipal(context.Background(), accountPrincipal), false},
		{"system", "legacy", "research", domain.WithSchedulerPrincipal(context.Background(), domain.ExecutionSystemML, domain.AccessScope{Funds: []string{"operational", "presentation"}}), false},
		{"maintenance", "legacy", "research", domain.WithSystemPrincipal(context.Background()), false},
		{"unconfigured", "", "", domain.WithPrincipal(context.Background(), legacy), false},
		{"local", "local", "research", domain.WithPrincipal(context.Background(), legacy), false},
		{"cohort", "legacy", "cohort", domain.WithPrincipal(context.Background(), legacy), false},
		{"partial", "legacy", "research", domain.WithPrincipal(context.Background(), partial), false},
		{"missing-permission", "legacy", "research", domain.WithPrincipal(context.Background(), noPermission), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := base.WithExecutionProfile(tc.authMode, tc.appScope)
			value := 0.0
			stamp := time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC)
			log, e := r.CommitTelemetry(tc.ctx, domain.TelemetryWrite{SourceID: "manual", Points: []domain.Measurement{{WellID: "legacy-opaque", Timestamp: stamp, Values: map[string]*float64{"F_out": &value}}}})
			if (e == nil) != tc.want {
				t.Fatalf("raw telemetry accepted=%v want=%v err=%v", e == nil, tc.want, e)
			}
			if tc.want && log.Accepted != 1 {
				t.Fatal(log)
			}
			var points int
			if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM telemetry_measurement WHERE measured_at=$1`, stamp).Scan(&points); e != nil || points != boolCount(tc.want) {
				t.Fatalf("points=%d err=%v", points, e)
			}
			lease, ok, e := r.Reserve(tc.ctx)
			if e != nil || !ok {
				t.Fatal(ok, e)
			}
			run, e := lease.Begin(tc.ctx, "manual")
			if e == nil {
				e = lease.Persist(tc.ctx, run, domain.Snapshot{Wells: []domain.WellRisk{{WellID: 42, UnitekID: ptr(int64(42)), Alert: true, WindowEnd: ptr("2026-01-01T00:00:00")}}})
				if e != nil {
					if fail := lease.Fail(context.Background(), run, "denied fixture"); fail != nil {
						t.Fatal(fail)
					}
				}
			}
			lease.Close()
			if (e == nil) != tc.want {
				t.Fatalf("unbound research accepted=%v want=%v err=%v", e == nil, tc.want, e)
			}
			if tc.want {
				var bound *int64
				if e = pool.QueryRow(context.Background(), `SELECT app_well_id FROM ml_alert WHERE run_id=$1`, run).Scan(&bound); e != nil || bound != nil {
					t.Fatal(bound, e)
				}
				if _, e = r.CommitTelemetry(tc.ctx, domain.TelemetryWrite{SourceID: "manual", Points: []domain.Measurement{{WellID: "pulse:999999", Timestamp: stamp, Values: map[string]*float64{"F_out": &value}}}}); e == nil {
					t.Fatal("explicit missing Pulse target bypassed")
				}
			}
		})
	}
}
func boolCount(v bool) int {
	if v {
		return 1
	}
	return 0
}

// legacyResearchFixtureContext models the explicitly trusted historical research profile, never a local account.
func legacyResearchFixtureContext() context.Context {
	return domain.WithPrincipal(context.Background(), domain.TrustedLegacyPrincipal("legacy research fixture"))
}

func TestRBACLegacyResearchPersistedKinds(t *testing.T) {
	pool := testDB(t)
	r := postgres.New(pool).WithExecutionProfile("legacy", "research")
	ctx := legacyResearchFixtureContext()
	system := domain.WithSchedulerPrincipal(context.Background(), domain.ExecutionSystemCollect, domain.AccessScope{Funds: []string{"operational", "presentation"}})
	source, err := r.SaveSource(ctx, domain.TelemetrySource{ID: "legacy-cross-kind", Kind: "json_pull", Status: "running", IntervalSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := r.ClaimSource(system, source.ID, false)
	if err != nil || lease == nil {
		t.Fatal(lease, err)
	}
	for _, tamper := range []bool{false, true} {
		copy := *lease
		if tamper {
			copy.ExecutionActor = domain.ExecutionLegacyHTTP
		}
		value := 1.0
		if _, err = r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: source.ID, ProfileVersion: source.ProfileVersion, Lease: &copy, Release: true, Points: []domain.Measurement{{WellID: "raw-cross-kind", Timestamp: time.Now().UTC(), Values: map[string]*float64{"F_out": &value}}}}); err == nil {
			t.Fatal("system persisted source accepted legacy caller", tamper)
		}
	}
	if err = r.FailCollection(system, *lease, "fixture cleanup"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO app_cohort(version,manifest_sha256)VALUES('fixture-cohort',$1)`, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	mlSystem := domain.WithSchedulerPrincipal(context.Background(), domain.ExecutionSystemML, domain.AccessScope{Funds: []string{"operational", "presentation"}})
	for _, tc := range []struct {
		name   string
		actor  context.Context
		cohort bool
	}{{"system", mlSystem, false}, {"cohort", ctx, true}} {
		t.Run(tc.name, func(t *testing.T) {
			l, ok, e := r.Reserve(tc.actor)
			if e != nil || !ok {
				t.Fatal(ok, e)
			}
			defer l.Close()
			var run int64
			if tc.cohort {
				run, e = l.(interface {
					BeginCohort(context.Context, string, string) (int64, error)
				}).BeginCohort(tc.actor, "fixture", "fixture-cohort")
			} else {
				run, e = l.Begin(tc.actor, "fixture")
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = l.Persist(ctx, run, domain.Snapshot{Wells: []domain.WellRisk{{WellID: 42, UnitekID: ptr(int64(42))}}}); e == nil {
				t.Fatal("persisted nonlegacy/nonresearch run bypassed")
			}
			if e = l.Fail(context.Background(), run, "fixture cleanup"); e != nil {
				t.Fatal(e)
			}
		})
	}
	if _, err = pool.Exec(ctx, `ALTER TABLE well RENAME TO temporarily_unavailable_well`); err != nil {
		t.Fatal(err)
	}
	value := 0.0
	if _, err = r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: "manual", Points: []domain.Measurement{{WellID: "opaque-sql-error", Timestamp: time.Now().UTC(), Values: map[string]*float64{"F_out": &value}}}}); err == nil {
		t.Fatal("SQL failure swallowed as absent binding")
	}
	var points, alerts int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM telemetry_measurement),(SELECT count(*) FROM ml_alert)`).Scan(&points, &alerts); err != nil || points != 0 || alerts != 0 {
		t.Fatal(points, alerts, err)
	}
}

// A persisted local actor is not transferable to another execution mechanism.
func TestRBACLegacyAccountCallerMismatch(t *testing.T) {
	for _, kind := range []string{"run", "source"} {
		t.Run(kind, func(t *testing.T) {
			pool := testDB(t)
			r := postgres.New(pool).WithExecutionProfile("legacy", "research")
			id := rbacReadUser(t, pool, "bound-account", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "runs.start", "runs.read", "risks.read", "wells.read", "telemetry.read", "telemetry.import", "integrations.collect", "integrations.read")
			if _, e := pool.Exec(context.Background(), `UPDATE auth_user SET scope='{"funds":["operational","presentation"]}' WHERE id=$1`, id); e != nil {
				t.Fatal(e)
			}
			a, e := r.Authorization(context.Background(), id)
			if e != nil {
				t.Fatal(e)
			}
			account, e := r.Account(context.Background(), id)
			if e != nil {
				t.Fatal(e)
			}
			p := domain.AccountPrincipal(account)
			p.Authorization = &a
			actor := domain.WithRBACMode(domain.WithPrincipal(context.Background(), p), domain.RBACEnforce)
			legacy := domain.WithRBACMode(legacyResearchFixtureContext(), domain.RBACEnforce)
			if _, e = r.CreateWell(legacy, domain.Well{UnitekID: ptr(int64(42)), FundType: "operational", Active: true}); e != nil {
				t.Fatal(e)
			}
			if kind == "run" {
				l, ok, e := r.Reserve(actor)
				if e != nil || !ok {
					t.Fatal(ok, e)
				}
				defer l.Close()
				run, e := l.Begin(actor, "manual")
				if e != nil {
					t.Fatal(e)
				}
				if e = l.Persist(legacy, run, domain.Snapshot{Wells: []domain.WellRisk{{WellID: 42, UnitekID: ptr(int64(42)), Alert: true, WindowEnd: ptr("2026-01-01T00:00:00")}}}); e == nil {
					t.Fatal("legacy caller published persisted account bound run")
				}
			} else {
				source, e := r.SaveSource(legacy, domain.TelemetrySource{ID: "bound-account-source", Status: "running", Kind: "json_pull", IntervalSeconds: 60})
				if e != nil {
					t.Fatal(e)
				}
				l, e := r.ClaimSource(actor, source.ID, false)
				if e != nil || l == nil {
					t.Fatal(l, e)
				}
				if e = r.AuthorizeTelemetrySourceLease(actor, *l); e != nil {
					t.Fatalf("valid account lease failed before adversarial caller: %v", e)
				}
				value := 0.0
				if _, e = r.CommitTelemetry(legacy, domain.TelemetryWrite{SourceID: source.ID, ProfileVersion: source.ProfileVersion, Lease: l, Release: true, Points: []domain.Measurement{{WellID: "pulse:1", Timestamp: time.Now().UTC(), Values: map[string]*float64{"F_out": &value}}}}); e == nil {
					t.Fatal("legacy caller published persisted account bound points")
				}
			}
			var points, alerts, done int
			if e = pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM telemetry_measurement),(SELECT count(*) FROM ml_alert),(SELECT count(*) FROM ml_run WHERE status='done')`).Scan(&points, &alerts, &done); e != nil || points != 0 || alerts != 0 || done != 0 {
				t.Fatal(points, alerts, done, e)
			}
		})
	}
}
