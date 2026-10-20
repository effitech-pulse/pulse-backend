// Package fileinput читает ограниченный файл телеметрии, сохраняя строковые ID.
package fileinput

import (
	"archive/zip"
	"bytes"
	"context"
	_ "embed"
	"encoding/csv"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type Reader struct{}

func invalid(message string) error { return domain.NewError(domain.Invalid, message) }
func bounded(ctx context.Context, r io.Reader, max int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if int64(len(raw)) > max {
		return nil, invalid("raw_limit")
	}
	return raw, nil
}
func (Reader) Read(ctx context.Context, raw io.Reader, spec domain.FileParseSpec, l domain.FileInputLimits) (domain.FileInputRows, error) {
	if !l.Valid() || spec.HeaderRow < 1 || spec.HeaderRow > l.HeaderRow {
		return domain.FileInputRows{}, invalid("invalid_reader_options")
	}
	data, err := bounded(ctx, raw, l.RawBytes)
	if err != nil {
		return domain.FileInputRows{}, err
	}
	if spec.Format == "xlsx" {
		return readXLSX(ctx, data, spec, l)
	}
	if spec.Format != "csv" || len(spec.Delimiter) != 1 || !strings.ContainsAny(spec.Delimiter, ",;\t") {
		return domain.FileInputRows{}, invalid("unsupported_file_format")
	}
	if !utf8.Valid(data) {
		return domain.FileInputRows{}, invalid("csv_requires_utf8")
	}
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	r.Comma = rune(spec.Delimiter[0])
	r.FieldsPerRecord = -1
	out := domain.FileInputRows{}
	for n := 1; ; n++ {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		row, e := r.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return out, invalid("invalid_csv")
		}
		if err = checkRow(row, l); err != nil {
			return out, err
		}
		if n < spec.HeaderRow {
			continue
		}
		if n == spec.HeaderRow {
			out.Columns = row
			if err = checkHeader(row); err != nil {
				return out, err
			}
			continue
		}
		if len(row) != len(out.Columns) {
			return out, invalid("inconsistent_columns")
		}
		if len(out.Rows) >= l.Rows {
			return out, invalid("rows_limit")
		}
		out.Rows = append(out.Rows, row)
	}
	if len(out.Columns) == 0 || len(out.Rows) == 0 {
		return out, invalid("empty_file")
	}
	return out, nil
}
func checkRow(row []string, l domain.FileInputLimits) error {
	if len(row) > l.Columns {
		return invalid("columns_limit")
	}
	for _, v := range row {
		if len(v) > l.FieldBytes {
			return invalid("field_limit")
		}
	}
	return nil
}
func checkHeader(row []string) error {
	seen := map[string]bool{}
	for _, v := range row {
		if v == "" || seen[v] {
			return invalid("duplicate_or_empty_header")
		}
		seen[v] = true
	}
	return nil
}
func readXLSX(ctx context.Context, data []byte, spec domain.FileParseSpec, l domain.FileInputLimits) (out domain.FileInputRows, err error) {
	if spec.Sheet == "" {
		return out, invalid("explicit_sheet_required")
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return out, invalid("invalid_xlsx")
	}
	if len(z.File) > l.Entries {
		return out, invalid("zip_entries_limit")
	}
	var expanded int64
	seen := map[string]bool{}
	for _, entry := range z.File {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		if seen[entry.Name] || strings.Contains(entry.Name, "..") || strings.HasPrefix(entry.Name, "/") {
			return out, invalid("invalid_zip_entry")
		}
		seen[entry.Name] = true
		r, e := entry.Open()
		if e != nil {
			return out, invalid("invalid_zip_entry")
		}
		n, e := io.Copy(io.Discard, io.LimitReader(r, l.EntryBytes+1))
		_ = r.Close()
		if e != nil {
			return out, invalid("invalid_zip_entry")
		}
		if n > l.EntryBytes {
			return out, invalid("zip_entry_limit")
		}
		expanded += n
		if expanded > l.ExpandedBytes || expanded > int64(len(data))*l.ExpansionRatio {
			return out, invalid("zip_expansion_limit")
		}
	}
	temp, err := os.MkdirTemp("", "pulse-fileinput-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(temp)
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{RawCellValue: true, UnzipSizeLimit: l.ExpandedBytes, UnzipXMLSizeLimit: min(l.TempXMLBytes, l.ExpandedBytes), TmpDir: temp})
	if err != nil {
		return out, invalid("invalid_xlsx")
	}
	defer f.Close()
	props, err := f.GetWorkbookProps()
	if err != nil {
		return out, invalid("invalid_xlsx")
	}
	if props.Date1904 != nil {
		out.Date1904 = *props.Date1904
	}
	rows, err := f.Rows(spec.Sheet)
	if err != nil {
		return out, invalid("unknown_sheet")
	}
	defer rows.Close()
	sensitive := map[int]bool{}
	for n := 1; rows.Next(); n++ {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		row, e := rows.Columns(excelize.Options{RawCellValue: true})
		if e != nil {
			return out, invalid("invalid_xlsx_row")
		}
		if err = checkRow(row, l); err != nil {
			return out, err
		}
		if n < spec.HeaderRow {
			continue
		}
		if n == spec.HeaderRow {
			out.Columns = row
			if err = checkHeader(row); err != nil {
				return out, err
			}
			for i, col := range row {
				if col == spec.TimeColumn || (spec.WellColumn != nil && col == *spec.WellColumn) {
					sensitive[i] = true
				}
				for _, m := range spec.Mappings {
					if m.Field == col {
						sensitive[i] = true
					}
				}
			}
			continue
		}
		if len(row) > len(out.Columns) {
			return out, invalid("inconsistent_columns")
		}
		for len(row) < len(out.Columns) {
			row = append(row, "")
		}
		for i := range sensitive {
			cell, _ := excelize.CoordinatesToCellName(i+1, n)
			formula, e := f.GetCellFormula(spec.Sheet, cell)
			if e != nil || formula != "" {
				return out, invalid("formula_not_allowed")
			}
		}
		if len(out.Rows) >= l.Rows {
			return out, invalid("rows_limit")
		}
		out.Rows = append(out.Rows, row)
	}
	if err = rows.Error(); err != nil {
		return out, invalid("invalid_xlsx")
	}
	if len(out.Columns) == 0 || len(out.Rows) == 0 {
		return out, invalid("empty_file")
	}
	return out, nil
}

//go:embed reader.go
var parserReaderSource []byte
var parserReaderSHA = domain.FileSHA256(parserReaderSource)

// ParserSources описывает именно bytes, встроенные в этот executable.
func (Reader) ParserSources() []domain.FileParserPart {
	return []domain.FileParserPart{{Name: "adapter/fileinput/reader.go", SHA256: parserReaderSHA}}
}
