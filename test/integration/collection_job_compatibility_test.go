package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

func TestCollectionCollisionMatrix(t *testing.T) {
	for _, tc := range []struct {
		name       string
		provenance bool
		change     string
		want       string
		identical  int
	}{
		{"same", true, "", "", 2}, {"value", true, "value", "measurement_conflict", 0}, {"quality", true, "quality", "measurement_conflict", 0}, {"null", true, "null", "measurement_conflict", 0}, {"channels", true, "channels", "measurement_conflict", 0}, {"source", true, "source", "measurement_conflict", 0}, {"profile", true, "profile", "measurement_conflict", 0}, {"revision", true, "revision", "measurement_conflict", 0}, {"legacy_equal", false, "", "unverifiable_existing", 0}, {"legacy_different", false, "value", "unverifiable_existing", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, ctx, r, in := collectionSetup(t)
			if _, e := r.AcceptCollectionJob(ctx, in); e != nil {
				t.Fatal(e)
			}
			c, _ := collectionClaim(t, ctx, r)
			capture := collectionPrepared(t, ctx, r, c.Lease(), 50)
			if e := r.SaveCollectionCapture(ctx, c.Lease(), capture); e != nil {
				t.Fatal(e)
			}
			var canonical struct {
				Points []domain.Measurement `json:"points"`
			}
			if e := json.Unmarshal(capture.Canonical, &canonical); e != nil {
				t.Fatal(e)
			}
			p := capture.Provenance
			source := in.SourceID
			if tc.change == "source" {
				source = "other"
				p.SourceID = source
			}
			if tc.change == "profile" {
				p.SourceProfileVersion++
			}
			if tc.change == "revision" {
				p.DataRevision = "generation-b"
			}
			for _, point := range canonical.Points {
				if tc.change == "value" {
					v := 52.0
					point.Values["F_out"] = &v
				}
				if tc.change == "quality" {
					point.Quality = "changed"
				}
				if tc.change == "null" {
					point.Values["F_out"] = nil
				}
				if tc.change == "channels" {
					point.Values["Ia"] = nil
				}
				raw, _ := json.Marshal(point.Values)
				var attribution any
				if tc.provenance {
					attribution, _ = json.Marshal(p)
				}
				if _, e := pool.Exec(ctx, `INSERT INTO telemetry_measurement(well_id,measured_at,values,quality,source_id,collection_provenance) VALUES($1,$2,$3,$4,$5,$6)`, point.WellID, point.Timestamp, raw, point.Quality, source, attribution); e != nil {
					t.Fatal(e)
				}
			}
			var before string
			if e := pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(m) ORDER BY measured_at)::text FROM telemetry_measurement m`).Scan(&before); e != nil {
				t.Fatal(e)
			}
			out, e := r.ApplyCollectionCapture(ctx, c.Lease())
			if e != nil {
				t.Fatal(e)
			}
			if tc.want == "" {
				if out.Status.State != domain.CollectionSucceeded || out.Status.Write.Identical != tc.identical || out.Status.Write.Inserted != 0 {
					t.Fatal(out)
				}
			} else {
				if out.Status.State != domain.CollectionFailed || out.Status.ErrorCode == nil || string(*out.Status.ErrorCode) != tc.want || out.Status.Write.Rejected != 2 || out.Status.Coverage.State != "none" {
					t.Fatal(out)
				}
			}
			var after string
			if e = pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(m) ORDER BY measured_at)::text FROM telemetry_measurement m`).Scan(&after); e != nil || before != after {
				t.Fatal("existing values/provenance changed", e)
			}
		})
	}
}
func TestCollectionExistingUnattributedIsConflict(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	if _, e := r.AcceptCollectionJob(ctx, in); e != nil {
		t.Fatal(e)
	}
	c, _ := collectionClaim(t, ctx, r)
	capture := collectionPrepared(t, ctx, r, c.Lease(), 50)
	work, e := r.LoadCollectionWork(ctx, c.Lease())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO telemetry_measurement(well_id,measured_at,values,quality,source_id)VALUES($1,$2,'{"F_out":50}','','legacy')`, "pulse:1", work.Intent.Start); e != nil {
		t.Fatal(e)
	}
	if e = r.SaveCollectionCapture(ctx, c.Lease(), capture); e != nil {
		t.Fatal(e)
	}
	out, e := r.ApplyCollectionCapture(ctx, c.Lease())
	if e != nil || out.Status.ErrorCode == nil || *out.Status.ErrorCode != domain.CollectionCodeUnverifiableExisting {
		t.Fatal(out, e)
	}
	if collectionCount(t, pool, "telemetry_measurement") != 1 || collectionCount(t, pool, "collection_job_coverage") != 0 {
		t.Fatal("unattributed row promoted")
	}
}
func TestCollectionSourceFenceAndProtectedKeys(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	// Genuine 60s legacy expiry is separate from 30s C2 expiry; source edit invalidates immediately.
	old, e := r.ClaimSource(ctx, in.SourceID, false)
	if e != nil || old == nil {
		t.Fatal(old, e)
	}
	source, e := r.Source(ctx, in.SourceID)
	if e != nil {
		t.Fatal(e)
	}
	source.Name = "revision2"
	if _, e = mutateSourceFixture(domain.WithSystemPrincipal(ctx), r, domain.SourceUpdate, source); e != nil {
		t.Fatal(e)
	}
	newer, e := r.ClaimSource(ctx, in.SourceID, false)
	if e != nil || newer == nil {
		t.Fatal(newer, e)
	}
	if e = r.FailCollection(ctx, *old, "old-error"); e == nil {
		t.Fatal("old failure cleared newer lease")
	}
	if _, e = r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: in.SourceID, ProfileVersion: old.ProfileVersion, Lease: old, Release: true}); e == nil {
		t.Fatal("old commit accepted")
	}
	if e = r.FailCollection(ctx, *newer, "test retry"); e != nil {
		t.Fatal(e)
	}
	if _, e = r.AcceptCollectionJob(ctx, in); e != nil {
		t.Fatal(e)
	}
	if lease, e := r.ClaimSource(ctx, in.SourceID, false); e != nil || lease != nil {
		t.Fatal("legacy crossed hold", lease, e)
	}
	if _, e = r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: in.SourceID, ProfileVersion: newer.ProfileVersion}); e == nil {
		t.Fatal("ingest crossed hold")
	}
	c, _ := collectionClaim(t, ctx, r)
	capture := collectionPrepared(t, ctx, r, c.Lease(), 50)
	if e = r.SaveCollectionCapture(ctx, c.Lease(), capture); e != nil {
		t.Fatal(e)
	}
	if _, e = r.ApplyCollectionCapture(ctx, c.Lease()); e != nil {
		t.Fatal(e)
	}
	v := 50.0
	if _, e = r.CreateWell(ctx, domain.Well{FundType: "presentation", Active: true, UnitekID: ptr(int64(-900001))}); e != nil {
		t.Fatal(e)
	}
	points := []domain.Measurement{{WellID: "pulse:1", Timestamp: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC), Values: map[string]*float64{"F_out": &v}}, {WellID: "-900001", Timestamp: time.Now().UTC(), Values: map[string]*float64{"F_out": &v}}}
	if _, e = r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: "manual", Points: points}); e == nil {
		t.Fatal("manual overwrote protected key")
	}
	if collectionCount(t, pool, "telemetry_measurement") != 2 {
		t.Fatal("manual batch partially published")
	}
	points = points[1:]
	if _, e = r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: "manual", Points: points}); e != nil {
		t.Fatal(e)
	}
	v = 51
	log, e := r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: "manual", Points: points})
	if e != nil || log.Duplicates != 1 || log.Accepted != 0 {
		t.Fatal("unprotected DO NOTHING changed", log, e)
	}
}
func TestCollectionRetryScheduleAndTerminalDelivery(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	s, e := r.AcceptCollectionJob(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	c, message := collectionClaim(t, ctx, r)
	work, e := r.LoadCollectionWork(ctx, c.Lease())
	if e != nil {
		t.Fatal(e)
	}
	intent := work.Intent
	if intent.Deadline.Sub(intent.AcceptedAt) != 300*time.Second {
		t.Fatal("job deadline not persisted at300s")
	}
	policy := domain.CollectionPolicy{RunID: intent.RunID, TestSchema: pool.Config().ConnConfig.RuntimeParams["search_path"], ManifestSHA256: intent.ManifestSHA256, FixtureOrigin: intent.FixtureOrigin, AllowedAccountIDs: []int64{intent.ActorAccountID}, Bindings: []domain.CollectionBinding{intent.Binding}, Fixtures: []domain.CollectionFixture{intent.Fixture}}
	for attempt := 1; attempt <= 3; attempt++ {
		start := time.Now()
		failureCode := domain.CollectionCodeSourceTimeout
		if attempt == 2 {
			failureCode = domain.CollectionCodeSourceUnavailable
		}
		out, e := r.FinishCollectionFailure(ctx, c.Lease(), domain.CollectionFailure{Code: failureCode, Retryable: true})
		if e != nil {
			t.Fatal(e)
		}
		c.Close()
		if out.Status.Source.Availability != "unavailable" {
			t.Fatalf("observed source failure lost at attempt%d: availability=%s state=%s", attempt, out.Status.Source.Availability, out.Status.State)
		}
		// A new Repository/claim registry models a real worker restart: counters remain in PG.
		r, e = postgres.New(pool).WithCollectionJobPolicy(policy)
		if e != nil {
			t.Fatal(e)
		}
		if attempt == 3 {
			if out.Status.State != domain.CollectionFailed || out.Status.ErrorCode == nil || *out.Status.ErrorCode != domain.CollectionCodeAttemptsExhausted {
				t.Fatal(out)
			}
			break
		}
		delay := 5 * time.Second
		if attempt == 2 {
			delay = 15 * time.Second
		}
		if out.Status.NextAttemptAt == nil || out.Status.NextAttemptAt.Sub(start) < delay-time.Second || out.Status.NextAttemptAt.Sub(start) > delay+time.Second {
			t.Fatal("wrong persisted schedule", out)
		}
		if event, e := r.ClaimCollectionOutbox(ctx, "early"); e != nil || event != nil {
			t.Fatal("retry dispatched before due", event, e)
		}
		time.Sleep(delay + 100*time.Millisecond)
		c, message = collectionClaim(t, ctx, r)
	}
	if got, e := r.ClaimCollectionJob(context.Background(), message, "late-worker"); e != nil || got != nil {
		t.Fatal("terminal message claimed", got, e)
	}
	status, e := r.CollectionJobStatus(ctx, s.ID)
	if e != nil || status.Attempt != 3 {
		t.Fatal(status, e)
	}
	if collectionCount(t, pool, "collection_job_source_hold") != 0 || collectionCount(t, pool, "telemetry_measurement") != 0 {
		t.Fatal("retry terminal effects")
	}
}

func TestCollectionSourceExpiredLegacyCannotCommitOrFail(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	old, e := r.ClaimSource(ctx, in.SourceID, false)
	if e != nil || old == nil {
		t.Fatal(old, e)
	}
	time.Sleep(time.Until(old.Until) + 100*time.Millisecond)
	if _, e = r.AcceptCollectionJob(ctx, in); e != nil {
		t.Fatal(e)
	}
	if _, e = r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: in.SourceID, ProfileVersion: old.ProfileVersion, Lease: old, Release: true}); e == nil {
		t.Fatal("expired legacy committed across C2 hold")
	}
	if e = r.FailCollection(ctx, *old, "late-error"); e == nil {
		t.Fatal("expired legacy failure changed C2 source")
	}
	var token *string
	var message string
	if e = pool.QueryRow(ctx, "SELECT writer_token_sha256,error FROM telemetry_source WHERE id=$1", in.SourceID).Scan(&token, &message); e != nil || token != nil || message != "" {
		t.Fatal("late legacy mutated source", e)
	}
	if collectionCount(t, pool, "telemetry_collection_log") != 0 {
		t.Fatal("late legacy wrote log")
	}
}

func TestCollectionSourceOtherSourceCannotOverwriteProtectedKey(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	if _, e := r.AcceptCollectionJob(ctx, in); e != nil {
		t.Fatal(e)
	}
	c, _ := collectionClaim(t, ctx, r)
	capture := collectionPrepared(t, ctx, r, c.Lease(), 50)
	if e := r.SaveCollectionCapture(ctx, c.Lease(), capture); e != nil {
		t.Fatal(e)
	}
	if _, e := r.ApplyCollectionCapture(ctx, c.Lease()); e != nil {
		t.Fatal(e)
	}
	other, e := mutateSourceFixture(ctx, r, domain.SourceCreate, domain.TelemetrySource{ID: "other-source", Kind: "json_push", Status: "running", IntervalSeconds: 60})
	if e != nil {
		t.Fatal(e)
	}
	v := 50.0
	point := domain.Measurement{WellID: "pulse:1", Timestamp: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC), Values: map[string]*float64{"F_out": &v}}
	if _, e = r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: other.ID, ProfileVersion: other.ProfileVersion, Points: []domain.Measurement{point}}); e == nil {
		t.Fatal("other source collision accepted")
	}
	var message string
	if e = pool.QueryRow(ctx, "SELECT error FROM telemetry_source WHERE id=$1", other.ID).Scan(&message); e != nil || message != "" {
		t.Fatal("collision changed unrelated source controls", e)
	}
	if collectionCount(t, pool, "telemetry_measurement") != 2 || collectionCount(t, pool, "telemetry_collection_log") != 1 {
		t.Fatal("protected collision published effects")
	}
}

func TestCollectionRetryDispatchGenerationBound(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	s, e := r.AcceptCollectionJob(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	for generation := 1; generation <= 8; generation++ {
		event, e := r.ClaimCollectionOutbox(ctx, "dispatcher")
		if e != nil || event == nil || event.Message.Generation != generation {
			t.Fatal("wrong generation", event, e)
		}
		if e = r.CompleteCollectionPublish(ctx, *event, domain.CollectionPublishConfirmed); e != nil {
			t.Fatal(e)
		}
		time.Sleep(5100 * time.Millisecond)
		if _, e = r.ReconcileCollectionJobs(ctx, 8); e != nil {
			t.Fatal(e)
		}
	}
	got, e := r.CollectionJobStatus(ctx, s.ID)
	if e != nil || got.State != domain.CollectionQuarantined || got.ErrorCode == nil || *got.ErrorCode != domain.CollectionCodeDispatchExhausted || got.Attempt != 0 {
		t.Fatal(got, e)
	}
	if collectionCount(t, pool, "collection_job_outbox") != 8 || collectionCount(t, pool, "collection_job_source_hold") != 0 {
		t.Fatal("dispatch cap or hold leak")
	}
}

// C2 coverage не меняет legacy since: поздняя finite загрузка не закрывает старые gaps.
func TestCollectionApplyPreservesLegacyWatermark(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		empty, prior, overlap bool
	}{
		{name: "dense_null"}, {name: "dense_prior", prior: true}, {name: "empty_null", empty: true}, {name: "empty_prior", empty: true, prior: true}, {name: "overlap_prior", prior: true, overlap: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool, ctx, r, in := collectionSetup(t)
			source, e := r.Source(ctx, in.SourceID)
			if e != nil {
				t.Fatal(e)
			}
			if tc.prior {
				value := 40.0
				legacy := domain.Measurement{WellID: "pulse:1", Timestamp: time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC), Values: map[string]*float64{"F_out": &value}}
				if _, e = r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: source.ID, ProfileVersion: source.ProfileVersion, Points: []domain.Measurement{legacy}}); e != nil {
					t.Fatal(e)
				}
			}
			source, e = r.Source(ctx, in.SourceID)
			if e != nil {
				t.Fatal(e)
			}
			before := source.LastMeasured
			if tc.empty {
				horizon := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
				policy := domain.CollectionPolicy{RunID: "collection-test", TestSchema: pool.Config().ConnConfig.RuntimeParams["search_path"], ManifestSHA256: strings.Repeat("a", 64), FixtureOrigin: "http://fixture:8080", AllowedAccountIDs: []int64{1}, Bindings: []domain.CollectionBinding{{SourceID: source.ID, WellID: 1, ExternalID: "-900000", IdentityKind: "unitek"}}, Fixtures: []domain.CollectionFixture{{ID: "empty-v1", Protocol: "collection-fixture-v1", Horizon: horizon, RetentionStart: horizon.Add(-31 * 24 * time.Hour), Empty: true, Channels: []domain.CollectionChannel{{Name: "F_out", Aliases: []string{"f", "F_out"}, Unit: "Hz", Minimum: 0, Maximum: 100}}, Mappings: source.Mappings}}}
				r, e = r.WithCollectionJobPolicy(policy)
				if e != nil {
					t.Fatal(e)
				}
				in.FixtureID = "empty-v1"
			}
			attempts := 1
			if tc.overlap {
				attempts = 2
			}
			for n := 0; n < attempts; n++ {
				in.ReplayKey = fmt.Sprintf("watermark-preserve-%02d", n)
				if _, e = r.AcceptCollectionJob(ctx, in); e != nil {
					t.Fatal(e)
				}
				claim, _ := collectionClaim(t, ctx, r)
				var capture domain.CollectionPreparedCapture
				if tc.empty {
					work, e := r.LoadCollectionWork(ctx, claim.Lease())
					if e != nil {
						t.Fatal(e)
					}
					raw := []byte(`{"protocol":"collection-fixture-v1","fixture_id":"empty-v1","well_id":"-900000","start":"2026-10-19T00:00:00Z","end":"2026-10-19T00:02:00Z","data_revision":"generation-empty","complete":true,"empty":true,"expected_slots":0,"records":[]}`)
					capture, e = domain.PrepareCollectionCapture(work.Intent, domain.CollectionRawCapture{Body: raw, ReceivedAt: time.Now().UTC()})
					if e != nil {
						t.Fatal(e)
					}
				} else {
					capture = collectionPrepared(t, ctx, r, claim.Lease(), 50)
				}
				if e = r.SaveCollectionCapture(ctx, claim.Lease(), capture); e != nil {
					t.Fatal(e)
				}
				out, e := r.ApplyCollectionCapture(ctx, claim.Lease())
				if e != nil || out.Status.State != domain.CollectionSucceeded {
					t.Fatal(out, e)
				}
				claim.Close()
				got, e := r.Source(ctx, in.SourceID)
				if e != nil {
					t.Fatal(e)
				}
				if n == attempts-1 && ((before == nil) != (got.LastMeasured == nil) || before != nil && !before.Equal(*got.LastMeasured)) {
					t.Fatalf("C2 advanced legacy since: before=%v after=%v", before, got.LastMeasured)
				}
				if got.LastReceived == nil {
					t.Fatal("successful capture lost received observation")
				}
				coverage := out.Status.Coverage
				if tc.empty {
					if coverage.State != "complete_empty" || coverage.MaximumMeasuredAt != nil {
						t.Fatal("empty coverage maximum is not nullable", coverage)
					}
				} else {
					want := time.Date(2026, 10, 19, 0, 1, 0, 0, time.UTC)
					if coverage.State != "complete" || coverage.MaximumMeasuredAt == nil || !coverage.MaximumMeasuredAt.Equal(want) {
						t.Fatal("captured maximum lost from coverage", coverage)
					}
				}
				if tc.overlap && n == 1 && (out.Status.Write.Inserted != 0 || out.Status.Write.Identical != 2) {
					t.Fatal("overlap ceased to be identical", out)
				}
			}
		})
	}
}

// Допустимый legacy Import100000 не должен регрессировать в timeout после защиты C2 keys.
func TestCollectionLegacyBulk100000(t *testing.T) {
	pool := testDB(t)
	r := postgres.New(pool).WithExecutionProfile("legacy", "research")
	ctx, cancel := context.WithTimeout(legacyResearchFixtureContext(), 5*time.Second)
	defer cancel()
	value := 50.0
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	points := make([]domain.Measurement, 100000)
	for i := range points {
		points[i] = domain.Measurement{WellID: "legacy-bulk", Timestamp: start.Add(time.Duration(i) * time.Minute), Values: map[string]*float64{"F_out": &value}}
	}
	began := time.Now()
	out, e := r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: "manual", Points: points})
	elapsed := time.Since(began)
	rows := collectionCount(t, pool, "telemetry_measurement")
	t.Logf("elapsed=%s accepted=%d duplicates=%d persisted=%d", elapsed, out.Accepted, out.Duplicates, rows)
	if e != nil || rows != 100000 || out.Accepted != 100000 || out.Duplicates != 0 || elapsed >= 5*time.Second {
		t.Fatal("permitted100000pointlegacyimportfailed", elapsed, out, e)
	}
}

func TestCollectionLegacyBatchProtectedLateRollback(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	if _, e := r.AcceptCollectionJob(ctx, in); e != nil {
		t.Fatal(e)
	}
	claim, _ := collectionClaim(t, ctx, r)
	capture := collectionPrepared(t, ctx, r, claim.Lease(), 50)
	if e := r.SaveCollectionCapture(ctx, claim.Lease(), capture); e != nil {
		t.Fatal(e)
	}
	if _, e := r.ApplyCollectionCapture(ctx, claim.Lease()); e != nil {
		t.Fatal(e)
	}
	claim.Close()
	var before string
	if e := pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(m) ORDER BY measured_at)::text FROM telemetry_measurement m`).Scan(&before); e != nil {
		t.Fatal(e)
	}
	value := 50.0
	stamp := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	if _, e := r.CreateWell(ctx, domain.Well{UnitekID: ptr(int64(-900001)), FundType: "operational", Active: true}); e != nil {
		t.Fatal(e)
	}
	points := make([]domain.Measurement, 4097)
	for i := 0; i < 4096; i++ {
		points[i] = domain.Measurement{WellID: "-900001", Timestamp: stamp.Add(time.Duration(i) * time.Minute), Values: map[string]*float64{"F_out": &value}}
	}
	// Protected key sorts after all fresh rows and lands beyond the initial bounded batches.
	points[4096] = domain.Measurement{WellID: "pulse:1", Timestamp: stamp, Values: map[string]*float64{"F_out": &value}}
	if _, e := r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: "manual", Points: points}); e == nil || e.Error() != "telemetry_measurement_conflict" {
		t.Fatal("late protected conflict accepted", e)
	}
	if collectionCount(t, pool, "telemetry_measurement") != 2 || collectionCount(t, pool, "telemetry_collection_log") != 1 {
		t.Fatal("late collision leaked partial batch")
	}
	var after string
	if e := pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(m) ORDER BY measured_at)::text FROM telemetry_measurement m`).Scan(&after); e != nil || before != after {
		t.Fatal("protected payload/provenance changed", e)
	}
}

func TestCollectionLegacyBatchUnprotectedFirstWins(t *testing.T) {
	pool := testDB(t)
	r := postgres.New(pool).WithExecutionProfile("legacy", "research")
	ctx := legacyResearchFixtureContext()
	stamp := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	first, later := 1.0, 2.0
	points := make([]domain.Measurement, 4096)
	for i := range points {
		points[i] = domain.Measurement{WellID: "legacy-first-wins", Timestamp: stamp.Add(time.Duration(i) * time.Minute), Values: map[string]*float64{"F_out": &first}, Quality: "first"}
	}
	points = append(points, domain.Measurement{WellID: points[0].WellID, Timestamp: points[0].Timestamp, Values: map[string]*float64{"F_out": &later}, Quality: "later"})
	// pgx stores timestamptz at microsecond precision: input order wins even if nanoseconds differ.
	points = append(points, domain.Measurement{WellID: "precision", Timestamp: stamp.Add(900 * time.Nanosecond), Values: map[string]*float64{"F_out": &first}, Quality: "first"}, domain.Measurement{WellID: "precision", Timestamp: stamp.Add(100 * time.Nanosecond), Values: map[string]*float64{"F_out": &later}, Quality: "later"})
	out, e := r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: "manual", Points: points})
	if e != nil || out.Accepted != 4097 || out.Duplicates != 2 {
		t.Fatal(out, e)
	}
	var value float64
	var quality string
	for _, well := range []string{"legacy-first-wins", "precision"} {
		if e = pool.QueryRow(ctx, `SELECT (values->>'F_out')::double precision,quality FROM telemetry_measurement WHERE well_id=$1 AND measured_at=$2`, well, stamp).Scan(&value, &quality); e != nil || value != 1 || quality != "first" {
			t.Fatal("input first-wins changed", well, value, quality, e)
		}
	}
	out, e = r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: "manual", Points: points})
	if e != nil || out.Accepted != 0 || out.Duplicates != len(points) {
		t.Fatal("unprotected replay changed", out, e)
	}
}
