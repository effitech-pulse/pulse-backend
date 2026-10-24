package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func passportPtr[T any](v T) *T       { return &v }
func passportSet[T any](v T) Field[T] { return Field[T]{Present: true, Value: v} }
func passportClear[T any]() Field[T]  { return Field[T]{Present: true, Null: true} }

func TestEquipmentPassportPatchAndScalars(t *testing.T) {
	var m PassportMutation
	if err := json.Unmarshal([]byte(`{"well_id":"9007199254740993","expected_well_revision":"0","expected_passport_revision":"0","model":null,"control_output_frequency_min_hz":"0"}`), &m); err != nil {
		t.Fatal(err)
	}
	if int64(m.WellID) != 9007199254740993 || !m.Model.Present || !m.Model.Null || m.Manufacturer.Present || m.ControlOutputFrequencyMinHz.Value != "0" {
		t.Fatalf("patch lost: %+v", m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"model":null`) || strings.Contains(string(b), `"manufacturer"`) || !strings.Contains(string(b), `"well_id":"9007199254740993"`) {
		t.Fatal(string(b))
	}
	for _, raw := range []string{`1`, `"1e3"`, `"0"`, `"-1"`, `"9223372036854775808"`} {
		var id PassportID
		if json.Unmarshal([]byte(raw), &id) == nil {
			t.Errorf("accepted ID %s", raw)
		}
	}
	for raw, want := range map[string]string{"0001.230000": "1.23", "0.000001": "0.000001", "0": "0"} {
		d, e := ParsePassportDecimal(raw)
		if e != nil || string(d) != want {
			t.Fatalf("%s -> %s %v", raw, d, e)
		}
	}
	for _, raw := range []string{"1e3", "+1", "1,2", "NaN", ".5", "1.", " 1"} {
		if _, e := ParsePassportDecimal(raw); e == nil {
			t.Errorf("accepted decimal %s", raw)
		}
	}
	for _, raw := range []string{"2026-02-29", "2026-1-01", "2026-10-24T00:00:00Z"} {
		if _, e := ParsePassportDate(raw); e == nil {
			t.Errorf("accepted date %s", raw)
		}
	}
	if d, e := ParsePassportDate("2024-02-29"); e != nil || d != "2024-02-29" {
		t.Fatal(d, e)
	}
}

func TestEquipmentPassportLinkedNullDTO(t *testing.T) {
	const input = `{"row_key":"correction-1","well_id":"9007199254740993","expected_well_revision":"2","expected_passport_revision":"3","slot":"pump","action":"correct","component_id":"11","installation_id":"21","linked_installation_id":"22","replacement_on":null,"change_reason":"Дата замены неизвестна"}`
	var m PassportMutation
	if err := json.Unmarshal([]byte(input), &m); err != nil {
		t.Fatal(err)
	}
	if !m.ReplacementOn.Present || !m.ReplacementOn.Null || m.InstalledOn.Present || m.RemovedOn.Present {
		t.Fatal("linked intent lost")
	}
	output, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), `"replacement_on":null`) || strings.Contains(string(output), `"installed_on"`) || strings.Contains(string(output), `"removed_on"`) || !strings.Contains(string(output), `"linked_installation_id":"22"`) {
		t.Fatal(string(output))
	}
}
