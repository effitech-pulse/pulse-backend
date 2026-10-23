package domain

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestCollectionIdentityDigests(t *testing.T) {
	i := collectionIntent()
	s := CollectionSubmit{ReplayKey: "replay-key-00000001", FixtureID: i.Fixture.ID, SourceID: i.Binding.SourceID}
	a, e := CollectionRequestSHA256(s, i.Start, i.End)
	if e != nil {
		t.Fatal(e)
	}
	b, e := CollectionRequestSHA256(s, i.Start.In(time.FixedZone("offset", 10800)), i.End)
	if e != nil || a != b {
		t.Fatal("offset changed normalized identity")
	}
	m := Measurement{WellID: "pulse:42", Timestamp: i.Start, Values: map[string]*float64{"F_out": nil}}
	p := CollectionProvenance{SourceID: "source", DataRevision: "a"}
	digest, e := CollectionPayloadSHA256(m, p)
	if e != nil {
		t.Fatal(e)
	}
	p.DataRevision = "b"
	other, _ := CollectionPayloadSHA256(m, p)
	if digest == other {
		t.Fatal("revision missing from payload identity")
	}
	for _, v := range []string{a, b, digest, other} {
		if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(v) {
			t.Fatal("not rawhex64", v)
		}
	}
}
func TestCollectionCombinedMaximum(t *testing.T) {
	i := collectionIntent()
	i.End = i.Start.Add(24 * time.Hour)
	i.Fixture.Channels = nil
	i.Fixture.Mappings = nil
	for n := 0; n < 20; n++ {
		name := []string{"F_out", "Ia", "Ib", "Ic", "Ped_load", "CosF", "R_izol", "Motor_winding_temperature", "Pump_intake_pressure_kgs", "Annulus_pressure", "Vibration_X", "Vibration_Y", "Vibration_Z", "energy_Input_energy_reactive_total_MW", "energy_Input_energy_reactive_total_kW", "Flowrate for current day", "set_a", "set_b", "set_c", "set_d"}[n]
		field := name
		i.Fixture.Channels = append(i.Fixture.Channels, CollectionChannel{Name: name, Minimum: 0, Maximum: 1e12, IsSet: n >= 16})
		i.Fixture.Mappings = append(i.Fixture.Mappings, Mapping{Field: field, Parameter: name, Scale: 1})
	}
	rows := make([]map[string]any, 0, 2880)
	for n := 0; n < 1440; n++ {
		values := map[string]any{}
		for _, m := range i.Fixture.Mappings {
			values[m.Field] = 0
		}
		row := map[string]any{"well_id": "-900000", "timestamp": i.Start.Add(time.Duration(n) * time.Minute).Format(time.RFC3339), "values": values}
		rows = append(rows, row, row)
	}
	envelope := map[string]any{"protocol": "collection-fixture-v1", "fixture_id": "dense-v1", "well_id": "-900000", "start": i.Start.Format(time.RFC3339), "end": i.End.Format(time.RFC3339), "data_revision": "generation-a", "complete": true, "empty": false, "expected_slots": 1440, "records": rows}
	raw, e := json.Marshal(envelope)
	if e != nil {
		t.Fatal(e)
	}
	if len(raw) > 2097152 {
		t.Fatal("test fixture exceeds byte cap")
	}
	raw = append(raw, []byte(strings.Repeat(" ", 2097152-len(raw)))...)
	out, e := PrepareCollectionCapture(i, CollectionRawCapture{Body: raw})
	if e != nil || out.RawRows != 2880 || out.UniqueRows != 1440 || out.RepeatedRows != 1440 {
		t.Fatalf("max control rows=%d/%d/%d err=%v", out.RawRows, out.UniqueRows, out.RepeatedRows, e)
	}
	envelope["records"] = append(rows, rows[0])
	tooMany, _ := json.Marshal(envelope)
	if _, e = PrepareCollectionCapture(i, CollectionRawCapture{Body: tooMany}); e == nil {
		t.Fatal("2881 raw rows accepted")
	}
	if _, e = PrepareCollectionCapture(i, CollectionRawCapture{Body: append(raw, ' ')}); e == nil {
		t.Fatal("max+1 accepted")
	}
}

func TestCollectionUnknownCanonicalChannelRejected(t *testing.T) {
	f := collectionIntent().Fixture
	f.Channels[0].Name = "guessed_sensor"
	f.Channels[0].Aliases = nil
	f.Mappings[0].Parameter = "guessed_sensor"
	if _, e := collectionMappings(f); e == nil {
		t.Fatal("arbitrary canonical channel accepted")
	}
}

func TestCollectionPolicyLimitsAndAllowedNames(t *testing.T) {
	i := collectionIntent()
	i.Fixture.RetentionStart = i.Fixture.Horizon.Add(-31 * 24 * time.Hour)
	valid := CollectionPolicy{RunID: "test-run", TestSchema: "q2c2_test", ManifestSHA256: strings.Repeat("a", 64), FixtureOrigin: "http://fixture:8080", AllowedAccountIDs: []int64{1}, Bindings: []CollectionBinding{i.Binding}, Fixtures: []CollectionFixture{i.Fixture}}
	if e := ValidateCollectionPolicy(valid); e != nil {
		t.Fatal("valid policy", e)
	}
	for _, mutate := range []func(*CollectionPolicy){func(p *CollectionPolicy) { p.FixtureOrigin = "http://user:secret@fixture:8080" }, func(p *CollectionPolicy) { p.FixtureOrigin = "http://example.com:8080" }, func(p *CollectionPolicy) { p.TestSchema = "public" }, func(p *CollectionPolicy) { p.TestSchema = "pg_catalog" }, func(p *CollectionPolicy) { p.AllowedAccountIDs = []int64{1, 2, 3} }, func(p *CollectionPolicy) { p.Bindings = append(p.Bindings, p.Bindings[0], p.Bindings[0]) }, func(p *CollectionPolicy) {
		p.Fixtures = append(p.Fixtures, p.Fixtures[0], p.Fixtures[0], p.Fixtures[0], p.Fixtures[0])
	}} {
		copy := valid
		mutate(&copy)
		if e := ValidateCollectionPolicy(copy); e == nil {
			t.Fatal("invalid policy accepted")
		}
	}
}
