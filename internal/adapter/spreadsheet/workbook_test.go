package spreadsheet

import (
	"bytes"
	"math"
	"strconv"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/xuri/excelize/v2"
)

func TestPulseWellExportKeepsExactIDsAsText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		write   func([]domain.Well) ([]byte, error)
		columns int
	}{
		{"pulse", WritePulseWells, 4}, {"lifecycle", WriteLifecycleWells, 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wells := []domain.Well{{ID: 9007199254740993}, {ID: 9007199254740995}, {ID: math.MaxInt64}}
			data, err := tc.write(wells)
			if err != nil {
				t.Fatal(err)
			}
			book, err := excelize.OpenReader(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			defer book.Close()
			rows, err := book.GetRows("wells", excelize.Options{RawCellValue: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 4 || len(rows[0]) != tc.columns || rows[0][0] != "PULSE_ID" {
				t.Fatal("export headers/rows changed", rows)
			}
			for i, w := range wells {
				cell := "A" + strconv.Itoa(i+2)
				typ, err := book.GetCellType("wells", cell)
				if err != nil {
					t.Fatal(err)
				}
				if typ != excelize.CellTypeSharedString && typ != excelize.CellTypeInlineString {
					t.Errorf("%s ID must be text, got type %v", cell, typ)
				}
				value, err := book.GetCellValue("wells", cell, excelize.Options{RawCellValue: true})
				if err != nil || value != strconv.FormatInt(w.ID, 10) {
					t.Errorf("%s exact ID lost: %q %v", cell, value, err)
				}
			}
			inputs, failures, err := ReadWells(bytes.NewReader(data))
			if err != nil || len(failures) != 0 || len(inputs) != len(wells) {
				t.Fatal("export/import roundtrip", inputs, failures, err)
			}
			for i, input := range inputs {
				if input.ID == nil || *input.ID != wells[i].ID || input.NGDU == nil || *input.NGDU != 0 || input.Department == nil || *input.Department != 0 {
					t.Fatal("roundtrip ID/valid zero lost", input)
				}
			}
			if tc.name == "lifecycle" {
				typ, err := book.GetCellType("wells", "E2")
				if err != nil || typ != excelize.CellTypeBool {
					t.Fatal("ACTIVE false type changed", typ, err)
				}
				v, err := book.GetCellValue("wells", "E2", excelize.Options{RawCellValue: true})
				if err != nil || v != "0" {
					t.Fatal("ACTIVE false changed", v, err)
				}
			}
		})
	}
}
