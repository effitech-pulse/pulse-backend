package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

func passportCommand(action PassportAction) PassportMutation {
	m := PassportMutation{RowKey: "r1", WellID: 1, ExpectedWellRevision: 2, ExpectedPassportRevision: 3, Slot: "pump", Action: action, ChangeReason: "Уточнение"}
	if action != "register" {
		m.ComponentID = passportPtr(PassportID(11))
		m.InstallationID = passportPtr(PassportID(21))
	}
	return m
}
func passportBasis() PassportBasis {
	return PassportBasis{Well: Well{ID: 1, Revision: 2}, PassportID: passportPtr(PassportID(31)), Revision: 3, Installations: []PassportInstallation{{ID: passportPtr(PassportID(21)), ComponentID: passportPtr(PassportID(11)), Slot: "pump", Status: "current", InstalledOn: passportPtr(PassportDate("2026-01-01")), Attributes: PassportAttributes{Model: passportPtr("Старый"), SerialNumber: passportPtr("S1")}}}}
}
func passportPrepare(t *testing.T, m PassportMutation, b PassportBasis) PassportPrepared {
	t.Helper()
	p, e := PreparePassportChanges(PassportChangeSet{SchemaVersion: "equipment-passport-v1", Rows: []PassportMutation{m}}, []PassportBasis{b}, "2026-10-24")
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func passportHasIssue(p PassportPrepared, code string) bool {
	for _, i := range p.Issues {
		if i.Code == code && i.Severity == "error" {
			return true
		}
	}
	return false
}
func passportValid(t *testing.T, p PassportPrepared) {
	t.Helper()
	for _, i := range p.Issues {
		if i.Severity == "error" {
			t.Fatalf("unexpected issue %+v", i)
		}
	}
}

func TestEquipmentPassportTransitions(t *testing.T) {
	t.Run("register empty unknown current", func(t *testing.T) {
		b := passportBasis()
		b.Installations = nil
		m := passportCommand("register")
		m.Model = passportSet(" Новый ")
		p := passportPrepare(t, m, b)
		passportValid(t, p)
		if p.ChangedCount != 1 || p.Diff[0].After[0].InstalledOn != nil || p.Diff[0].After[0].Status != "current" || p.Diff[0].After[0].ID != nil || *p.Diff[0].After[0].Attributes.Model != "Новый" {
			t.Fatal(p)
		}
	})
	t.Run("update noop and reason", func(t *testing.T) {
		m := passportCommand("update")
		m.ChangeReason = ""
		p := passportPrepare(t, m, passportBasis())
		passportValid(t, p)
		if p.UnchangedCount != 1 || p.ChangedCount != 0 || len(p.Diff) != 0 {
			t.Fatal(p)
		}
		m.Model = passportSet("Другой")
		if !passportHasIssue(passportPrepare(t, m, passportBasis()), "required") {
			t.Fatal("missing reason accepted")
		}
	})
	t.Run("replace without inheritance", func(t *testing.T) {
		m := passportCommand("replace")
		m.Model = passportSet("Новый")
		m.ReplacementOn = passportClear[PassportDate]()
		b := passportBasis()
		before, _ := json.Marshal(b)
		p := passportPrepare(t, m, b)
		passportValid(t, p)
		if len(p.Diff[0].After) != 2 || p.Diff[0].After[0].Status != "closed" || p.Diff[0].After[0].RemovedOn != nil || p.Diff[0].After[1].Attributes.SerialNumber != nil || p.Diff[0].After[1].InstalledOn != nil {
			t.Fatal(p)
		}
		*p.Diff[0].After[0].Attributes.Model = "changed output"
		after, _ := json.Marshal(b)
		if !reflect.DeepEqual(before, after) || *p.Diff[0].Before[0].Attributes.Model != "Старый" {
			t.Fatal("basis/before mutated")
		}
	})
	for _, action := range []PassportAction{"correct", "close", "void"} {
		t.Run(string(action), func(t *testing.T) {
			m := passportCommand(action)
			if action == "close" {
				m.RemovedOn = passportClear[PassportDate]()
			} else if action == "correct" {
				m.Model = passportSet("Исправлен")
			}
			p := passportPrepare(t, m, passportBasis())
			passportValid(t, p)
			if p.ChangedCount != 1 {
				t.Fatal(p)
			}
			if action == "close" && (p.Diff[0].After[0].Status != "closed" || p.Diff[0].After[0].RemovedOn != nil) {
				t.Fatal(p)
			}
			if action == "void" && p.Diff[0].After[0].Status != "voided" {
				t.Fatal(p)
			}
		})
	}
	t.Run("linked boundary nullable atomic", func(t *testing.T) {
		b := passportBasis()
		b.Installations[0].Status = "closed"
		b.Installations[0].RemovedOn = passportPtr(PassportDate("2026-04-01"))
		b.Installations = append(b.Installations, PassportInstallation{ID: passportPtr(PassportID(22)), ComponentID: passportPtr(PassportID(12)), PredecessorID: passportPtr(PassportID(21)), Slot: "pump", Status: "current", InstalledOn: passportPtr(PassportDate("2026-04-01"))})
		m := passportCommand("correct")
		m.RemovedOn = passportClear[PassportDate]()
		if !passportHasIssue(passportPrepare(t, m, b), "linked_boundary_required") {
			t.Fatal("individual boundary accepted")
		}
		m.RemovedOn = Field[PassportDate]{}
		m.LinkedInstallationID = passportPtr(PassportID(22))
		m.ReplacementOn = passportClear[PassportDate]()
		p := passportPrepare(t, m, b)
		passportValid(t, p)
		if len(p.Diff[0].Before) != 2 || p.Diff[0].After[0].RemovedOn != nil || p.Diff[0].After[1].InstalledOn != nil || b.Installations[0].RemovedOn == nil {
			t.Fatal(p)
		}
	})
}

func TestEquipmentPassportValidation(t *testing.T) {
	tests := []struct {
		name, code string
		change     func(*PassportMutation)
	}{
		{"foreign clear", "field_not_allowed", func(m *PassportMutation) { m.MotorRatedPowerKW = passportClear[PassportDecimal]() }},
		{"future", "future_date", func(m *PassportMutation) { m.InstalledOn = passportSet(PassportDate("2026-10-25")) }},
		{"bad calendar", "invalid_date", func(m *PassportMutation) { m.InstalledOn = passportSet(PassportDate("2026-02-29")) }},
		{"stage zero", "invalid_decimal", func(m *PassportMutation) { m.PumpStageCount = passportSet(int64(0)) }},
		{"decimal ceiling", "invalid_decimal", func(m *PassportMutation) { m.PumpNominalHeadM = passportSet(PassportDecimal("1000000001")) }},
		{"decimal scale", "invalid_decimal", func(m *PassportMutation) { m.PumpNominalHeadM = passportSet(PassportDecimal("0.0000001")) }},
		{"wrong installation", "installation_mismatch", func(m *PassportMutation) { m.InstallationID = passportPtr(PassportID(99)) }},
		{"revision", "revision_mismatch", func(m *PassportMutation) { m.ExpectedPassportRevision = 9 }},
		{"interval", "interval_overlap", func(m *PassportMutation) { m.Action = "close"; m.RemovedOn = passportSet(PassportDate("2026-01-01")) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := passportCommand("update")
			tt.change(&m)
			if p := passportPrepare(t, m, passportBasis()); !passportHasIssue(p, tt.code) {
				t.Fatalf("want %s got %+v", tt.code, p)
			}
		})
	}
	m := passportCommand("update")
	n := m
	n.RowKey = "r2"
	p, e := PreparePassportChanges(PassportChangeSet{SchemaVersion: "equipment-passport-v1", Rows: []PassportMutation{m, n}}, []PassportBasis{passportBasis()}, "2026-10-24")
	if e != nil || !passportHasIssue(p, "duplicate_slot") {
		t.Fatal(p, e)
	}
}

func TestEquipmentPassportDuplicateSerialAcrossCommands(t *testing.T) {
	first := passportCommand("register")
	first.Model = passportSet("Насос")
	first.SerialNumber = passportSet("shared")
	second := first
	second.RowKey = "r2"
	second.WellID = 2
	b1 := passportBasis()
	b1.Installations = nil
	b2 := b1
	b2.Well.ID = 2
	p, err := PreparePassportChanges(PassportChangeSet{SchemaVersion: PassportSchemaVersion, Rows: []PassportMutation{second, first}}, []PassportBasis{b1, b2}, "2026-10-24")
	if err != nil {
		t.Fatal(err)
	}
	passportValid(t, p)
	if p.ChangedCount != 2 || p.ChangeSet.Rows[0].WellID != 1 || p.Diff[0].WellID != 1 {
		t.Fatal(p)
	}
	warnings := 0
	for _, issue := range p.Issues {
		if issue.Code == "duplicate_serial" && issue.Severity == "warning" {
			warnings++
		}
	}
	if warnings != 2 {
		t.Fatalf("want both command rows flagged, got %d: %+v", warnings, p.Issues)
	}
}

func TestEquipmentPassportActionRestrictions(t *testing.T) {
	for _, tt := range []struct {
		name, code string
		action     PassportAction
		change     func(*PassportMutation)
	}{
		{"register occupied", "installation_mismatch", "register", func(m *PassportMutation) { m.Model = passportSet("Насос") }},
		{"replace missing boundary", "required", "replace", func(m *PassportMutation) { m.Model = passportSet("Новый") }},
		{"close attributes", "field_not_allowed", "close", func(m *PassportMutation) { m.RemovedOn = passportClear[PassportDate](); m.Model = passportSet("other") }},
		{"void dates", "field_not_allowed", "void", func(m *PassportMutation) { m.InstalledOn = passportClear[PassportDate]() }},
		{"void reason", "required", "void", func(m *PassportMutation) { m.ChangeReason = "" }},
		{"correct reason even noop", "required", "correct", func(m *PassportMutation) { m.ChangeReason = "" }},
		{"update removed", "field_not_allowed", "update", func(m *PassportMutation) { m.RemovedOn = passportClear[PassportDate]() }},
		{"replace identity required", "required", "replace", func(m *PassportMutation) { m.ReplacementOn = passportClear[PassportDate]() }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := passportCommand(tt.action)
			tt.change(&m)
			if p := passportPrepare(t, m, passportBasis()); !passportHasIssue(p, tt.code) {
				t.Fatalf("want %s: %+v", tt.code, p)
			}
		})
	}
	t.Run("closed correction retains original assertion", func(t *testing.T) {
		b := passportBasis()
		b.Installations[0].Status = "closed"
		m := passportCommand("correct")
		m.Model = passportSet("Исправлен")
		p := passportPrepare(t, m, b)
		passportValid(t, p)
		if *p.Diff[0].Before[0].Attributes.Model != "Старый" || *p.Diff[0].After[0].Attributes.Model != "Исправлен" || p.Diff[0].After[0].Status != "closed" {
			t.Fatal(p)
		}
		*p.Diff[0].After[0].Attributes.Model = "Позднее"
		if *b.Installations[0].Attributes.Model != "Старый" || *p.Diff[0].Before[0].Attributes.Model != "Старый" {
			t.Fatal("original assertion mutated")
		}
	})
	t.Run("closed update forbidden", func(t *testing.T) {
		b := passportBasis()
		b.Installations[0].Status = "closed"
		if !passportHasIssue(passportPrepare(t, passportCommand("update"), b), "field_not_allowed") {
			t.Fatal("closed update accepted")
		}
	})
	t.Run("frequency bounds and exact small decimal", func(t *testing.T) {
		b := passportBasis()
		b.Installations = nil
		m := passportCommand("register")
		m.Slot = "control_station"
		m.Model = passportSet("СУ")
		m.ControlOutputFrequencyMinHz = passportSet(PassportDecimal("0"))
		m.ControlOutputFrequencyMaxHz = passportSet(PassportDecimal("0.000001"))
		p := passportPrepare(t, m, b)
		passportValid(t, p)
		if *p.Diff[0].After[0].Attributes.ControlOutputFrequencyMinHz != "0" || *p.Diff[0].After[0].Attributes.ControlOutputFrequencyMaxHz != "0.000001" {
			t.Fatal(p)
		}
		m.ControlOutputFrequencyMinHz = passportSet(PassportDecimal("1"))
		if !passportHasIssue(passportPrepare(t, m, b), "invalid_decimal") {
			t.Fatal("inverted bounds accepted")
		}
	})
	t.Run("linked known boundary interval", func(t *testing.T) {
		b := passportBasis()
		b.Installations[0].Status = "closed"
		b.Installations[0].RemovedOn = passportPtr(PassportDate("2026-04-01"))
		b.Installations = append(b.Installations, PassportInstallation{ID: passportPtr(PassportID(22)), ComponentID: passportPtr(PassportID(12)), PredecessorID: passportPtr(PassportID(21)), Slot: "pump", Status: "closed", InstalledOn: passportPtr(PassportDate("2026-04-01")), RemovedOn: passportPtr(PassportDate("2026-06-01"))})
		m := passportCommand("correct")
		m.LinkedInstallationID = passportPtr(PassportID(22))
		m.ReplacementOn = passportSet(PassportDate("2026-05-01"))
		p := passportPrepare(t, m, b)
		passportValid(t, p)
		if *p.Diff[0].After[0].RemovedOn != "2026-05-01" || *p.Diff[0].After[1].InstalledOn != "2026-05-01" {
			t.Fatal(p)
		}
		m.ReplacementOn = passportSet(PassportDate("2026-06-01"))
		if !passportHasIssue(passportPrepare(t, m, b), "interval_overlap") {
			t.Fatal("zero interval accepted")
		}
	})
}

func TestEquipmentPassportDecimalUpperBoundary(t *testing.T) {
	m := passportCommand("update")
	m.PumpNominalHeadM = passportSet(PassportDecimal("1000000000"))
	p := passportPrepare(t, m, passportBasis())
	passportValid(t, p)
	if p.ChangedCount != 1 || *p.Diff[0].After[0].Attributes.PumpNominalHeadM != "1000000000" {
		t.Fatalf("exact upper bound lost: %+v", p)
	}
	m.PumpNominalHeadM = passportSet(PassportDecimal("999999999.123456"))
	p = passportPrepare(t, m, passportBasis())
	passportValid(t, p)
	if *p.Diff[0].After[0].Attributes.PumpNominalHeadM != "999999999.123456" {
		t.Fatalf("precision15 scale6 lost: %+v", p)
	}
	m.PumpNominalHeadM = passportSet(PassportDecimal("1000000000.000001"))
	if p = passportPrepare(t, m, passportBasis()); !passportHasIssue(p, "invalid_decimal") {
		t.Fatal("above upper bound accepted")
	}
}
