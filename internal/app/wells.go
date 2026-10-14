package app

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/xuri/excelize/v2"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
)

const wellColumns = "id,unitek_id,armits_id,well_number,ngdu,department,active"

var wellFields = []string{"unitek_id", "armits_id", "well_number", "ngdu", "department", "active"}

func validateWell(raw map[string]any, patch bool) (map[string]any, error) {
	out := map[string]any{}
	for _, key := range []string{"unitek_id", "armits_id", "ngdu", "department"} {
		v, ok := raw[key]
		if !ok && patch {
			continue
		}
		n, err := intValue(v)
		if err != nil {
			return nil, validation("Некорректное поле " + key)
		}
		out[key] = n
	}
	if v, ok := raw["well_number"]; ok {
		if v != nil {
			str, ok := v.(string)
			if !ok || len([]rune(str)) > 50 {
				return nil, validation("Некорректный well_number")
			}
		}
		out["well_number"] = v
	} else if !patch {
		out["well_number"] = nil
	}
	if v, ok := raw["active"]; ok {
		b, ok := v.(bool)
		if !ok {
			return nil, validation("Некорректный active")
		}
		out["active"] = b
	} else if !patch {
		out["active"] = true
	}
	return out, nil
}
func (a *App) listWells(w http.ResponseWriter, r *http.Request) error {
	active, err := queryBool(r, "active_only", true)
	if err != nil {
		return err
	}
	sql := "SELECT " + wellColumns + " FROM well"
	if active {
		sql += " WHERE active"
	}
	rows, err := queryJSON(r.Context(), a.Pool, sql+" ORDER BY id")
	if err != nil {
		return err
	}
	writeJSON(w, 200, rows)
	return nil
}
func (a *App) getWell(w http.ResponseWriter, r *http.Request) error {
	n, err := id(r, "well_id")
	if err != nil {
		return err
	}
	row, err := single(r.Context(), a.Pool, "SELECT "+wellColumns+" FROM well WHERE id=$1", n)
	if err != nil {
		return err
	}
	writeJSON(w, 200, row)
	return nil
}
func insertWell(ctx context.Context, db DB, raw map[string]any) (map[string]any, error) {
	return mutateRow(ctx, db, "INSERT INTO well(unitek_id,armits_id,well_number,ngdu,department,active) VALUES($1,$2,$3,$4,$5,$6) RETURNING "+wellColumns, raw["unitek_id"], raw["armits_id"], raw["well_number"], raw["ngdu"], raw["department"], raw["active"])
}
func (a *App) createWell(w http.ResponseWriter, r *http.Request) error {
	var raw map[string]any
	if err := readJSON(r, &raw); err != nil {
		return err
	}
	row, err := validateWell(raw, false)
	if err != nil {
		return err
	}
	out, err := insertWell(r.Context(), a.Pool, row)
	if err != nil {
		return err
	}
	writeJSON(w, 201, out)
	return nil
}
func (a *App) patchWell(w http.ResponseWriter, r *http.Request) error {
	n, err := id(r, "well_id")
	if err != nil {
		return err
	}
	var raw map[string]any
	if err = readJSON(r, &raw); err != nil {
		return err
	}
	row, err := validateWell(raw, true)
	if err != nil {
		return err
	}
	args := []any{n}
	sets := []string{}
	for _, key := range wellFields {
		if v, ok := row[key]; ok {
			args = append(args, v)
			sets = append(sets, fmt.Sprintf("%s=$%d", key, len(args)))
		}
	}
	var out map[string]any
	if len(sets) == 0 {
		out, err = single(r.Context(), a.Pool, "SELECT "+wellColumns+" FROM well WHERE id=$1", n)
	} else {
		out, err = mutateRow(r.Context(), a.Pool, "UPDATE well SET "+strings.Join(sets, ",")+" WHERE id=$1 RETURNING "+wellColumns, args...)
	}
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}
func (a *App) deleteWell(w http.ResponseWriter, r *http.Request) error {
	n, err := id(r, "well_id")
	if err != nil {
		return err
	}
	tag, err := a.Pool.Exec(r.Context(), "DELETE FROM well WHERE id=$1", n)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return notFound("Скважина не найдена")
	}
	w.WriteHeader(204)
	return nil
}

type importResult struct {
	Created int      `json:"created"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors"`
}

func bulkInsert(ctx context.Context, tx pgx.Tx, rows []map[string]any) (importResult, error) {
	result := importResult{Errors: []string{}}
	for _, row := range rows {
		tag, err := tx.Exec(ctx, "INSERT INTO well(unitek_id,armits_id,well_number,ngdu,department,active) VALUES($1,$2,$3,$4,$5,true) ON CONFLICT DO NOTHING", row["unitek_id"], row["armits_id"], row["well_number"], row["ngdu"], row["department"])
		if err != nil {
			return result, err
		}
		if tag.RowsAffected() == 0 {
			result.Skipped++
		} else {
			result.Created++
		}
	}
	return result, nil
}
func (a *App) bulkWells(w http.ResponseWriter, r *http.Request) error {
	var raw []map[string]any
	if err := readJSON(r, &raw); err != nil {
		return err
	}
	if raw == nil {
		return validation("Ожидается массив скважин")
	}
	rows := []map[string]any{}
	for _, v := range raw {
		row, err := validateWell(v, false)
		if err != nil {
			return err
		}
		rows = append(rows, row)
	}
	tx, err := a.Pool.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	result, err := bulkInsert(r.Context(), tx, rows)
	if err != nil {
		return err
	}
	if err = tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, result)
	return nil
}

var headerAliases = map[string]string{"ngdu": "ngdu", "нгду": "ngdu", "well_number": "well_number", "well": "well_number", "номер скважины": "well_number", "armits_id": "armits_id", "armits": "armits_id", "unitek_id": "unitek_id", "unitek": "unitek_id", "cex": "department", "цех": "department", "department": "department"}

func parseImport(input io.Reader) ([]map[string]any, []string, error) {
	book, err := excelize.OpenReader(input)
	if err != nil {
		return nil, nil, validation("Не удалось прочитать файл Excel")
	}
	defer book.Close()
	sheets := book.GetSheetList()
	if len(sheets) == 0 {
		return nil, nil, validation("Файл Excel не содержит листов")
	}
	rows, err := book.GetRows(sheets[book.GetActiveSheetIndex()], excelize.Options{RawCellValue: true})
	if err != nil {
		return nil, nil, err
	}
	out := []map[string]any{}
	failures := []string{}
	if len(rows) == 0 {
		return out, failures, nil
	}
	cols := map[string]int{}
	for i, header := range rows[0] {
		if key, ok := headerAliases[strings.ToLower(strings.TrimSpace(header))]; ok {
			cols[key] = i
		}
	}
	recognized := false
	for _, k := range []string{"unitek_id", "armits_id", "ngdu", "department"} {
		if _, ok := cols[k]; ok {
			recognized = true
		}
	}
	if !recognized {
		return nil, nil, validation("В файле не найдены ожидаемые колонки (NGDU, ARMITS_ID, UNITEK_ID, CEX)")
	}
	for index, cells := range rows[1:] {
		empty := true
		for _, v := range cells {
			if strings.TrimSpace(v) != "" {
				empty = false
			}
		}
		if empty {
			continue
		}
		row := map[string]any{}
		valid := true
		for _, k := range []string{"unitek_id", "armits_id", "ngdu", "department"} {
			col, ok := cols[k]
			if !ok || col >= len(cells) {
				valid = false
				break
			}
			value := strings.TrimSpace(cells[col])
			n, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n > float64(math.MaxInt64) || n < float64(math.MinInt64) {
				valid = false
				break
			}
			row[k] = int64(n)
		}
		if !valid {
			failures = append(failures, fmt.Sprintf("строка %d: пропущены/некорректны обязательные поля (UNITEK_ID, ARMITS_ID, NGDU, CEX)", index+2))
			continue
		}
		row["well_number"] = nil
		if col, ok := cols["well_number"]; ok && col < len(cells) && strings.TrimSpace(cells[col]) != "" {
			row["well_number"] = strings.TrimSpace(cells[col])
		}
		out = append(out, row)
	}
	return out, failures, nil
}
func (a *App) importWells(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024*1024)
	if err := r.ParseMultipartForm(2 * 1024 * 1024); err != nil {
		return validation("Некорректный файл")
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		return validation("Не передан файл")
	}
	defer file.Close()
	name := strings.ToLower(header.Filename)
	if !strings.HasSuffix(name, ".xlsx") && !strings.HasSuffix(name, ".xlsm") {
		return validation("Ожидается файл Excel (.xlsx)")
	}
	rows, failures, err := parseImport(file)
	if err != nil {
		return err
	}
	tx, err := a.Pool.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	result, err := bulkInsert(r.Context(), tx, rows)
	if err != nil {
		return err
	}
	result.Errors = failures
	if err = tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, result)
	return nil
}
func writeXLSX(w http.ResponseWriter, sheet, filename string, header []string, rows [][]any) error {
	book := excelize.NewFile()
	defer book.Close()
	if err := book.SetSheetName("Sheet1", sheet); err != nil {
		return err
	}
	top := make([]any, len(header))
	for i, v := range header {
		top[i] = v
	}
	if err := book.SetSheetRow(sheet, "A1", &top); err != nil {
		return err
	}
	for i, row := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+2)
		if err := book.SetSheetRow(sheet, cell, &row); err != nil {
			return err
		}
	}
	buf, err := book.WriteToBuffer()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, err = w.Write(buf.Bytes())
	return err
}
func (a *App) exportWells(w http.ResponseWriter, r *http.Request) error {
	rows, err := queryJSON(r.Context(), a.Pool, "SELECT "+wellColumns+" FROM well ORDER BY id")
	if err != nil {
		return err
	}
	out := [][]any{}
	for _, row := range rows {
		out = append(out, []any{cellValue(row["ngdu"]), row["well_number"], cellValue(row["armits_id"]), cellValue(row["unitek_id"]), cellValue(row["department"])})
	}
	return writeXLSX(w, "wells", "wells.xlsx", []string{"NGDU", "WELL_NUMBER", "ARMITS_ID", "UNITEK_ID", "CEX"}, out)
}
