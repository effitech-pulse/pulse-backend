package spreadsheet

import (
	"bytes"
	"context"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/xuri/excelize/v2"
	"testing"
)

func TestDailyFlowWorkbookMatchesView(t *testing.T) {
	zero, pct := 0.0, -10.0
	view := domain.DailyFlowView{WellID: "9007199254740993", StartDate: "2026-10-23", EndDate: "2026-10-24", Source: domain.DailyFlowSource{Label: "=literal"}, Rows: []domain.DailyFlowRow{{Date: "2026-10-23", Actual: &zero, DeltaPercent: &pct, RecordState: "present", ComparisonStatus: "missing_expected"}, {Date: "2026-10-24", RecordState: "missing_day", ComparisonStatus: "missing_both"}}}
	raw, e := (DailyFlowWriter{}).WriteDailyFlow(context.Background(), view)
	if e != nil {
		t.Fatal(e)
	}
	f, e := excelize.OpenReader(bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	for cell, want := range map[string]string{"A2": "2026-10-23", "B2": "0.00", "C2": "", "E2": "-10.00", "A3": "2026-10-24", "B3": ""} {
		v, e := f.GetCellValue("Суточный дебит", cell)
		if e != nil || v != want {
			t.Fatalf("%s %q want %q %v", cell, v, want, e)
		}
	}
	v, _ := f.GetCellValue("Источник", "B2")
	if v != "9007199254740993" {
		t.Fatal(v)
	}
	for _, sheet := range f.GetSheetList() {
		rows, _ := f.GetRows(sheet)
		for i, row := range rows {
			for j := range row {
				cell, _ := excelize.CoordinatesToCellName(j+1, i+1)
				formula, _ := f.GetCellFormula(sheet, cell)
				if formula != "" {
					t.Fatal("formula", cell)
				}
			}
		}
	}
}
