package spreadsheet

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// PassportCodec adapts the strict equipment-passport-v1 workbook, without authority or storage.
type PassportCodec struct{}

var passportHeaders = strings.Split("row_key,well_id,expected_well_revision,expected_passport_revision,slot,action,component_id,installation_id,installed_on,replacement_on,change_reason,source_reference,model,manufacturer,serial_number,pump_nominal_flow_m3_day,pump_nominal_head_m,pump_stage_count,motor_rated_power_kw,motor_rated_voltage_v,motor_rated_current_a,motor_nominal_frequency_hz,control_rated_power_kw,control_rated_voltage_v,control_rated_current_a,control_output_frequency_min_hz,control_output_frequency_max_hz,clear_fields", ",")

func passportFailure(code string, cause error) error {
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	e := domain.NewPassportError(domain.Invalid, code, code, nil)
	if cause != nil {
		return fmt.Errorf("%w: %w", e, cause)
	}
	return e
}

func (PassportCodec) Decode(ctx context.Context, input io.Reader) (domain.PassportWorkbook, error) {
	empty := domain.PassportWorkbook{}
	if e := ctx.Err(); e != nil {
		return empty, e
	}
	raw, e := io.ReadAll(io.LimitReader(passportContextReader{ctx, input}, passportMaxBytes+1))
	if e != nil {
		return empty, e
	}
	if len(raw) > passportMaxBytes {
		return empty, passportFailure("file_limit_exceeded", nil)
	}
	sheets, e := passportPreflight(ctx, raw)
	if e != nil {
		return empty, e
	}
	// Excelize is opened only after bounded raw ZIP/XML inspection. Raw tokens remain
	// the numeric source: formatted/float cell APIs must never round identifiers or decimals.
	book, e := excelize.OpenReader(passportContextReader{ctx, bytes.NewReader(raw)}, excelize.Options{UnzipSizeLimit: passportMaxExpanded, UnzipXMLSizeLimit: passportMaxExpanded})
	if e != nil {
		return empty, passportFailure("workbook_structure", e)
	}
	defer book.Close()
	meta := map[string]string{}
	for row, cells := range sheets["meta"] {
		if row == 1 {
			if cells[1].value != "key" || cells[2].value != "value" || len(cells) > 2 {
				return empty, passportStructure("meta header")
			}
			continue
		}
		if len(cells) > 2 {
			return empty, passportStructure("meta columns")
		}
		key, value := cells[1].value, cells[2].value
		if key == "" && value == "" {
			continue
		}
		if key == "" {
			return empty, passportStructure("meta key")
		}
		if _, ok := meta[key]; ok {
			return empty, passportStructure("duplicate metadata")
		}
		meta[key] = value
		if key != "template_version" && key != "mode" && key != "date_format" && key != "exported_at" && key != "empty_slot_count" && !strings.HasPrefix(key, "provenance.") {
			return empty, passportStructure("unknown metadata")
		}
	}
	if sheets["meta"][1][1].value != "key" || meta["template_version"] != domain.PassportSchemaVersion || meta["mode"] != "current_components" || meta["date_format"] != "YYYY-MM-DD" {
		return empty, passportFailure("unsupported_template", nil)
	}
	columns := map[string]int{}
	allowed := map[string]bool{}
	for _, h := range passportHeaders {
		allowed[h] = true
	}
	for col, c := range sheets["components"][1] {
		if !allowed[c.value] || columns[c.value] != 0 {
			return empty, passportStructure("unknown or duplicate header")
		}
		columns[c.value] = col
	}
	if len(columns) != len(passportHeaders) {
		return empty, passportStructure("missing header")
	}
	indexes := []int{}
	for i := range sheets["components"] {
		if i > 1 {
			indexes = append(indexes, i)
		}
	}
	sort.Ints(indexes)
	result := domain.PassportWorkbook{ChangeSet: domain.PassportChangeSet{SchemaVersion: domain.PassportSchemaVersion, Rows: []domain.PassportMutation{}}, FileSHA256: fmt.Sprintf("%x", sha256.Sum256(raw)), Rows: []domain.PassportRowSource{}}
	keys, slots := map[string]bool{}, map[string]bool{}
	wells := map[domain.PassportID][2]domain.PassportRevision{}
	for _, index := range indexes {
		if e = ctx.Err(); e != nil {
			return empty, e
		}
		cells := sheets["components"][index]
		nonempty := false
		for col, c := range cells {
			if strings.TrimSpace(c.value) != "" {
				nonempty = true
			}
			if col > len(passportHeaders) {
				return empty, passportStructure("extra column")
			}
		}
		if !nonempty {
			continue
		}
		if len(result.ChangeSet.Rows) >= 300 {
			return empty, passportFailure("file_limit_exceeded", nil)
		}
		rowFailure := func(detail string) error {
			return domain.NewPassportError(domain.Invalid, "workbook_structure", detail, []domain.PassportIssue{{RowKey: strings.TrimSpace(cells[columns["row_key"]].value), Severity: "error", Code: "workbook_structure", Message: fmt.Sprintf("components, строка %d: %s", index, detail)}})
		}
		wire := map[string]any{}
		values := map[string]string{}
		for _, h := range passportHeaders {
			cell := cells[columns[h]]
			v := strings.TrimSpace(cell.value)
			values[h] = v
			if v == "" {
				continue
			}
			switch h {
			case "well_id", "expected_well_revision", "expected_passport_revision", "component_id", "installation_id", "serial_number", "installed_on", "replacement_on":
				if !cell.text {
					return empty, rowFailure("text cell required: " + h)
				}
			}
			switch h {
			case "clear_fields":
				continue
			case "pump_stage_count":
				if strings.ContainsAny(v, ".,eE+-") {
					return empty, rowFailure("integer required")
				}
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil || n < 1 || n > 100000 {
					return empty, rowFailure("stage count")
				}
				wire[h] = n
			default:
				if passportNumeric(h) {
					d, err := domain.ParsePassportDecimal(strings.ReplaceAll(v, ",", "."))
					if err != nil {
						return empty, rowFailure("invalid decimal")
					}
					wire[h] = string(d)
				} else {
					wire[h] = v
				}
			}
		}
		for _, h := range []string{"row_key", "well_id", "expected_well_revision", "expected_passport_revision", "slot", "action"} {
			if values[h] == "" {
				return empty, rowFailure("required: " + h)
			}
		}
		if utf8.RuneCountInString(values["row_key"]) > 64 || keys[values["row_key"]] {
			return empty, rowFailure("duplicate or invalid row key")
		}
		keys[values["row_key"]] = true
		action := values["action"]
		if action != "register" && action != "update" && action != "replace" {
			return empty, rowFailure("action")
		}
		slot := values["slot"]
		if slot != "pump" && slot != "motor" && slot != "control_station" {
			return empty, rowFailure("slot")
		}
		for _, h := range passportHeaders {
			if (values[h] != "" || strings.Contains(";"+values["clear_fields"]+";", ";"+h+";")) && passportNumeric(h) && !passportFieldForSlot(h, slot) {
				return empty, rowFailure("foreign slot field")
			}
		}
		if action == "register" {
			if values["component_id"] != "" || values["installation_id"] != "" {
				return empty, rowFailure("register IDs")
			}
		} else if values["component_id"] == "" || values["installation_id"] == "" {
			return empty, rowFailure("target IDs")
		}
		if values["replacement_on"] != "" && (action != "replace" || values["installed_on"] != "") {
			return empty, rowFailure("replacement date")
		}
		if action == "replace" && values["installed_on"] != "" {
			return empty, rowFailure("replace installed date")
		}
		if action == "replace" && values["replacement_on"] == "" {
			wire["replacement_on"] = nil
		}
		if values["clear_fields"] != "" {
			if action != "update" {
				return empty, rowFailure("clear action")
			}
			seen := map[string]bool{}
			for _, key := range strings.Split(values["clear_fields"], ";") {
				if key == "" || seen[key] || !passportClearable(key) || values[key] != "" || passportNumeric(key) && !passportFieldForSlot(key, slot) {
					return empty, rowFailure("clear fields")
				}
				seen[key] = true
				wire[key] = nil
			}
		}
		for field, max := range map[string]int{"model": 200, "manufacturer": 200, "serial_number": 120, "source_reference": 200, "change_reason": 500} {
			if utf8.RuneCountInString(values[field]) > max {
				return empty, rowFailure("field length")
			}
		}
		encoded, err := json.Marshal(wire)
		if err != nil {
			return empty, rowFailure("invalid cell value")
		}
		var mutation domain.PassportMutation
		if err = json.Unmarshal(encoded, &mutation); err != nil {
			return empty, rowFailure("invalid cell value")
		}
		key := strconv.FormatInt(int64(mutation.WellID), 10) + ":" + slot
		if slots[key] {
			return empty, rowFailure("duplicate slot")
		}
		slots[key] = true
		revisions := [2]domain.PassportRevision{mutation.ExpectedWellRevision, mutation.ExpectedPassportRevision}
		if old, ok := wells[mutation.WellID]; ok && old != revisions {
			return empty, rowFailure("mixed revisions")
		}
		wells[mutation.WellID] = revisions
		if len(wells) > 100 {
			return empty, passportFailure("file_limit_exceeded", nil)
		}
		result.ChangeSet.Rows = append(result.ChangeSet.Rows, mutation)
		result.Rows = append(result.Rows, domain.PassportRowSource{RowKey: mutation.RowKey, Sheet: "components", Index: index})
	}
	return result, nil
}
func passportNumeric(h string) bool {
	return strings.HasPrefix(h, "pump_") || strings.HasPrefix(h, "motor_") || strings.HasPrefix(h, "control_")
}
func passportFieldForSlot(h, slot string) bool {
	if slot == "control_station" {
		slot = "control"
	}
	return strings.HasPrefix(h, slot+"_")
}
func passportClearable(h string) bool {
	if h == "model" || h == "manufacturer" || h == "serial_number" || h == "installed_on" {
		return true
	}
	for _, known := range passportHeaders {
		if known == h && passportNumeric(h) {
			return true
		}
	}
	return false
}

func (PassportCodec) Encode(ctx context.Context, output io.Writer, in domain.PassportExport) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if len(in.Current) > 100 || len(in.History) > 1000 {
		return passportFailure("file_limit_exceeded", nil)
	}
	if in.Mode != "" && in.Mode != "current_components" && in.Mode != "history" {
		return passportFailure("unsupported_template", nil)
	}
	book := excelize.NewFile()
	defer book.Close()
	if e := book.SetSheetName("Sheet1", "meta"); e != nil {
		return e
	}
	mode, sheet := "current_components", "components"
	if in.Mode == "history" {
		mode, sheet = "history", "history"
	}
	if _, e := book.NewSheet(sheet); e != nil {
		return e
	}
	emittedCells := 0
	set := func(sheet string, row, col int, value string, readonlyJSON ...bool) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		limit := 512
		if len(readonlyJSON) > 0 && readonlyJSON[0] {
			limit = passportMaxJSONChars
		}
		emittedCells++
		if emittedCells > passportMaxCells {
			return passportFailure("file_limit_exceeded", nil)
		}
		if utf8.RuneCountInString(value) > limit {
			return passportFailure("file_limit_exceeded", nil)
		}
		cell, e := excelize.CoordinatesToCellName(col, row)
		if e != nil {
			return e
		}
		encoded := passportEscapeText(value)
		// Excelize silently truncates values above its UTF-16 cell limit. Check the
		// encoded representation before passing it to the shared-string writer.
		if len(utf16.Encode([]rune(encoded))) > excelize.TotalCellChars {
			return passportFailure("file_limit_exceeded", nil)
		}
		return book.SetCellStr(sheet, cell, encoded)
	}
	meta := [][]string{{"key", "value"}, {"template_version", domain.PassportSchemaVersion}, {"mode", mode}, {"date_format", "YYYY-MM-DD"}}
	if !in.Template {
		meta = append(meta, []string{"exported_at", in.ExportedAt.UTC().Format(time.RFC3339Nano)}, []string{"empty_slot_count", strconv.Itoa(in.EmptySlotCount)})
		for _, p := range in.Provenance {
			v, e := json.Marshal(struct {
				Source          domain.PassportSource `json:"source"`
				ActorID         domain.PassportID     `json:"actor_id"`
				RecordedAt      time.Time             `json:"recorded_at"`
				ReceiptID       domain.PassportID     `json:"receipt_id"`
				SourceReference *string               `json:"source_reference"`
			}{p.Source, p.ActorID, p.RecordedAt, p.ReceiptID, p.SourceReference})
			if e != nil {
				return e
			}
			meta = append(meta, []string{"provenance." + p.RowKey, string(v)})
		}
	}
	for i, row := range meta {
		for j, v := range row {
			if e := set("meta", i+1, j+1, v, i > 0 && j == 1 && strings.HasPrefix(row[0], "provenance.")); e != nil {
				return e
			}
		}
	}
	if mode == "history" {
		for j, h := range []string{"recorded_at", "actor", "action", "before", "after"} {
			if e := set(sheet, 1, j+1, h); e != nil {
				return e
			}
		}
		for i, event := range in.History {
			before, e := json.Marshal(event.Diff.Before)
			if e != nil {
				return e
			}
			after, e := json.Marshal(event.Diff.After)
			if e != nil {
				return e
			}
			for j, v := range []string{event.RecordedAt.UTC().Format(time.RFC3339Nano), event.Actor.Name, string(event.Action), string(before), string(after)} {
				if e = set(sheet, i+2, j+1, v, j >= 3); e != nil {
					return e
				}
			}
		}
	} else {
		for j, h := range passportHeaders {
			if e := set(sheet, 1, j+1, h); e != nil {
				return e
			}
		}
		row := 2
		for _, view := range in.Current {
			slots := map[domain.PassportSlot]domain.PassportInstallation{}
			for _, s := range view.Slots {
				if s.Status == domain.PassportStatusCurrent {
					slots[s.Slot] = s
				}
			}
			for _, slot := range []domain.PassportSlot{domain.PassportSlotPump, domain.PassportSlotMotor, domain.PassportSlotControlStation} {
				s, exists := slots[slot]
				if !exists && !in.Template {
					continue
				}
				if row > 301 {
					return passportFailure("file_limit_exceeded", nil)
				}
				values := map[string]string{"row_key": fmt.Sprintf("%d:%s", view.WellID, slot), "well_id": strconv.FormatInt(int64(view.WellID), 10), "expected_well_revision": strconv.FormatInt(int64(view.WellRevision), 10), "expected_passport_revision": strconv.FormatInt(int64(view.Revision), 10), "slot": string(slot), "action": "register"}
				if exists {
					if s.ComponentID == nil || s.ID == nil {
						return passportStructure("export missing IDs")
					}
					values["action"] = "update"
					values["component_id"] = strconv.FormatInt(int64(*s.ComponentID), 10)
					values["installation_id"] = strconv.FormatInt(int64(*s.ID), 10)
					if s.InstalledOn != nil {
						values["installed_on"] = string(*s.InstalledOn)
					}
					raw, e := json.Marshal(s.Attributes)
					if e != nil {
						return e
					}
					var attrs map[string]json.RawMessage
					if e = json.Unmarshal(raw, &attrs); e != nil {
						return e
					}
					for k, v := range attrs {
						if string(v) == "null" {
							continue
						}
						if k == "pump_stage_count" {
							values[k] = string(v)
						} else {
							var value string
							if e = json.Unmarshal(v, &value); e != nil {
								return e
							}
							values[k] = value
						}
					}
				}
				for j, h := range passportHeaders {
					if e := set(sheet, row, j+1, values[h]); e != nil {
						return e
					}
				}
				row++
			}
		}
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	buf, e := book.WriteToBuffer()
	if e != nil {
		return e
	}
	if e = ctx.Err(); e != nil {
		return e
	}
	zipOutput, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		return passportFailure("workbook_structure", err)
	}
	if len(zipOutput.File) > 64 {
		return passportFailure("file_limit_exceeded", nil)
	}
	var expanded uint64
	for _, f := range zipOutput.File {
		expanded += f.UncompressedSize64
		if expanded > passportMaxExpanded {
			return passportFailure("file_limit_exceeded", nil)
		}
	}
	if buf.Len() > passportMaxBytes {
		return passportFailure("file_limit_exceeded", nil)
	}
	_, e = io.Copy(passportWriter{ctx, output}, bytes.NewReader(buf.Bytes()))
	return e
}

type passportWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (w passportWriter) Write(p []byte) (int, error) {
	if e := w.ctx.Err(); e != nil {
		return 0, e
	}
	n, e := w.writer.Write(p)
	if ce := w.ctx.Err(); ce != nil {
		return n, ce
	}
	return n, e
}
