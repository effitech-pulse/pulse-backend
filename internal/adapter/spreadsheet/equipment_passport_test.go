package spreadsheet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

const passportTestHeaders = "row_key,well_id,expected_well_revision,expected_passport_revision,slot,action,component_id,installation_id,installed_on,replacement_on,change_reason,source_reference,model,manufacturer,serial_number,pump_nominal_flow_m3_day,pump_nominal_head_m,pump_stage_count,motor_rated_power_kw,motor_rated_voltage_v,motor_rated_current_a,motor_nominal_frequency_hz,control_rated_power_kw,control_rated_voltage_v,control_rated_current_a,control_output_frequency_min_hz,control_output_frequency_max_hz,clear_fields"

func passportFixture(t testing.TB, edit func(*excelize.File)) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetSheetName("Sheet1", "meta")
	_, _ = f.NewSheet("components")
	for i, row := range [][]string{{"key", "value"}, {"template_version", "equipment-passport-v1"}, {"mode", "current_components"}, {"date_format", "YYYY-MM-DD"}} {
		for j, v := range row {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+1)
			_ = f.SetCellStr("meta", cell, v)
		}
	}
	for j, v := range strings.Split(passportTestHeaders, ",") {
		cell, _ := excelize.CoordinatesToCellName(j+1, 1)
		_ = f.SetCellStr("components", cell, v)
	}
	for cell, v := range map[string]string{"A2": "r1", "B2": "9007199254740993", "C2": "1", "D2": "0", "E2": "pump", "F2": "register", "M2": "pump", "O2": "000123", "P2": "12,340001"} {
		_ = f.SetCellStr("components", cell, v)
	}
	if edit != nil {
		edit(f)
	}
	b, e := f.WriteToBuffer()
	if e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}

func TestPassportWorkbookStrictContract(t *testing.T) {
	raw := passportFixture(t, nil)
	got, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	r := got.ChangeSet.Rows[0]
	if r.WellID != 9007199254740993 || r.SerialNumber.Value != "000123" || r.PumpNominalFlowM3Day.Value != "12.340001" || got.FileSHA256 != fmt.Sprintf("%x", sha256.Sum256(raw)) {
		t.Fatalf("lost exact values: %+v", got)
	}
	for name, edit := range map[string]func(*excelize.File){
		"missing header":   func(f *excelize.File) { _ = f.SetCellStr("components", "AB1", "") },
		"duplicate header": func(f *excelize.File) { _ = f.SetCellStr("components", "AB1", "model") },
		"unknown header":   func(f *excelize.File) { _ = f.SetCellStr("components", "AC1", "actor") },
		"numeric id":       func(f *excelize.File) { _ = f.SetCellInt("components", "B2", 9007199254740993) },
		"numeric serial":   func(f *excelize.File) { _ = f.SetCellInt("components", "O2", 123) },
		"numeric date":     func(f *excelize.File) { _ = f.SetCellInt("components", "I2", 45000) },
		"foreign field":    func(f *excelize.File) { _ = f.SetCellStr("components", "S2", "1") },
		"clear register":   func(f *excelize.File) { _ = f.SetCellStr("components", "AB2", "model") },
		"history":          func(f *excelize.File) { _ = f.SetCellStr("meta", "B3", "history") },
		"duplicate slot": func(f *excelize.File) {
			for _, cell := range []string{"A", "B", "C", "D", "E", "F", "M"} {
				v, _ := f.GetCellValue("components", cell+"2")
				if cell == "A" {
					v = "r2"
				}
				_ = f.SetCellStr("components", cell+"3", v)
			}
		},
		"row key":  func(f *excelize.File) { _ = f.SetCellStr("components", "A2", strings.Repeat("x", 65)) },
		"exponent": func(f *excelize.File) { _ = f.SetCellStr("components", "P2", "1e3") },
	} {
		t.Run(name, func(t *testing.T) {
			_, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(passportFixture(t, edit)))
			if e == nil {
				t.Fatal("unsafe workbook accepted")
			}
		})
	}
}

func TestPassportWorkbookRoundTrip(t *testing.T) {
	id, component := domain.PassportID(51), domain.PassportID(61)
	model, serial, ref := "=SUM(1)", "000001", "reference"
	decimal := domain.PassportDecimal("0.000001")
	installation := domain.PassportInstallation{ID: &id, ComponentID: &component, Slot: domain.PassportSlotPump, Status: domain.PassportStatusCurrent, Attributes: domain.PassportAttributes{Model: &model, SerialNumber: &serial, PumpNominalFlowM3Day: &decimal}}
	view := domain.PassportView{WellID: 9007199254740993, WellRevision: 1, Revision: 2, Slots: []domain.PassportInstallation{installation}}
	var b bytes.Buffer
	e := (PassportCodec{}).Encode(context.Background(), &b, domain.PassportExport{Current: []domain.PassportView{view}, ExportedAt: time.Now(), Provenance: []domain.PassportRowProvenance{{RowKey: "9007199254740993:pump", ActorID: 1, ReceiptID: 2, SourceReference: &ref}}})
	if e != nil {
		t.Fatal(e)
	}
	workbook, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(b.Bytes()))
	if e != nil {
		t.Fatal(e)
	}
	if workbook.ChangeSet.Rows[0].SourceReference != nil || workbook.ChangeSet.Rows[0].Model.Value != model {
		t.Fatal("metadata became command or text changed")
	}
	p, e := domain.PreparePassportChanges(workbook.ChangeSet, []domain.PassportBasis{{Well: domain.Well{ID: 9007199254740993, Revision: 1}, Revision: 2, Installations: []domain.PassportInstallation{installation}}}, "2026-10-24")
	if e != nil || p.ChangedCount != 0 || p.UnchangedCount != 1 {
		t.Fatalf("roundtrip: %+v %v", p, e)
	}
}

func TestPassportWorkbookCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := (PassportCodec{}).Decode(ctx, bytes.NewReader(nil)); e != context.Canceled {
		t.Fatalf("cancel: %v", e)
	}
	if e := (PassportCodec{}).Encode(ctx, &bytes.Buffer{}, domain.PassportExport{}); e != context.Canceled {
		t.Fatalf("cancel encode: %v", e)
	}
}

func TestPassportWorkbookClearFields(t *testing.T) {
	update := func(f *excelize.File) {
		for c, v := range map[string]string{"F2": "update", "G2": "11", "H2": "21", "M2": "", "O2": "", "P2": "", "AB2": "installed_on;model;pump_nominal_flow_m3_day"} {
			_ = f.SetCellStr("components", c, v)
		}
	}
	raw := passportFixture(t, update)
	got, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	r := got.ChangeSet.Rows[0]
	if !r.Model.Null || !r.InstalledOn.Null || !r.PumpNominalFlowM3Day.Null || r.Manufacturer.Present {
		t.Fatalf("clear/no-change lost: %+v", r)
	}
	for _, clear := range []string{"pump_hack", "source_reference", "replacement_on", "model;model", "motor_rated_current_a", "model;"} {
		t.Run(clear, func(t *testing.T) {
			raw := passportFixture(t, func(f *excelize.File) { update(f); _ = f.SetCellStr("components", "AB2", clear) })
			if _, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(raw)); e == nil {
				t.Fatal("invalid clear accepted")
			}
		})
	}
}

func TestPassportWorkbookPhysicalRows(t *testing.T) {
	raw := passportFixture(t, func(f *excelize.File) { _ = f.InsertRows("components", 2, 2) })
	got, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	if len(got.Rows) != 1 || got.Rows[0].Index != 4 || got.Rows[0].Sheet != "components" || got.Rows[0].RowKey != "r1" {
		t.Fatalf("source index: %+v", got.Rows)
	}
}

func TestPassportWorkbookStageUpperBoundary(t *testing.T) {
	raw := passportFixture(t, func(f *excelize.File) { _ = f.SetCellInt("components", "R2", 100000) })
	got, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	if got.ChangeSet.Rows[0].PumpStageCount.Value != 100000 {
		t.Fatal("stage boundary lost")
	}
}

// passportMaxFixture exercises 100 wells, all 300 slots, all 28 columns and
// maximum-length Unicode text fields. It is synthetic and needs no DB or files.
func passportMaxFixture(t testing.TB) []byte {
	t.Helper()
	f, e := excelize.OpenReader(bytes.NewReader(passportFixture(t, nil)))
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	headers := strings.Split(passportTestHeaders, ",")
	for i := 0; i < 300; i++ {
		slot := []string{"pump", "motor", "control_station"}[i%3]
		values := map[string]string{"row_key": fmt.Sprintf("%04d", i) + strings.Repeat("界", 60), "well_id": fmt.Sprint(9007199254740993 + int64(i/3)), "expected_well_revision": "1", "expected_passport_revision": "0", "slot": slot, "action": "register", "model": strings.Repeat("界", 200), "manufacturer": strings.Repeat("界", 200), "serial_number": fmt.Sprintf("%04d", i) + strings.Repeat("界", 116), "source_reference": strings.Repeat("界", 200), "change_reason": strings.Repeat("界", 500)}
		switch slot {
		case "pump":
			values["pump_nominal_flow_m3_day"] = "999999999.123456"
			values["pump_nominal_head_m"] = "1000000000"
			values["pump_stage_count"] = "100000"
		case "motor":
			for _, key := range []string{"motor_rated_power_kw", "motor_rated_voltage_v", "motor_rated_current_a", "motor_nominal_frequency_hz"} {
				values[key] = "999999999.123456"
			}
		case "control_station":
			for _, key := range []string{"control_rated_power_kw", "control_rated_voltage_v", "control_rated_current_a", "control_output_frequency_max_hz"} {
				values[key] = "999999999.123456"
			}
			values["control_output_frequency_min_hz"] = "0"
		}
		for j, h := range headers {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+2)
			if e = f.SetCellStr("components", cell, values[h]); e != nil {
				t.Fatal(e)
			}
		}
	}
	b, e := f.WriteToBuffer()
	if e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}

func TestPassportWorkbookBatchLimits(t *testing.T) {
	raw := passportMaxFixture(t)
	got, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	if len(got.ChangeSet.Rows) != 300 || got.ChangeSet.Rows[299].WellID != 9007199254741092 {
		t.Fatal("max batch lost data")
	}
	for name, edit := range map[string]func(*excelize.File){
		"301 rows": func(f *excelize.File) {
			for j, h := range strings.Split(passportTestHeaders, ",") {
				v := ""
				switch h {
				case "row_key":
					v = "extra"
				case "well_id":
					v = "9007199254740993"
				case "expected_well_revision":
					v = "1"
				case "expected_passport_revision":
					v = "0"
				case "slot":
					v = "pump"
				case "action":
					v = "register"
				}
				cell, _ := excelize.CoordinatesToCellName(j+1, 302)
				_ = f.SetCellStr("components", cell, v)
			}
		},
		"101 wells":      func(f *excelize.File) { _ = f.SetCellStr("components", "B301", "9007199254741093") },
		"mixed revision": func(f *excelize.File) { _ = f.SetCellStr("components", "C3", "2") },
		"duplicate row key": func(f *excelize.File) {
			v, _ := f.GetCellValue("components", "A2")
			_ = f.SetCellStr("components", "A3", v)
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, e := excelize.OpenReader(bytes.NewReader(raw))
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			edit(f)
			b, e := f.WriteToBuffer()
			if e != nil {
				t.Fatal(e)
			}
			if _, e = (PassportCodec{}).Decode(context.Background(), bytes.NewReader(b.Bytes())); e == nil {
				t.Fatal("invalid batch accepted")
			}
		})
	}
}

func TestPassportWorkbookNativeDecimal(t *testing.T) {
	raw := passportRewriteZIP(t, passportFixture(t, nil), func(p map[string][]byte) {
		p["xl/worksheets/sheet2.xml"] = bytes.Replace(p["xl/worksheets/sheet2.xml"], []byte("</sheetData>"), []byte(`<row r="3"><c r="A3" t="inlineStr"><is><t>r2</t></is></c><c r="B3" t="inlineStr"><is><t>1</t></is></c><c r="C3" t="inlineStr"><is><t>1</t></is></c><c r="D3" t="inlineStr"><is><t>0</t></is></c><c r="E3" t="inlineStr"><is><t>pump</t></is></c><c r="F3" t="inlineStr"><is><t>register</t></is></c><c r="P3"><v>999999999.123456</v></c></row></sheetData>`), 1)
	})
	got, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	if got.ChangeSet.Rows[1].PumpNominalFlowM3Day.Value != "999999999.123456" {
		t.Fatal("native decimal rounded")
	}
}

func BenchmarkPassportDecodeMax(b *testing.B) {
	raw := passportMaxFixture(b)
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(raw)); e != nil {
			b.Fatal(e)
		}
	}
}

func TestPassportWorkbookDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, e := (PassportCodec{}).Decode(ctx, bytes.NewReader(nil))
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", e)
	}
}

func TestPassportWorkbookExportTextPrefixes(t *testing.T) {
	for _, model := range []string{"=1+1", "+12", "-12", "@data"} {
		t.Run(model, func(t *testing.T) {
			id, component := domain.PassportID(1), domain.PassportID(2)
			var b bytes.Buffer
			e := (PassportCodec{}).Encode(context.Background(), &b, domain.PassportExport{Current: []domain.PassportView{{WellID: 1, Slots: []domain.PassportInstallation{{ID: &id, ComponentID: &component, Slot: domain.PassportSlotPump, Status: domain.PassportStatusCurrent, Attributes: domain.PassportAttributes{Model: &model}}}}}})
			if e != nil {
				t.Fatal(e)
			}
			got, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(b.Bytes()))
			if e != nil {
				t.Fatal(e)
			}
			if got.ChangeSet.Rows[0].Model.Value != model {
				t.Fatal("text changed")
			}
		})
	}
}

func TestPassportWorkbookReadOnlyJSONBudgets(t *testing.T) {
	id, component := domain.PassportID(11), domain.PassportID(21)
	model := "pump"
	ref := strings.Repeat("\"\\\n\t\x01", 40)
	installation := domain.PassportInstallation{ID: &id, ComponentID: &component, Slot: domain.PassportSlotPump, Status: domain.PassportStatusCurrent, Attributes: domain.PassportAttributes{Model: &model}}
	view := domain.PassportView{WellID: 1, WellRevision: 1, Revision: 2, Slots: []domain.PassportInstallation{installation}}
	in := domain.PassportExport{Current: []domain.PassportView{view}, Provenance: []domain.PassportRowProvenance{{RowKey: "1:pump", ActorID: 1, ReceiptID: 2, SourceReference: &ref}}}
	t.Run("escaped200reference roundtrip", func(t *testing.T) {
		var b bytes.Buffer
		if e := (PassportCodec{}).Encode(context.Background(), &b, in); e != nil {
			t.Fatal(e)
		}
		f, e := excelize.OpenReader(bytes.NewReader(b.Bytes()))
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		v, e := f.GetCellValue("meta", "B7")
		if e != nil {
			t.Fatal(e)
		}
		var meta map[string]json.RawMessage
		if e = json.Unmarshal([]byte(v), &meta); e != nil {
			t.Fatal(e)
		}
		if _, exists := meta["row_key"]; exists {
			t.Fatal("duplicated row key in metadata")
		}
		var actual string
		if e = json.Unmarshal(meta["source_reference"], &actual); e != nil || actual != ref {
			t.Fatalf("metadata changed: %q %v", actual, e)
		}
		got, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(b.Bytes()))
		if e != nil {
			t.Fatal(e)
		}
		if got.ChangeSet.Rows[0].SourceReference != nil {
			t.Fatal("metadata became command")
		}
		p, e := domain.PreparePassportChanges(got.ChangeSet, []domain.PassportBasis{{Well: domain.Well{ID: 1, Revision: 1}, Revision: 2, Installations: []domain.PassportInstallation{installation}}}, "2026-10-24")
		if e != nil || p.ChangedCount != 0 || p.UnchangedCount != 1 {
			t.Fatalf("roundtrip: %+v %v", p, e)
		}
	})
	t.Run("history JSON over512", func(t *testing.T) {
		var b bytes.Buffer
		e := (PassportCodec{}).Encode(context.Background(), &b, domain.PassportExport{Mode: "history", History: []domain.PassportEvent{{Actor: domain.PassportActor{Name: "actor"}, Action: domain.PassportActionUpdate, Diff: domain.PassportDiff{Before: []domain.PassportInstallation{installation}, After: []domain.PassportInstallation{installation}}}}})
		if e != nil {
			t.Fatal(e)
		}
		f, e := excelize.OpenReader(bytes.NewReader(b.Bytes()))
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		v, _ := f.GetCellValue("history", "D2")
		if len(v) <= 512 || !json.Valid([]byte(v)) {
			t.Fatal("history JSON missing")
		}
		if _, e = (PassportCodec{}).Decode(context.Background(), bytes.NewReader(b.Bytes())); e == nil {
			t.Fatal("history imported")
		}
	})
	t.Run("readonly JSON over16384 encode", func(t *testing.T) {
		huge := strings.Repeat("x", 16385)
		bad := in
		bad.Provenance = []domain.PassportRowProvenance{{RowKey: "1:pump", ActorID: 1, ReceiptID: 2, SourceReference: &huge}}
		var b bytes.Buffer
		if e := (PassportCodec{}).Encode(context.Background(), &b, bad); e == nil || b.Len() != 0 {
			t.Fatal("oversized JSON delivered")
		}
	})
	t.Run("history actor513", func(t *testing.T) {
		var b bytes.Buffer
		if e := (PassportCodec{}).Encode(context.Background(), &b, domain.PassportExport{Mode: "history", History: []domain.PassportEvent{{Actor: domain.PassportActor{Name: strings.Repeat("x", 513)}}}}); e == nil {
			t.Fatal("history gained general exemption")
		}
	})
	for _, name := range []string{"component513", "metadata key513", "ordinary metadata value513", "readonly JSON16385"} {
		t.Run(name, func(t *testing.T) {
			raw := passportFixture(t, func(f *excelize.File) {
				switch name {
				case "component513":
					_ = f.SetCellStr("components", "M2", strings.Repeat("x", 513))
				case "metadata key513":
					_ = f.SetCellStr("meta", "A5", "provenance."+strings.Repeat("x", 503))
					_ = f.SetCellStr("meta", "B5", "{}")
				case "ordinary metadata value513":
					_ = f.SetCellStr("meta", "A5", "exported_at")
					_ = f.SetCellStr("meta", "B5", strings.Repeat("x", 513))
				default:
					_ = f.SetCellStr("meta", "A5", "provenance.r1")
					_ = f.SetCellStr("meta", "B5", strings.Repeat("x", 16385))
				}
			})
			if _, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(raw)); e == nil {
				t.Fatal("cell budget exceeded")
			}
		})
	}
}

func TestPassportWorkbookIssuePhysicalRow(t *testing.T) {
	raw := passportFixture(t, func(f *excelize.File) { _ = f.InsertRows("components", 2, 2); _ = f.SetCellInt("components", "B4", 1) })
	_, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(raw))
	var feature *domain.PassportError
	if !errors.As(e, &feature) || len(feature.Issues) == 0 || !strings.Contains(feature.Issues[0].Message, "components, строка 4") {
		t.Fatalf("missing physical source in issue: %v", e)
	}
}

// passportEscapedExportFixture maximizes valid escaped text without source files:
// 300 current rows + provenance, or 1000 history events with two installations
// in each JSON side. Event-specific serials prevent shared-string deduplication.
func passportEscapedExportFixture(history bool) domain.PassportExport {
	in := domain.PassportExport{Mode: "current_components"}
	if history {
		in.Mode = "history"
	}
	count := 300
	if history {
		count = 1000
	}
	for i := 0; i < count; i++ {
		model, manufacturer := strings.Repeat("\x01", 200), strings.Repeat("\x02", 200)
		serial := fmt.Sprintf("%04d", i) + strings.Repeat("\x03", 116)
		ref := strings.Repeat("\"\\\n\t\x01", 40)
		id, component := domain.PassportID(i*2+1), domain.PassportID(i*2+2)
		slot := []domain.PassportSlot{domain.PassportSlotPump, domain.PassportSlotMotor, domain.PassportSlotControlStation}[i%3]
		install := domain.PassportInstallation{ID: &id, ComponentID: &component, Slot: slot, Status: domain.PassportStatusCurrent, Attributes: domain.PassportAttributes{Model: &model, Manufacturer: &manufacturer, SerialNumber: &serial}}
		if history {
			in.History = append(in.History, domain.PassportEvent{Actor: domain.PassportActor{Name: "synthetic actor"}, Action: domain.PassportActionUpdate, Diff: domain.PassportDiff{Before: []domain.PassportInstallation{install, install}, After: []domain.PassportInstallation{install, install}}})
		} else {
			well := domain.PassportID(i/3 + 1)
			if i%3 == 0 {
				in.Current = append(in.Current, domain.PassportView{WellID: well, WellRevision: 1, Revision: 1})
			}
			last := len(in.Current) - 1
			in.Current[last].Slots = append(in.Current[last].Slots, install)
			in.Provenance = append(in.Provenance, domain.PassportRowProvenance{RowKey: fmt.Sprintf("%d:%s", well, slot), Source: domain.PassportSourceExcel, ActorID: 1, ReceiptID: 2, SourceReference: &ref})
		}
	}
	return in
}

func TestPassportWorkbookHistoryJSONUpperBudget(t *testing.T) {
	model := strings.Repeat("x", 16385)
	var b bytes.Buffer
	e := (PassportCodec{}).Encode(context.Background(), &b, domain.PassportExport{Mode: "history", History: []domain.PassportEvent{{Diff: domain.PassportDiff{Before: []domain.PassportInstallation{{Attributes: domain.PassportAttributes{Model: &model}}}}}}})
	var feature *domain.PassportError
	if !errors.As(e, &feature) || feature.Code != "file_limit_exceeded" || b.Len() != 0 {
		t.Fatalf("oversized history JSON delivered: %v", e)
	}
}

func TestPassportWorkbookEscapedResourceFixtures(t *testing.T) {
	for _, history := range []bool{false, true} {
		in := passportEscapedExportFixture(history)
		var b bytes.Buffer
		if e := (PassportCodec{}).Encode(context.Background(), &b, in); e != nil {
			t.Fatalf("history=%t: %v", history, e)
		}
		if !history {
			got, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(b.Bytes()))
			if e != nil || len(got.Rows) != 300 {
				t.Fatalf("escaped metadata fixture: %v", e)
			}
		}
	}
}

func BenchmarkPassportEncodeEscaped(b *testing.B) {
	for _, history := range []bool{false, true} {
		name := "current300"
		if history {
			name = "history1000"
		}
		b.Run(name, func(b *testing.B) {
			in := passportEscapedExportFixture(history)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if e := (PassportCodec{}).Encode(context.Background(), io.Discard, in); e != nil {
					b.Fatal(e)
				}
			}
		})
	}
}

func BenchmarkPassportDecodeEscapedMetadata(b *testing.B) {
	in := passportEscapedExportFixture(false)
	var raw bytes.Buffer
	if e := (PassportCodec{}).Encode(context.Background(), &raw, in); e != nil {
		b.Fatal(e)
	}
	b.ReportAllocs()
	b.SetBytes(int64(raw.Len()))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(raw.Bytes())); e != nil {
			b.Fatal(e)
		}
	}
}

func TestPassportWorkbookSTXstring(t *testing.T) {
	for _, model := range []string{"_x0041_", "_x005F_", "_x005F_x000A_"} {
		t.Run("export "+model, func(t *testing.T) {
			id, component := domain.PassportID(1), domain.PassportID(2)
			serial := "000_x005F_"
			installation := domain.PassportInstallation{ID: &id, ComponentID: &component, Slot: domain.PassportSlotPump, Status: domain.PassportStatusCurrent, Attributes: domain.PassportAttributes{Model: &model, SerialNumber: &serial}}
			var b bytes.Buffer
			if e := (PassportCodec{}).Encode(context.Background(), &b, domain.PassportExport{Current: []domain.PassportView{{WellID: 9007199254740993, WellRevision: 1, Revision: 2, Slots: []domain.PassportInstallation{installation}}}}); e != nil {
				t.Fatal(e)
			}
			f, e := excelize.OpenReader(bytes.NewReader(b.Bytes()))
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			actual, e := f.GetCellValue("components", "M2", excelize.Options{RawCellValue: true})
			if e != nil || actual != model {
				t.Fatalf("spreadsheet string changed: got%q want%q err%v", actual, model, e)
			}
			got, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(b.Bytes()))
			if e != nil {
				t.Fatal(e)
			}
			r := got.ChangeSet.Rows[0]
			if r.Model.Value != model || r.SerialNumber.Value != serial || r.WellID != 9007199254740993 {
				t.Fatalf("raw decode changed: %+v", r)
			}
			p, e := domain.PreparePassportChanges(got.ChangeSet, []domain.PassportBasis{{Well: domain.Well{ID: 9007199254740993, Revision: 1}, Revision: 2, Installations: []domain.PassportInstallation{installation}}}, "2026-10-24")
			if e != nil || p.ChangedCount != 0 || p.UnchangedCount != 1 {
				t.Fatalf("not no-op: %+v %v", p, e)
			}
		})
	}
	t.Run("external shared string and500reason", func(t *testing.T) {
		reason := strings.Repeat("_x000A_", 71) + "abc"
		raw := passportFixture(t, func(f *excelize.File) {
			_ = f.SetCellStr("components", "K2", "reason-placeholder")
			_ = f.SetCellStr("components", "M2", "model-placeholder")
		})
		raw = passportRewriteZIP(t, raw, func(p map[string][]byte) {
			p["xl/sharedStrings.xml"] = bytes.ReplaceAll(p["xl/sharedStrings.xml"], []byte("<t>model-placeholder</t>"), []byte("<t>_x005F_x0041_</t>"))
			p["xl/sharedStrings.xml"] = bytes.ReplaceAll(p["xl/sharedStrings.xml"], []byte("reason-placeholder"), []byte(strings.Repeat("_x005F_x000A_", 71)+"abc"))
		})
		got, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		if got.ChangeSet.Rows[0].Model.Value != "_x0041_" || got.ChangeSet.Rows[0].ChangeReason != reason {
			t.Fatalf("external text changed: %+v", got.ChangeSet.Rows[0])
		}
	})
	t.Run("external inline string", func(t *testing.T) {
		raw := passportRewriteZIP(t, passportFixture(t, nil), func(p map[string][]byte) {
			p["xl/worksheets/sheet2.xml"] = bytes.Replace(p["xl/worksheets/sheet2.xml"], []byte("</sheetData>"), []byte(`<row r="3"><c r="A3" t="inlineStr"><is><t>r2</t></is></c><c r="B3" t="inlineStr"><is><t>1</t></is></c><c r="C3" t="inlineStr"><is><t>1</t></is></c><c r="D3" t="inlineStr"><is><t>0</t></is></c><c r="E3" t="inlineStr"><is><t>pump</t></is></c><c r="F3" t="inlineStr"><is><t>register</t></is></c><c r="M3" t="inlineStr"><is><t>_x005F_x005F_</t></is></c><c r="P3"><v>999999999.123456</v></c></row></sheetData>`), 1)
		})
		got, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		if got.ChangeSet.Rows[1].Model.Value != "_x005F_" || got.ChangeSet.Rows[1].PumpNominalFlowM3Day.Value != "999999999.123456" {
			t.Fatal("inline text/raw numeric changed")
		}
	})
}
