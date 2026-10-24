package domain

import (
	"fmt"
	"math/big"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var passportDecimalPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// ParsePassportDecimal канонизирует точное число без float и экспоненты.
func ParsePassportDecimal(raw string) (PassportDecimal, error) {
	if !passportDecimalPattern.MatchString(raw) {
		return "", fmt.Errorf("invalid passport decimal")
	}
	whole, frac, _ := strings.Cut(raw, ".")
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	frac = strings.TrimRight(frac, "0")
	if frac != "" {
		whole += "." + frac
	}
	return PassportDecimal(whole), nil
}

// ParsePassportDate проверяет календарную дату без timestamp/timezone conversion.
func ParsePassportDate(raw string) (PassportDate, error) {
	d, e := time.Parse("2006-01-02", raw)
	if e != nil || d.Format("2006-01-02") != raw {
		return "", fmt.Errorf("invalid passport date")
	}
	return PassportDate(raw), nil
}
func passportCopy[T any](v *T) *T {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}
func passportApply[T any](f Field[T], p **T) {
	if f.Present {
		if f.Null {
			*p = nil
		} else {
			*p = passportCopy(&f.Value)
		}
	}
}
func passportClone(v PassportInstallation) PassportInstallation {
	v.ID = passportCopy(v.ID)
	v.ComponentID = passportCopy(v.ComponentID)
	v.PredecessorID = passportCopy(v.PredecessorID)
	v.InstalledOn = passportCopy(v.InstalledOn)
	v.RemovedOn = passportCopy(v.RemovedOn)
	v.Attributes.Model = passportCopy(v.Attributes.Model)
	v.Attributes.Manufacturer = passportCopy(v.Attributes.Manufacturer)
	v.Attributes.SerialNumber = passportCopy(v.Attributes.SerialNumber)
	v.Attributes.PumpNominalFlowM3Day = passportCopy(v.Attributes.PumpNominalFlowM3Day)
	v.Attributes.PumpNominalHeadM = passportCopy(v.Attributes.PumpNominalHeadM)
	v.Attributes.PumpStageCount = passportCopy(v.Attributes.PumpStageCount)
	v.Attributes.MotorRatedPowerKW = passportCopy(v.Attributes.MotorRatedPowerKW)
	v.Attributes.MotorRatedVoltageV = passportCopy(v.Attributes.MotorRatedVoltageV)
	v.Attributes.MotorRatedCurrentA = passportCopy(v.Attributes.MotorRatedCurrentA)
	v.Attributes.MotorNominalFrequencyHz = passportCopy(v.Attributes.MotorNominalFrequencyHz)
	v.Attributes.ControlRatedPowerKW = passportCopy(v.Attributes.ControlRatedPowerKW)
	v.Attributes.ControlRatedVoltageV = passportCopy(v.Attributes.ControlRatedVoltageV)
	v.Attributes.ControlRatedCurrentA = passportCopy(v.Attributes.ControlRatedCurrentA)
	v.Attributes.ControlOutputFrequencyMinHz = passportCopy(v.Attributes.ControlOutputFrequencyMinHz)
	v.Attributes.ControlOutputFrequencyMaxHz = passportCopy(v.Attributes.ControlOutputFrequencyMaxHz)
	return v
}
func passportApplyAttributes(m PassportMutation, a *PassportAttributes) {
	passportApply(m.Model, &a.Model)
	passportApply(m.Manufacturer, &a.Manufacturer)
	passportApply(m.SerialNumber, &a.SerialNumber)
	passportApply(m.PumpNominalFlowM3Day, &a.PumpNominalFlowM3Day)
	passportApply(m.PumpNominalHeadM, &a.PumpNominalHeadM)
	passportApply(m.PumpStageCount, &a.PumpStageCount)
	passportApply(m.MotorRatedPowerKW, &a.MotorRatedPowerKW)
	passportApply(m.MotorRatedVoltageV, &a.MotorRatedVoltageV)
	passportApply(m.MotorRatedCurrentA, &a.MotorRatedCurrentA)
	passportApply(m.MotorNominalFrequencyHz, &a.MotorNominalFrequencyHz)
	passportApply(m.ControlRatedPowerKW, &a.ControlRatedPowerKW)
	passportApply(m.ControlRatedVoltageV, &a.ControlRatedVoltageV)
	passportApply(m.ControlRatedCurrentA, &a.ControlRatedCurrentA)
	passportApply(m.ControlOutputFrequencyMinHz, &a.ControlOutputFrequencyMinHz)
	passportApply(m.ControlOutputFrequencyMaxHz, &a.ControlOutputFrequencyMaxHz)
}
func passportHasAttributes(m PassportMutation) bool {
	return m.Model.Present || m.Manufacturer.Present || m.SerialNumber.Present || m.PumpNominalFlowM3Day.Present || m.PumpNominalHeadM.Present || m.PumpStageCount.Present || m.MotorRatedPowerKW.Present || m.MotorRatedVoltageV.Present || m.MotorRatedCurrentA.Present || m.MotorNominalFrequencyHz.Present || m.ControlRatedPowerKW.Present || m.ControlRatedVoltageV.Present || m.ControlRatedCurrentA.Present || m.ControlOutputFrequencyMinHz.Present || m.ControlOutputFrequencyMaxHz.Present
}
func passportIssue(row, field, severity, code string) PassportIssue {
	var f *string
	if field != "" {
		f = &field
	}
	return PassportIssue{RowKey: row, Field: f, Severity: severity, Code: code, Message: code}
}

func passportNormalize(m *PassportMutation, today PassportDate) []PassportIssue {
	issues := []PassportIssue{}
	add := func(field, code string) { issues = append(issues, passportIssue(m.RowKey, field, "error", code)) }
	m.RowKey = strings.TrimSpace(m.RowKey)
	m.ChangeReason = strings.TrimSpace(m.ChangeReason)
	if m.RowKey == "" || utf8.RuneCountInString(m.RowKey) > 64 {
		add("row_key", "required")
	}
	if m.WellID <= 0 {
		add("well_id", "invalid_id")
	}
	if m.ExpectedWellRevision < 0 || m.ExpectedPassportRevision < 0 {
		add("expected_passport_revision", "revision_mismatch")
	}
	for _, id := range []*PassportID{m.ComponentID, m.InstallationID, m.LinkedInstallationID} {
		if id != nil && *id <= 0 {
			add("installation_id", "invalid_id")
		}
	}
	if utf8.RuneCountInString(m.ChangeReason) > 500 {
		add("change_reason", "field_not_allowed")
	}
	m.SourceReference = passportCopy(m.SourceReference)
	if m.SourceReference != nil {
		*m.SourceReference = strings.TrimSpace(*m.SourceReference)
		if utf8.RuneCountInString(*m.SourceReference) > 200 {
			add("source_reference", "field_not_allowed")
		}
	}
	m.ComponentID = passportCopy(m.ComponentID)
	m.InstallationID = passportCopy(m.InstallationID)
	m.LinkedInstallationID = passportCopy(m.LinkedInstallationID)
	if m.Slot != PassportSlotPump && m.Slot != PassportSlotMotor && m.Slot != PassportSlotControlStation {
		add("slot", "field_not_allowed")
	}
	if m.Model.Present {
		if !m.Model.Null {
			m.Model.Value = strings.TrimSpace(m.Model.Value)
			if m.Model.Value == "" || utf8.RuneCountInString(m.Model.Value) > 200 {
				add("model", "required")
			}
		}
	}
	if m.Manufacturer.Present {
		if !m.Manufacturer.Null {
			m.Manufacturer.Value = strings.TrimSpace(m.Manufacturer.Value)
			if m.Manufacturer.Value == "" || utf8.RuneCountInString(m.Manufacturer.Value) > 200 {
				add("manufacturer", "required")
			}
		}
	}
	if m.SerialNumber.Present {
		if !m.SerialNumber.Null {
			m.SerialNumber.Value = strings.TrimSpace(m.SerialNumber.Value)
			if m.SerialNumber.Value == "" || utf8.RuneCountInString(m.SerialNumber.Value) > 120 {
				add("serial_number", "required")
			}
		}
	}
	if m.PumpNominalFlowM3Day.Present {
		if m.Slot != "pump" {
			add("pump_nominal_flow_m3_day", "field_not_allowed")
		}
		if !m.PumpNominalFlowM3Day.Null {
			d, e := ParsePassportDecimal(string(m.PumpNominalFlowM3Day.Value))
			if e != nil || !passportDecimalAllowed(d, false) {
				add("pump_nominal_flow_m3_day", "invalid_decimal")
			} else {
				m.PumpNominalFlowM3Day.Value = d
			}
		}
	}
	if m.PumpNominalHeadM.Present {
		if m.Slot != "pump" {
			add("pump_nominal_head_m", "field_not_allowed")
		}
		if !m.PumpNominalHeadM.Null {
			d, e := ParsePassportDecimal(string(m.PumpNominalHeadM.Value))
			if e != nil || !passportDecimalAllowed(d, false) {
				add("pump_nominal_head_m", "invalid_decimal")
			} else {
				m.PumpNominalHeadM.Value = d
			}
		}
	}
	if m.PumpStageCount.Present {
		if m.Slot != "pump" {
			add("pump_stage_count", "field_not_allowed")
		}
		if !m.PumpStageCount.Null {
			if m.PumpStageCount.Value < 1 || m.PumpStageCount.Value > 100000 {
				add("pump_stage_count", "invalid_decimal")
			}
		}
	}
	if m.MotorRatedPowerKW.Present {
		if m.Slot != "motor" {
			add("motor_rated_power_kw", "field_not_allowed")
		}
		if !m.MotorRatedPowerKW.Null {
			d, e := ParsePassportDecimal(string(m.MotorRatedPowerKW.Value))
			if e != nil || !passportDecimalAllowed(d, false) {
				add("motor_rated_power_kw", "invalid_decimal")
			} else {
				m.MotorRatedPowerKW.Value = d
			}
		}
	}
	if m.MotorRatedVoltageV.Present {
		if m.Slot != "motor" {
			add("motor_rated_voltage_v", "field_not_allowed")
		}
		if !m.MotorRatedVoltageV.Null {
			d, e := ParsePassportDecimal(string(m.MotorRatedVoltageV.Value))
			if e != nil || !passportDecimalAllowed(d, false) {
				add("motor_rated_voltage_v", "invalid_decimal")
			} else {
				m.MotorRatedVoltageV.Value = d
			}
		}
	}
	if m.MotorRatedCurrentA.Present {
		if m.Slot != "motor" {
			add("motor_rated_current_a", "field_not_allowed")
		}
		if !m.MotorRatedCurrentA.Null {
			d, e := ParsePassportDecimal(string(m.MotorRatedCurrentA.Value))
			if e != nil || !passportDecimalAllowed(d, false) {
				add("motor_rated_current_a", "invalid_decimal")
			} else {
				m.MotorRatedCurrentA.Value = d
			}
		}
	}
	if m.MotorNominalFrequencyHz.Present {
		if m.Slot != "motor" {
			add("motor_nominal_frequency_hz", "field_not_allowed")
		}
		if !m.MotorNominalFrequencyHz.Null {
			d, e := ParsePassportDecimal(string(m.MotorNominalFrequencyHz.Value))
			if e != nil || !passportDecimalAllowed(d, false) {
				add("motor_nominal_frequency_hz", "invalid_decimal")
			} else {
				m.MotorNominalFrequencyHz.Value = d
			}
		}
	}
	if m.ControlRatedPowerKW.Present {
		if m.Slot != "control_station" {
			add("control_rated_power_kw", "field_not_allowed")
		}
		if !m.ControlRatedPowerKW.Null {
			d, e := ParsePassportDecimal(string(m.ControlRatedPowerKW.Value))
			if e != nil || !passportDecimalAllowed(d, false) {
				add("control_rated_power_kw", "invalid_decimal")
			} else {
				m.ControlRatedPowerKW.Value = d
			}
		}
	}
	if m.ControlRatedVoltageV.Present {
		if m.Slot != "control_station" {
			add("control_rated_voltage_v", "field_not_allowed")
		}
		if !m.ControlRatedVoltageV.Null {
			d, e := ParsePassportDecimal(string(m.ControlRatedVoltageV.Value))
			if e != nil || !passportDecimalAllowed(d, false) {
				add("control_rated_voltage_v", "invalid_decimal")
			} else {
				m.ControlRatedVoltageV.Value = d
			}
		}
	}
	if m.ControlRatedCurrentA.Present {
		if m.Slot != "control_station" {
			add("control_rated_current_a", "field_not_allowed")
		}
		if !m.ControlRatedCurrentA.Null {
			d, e := ParsePassportDecimal(string(m.ControlRatedCurrentA.Value))
			if e != nil || !passportDecimalAllowed(d, false) {
				add("control_rated_current_a", "invalid_decimal")
			} else {
				m.ControlRatedCurrentA.Value = d
			}
		}
	}
	if m.ControlOutputFrequencyMinHz.Present {
		if m.Slot != "control_station" {
			add("control_output_frequency_min_hz", "field_not_allowed")
		}
		if !m.ControlOutputFrequencyMinHz.Null {
			d, e := ParsePassportDecimal(string(m.ControlOutputFrequencyMinHz.Value))
			if e != nil || !passportDecimalAllowed(d, true) {
				add("control_output_frequency_min_hz", "invalid_decimal")
			} else {
				m.ControlOutputFrequencyMinHz.Value = d
			}
		}
	}
	if m.ControlOutputFrequencyMaxHz.Present {
		if m.Slot != "control_station" {
			add("control_output_frequency_max_hz", "field_not_allowed")
		}
		if !m.ControlOutputFrequencyMaxHz.Null {
			d, e := ParsePassportDecimal(string(m.ControlOutputFrequencyMaxHz.Value))
			if e != nil || !passportDecimalAllowed(d, false) {
				add("control_output_frequency_max_hz", "invalid_decimal")
			} else {
				m.ControlOutputFrequencyMaxHz.Value = d
			}
		}
	}
	for _, date := range []struct {
		key   string
		value Field[PassportDate]
	}{{"installed_on", m.InstalledOn}, {"removed_on", m.RemovedOn}, {"replacement_on", m.ReplacementOn}} {
		if date.value.Present && !date.value.Null {
			d, e := ParsePassportDate(string(date.value.Value))
			if e != nil {
				add(date.key, "invalid_date")
			} else if d > today {
				add(date.key, "future_date")
			}
		}
	}
	return issues
}
func passportDecimalAllowed(d PassportDecimal, zero bool) bool {
	whole, frac, _ := strings.Cut(string(d), ".")
	precision := len(strings.TrimLeft(whole, "0")) + len(frac)
	if precision > 15 || len(frac) > 6 {
		return false
	}
	n, ok := new(big.Rat).SetString(string(d))
	if !ok {
		return false
	}
	return (n.Sign() > 0 || zero && n.Sign() == 0) && n.Cmp(big.NewRat(1000000000, 1)) <= 0
}

// PreparePassportChanges проверяет пакет и строит независимый before/after.
// Issues severity=error запрещают запись всего пакета; функция не выделяет IDs.
func PreparePassportChanges(in PassportChangeSet, bases []PassportBasis, today PassportDate) (PassportPrepared, error) {
	if _, err := ParsePassportDate(string(today)); err != nil {
		return PassportPrepared{}, fmt.Errorf("passport today: %w", err)
	}
	p := PassportPrepared{ChangeSet: PassportChangeSet{SchemaVersion: in.SchemaVersion, Rows: make([]PassportMutation, len(in.Rows))}, Diff: []PassportDiff{}, Issues: []PassportIssue{}}
	if in.SchemaVersion != PassportSchemaVersion {
		p.Issues = append(p.Issues, passportIssue("", "schema_version", "error", "unsupported_template"))
	}
	if len(in.Rows) == 0 {
		p.Issues = append(p.Issues, passportIssue("", "rows", "error", "required"))
	}
	if len(in.Rows) > 300 {
		p.Issues = append(p.Issues, passportIssue("", "rows", "error", "file_limit_exceeded"))
	}
	byWell := make(map[PassportID]PassportBasis, len(bases))
	for _, b := range bases {
		byWell[PassportID(b.Well.ID)] = b
	}
	rowKeys := make(map[string]bool)
	slots := make(map[struct {
		well PassportID
		slot PassportSlot
	}]bool)
	wells := make(map[PassportID]bool)
	for i, original := range in.Rows {
		m := original
		start := len(p.Issues)
		p.Issues = append(p.Issues, passportNormalize(&m, today)...)
		p.ChangeSet.Rows[i] = m
		add := func(field, code string) { p.Issues = append(p.Issues, passportIssue(m.RowKey, field, "error", code)) }
		if rowKeys[m.RowKey] {
			add("row_key", "duplicate_row_key")
		}
		rowKeys[m.RowKey] = true
		key := struct {
			well PassportID
			slot PassportSlot
		}{m.WellID, m.Slot}
		if slots[key] {
			add("slot", "duplicate_slot")
		}
		slots[key] = true
		wells[m.WellID] = true
		b, ok := byWell[m.WellID]
		if !ok {
			add("well_id", "invalid_id")
		} else if PassportRevision(b.Well.Revision) != m.ExpectedWellRevision || b.Revision != m.ExpectedPassportRevision {
			add("expected_passport_revision", "revision_mismatch")
		}
		if len(p.Issues) > start {
			continue
		}
		diff, issues, changed := passportTransition(m, b)
		p.Issues = append(p.Issues, issues...)
		if passportErrors(issues) {
			continue
		}
		if !changed {
			p.UnchangedCount++
			continue
		}
		p.ChangedCount++
		p.Diff = append(p.Diff, diff)
		// Serial совпадение предупреждает, но никогда не объединяет identity.
		seenSerial := false
		for _, after := range diff.After {
			if after.Status != PassportStatusVoided && after.Attributes.SerialNumber != nil {
				for _, basis := range bases {
					for _, other := range basis.Installations {
						if other.Status != PassportStatusVoided && other.Attributes.SerialNumber != nil && *other.Attributes.SerialNumber == *after.Attributes.SerialNumber && !passportSameID(after.ComponentID, other.ComponentID) {
							seenSerial = true
						}
					}
				}
			}
		}
		if seenSerial {
			p.Issues = append(p.Issues, passportIssue(m.RowKey, "serial_number", "warning", "duplicate_serial"))
		}
	}
	// Compare proposed rows too: neither provisional component exists in basis yet.
	for i, a := range p.Diff {
		for _, b := range p.Diff[i+1:] {
			duplicate := false
			for _, left := range a.After {
				for _, right := range b.After {
					if left.Status != PassportStatusVoided && right.Status != PassportStatusVoided && left.Attributes.SerialNumber != nil && right.Attributes.SerialNumber != nil && *left.Attributes.SerialNumber == *right.Attributes.SerialNumber && !passportSameID(left.ComponentID, right.ComponentID) {
						duplicate = true
					}
				}
			}
			if duplicate {
				for _, row := range []string{a.RowKey, b.RowKey} {
					if !slices.ContainsFunc(p.Issues, func(issue PassportIssue) bool { return issue.RowKey == row && issue.Code == "duplicate_serial" }) {
						p.Issues = append(p.Issues, passportIssue(row, "serial_number", "warning", "duplicate_serial"))
					}
				}
			}
		}
	}
	if len(wells) > 100 {
		p.Issues = append(p.Issues, passportIssue("", "rows", "error", "file_limit_exceeded"))
	}
	slices.SortFunc(p.ChangeSet.Rows, func(a, b PassportMutation) int {
		if a.WellID < b.WellID {
			return -1
		}
		if a.WellID > b.WellID {
			return 1
		}
		return strings.Compare(string(a.Slot), string(b.Slot))
	})
	slices.SortFunc(p.Diff, func(a, b PassportDiff) int {
		if a.WellID < b.WellID {
			return -1
		}
		if a.WellID > b.WellID {
			return 1
		}
		return strings.Compare(string(a.Slot), string(b.Slot))
	})
	return p, nil
}
func passportErrors(issues []PassportIssue) bool {
	return slices.ContainsFunc(issues, func(i PassportIssue) bool { return i.Severity == "error" })
}
func passportSameID(a, b *PassportID) bool { return a != nil && b != nil && *a == *b }
func passportSameDate(a, b *PassportDate) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func passportTransition(m PassportMutation, b PassportBasis) (PassportDiff, []PassportIssue, bool) {
	d := PassportDiff{RowKey: m.RowKey, WellID: m.WellID, Slot: m.Slot, Action: m.Action, Before: []PassportInstallation{}, After: []PassportInstallation{}}
	issues := []PassportIssue{}
	add := func(field, code string) { issues = append(issues, passportIssue(m.RowKey, field, "error", code)) }
	warn := func(field, code string) { issues = append(issues, passportIssue(m.RowKey, field, "warning", code)) }
	target := -1
	successor := -1
	current := -1
	for i, v := range b.Installations {
		if v.Slot != m.Slot {
			continue
		}
		if v.Status == PassportStatusCurrent {
			if current != -1 {
				add("slot", "installation_mismatch")
			}
			current = i
		}
		if passportSameID(v.ID, m.InstallationID) && passportSameID(v.ComponentID, m.ComponentID) {
			target = i
		}
		if passportSameID(v.PredecessorID, m.InstallationID) {
			if successor != -1 {
				add("linked_installation_id", "installation_mismatch")
			}
			successor = i
		}
	}
	if m.Action == PassportActionRegister {
		if current != -1 || m.ComponentID != nil || m.InstallationID != nil || m.LinkedInstallationID != nil {
			add("installation_id", "installation_mismatch")
		}
	} else if target < 0 {
		add("installation_id", "installation_mismatch")
	}
	if passportErrors(issues) {
		return d, issues, false
	}
	var old PassportInstallation
	if target >= 0 {
		old = passportClone(b.Installations[target])
	}
	switch m.Action {
	case PassportActionRegister:
		if m.RemovedOn.Present || m.ReplacementOn.Present {
			add("removed_on", "field_not_allowed")
		}
		next := PassportInstallation{Slot: m.Slot, Status: PassportStatusCurrent}
		passportApplyAttributes(m, &next.Attributes)
		passportApply(m.InstalledOn, &next.InstalledOn)
		if !passportIdentityKnown(next.Attributes) {
			add("model", "required")
		}
		d.After = append(d.After, next)
	case PassportActionUpdate, PassportActionCorrect:
		if m.Action == PassportActionUpdate && (old.Status != PassportStatusCurrent || m.RemovedOn.Present || m.ReplacementOn.Present || m.LinkedInstallationID != nil) {
			add("action", "field_not_allowed")
		}
		if old.Status != PassportStatusCurrent && old.Status != PassportStatusClosed {
			add("installation_id", "installation_mismatch")
		}
		next := passportClone(old)
		passportApplyAttributes(m, &next.Attributes)
		if m.Action == PassportActionCorrect && (m.LinkedInstallationID != nil || m.ReplacementOn.Present) {
			if m.LinkedInstallationID == nil || !m.ReplacementOn.Present {
				add("replacement_on", "required")
			}
			if m.InstalledOn.Present || m.RemovedOn.Present {
				add("replacement_on", "field_not_allowed")
			}
			if successor < 0 || !passportSameID(m.LinkedInstallationID, b.Installations[successor].ID) || b.Installations[successor].Status == PassportStatusVoided {
				add("linked_installation_id", "installation_mismatch")
			}
			if !passportErrors(issues) {
				other := passportClone(b.Installations[successor])
				passportApply(m.ReplacementOn, &next.RemovedOn)
				passportApply(m.ReplacementOn, &other.InstalledOn)
				d.Before = append(d.Before, passportClone(old), passportClone(b.Installations[successor]))
				d.After = append(d.After, next, other)
			}
		} else {
			passportApply(m.InstalledOn, &next.InstalledOn)
			passportApply(m.RemovedOn, &next.RemovedOn)
			if old.PredecessorID != nil && !passportSameDate(next.InstalledOn, old.InstalledOn) || successor >= 0 && !passportSameDate(next.RemovedOn, old.RemovedOn) {
				add("replacement_on", "linked_boundary_required")
			}
			if old.Status == PassportStatusCurrent && m.RemovedOn.Present {
				add("removed_on", "field_not_allowed")
			}
			d.Before = append(d.Before, passportClone(old))
			d.After = append(d.After, next)
		}
	case PassportActionReplace:
		if old.Status != PassportStatusCurrent {
			add("installation_id", "installation_mismatch")
		}
		if m.InstalledOn.Present || m.RemovedOn.Present || m.LinkedInstallationID != nil {
			add("replacement_on", "field_not_allowed")
		}
		if !m.ReplacementOn.Present {
			add("replacement_on", "required")
		}
		next := PassportInstallation{Slot: m.Slot, Status: PassportStatusCurrent, PredecessorID: passportCopy(old.ID)}
		passportApplyAttributes(m, &next.Attributes)
		passportApply(m.ReplacementOn, &next.InstalledOn)
		if !passportIdentityKnown(next.Attributes) {
			add("model", "required")
		}
		closed := passportClone(old)
		closed.Status = PassportStatusClosed
		passportApply(m.ReplacementOn, &closed.RemovedOn)
		d.Before = append(d.Before, passportClone(old))
		d.After = append(d.After, closed, next)
	case PassportActionClose:
		if old.Status != PassportStatusCurrent {
			add("installation_id", "installation_mismatch")
		}
		if !m.RemovedOn.Present {
			add("removed_on", "required")
		}
		if passportHasAttributes(m) || m.InstalledOn.Present || m.ReplacementOn.Present || m.LinkedInstallationID != nil {
			add("action", "field_not_allowed")
		}
		next := passportClone(old)
		next.Status = PassportStatusClosed
		passportApply(m.RemovedOn, &next.RemovedOn)
		d.Before = append(d.Before, passportClone(old))
		d.After = append(d.After, next)
	case PassportActionVoid:
		if old.Status != PassportStatusCurrent && old.Status != PassportStatusClosed {
			add("installation_id", "installation_mismatch")
		}
		if passportHasAttributes(m) || m.InstalledOn.Present || m.RemovedOn.Present || m.ReplacementOn.Present || m.LinkedInstallationID != nil {
			add("action", "field_not_allowed")
		}
		next := passportClone(old)
		next.Status = PassportStatusVoided
		d.Before = append(d.Before, passportClone(old))
		d.After = append(d.After, next)
	default:
		add("action", "field_not_allowed")
	}
	if passportErrors(issues) {
		return d, issues, false
	}
	changed := !reflect.DeepEqual(d.Before, d.After)
	if m.ChangeReason == "" && (m.Action != PassportActionRegister && m.Action != PassportActionUpdate || m.Action == PassportActionUpdate && changed) {
		add("change_reason", "required")
	}
	if !changed {
		return d, issues, false
	}
	for _, next := range d.After {
		a := next.Attributes
		if a.ControlOutputFrequencyMinHz != nil && a.ControlOutputFrequencyMaxHz != nil {
			lo, _ := new(big.Rat).SetString(string(*a.ControlOutputFrequencyMinHz))
			hi, _ := new(big.Rat).SetString(string(*a.ControlOutputFrequencyMaxHz))
			if lo == nil || hi == nil || lo.Cmp(hi) > 0 {
				add("control_output_frequency_min_hz", "invalid_decimal")
			}
		}
		if next.Status == PassportStatusVoided {
			continue
		}
		if next.InstalledOn != nil && next.RemovedOn != nil && *next.RemovedOn <= *next.InstalledOn {
			add("removed_on", "interval_overlap")
		}
		if next.InstalledOn == nil {
			warn("installed_on", "unknown_installation_date")
		}
		if next.Status == PassportStatusClosed && next.RemovedOn == nil {
			warn("removed_on", "unknown_removal_date")
		}
	}
	if m.ReplacementOn.Present && m.ReplacementOn.Null {
		warn("replacement_on", "unknown_replacement_date")
	}
	// Check the proposed snapshot, replacing only the diff's existing identities.
	snapshot := []PassportInstallation{}
	for _, v := range b.Installations {
		if v.Slot != m.Slot {
			continue
		}
		replaced := slices.ContainsFunc(d.Before, func(before PassportInstallation) bool { return passportSameID(before.ID, v.ID) })
		if !replaced {
			snapshot = append(snapshot, v)
		}
	}
	snapshot = append(snapshot, d.After...)
	for i, a := range snapshot {
		for _, c := range snapshot[i+1:] {
			if passportIntervalsOverlap(a, c) {
				add("installed_on", "interval_overlap")
			}
		}
	}
	return d, issues, !passportErrors(issues)
}
func passportIdentityKnown(a PassportAttributes) bool {
	return a.Model != nil && strings.TrimSpace(*a.Model) != "" || a.Manufacturer != nil && strings.TrimSpace(*a.Manufacturer) != "" || a.SerialNumber != nil && strings.TrimSpace(*a.SerialNumber) != ""
}
func passportIntervalsOverlap(a, b PassportInstallation) bool {
	if a.Status == PassportStatusVoided || b.Status == PassportStatusVoided || a.InstalledOn == nil || b.InstalledOn == nil {
		return false
	}
	endA, endB := a.RemovedOn, b.RemovedOn
	ongoing := PassportDate("9999-12-31")
	if a.Status == PassportStatusCurrent {
		endA = &ongoing
	}
	if b.Status == PassportStatusCurrent {
		endB = &ongoing
	}
	// Unknown closed endpoints provide no proof of overlap.
	return endA != nil && endB != nil && *a.InstalledOn < *endB && *b.InstalledOn < *endA
}
