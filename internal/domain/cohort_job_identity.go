package domain

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

func cohortInvalid() error { return NewError(Invalid, "invalid_cohort_identity") }
func cohortJSONHash(v any) (string, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return "", cohortInvalid()
	}
	return FileSHA256(raw), nil
}
func cohortCopy[T any](v T) (T, error) {
	var out T
	raw, e := json.Marshal(v)
	if e != nil {
		return out, cohortInvalid()
	}
	e = json.Unmarshal(raw, &out)
	return out, e
}
func cohortUTC(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC()
	return &v
}
func cohortNormalizeMembers(v []CohortFrozenMember) ([]CohortFrozenMember, error) {
	out, e := cohortCopy(v)
	if e != nil || len(out) != 100 {
		return nil, cohortInvalid()
	}
	for i := range out {
		if out[i].Input != nil && v[i].Input != nil {
			out[i].Input.Canonical = bytes.Clone(v[i].Input.Canonical)
		}
	}
	slices.SortFunc(out, func(a, b CohortFrozenMember) int {
		if a.Well.ID < b.Well.ID {
			return -1
		}
		if a.Well.ID > b.Well.ID {
			return 1
		}
		return 0
	})
	for i := range out {
		m := &out[i]
		if m.Well.ID <= 0 || (i > 0 && out[i-1].Well.ID == m.Well.ID) || (m.Input == nil) == (m.NoComputeReason == nil) {
			return nil, cohortInvalid()
		}
		m.Ordinal = i + 1
		m.Well.ArchivedAt = cohortUTC(m.Well.ArchivedAt)
		if m.Input != nil {
			m.Input.Bounds.Start = cohortUTC(m.Input.Bounds.Start)
			m.Input.Bounds.End = cohortUTC(m.Input.Bounds.End)
			m.Input.Watermark = cohortUTC(m.Input.Watermark)
		}
	}
	return out, nil
}
func cohortRecipe(v CohortCaptureRecipe) bool {
	return v.Version == "cohort_fixture_capture_v1" && v.Normalization == "none_supplied_canonical" && ValidFileSHA256(v.BackendBuildSHA256)
}
func CanonicalCohortFixture(v CohortFixtureManifest) ([]byte, error) {
	out, e := cohortCopy(v)
	if e != nil || out.Version != "cohort_fixture_v1" || out.FixtureID == "" || len(out.FixtureID) > 80 || out.CohortVersion == "" || len(out.CohortManifestSHA256) != 64 || strings.Trim(out.CohortManifestSHA256, "0123456789abcdef") != "" || out.AsOf.IsZero() || out.ExecutionPin.Validate() != nil || !cohortRecipe(out.CaptureRecipe) || len(out.Members) != 100 {
		return nil, cohortInvalid()
	}
	out.AsOf = out.AsOf.UTC()
	slices.SortFunc(out.Members, func(a, b CohortFixtureMember) int {
		if a.WellID < b.WellID {
			return -1
		}
		if a.WellID > b.WellID {
			return 1
		}
		return 0
	})
	for i, m := range out.Members {
		if m.WellID <= 0 || (i > 0 && out.Members[i-1].WellID == m.WellID) {
			return nil, cohortInvalid()
		}
		if m.NoComputeReason != nil {
			if (*m.NoComputeReason != "insufficient_telemetry" && *m.NoComputeReason != "synthetic_source_unavailable") || m.CanonicalSHA256 != "" || m.CanonicalBytes != 0 {
				return nil, cohortInvalid()
			}
		} else if !ValidFileSHA256(m.CanonicalSHA256) || m.CanonicalBytes <= 0 || m.CanonicalBytes > 512<<10 {
			return nil, cohortInvalid()
		}
	}
	raw, e := json.Marshal(out)
	if e != nil || len(raw) > 512<<10 {
		return nil, cohortInvalid()
	}
	return raw, nil
}
func CohortFixtureSHA256(v CohortFixtureManifest) (string, error) {
	b, e := CanonicalCohortFixture(v)
	if e != nil {
		return "", e
	}
	return FileSHA256(b), nil
}
func CohortRequestCommitment(actorID int64, fixtureID, fixtureSHA256 string) (string, error) {
	if actorID <= 0 || fixtureID == "" || !ValidFileSHA256(fixtureSHA256) {
		return "", cohortInvalid()
	}
	return cohortJSONHash(struct {
		Version   string `json:"version"`
		ActorID   int64  `json:"actor_id"`
		Operation string `json:"operation_kind"`
		FixtureID string `json:"fixture_id"`
		SHA       string `json:"fixture_manifest_sha256"`
	}{"cohort_request_v1", actorID, "cohort_score", fixtureID, fixtureSHA256})
}
func CohortExecutionPinSHA256(pin ExecutionPin) (string, error) {
	if e := pin.Validate(); e != nil {
		return "", e
	}
	return cohortJSONHash(pin)
}
func CohortInputFingerprint(wellID int64, input CohortFrozenInput, pin ExecutionPin, recipe CohortCaptureRecipe) (string, error) {
	h, e := CohortExecutionPinSHA256(pin)
	if e != nil {
		return "", e
	}
	if wellID <= 0 || !fileInputID.MatchString(input.Identity.InputSnapshotID) || input.Identity.SourceKind != "cohort_snapshot" || !ValidFileSHA256(input.Identity.CanonicalSHA256) || input.Identity.CanonicalBytes <= 0 || !cohortRecipe(recipe) {
		return "", cohortInvalid()
	}
	input.Bounds.Start = cohortUTC(input.Bounds.Start)
	input.Bounds.End = cohortUTC(input.Bounds.End)
	input.Watermark = cohortUTC(input.Watermark)
	return cohortJSONHash(struct {
		Version    string              `json:"version"`
		WellID     int64               `json:"well_id"`
		InputID    string              `json:"input_snapshot_id"`
		SHA        string              `json:"canonical_sha256"`
		Size       int64               `json:"canonical_bytes"`
		Kind       string              `json:"source_kind"`
		Bounds     TelemetryBounds     `json:"bounds"`
		Watermark  *time.Time          `json:"watermark"`
		Resolution string              `json:"resolution"`
		Rows       int                 `json:"rows"`
		Columns    int                 `json:"columns"`
		Recipe     CohortCaptureRecipe `json:"capture_recipe"`
		Pin        string              `json:"execution_pin_sha256"`
	}{"cohort_input_v1", wellID, input.Identity.InputSnapshotID, input.Identity.CanonicalSHA256, input.Identity.CanonicalBytes, input.Identity.SourceKind, input.Bounds, input.Watermark, input.Resolution, input.Rows, input.Columns, recipe, h})
}
func CohortInputManifestSHA256(fixture CohortFixtureManifest, members []CohortFrozenMember) (string, error) {
	m, e := cohortNormalizeMembers(members)
	if e != nil {
		return "", e
	}
	pin, e := CohortExecutionPinSHA256(fixture.ExecutionPin)
	if e != nil {
		return "", e
	}
	return cohortJSONHash(struct {
		Version       string               `json:"version"`
		CohortVersion string               `json:"cohort_version"`
		CohortSHA     string               `json:"cohort_manifest_sha256"`
		AsOf          time.Time            `json:"as_of"`
		Recipe        CohortCaptureRecipe  `json:"capture_recipe"`
		Pin           string               `json:"execution_pin_sha256"`
		Members       []CohortFrozenMember `json:"members"`
	}{"cohort_input_manifest_v1", fixture.CohortVersion, fixture.CohortManifestSHA256, fixture.AsOf.UTC(), fixture.CaptureRecipe, pin, m})
}
func CohortJobManifestSHA256(jobID string, runID int64, snapshotID, fixtureSHA256, inputManifestSHA256, pinSHA256 string) (string, error) {
	if !fileInputID.MatchString(jobID) || runID <= 0 || !fileInputID.MatchString(snapshotID) || !ValidFileSHA256(fixtureSHA256) || !ValidFileSHA256(inputManifestSHA256) || !ValidFileSHA256(pinSHA256) {
		return "", cohortInvalid()
	}
	return cohortJSONHash(struct {
		Version    string `json:"version"`
		JobID      string `json:"job_id"`
		RunID      int64  `json:"run_id"`
		SnapshotID string `json:"snapshot_id"`
		Fixture    string `json:"fixture_sha256"`
		Input      string `json:"input_manifest_sha256"`
		Pin        string `json:"execution_pin_sha256"`
	}{"cohort_job_manifest_v1", jobID, runID, snapshotID, fixtureSHA256, inputManifestSHA256, pinSHA256})
}
func cohortNoOverlay(o CohortOutcome) bool {
	return o.AlertID == nil && o.AlertStatus == nil && o.RunID == nil && o.SnapshotID == nil && o.AsOf == nil && o.CohortVersion == ""
}
func CohortCheckpointDigest(c CohortCheckpoint) (string, error) {
	if !cohortNoOverlay(c.Outcome) {
		return "", cohortInvalid()
	}
	o, e := cohortCopy(c.Outcome)
	if e != nil {
		return "", e
	}
	o.Metadata.ArchivedAt = cohortUTC(o.Metadata.ArchivedAt)
	o.InputWatermark = cohortUTC(o.InputWatermark)
	if o.Bounds != nil {
		o.Bounds.Start = cohortUTC(o.Bounds.Start)
		o.Bounds.End = cohortUTC(o.Bounds.End)
	}
	return cohortJSONHash(struct {
		Version     string        `json:"version"`
		Job         string        `json:"job_id"`
		Well        int64         `json:"well_id"`
		Fingerprint string        `json:"input_fingerprint_sha256"`
		Pin         string        `json:"execution_pin_sha256"`
		Manifest    string        `json:"input_manifest_sha256"`
		Outcome     CohortOutcome `json:"outcome"`
	}{"cohort_checkpoint_v1", c.JobID, c.WellID, c.InputFingerprintSHA256, c.ExecutionPinSHA256, c.InputManifestSHA256, o})
}

func validateCohortCSV(input CohortFrozenInput, asOf time.Time) error {
	if input.Identity.SourceKind != "cohort_snapshot" || len(input.Canonical) == 0 || len(input.Canonical) > 512<<10 || int64(len(input.Canonical)) != input.Identity.CanonicalBytes || FileSHA256(input.Canonical) != input.Identity.CanonicalSHA256 || input.Rows < 2 || input.Rows > 2000 || input.Columns != 7 || input.Resolution == "" || input.Bounds.Start == nil || input.Bounds.End == nil {
		return cohortInvalid()
	}
	b := input.Canonical
	if bytes.ContainsAny(b, "\r\x00") || b[len(b)-1] != '\n' {
		return cohortInvalid()
	}
	reader := csv.NewReader(bytes.NewReader(b))
	header, e := reader.Read()
	want := []string{"timestamp", "CosF", "F_out", "Ia", "Motor_winding_temperature", "Ped_load", "Pump_intake_pressure_kgs", "qg"}
	if e != nil || !slices.Equal(header, want) {
		return cohortInvalid()
	}
	n := 0
	var first, last time.Time
	for {
		row, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil || len(row) != 8 || n >= 2000 {
			return cohortInvalid()
		}
		for _, f := range row {
			if len(f) > 1024 {
				return cohortInvalid()
			}
		}
		ts, e := time.Parse(time.RFC3339Nano, row[0])
		if e != nil || !strings.HasSuffix(row[0], "Z") || ts.After(asOf) || ts.Before(asOf.Add(-60*24*time.Hour)) || (n > 0 && !ts.After(last)) {
			return cohortInvalid()
		}
		if n == 0 {
			first = ts
		}
		last = ts
		for _, f := range row[1:] {
			if f == "" {
				continue
			}
			v, e := strconv.ParseFloat(f, 64)
			if e != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				return cohortInvalid()
			}
		}
		n++
	}
	if n != input.Rows || last.Sub(first) < 30*24*time.Hour || last.Sub(first) > 60*24*time.Hour || !input.Bounds.Start.Equal(first) || !input.Bounds.End.Equal(last.Add(time.Nanosecond)) || (input.Watermark != nil && input.Watermark.After(asOf)) {
		return cohortInvalid()
	}
	return nil
}
func ValidatePreparedCohortJob(v PreparedCohortJob) error {
	raw, e := CanonicalCohortFixture(v.Fixture)
	if e != nil {
		return e
	}
	commit, e := CohortRequestCommitment(v.ActorAccountID, v.Fixture.FixtureID, v.FixtureSHA256)
	if e != nil || !fileRequestKey.MatchString(v.RequestKey) || commit != v.RequestCommitmentSHA256 || !bytes.Equal(raw, v.FixtureJSON) || FileSHA256(raw) != v.FixtureSHA256 {
		return cohortInvalid()
	}
	members, e := cohortNormalizeMembers(v.Members)
	if e != nil {
		return e
	}
	var f CohortFixtureManifest
	if json.Unmarshal(raw, &f) != nil {
		return cohortInvalid()
	}
	total := 0
	for i, m := range members {
		desc := f.Members[i]
		meta, _ := json.Marshal(m.Well)
		if m.Well.ID != desc.WellID || len(meta) > 4096 {
			return cohortInvalid()
		}
		if desc.NoComputeReason != nil {
			if m.Input != nil || m.NoComputeReason == nil || *m.NoComputeReason != *desc.NoComputeReason {
				return cohortInvalid()
			}
		} else {
			if m.Input == nil || m.NoComputeReason != nil || m.Input.Identity.CanonicalSHA256 != desc.CanonicalSHA256 || m.Input.Identity.CanonicalBytes != desc.CanonicalBytes {
				return cohortInvalid()
			}
			if e = validateCohortCSV(*m.Input, f.AsOf); e != nil {
				return e
			}
			total += len(m.Input.Canonical)
		}
	}
	if total > 24<<20 {
		return NewError(Limited, "cohort_input_capacity")
	}
	return nil
}
func cohortEqual(a, b any) bool {
	x, e := json.Marshal(a)
	if e != nil {
		return false
	}
	y, e := json.Marshal(b)
	return e == nil && bytes.Equal(x, y)
}
func cohortProvenance(input CohortFrozenInput, pin ExecutionPin, p *Provenance, rule *DecisionRule) bool {
	eq := func(got *string, want string) bool { return got != nil && *got == want }
	if pin.Validate() != nil || input.Identity.SourceKind != "cohort_snapshot" || !fileInputID.MatchString(input.Identity.InputSnapshotID) || !ValidFileSHA256(input.Identity.FingerprintSHA256) || int64(len(input.Canonical)) != input.Identity.CanonicalBytes || FileSHA256(input.Canonical) != input.Identity.CanonicalSHA256 || p == nil || p.InputKind != "cohort_snapshot" || !cohortEqual(pin.DecisionRule, rule) {
		return false
	}
	return eq(p.InputSnapshotID, input.Identity.InputSnapshotID) && eq(p.InputSHA256, input.Identity.CanonicalSHA256) && eq(p.ContextVersion, pin.ContextVersion) && eq(p.FeatureRecipeSHA256, pin.FeatureRecipeSHA256) && eq(p.FeatureSchemaSHA256, pin.FeatureSchemaSHA256) && eq(p.SourceManifestSHA256, pin.SourceManifestSHA256) && eq(p.RuntimeManifestSHA256, pin.RuntimeManifestSHA256) && (p.ModelName == nil || *p.ModelName == pin.ModelName) && (p.ModelVersion == nil || *p.ModelVersion == pin.BundleSHA256)
}
func ValidateCohortPinnedResult(input CohortFrozenInput, pin ExecutionPin, result UploadResult) error {
	raw, e := json.Marshal(result)
	if e != nil || len(raw) > 256<<10 || pin.Validate() != nil || input.Identity.SourceKind != "cohort_snapshot" || !fileInputID.MatchString(input.Identity.InputSnapshotID) || !ValidFileSHA256(input.Identity.FingerprintSHA256) || !ValidFileSHA256(input.Identity.CanonicalSHA256) || FileSHA256(input.Canonical) != input.Identity.CanonicalSHA256 || int64(len(input.Canonical)) != input.Identity.CanonicalBytes || result.ModelName == nil || *result.ModelName != pin.ModelName || result.ModelVersion == nil || *result.ModelVersion != pin.BundleSHA256 || !cohortProvenance(input, pin, result.Provenance, result.DecisionRule) {
		return NewError(Invalid, "result_identity_mismatch")
	}
	switch result.Verdict {
	case "norm", "watch", "alert", "insufficient":
		return nil
	}
	return NewError(Invalid, "unsupported_verdict")
}
func ValidateCohortOutcome(member CohortFrozenMember, pin ExecutionPin, o CohortOutcome) error {
	if !cohortNoOverlay(o) || o.AppWellID != member.Well.ID || !cohortEqual(o.Metadata, member.Well) {
		return cohortInvalid()
	}
	if member.Input == nil {
		if member.NoComputeReason == nil || o.Result != nil || o.Provenance != nil || o.DecisionRule != nil || o.InputSHA256 != "" || o.Bounds != nil || o.InputWatermark != nil || o.Resolution != "" || o.ReasonCode != *member.NoComputeReason {
			return cohortInvalid()
		}
		status := map[string]string{"archived": "archived", "inactive": "inactive", "insufficient_telemetry": "insufficient", "synthetic_source_unavailable": "error"}[*member.NoComputeReason]
		if status == "" || o.Status != status {
			return cohortInvalid()
		}
		return nil
	}
	input := *member.Input
	if !cohortEqual(o.Bounds, &input.Bounds) || !cohortEqual(o.InputWatermark, input.Watermark) || o.Resolution != input.Resolution || o.InputSHA256 != strings.TrimPrefix(input.Identity.CanonicalSHA256, "sha256:") {
		return cohortInvalid()
	}
	if o.Status == "scored" && o.ReasonCode == "evaluated" && o.Result != nil && o.Result.Verdict != "insufficient" {
		if e := ValidateCohortPinnedResult(input, pin, *o.Result); e != nil {
			return e
		}
		if !cohortEqual(o.Provenance, o.Result.Provenance) || !cohortEqual(o.DecisionRule, o.Result.DecisionRule) {
			return cohortInvalid()
		}
		return nil
	}
	if o.Status == "insufficient" && o.ReasonCode == "insufficient_running_windows" && o.Result == nil && o.Diagnostics != nil && o.Diagnostics.Verdict == "insufficient" && cohortProvenance(input, pin, o.Provenance, o.DecisionRule) {
		return nil
	}
	return cohortInvalid()
}
func ValidateCohortCheckpoint(execution CohortMemberExecution, c CohortCheckpoint) error {
	fingerprint := ""
	if execution.Member.Input != nil {
		fingerprint = execution.Member.Input.Identity.FingerprintSHA256
	}
	if c.JobID != execution.JobID || c.WellID != execution.Member.Well.ID || c.InputFingerprintSHA256 != fingerprint || c.ExecutionPinSHA256 != execution.ExecutionPinSHA256 || c.InputManifestSHA256 != execution.InputManifestSHA256 {
		return cohortInvalid()
	}
	if e := ValidateCohortOutcome(execution.Member, execution.ExecutionPin, c.Outcome); e != nil {
		return e
	}
	h, e := CohortCheckpointDigest(c)
	if e != nil || h != c.DigestSHA256 {
		return cohortInvalid()
	}
	return nil
}
