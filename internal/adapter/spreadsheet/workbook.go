// Package spreadsheet преобразует XLSX в типизированные данные приложения.
package spreadsheet

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

var headerAliases = map[string]string{
	"id": "id", "pulse_id": "id", "id pulse": "id",
	"ngdu":           "ngdu",
	"нгду":           "ngdu",
	"well_number":    "well_number",
	"well":           "well_number",
	"номер скважины": "well_number",
	"armits_id":      "armits_id",
	"armits":         "armits_id",
	"unitek_id":      "unitek_id",
	"unitek":         "unitek_id",
	"cex":            "department",
	"цех":            "department",
	"department":     "department",
}

func ReadWells(input io.Reader) ([]domain.WellInput, []string, error) {
	book, err := excelize.OpenReader(input)
	if err != nil {
		return nil, nil, domain.NewError(domain.Invalid, "Не удалось прочитать файл Excel")
	}
	defer book.Close()
	sheets := book.GetSheetList()
	if len(sheets) == 0 {
		return nil, nil, domain.NewError(domain.Invalid, "Файл Excel не содержит листов")
	}
	rows, err := book.GetRows(sheets[book.GetActiveSheetIndex()], excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, nil, err
	}
	out := []domain.WellInput{}
	failures := []string{}
	if len(rows) == 0 {
		return out, failures, nil
	}
	cols := map[string]int{}
	for i, name := range rows[0] {
		if key, ok := headerAliases[strings.ToLower(strings.TrimSpace(name))]; ok {
			cols[key] = i
		}
	}
	recognized := false
	for _, key := range []string{"id", "unitek_id", "armits_id", "ngdu", "department"} {
		if _, ok := cols[key]; ok {
			recognized = true
		}
	}
	if !recognized {
		return nil, nil, domain.NewError(domain.Invalid, "В файле не найдены ожидаемые колонки (PULSE_ID, NGDU, CEX)")
	}
	for index, cells := range rows[1:] {
		empty := true
		for _, cell := range cells {
			if strings.TrimSpace(cell) != "" {
				empty = false
			}
		}
		if empty {
			continue
		}
		value := func(key string) *int64 {
			col, ok := cols[key]
			if !ok || col >= len(cells) {
				return nil
			}
			raw := strings.TrimSpace(cells[col])
			if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
				return &n
			}
			f, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f || f >= float64(math.MaxInt64) || f < float64(math.MinInt64) {
				return nil
			}
			n := int64(f)
			return &n
		}
		w := domain.WellInput{ID: value("id"), UnitekID: value("unitek_id"), ArmitsID: value("armits_id"), NGDU: value("ngdu"), Department: value("department")}
		invalidOptional := false
		for _, key := range []string{"id", "unitek_id", "armits_id"} {
			if col, ok := cols[key]; ok && col < len(cells) && strings.TrimSpace(cells[col]) != "" && value(key) == nil {
				invalidOptional = true
			}
		}
		if invalidOptional || w.NGDU == nil || w.Department == nil {
			failures = append(failures, fmt.Sprintf("строка %d: пропущены НГДУ/цех или некорректен указанный идентификатор", index+2))
			continue
		}
		if col, ok := cols["well_number"]; ok && col < len(cells) {
			name := strings.TrimSpace(cells[col])
			if name != "" {
				w.WellNumber = &name
			}
		}
		out = append(out, w)
	}
	return out, failures, nil
}

func encode(sheet string, header []string, rows [][]any) ([]byte, error) {
	book := excelize.NewFile()
	defer book.Close()
	if err := book.SetSheetName("Sheet1", sheet); err != nil {
		return nil, err
	}
	top := make([]any, len(header))
	for i, v := range header {
		top[i] = v
	}
	if err := book.SetSheetRow(sheet, "A1", &top); err != nil {
		return nil, err
	}
	for i, row := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+2)
		if err := book.SetSheetRow(sheet, cell, &row); err != nil {
			return nil, err
		}
	}
	if len(header) > 0 {
		last, err := excelize.CoordinatesToCellName(len(header), len(rows)+1)
		if err != nil {
			return nil, err
		}
		if err = book.SetSheetDimension(sheet, "A1:"+last); err != nil {
			return nil, err
		}
	}
	buf, err := book.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func nullable[T any](v *T) any {
	if v == nil {
		return nil
	}
	return *v
}

func WriteWells(wells []domain.Well) ([]byte, error) {
	rows := make([][]any, 0, len(wells))
	for _, w := range wells {
		rows = append(rows, []any{w.NGDU, nullable(w.WellNumber), nullable(w.ArmitsID), nullable(w.UnitekID), w.Department})
	}
	return encode("wells", []string{"NGDU", "WELL_NUMBER", "ARMITS_ID", "UNITEK_ID", "CEX"}, rows)
}

func WritePredictions(input []domain.Prediction) ([]byte, error) {
	keys := map[string]bool{}
	for _, p := range input {
		for key := range p.Parameters {
			keys[key] = true
		}
	}
	fields := make([]string, 0, len(keys))
	for key := range keys {
		fields = append(fields, key)
	}
	sort.Strings(fields)
	header := append([]string{"processing_date", "status"}, fields...)
	rows := make([][]any, 0, len(input))
	for _, p := range input {
		row := []any{p.ProcessingDate, p.Status}
		for _, key := range fields {
			v := p.Parameters[key]
			switch v.(type) {
			case map[string]any, []any:
				raw, err := json.Marshal(v)
				if err != nil {
					return nil, err
				}
				v = string(raw)
			}
			row = append(row, v)
		}
		rows = append(rows, row)
	}
	return encode("predictions", header, rows)
}

func WriteRuns(input []domain.RunEvent) ([]byte, error) {
	rows := make([][]any, 0, len(input))
	for _, r := range input {
		rows = append(rows, []any{r.ExecutionDate, r.DagID, r.RunID, nullable(r.WellID), nullable(r.NGDU), nullable(r.Department), r.StatusType, r.Severity, nullable(r.Source), nullable(r.Message)})
	}
	return encode("runs", []string{"execution_date", "dag_id", "run_id", "well_id", "ngdu", "department", "status_type", "severity", "source", "message"}, rows)
}

// WriteLifecycleWells выгружает реестр с точными текстовыми Pulse ID.
func WriteLifecycleWells(wells []domain.Well) ([]byte, error) {
	rows := make([][]any, 0, len(wells))
	for _, w := range wells {
		archived := ""
		if w.ArchivedAt != nil {
			archived = w.ArchivedAt.Format(time.RFC3339Nano)
		}
		rows = append(rows, []any{strconv.FormatInt(w.ID, 10), nullable(w.WellNumber), w.NGDU, w.Department, w.Active, archived, nullable(w.Responsible), w.Revision})
	}
	return encode("wells", []string{"PULSE_ID", "WELL_NUMBER", "NGDU", "CEX", "ACTIVE", "ARCHIVED_AT", "RESPONSIBLE", "REVISION"}, rows)
}

// WritePulseWells сохраняет прежний четырёхколоночный формат реестра Pulse.
func WritePulseWells(wells []domain.Well) ([]byte, error) {
	rows := make([][]any, 0, len(wells))
	for _, w := range wells {
		rows = append(rows, []any{strconv.FormatInt(w.ID, 10), nullable(w.WellNumber), w.NGDU, w.Department})
	}
	return encode("wells", []string{"PULSE_ID", "WELL_NUMBER", "NGDU", "CEX"}, rows)
}
