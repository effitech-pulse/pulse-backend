package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func collectionIntent() CollectionIntent {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	return CollectionIntent{Start: start, End: start.Add(2 * time.Minute), Binding: CollectionBinding{SourceID: "synthetic", WellID: 42, ExternalID: "-900000", IdentityKind: "unitek"}, Fixture: CollectionFixture{ID: "dense-v1", Protocol: "collection-fixture-v1", Horizon: start.Add(24 * time.Hour), RetentionStart: start.Add(-30 * 24 * time.Hour), Channels: []CollectionChannel{{Name: "F_out", Aliases: []string{"#Выходная частота"}, Minimum: 0, Maximum: 100}, {Name: "R_izol", Minimum: 0, Maximum: 10000}, {Name: "set_Annular_pressure", IsSet: true, Minimum: 0, Maximum: 100}}, Mappings: []Mapping{{Field: "f", Parameter: "4x010D #Выходная частота", Scale: 1}, {Field: "r", Parameter: "R_izol", Scale: 2}, {Field: "s", Parameter: "set_Annular_pressure", Scale: 1}}}, SourceProfileVersion: 1, SourceConfigSHA256: strings.Repeat("a", 64), MappingSHA256: strings.Repeat("b", 64), BindingSHA256: strings.Repeat("c", 64), CanonicalVersion: "collection-canonical-v1", SchemaVersion: "collection-measurement-v1"}
}
func collectionBody(records string, empty bool) []byte {
	slots := 2
	if empty {
		slots = 0
	}
	b, _ := json.Marshal(map[string]any{"protocol": "collection-fixture-v1", "fixture_id": "dense-v1", "well_id": "-900000", "start": "2026-10-19T00:00:00Z", "end": "2026-10-19T00:02:00Z", "data_revision": "generation-a", "complete": true, "empty": empty, "expected_slots": slots, "records": json.RawMessage(records)})
	return b
}

const collectionRows = `[{"well_id":"-900000","timestamp":"2026-10-19T00:01:00Z","values":{"f":0,"r":9999,"s":10}},{"well_id":"-900000","timestamp":"2026-10-19T00:00:00Z","values":{"f":null,"r":2,"s":10}}]`

func TestCollectionCanonicalAliasesMarkersAndInvalid(t *testing.T) {
	intent := collectionIntent()
	got, err := PrepareCollectionCapture(intent, CollectionRawCapture{Body: collectionBody(collectionRows, false), ReceivedAt: intent.Start})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.RawSHA256) != 64 || len(got.CanonicalSHA256) != 64 {
		t.Fatalf("C2 digest must be rawhex64: %q", got.RawSHA256)
	}
	var canonical struct {
		Points []Measurement `json:"points"`
	}
	if err = json.Unmarshal(got.Canonical, &canonical); err != nil {
		t.Fatal(err)
	}
	if len(canonical.Points) != 2 || canonical.Points[0].WellID != "pulse:42" || canonical.Points[0].Values["F_out"] != nil || *canonical.Points[0].Values["R_izol"] != 4 || *canonical.Points[1].Values["F_out"] != 0 || canonical.Points[1].Values["R_izol"] != nil {
		t.Fatalf("wrong canonical capture: %s", got.Canonical)
	}
	if _, ok := canonical.Points[0].Values["set_Annular_pressure"]; ok {
		t.Fatal("setpoint became sensor")
	}
	if got.MarkerValues != 1 || got.NullValues != 1 || got.ExcludedSetpoints != 2 {
		t.Fatalf("wrong counters: %+v", got)
	}
	for name, rows := range map[string]string{"missing": strings.Replace(collectionRows, `"f":0,`, "", 1), "unknown": strings.Replace(collectionRows, `"f":0`, `"x":0`, 1), "malformed": strings.Replace(collectionRows, `"f":0`, `"f":"0"`, 1), "range": strings.Replace(collectionRows, `"f":0`, `"f":101`, 1), "duplicate-key": strings.Replace(collectionRows, `"f":0`, `"f":0,"f":0`, 1)} {
		t.Run(name, func(t *testing.T) {
			if _, err := PrepareCollectionCapture(intent, CollectionRawCapture{Body: collectionBody(rows, false)}); err == nil {
				t.Fatal("accepted invalid capture")
			}
		})
	}
}
func TestCollectionCoverageDenseHoleAndExplicitEmpty(t *testing.T) {
	i := collectionIntent()
	if _, e := PrepareCollectionCapture(i, CollectionRawCapture{Body: collectionBody(`[]`, false)}); e == nil {
		t.Fatal("dense empty accepted")
	}
	rows := strings.TrimSuffix(collectionRows, "]") + "," + strings.TrimPrefix(collectionRows, "[")
	got, e := PrepareCollectionCapture(i, CollectionRawCapture{Body: collectionBody(rows, false)})
	if e != nil || got.UniqueRows != 2 || got.RepeatedRows != 2 {
		t.Fatalf("duplicate collapse: %+v %v", got, e)
	}
	changed := strings.Replace(rows, `"r":2`, `"r":3`, 1)
	if _, e = PrepareCollectionCapture(i, CollectionRawCapture{Body: collectionBody(changed, false)}); e == nil {
		t.Fatal("conflicting duplicate accepted")
	}
	i.Fixture.Empty = true
	got, e = PrepareCollectionCapture(i, CollectionRawCapture{Body: collectionBody(`[]`, true)})
	if e != nil || !got.Empty || got.UniqueRows != 0 {
		t.Fatalf("explicit empty: %+v %v", got, e)
	}
}
func TestCollectionPeriodHorizonRetention(t *testing.T) {
	f := collectionIntent().Fixture
	for _, tc := range []struct {
		start, end string
		valid      bool
	}{{"2026-10-19T03:00:00+03:00", "2026-10-20T03:00:00+03:00", true}, {"2026-10-19T00:00:00Z", "2026-10-19T00:01:00Z", true}, {"2026-10-19T00:00:00Z", "2026-10-19T00:00:00Z", false}, {"2026-10-19T00:00:01Z", "2026-10-19T00:02:00Z", false}, {"2026-10-19T00:00:00Z", "2026-10-20T00:01:00Z", false}, {f.RetentionStart.Format(time.RFC3339), f.RetentionStart.Add(time.Minute).Format(time.RFC3339), true}, {f.RetentionStart.Add(-time.Minute).Format(time.RFC3339), f.RetentionStart.Format(time.RFC3339), false}} {
		_, _, e := NormalizeCollectionPeriod(tc.start, tc.end, f)
		if (e == nil) != tc.valid {
			t.Errorf("%+v: %v", tc, e)
		}
	}
}

func TestCollectionCoverageMissingMiddleAndEndExclusive(t *testing.T) {
	i := collectionIntent()
	i.End = i.End.Add(time.Minute)
	raw := strings.Replace(string(collectionBody(collectionRows, false)), `"end":"2026-10-19T00:02:00Z"`, `"end":"2026-10-19T00:03:00Z"`, 1)
	raw = strings.Replace(raw, `"expected_slots":2`, `"expected_slots":3`, 1)
	raw = strings.Replace(raw, `2026-10-19T00:01:00Z`, `2026-10-19T00:02:00Z`, 1)
	if _, e := PrepareCollectionCapture(i, CollectionRawCapture{Body: []byte(raw)}); e == nil {
		t.Fatal("latest timestamp hid missing middle slot")
	}
	i = collectionIntent()
	raw = strings.Replace(string(collectionBody(collectionRows, false)), `2026-10-19T00:01:00Z`, `2026-10-19T00:02:00Z`, 1)
	if _, e := PrepareCollectionCapture(i, CollectionRawCapture{Body: []byte(raw)}); e == nil {
		t.Fatal("exclusive-end row accepted")
	}
}
func TestCollectionAliasesConvergeOnlyOnEqualCleanValues(t *testing.T) {
	i := collectionIntent()
	i.Fixture.Mappings = append(i.Fixture.Mappings, Mapping{Field: "f2", Parameter: "F_out", Scale: 1})
	rows := strings.ReplaceAll(collectionRows, `"f":0`, `"f":0,"f2":0`)
	rows = strings.ReplaceAll(rows, `"f":null`, `"f":null,"f2":null`)
	if _, e := PrepareCollectionCapture(i, CollectionRawCapture{Body: collectionBody(rows, false)}); e != nil {
		t.Fatal("equal alias rejected", e)
	}
	for _, changed := range []string{strings.Replace(rows, `"f2":0`, `"f2":1`, 1), strings.Replace(rows, `"f2":null`, `"f2":0`, 1)} {
		if _, e := PrepareCollectionCapture(i, CollectionRawCapture{Body: collectionBody(changed, false)}); e == nil {
			t.Fatal("conflicting alias selected a winner")
		}
	}
}

func TestCollectionSchemaSetpointCannotBecomeSensor(t *testing.T) {
	i := collectionIntent()
	i.Fixture.Channels[0].IsSet = true
	out, e := PrepareCollectionCapture(i, CollectionRawCapture{Body: collectionBody(collectionRows, false)})
	if e != nil {
		t.Fatal(e)
	}
	var canonical struct {
		Points []Measurement `json:"points"`
	}
	if e = json.Unmarshal(out.Canonical, &canonical); e != nil {
		t.Fatal(e)
	}
	for _, p := range canonical.Points {
		if _, ok := p.Values["F_out"]; ok {
			t.Fatal("mapping.IsSet=false overrode schema setpoint")
		}
	}
}
