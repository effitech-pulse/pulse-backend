package domain

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCohortJobTerminal(t *testing.T) {
	for _, state := range []CohortJobState{"pending_dispatch", "queued", "running", "retry_wait", "cancel_requested", "quarantined", "unknown"} {
		if state.Terminal() {
			t.Fatal(state)
		}
	}
	for _, state := range []CohortJobState{"succeeded", "failed", "cancelled", "expired", "revoked"} {
		if !state.Terminal() {
			t.Fatal(state)
		}
	}
}
func TestCohortLegacySnapshotOmitsExecution(t *testing.T) {
	raw, e := json.Marshal(CohortSnapshot{})
	if e != nil || strings.Contains(string(raw), "execution") {
		t.Fatal(string(raw), e)
	}
}
func cohortTestPin() ExecutionPin {
	h := FileSHA256([]byte("pin"))
	return ExecutionPin{Version: "upload_pin_v1", ModelName: "synthetic", ContextVersion: "legacy_v0", BundleSHA256: h, FeatureRecipeSHA256: h, FeatureSchemaSHA256: h, SourceManifestSHA256: h, RuntimeManifestSHA256: h, DecisionRule: &DecisionRule{ContextVersion: "legacy_v0", PolicyID: "upload", RuleSHA256: h, Watch: &ThresholdRule{Scale: "raw", Comparison: ">", Threshold: .5, KWindows: 1}}}
}
func cohortTestFixture() CohortFixtureManifest {
	f := CohortFixtureManifest{Version: "cohort_fixture_v1", FixtureID: "synthetic", CohortVersion: "test-v1", CohortManifestSHA256: strings.Repeat("a", 64), AsOf: time.Date(2026, 2, 1, 0, 0, 0, 123, time.UTC), ExecutionPin: cohortTestPin(), CaptureRecipe: CohortCaptureRecipe{Version: "cohort_fixture_capture_v1", Normalization: "none_supplied_canonical", BackendBuildSHA256: FileSHA256([]byte("build"))}}
	reason := "insufficient_telemetry"
	for i := 1; i <= 100; i++ {
		f.Members = append(f.Members, CohortFixtureMember{WellID: int64(i), NoComputeReason: &reason})
	}
	return f
}
func TestCohortCanonicalHashOrderAndUTC(t *testing.T) {
	f := cohortTestFixture()
	want, e := CohortFixtureSHA256(f)
	if e != nil {
		t.Fatal(e)
	}
	slices.Reverse(f.Members)
	f.AsOf = f.AsOf.In(time.FixedZone("offset", 10800))
	got, e := CohortFixtureSHA256(f)
	if f.Members[0].WellID != 100 || f.AsOf.Location() == time.UTC {
		t.Fatal("canonical helper mutated caller")
	}
	if e != nil || got != want {
		t.Fatal(got, want, e)
	}
	f.Members[0].WellID = f.Members[1].WellID
	if _, e = CohortFixtureSHA256(f); e == nil {
		t.Fatal("duplicate accepted")
	}
}
func TestCohortCommitmentIncludesActorAndFixture(t *testing.T) {
	h := FileSHA256([]byte("fixture"))
	a, e := CohortRequestCommitment(1, "fixture", h)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := CohortRequestCommitment(2, "fixture", h)
	c, _ := CohortRequestCommitment(1, "other", h)
	if a != "sha256:43f940583fcc3655f0fa8f4ac5b9c5071795831e3d67f79a2c96ca55e40782ae" {
		t.Fatal("canonical payload changed", a)
	}
	if a == b || a == c {
		t.Fatal("identity lost")
	}
}
func TestCohortPreparedMarkersAndCorruptSource(t *testing.T) {
	f := cohortTestFixture()
	raw, _ := CanonicalCohortFixture(f)
	sha, _ := CohortFixtureSHA256(f)
	commit, _ := CohortRequestCommitment(1, f.FixtureID, sha)
	v := PreparedCohortJob{ActorAccountID: 1, RequestKey: "cohort-key-00000001", RequestCommitmentSHA256: commit, Fixture: f, FixtureSHA256: sha, FixtureJSON: raw}
	for _, m := range f.Members {
		v.Members = append(v.Members, CohortFrozenMember{Ordinal: len(v.Members) + 1, Well: Well{ID: m.WellID}, NoComputeReason: m.NoComputeReason})
	}
	if e := ValidatePreparedCohortJob(v); e != nil {
		t.Fatal(e)
	}
	v.Members[99].Input = &CohortFrozenInput{}
	if e := ValidatePreparedCohortJob(v); e == nil {
		t.Fatal("XOR not enforced")
	}
}
func cohortTestInput() CohortFrozenInput {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * 24 * time.Hour).Add(time.Nanosecond)
	raw := []byte("timestamp,CosF,F_out,Ia,Motor_winding_temperature,Ped_load,Pump_intake_pressure_kgs,qg\n2026-01-01T00:00:00Z,0.87,0,0,92,55,45,0\n2026-01-31T00:00:00Z,0.87,50,22,92,55,45,0\n")
	return CohortFrozenInput{Identity: PinnedInputIdentity{InputSnapshotID: strings.Repeat("a", 32), CanonicalSHA256: FileSHA256(raw), CanonicalBytes: int64(len(raw)), SourceKind: "cohort_snapshot"}, Canonical: raw, Bounds: TelemetryBounds{Start: &start, End: &end}, Resolution: "file", Rows: 2, Columns: 7}
}
func TestCohortCSVStrictAndZero(t *testing.T) {
	input := cohortTestInput()
	asof := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if e := validateCohortCSV(input, asof); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{"set_Annular_pressure", "4x010D F_out", "unknown"} {
		cp := input
		cp.Canonical = []byte(strings.Replace(string(cp.Canonical), "F_out", bad, 1))
		cp.Identity.CanonicalBytes = int64(len(cp.Canonical))
		cp.Identity.CanonicalSHA256 = FileSHA256(cp.Canonical)
		if e := validateCohortCSV(cp, asof); e == nil {
			t.Fatal(bad)
		}
	}
	if e := validateCohortCSV(input, asof.Add(-2*24*time.Hour)); e == nil {
		t.Fatal("future accepted")
	}
}
func TestCohortPinnedResultRejectsForgedKind(t *testing.T) {
	p := cohortTestPin()
	input := cohortTestInput()
	input.Identity.FingerprintSHA256, _ = CohortInputFingerprint(1, input, p, cohortTestFixture().CaptureRecipe)
	str := func(v string) *string { return &v }
	result := UploadResult{ModelName: &p.ModelName, ModelVersion: &p.BundleSHA256, DecisionRule: p.DecisionRule, Verdict: "norm", LatestRisk: 0, Provenance: &Provenance{InputKind: "cohort_snapshot", InputSnapshotID: str(input.Identity.InputSnapshotID), InputSHA256: str(input.Identity.CanonicalSHA256), ContextVersion: &p.ContextVersion, FeatureRecipeSHA256: &p.FeatureRecipeSHA256, FeatureSchemaSHA256: &p.FeatureSchemaSHA256, SourceManifestSHA256: &p.SourceManifestSHA256, RuntimeManifestSHA256: &p.RuntimeManifestSHA256}}
	if e := ValidateCohortPinnedResult(input, p, result); e != nil {
		t.Fatal(e)
	}
	result.Provenance.InputKind = "uploaded_file"
	if e := ValidateCohortPinnedResult(input, p, result); e == nil {
		t.Fatal("file provenance accepted")
	}
	result.Provenance.InputKind = "cohort_snapshot"
	result.DecisionRule = p.Clone().DecisionRule
	result.DecisionRule.HighEnabled = true
	if e := ValidateCohortPinnedResult(input, p, result); e == nil {
		t.Fatal("rule mismatch accepted")
	}
}
func TestCohortFingerprintMetadataAndOverlay(t *testing.T) {
	f := cohortTestFixture()
	in := cohortTestInput()
	a, e := CohortInputFingerprint(1, in, f.ExecutionPin, f.CaptureRecipe)
	if e != nil {
		t.Fatal(e)
	}
	in.Identity.SourceKind = "uploaded_file"
	if _, e = CohortInputFingerprint(1, in, f.ExecutionPin, f.CaptureRecipe); e == nil {
		t.Fatal("file source accepted")
	}
	in.Identity.SourceKind = "cohort_snapshot"
	in.Rows++
	b, _ := CohortInputFingerprint(1, in, f.ExecutionPin, f.CaptureRecipe)
	if a == b {
		t.Fatal("row count lost")
	}
	c := CohortCheckpoint{Outcome: CohortOutcome{}}
	n := int64(1)
	c.Outcome.AlertID = &n
	if _, e = CohortCheckpointDigest(c); e == nil {
		t.Fatal("live overlay hashed")
	}
}

func TestCohortPinHashRetainsFalseAndNull(t *testing.T) {
	pin := cohortTestPin()
	a, e := CohortExecutionPinSHA256(pin)
	if e != nil {
		t.Fatal(e)
	}
	copy := pin.Clone()
	copy.DecisionRule.High = &ThresholdRule{Scale: "raw", Comparison: ">", Threshold: 0, KWindows: 1}
	b, e := CohortExecutionPinSHA256(copy)
	if e != nil || a == b {
		t.Fatal("optional null threshold lost", e)
	}
	copy.DecisionRule.HighEnabled = true
	c, e := CohortExecutionPinSHA256(copy)
	if e != nil || b == c {
		t.Fatal("false/true high_enabled lost", e)
	}
	copy.DecisionRule.High.Threshold = .1
	d, e := CohortExecutionPinSHA256(copy)
	if e != nil || d == c {
		t.Fatal("zero threshold lost", e)
	}
	if pin.DecisionRule.High != nil || pin.DecisionRule.HighEnabled {
		t.Fatal("clone aliased rule")
	}
}
