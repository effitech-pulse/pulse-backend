package spreadsheet

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/xuri/excelize/v2"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type DailyFlowWriter struct{}
type dailyFlowBuffer struct {
	bytes.Buffer
	ctx context.Context
}

func (b *dailyFlowBuffer) Write(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if b.Len()+len(p) > 256*1024 {
		return 0, fmt.Errorf("daily workbook size limit")
	}
	return b.Buffer.Write(p)
}
func (DailyFlowWriter) WriteDailyFlow(ctx context.Context, v domain.DailyFlowView) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(v.Rows) > 90 {
		return nil, fmt.Errorf("daily workbook row limit")
	}
	f := excelize.NewFile()
	defer f.Close()
	const sheet = "Суточный дебит"
	const source = "Источник"
	if err := f.SetSheetName("Sheet1", sheet); err != nil {
		return nil, err
	}
	if _, err := f.NewSheet(source); err != nil {
		return nil, err
	}
	headers := []string{"Дата", "Факт, м³/сут", "Ожидание, м³/сут", "Отклонение, м³/сут", "Отклонение, %", "Состояние записи", "Сравнение"}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		if err := f.SetCellStr(sheet, cell, h); err != nil {
			return nil, err
		}
	}
	format := "0.00"
	style, err := f.NewStyle(&excelize.Style{CustomNumFmt: &format})
	if err != nil {
		return nil, err
	}
	for i, r := range v.Rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := i + 2
		if err := f.SetCellStr(sheet, fmt.Sprintf("A%d", n), r.Date); err != nil {
			return nil, err
		}
		for j, value := range []*float64{r.Actual, r.Expected, r.Delta, r.DeltaPercent} {
			cell, _ := excelize.CoordinatesToCellName(j+2, n)
			if value != nil {
				if err := f.SetCellValue(sheet, cell, *value); err != nil {
					return nil, err
				}
			}
			if err := f.SetCellStyle(sheet, cell, cell, style); err != nil {
				return nil, err
			}
		}
		for j, text := range []string{r.RecordState, r.ComparisonStatus} {
			cell, _ := excelize.CoordinatesToCellName(j+6, n)
			if err := f.SetCellStr(sheet, cell, text); err != nil {
				return nil, err
			}
		}
	}
	// Ordered literal metadata preserves IDs and dates as text, without formulas.
	metadata := [][2]string{{"Источник", v.Source.Label}, {"Pulse ID", v.WellID}, {"Начало", v.StartDate}, {"Конец", v.EndDate}, {"Календарь", v.CalendarTimezone}, {"Величина", v.Quantity}, {"Единица", v.Unit}, {"Ожидание", v.Source.ExpectedDefinition}, {"Сценарий", v.Source.ScenarioCode}, {"Версия схемы", v.SchemaVersion}, {"Версия набора", v.DatasetVersion}, {"View SHA-256", v.ViewID}, {"Recipe", v.Source.RecipeVersion}, {"Manifest SHA-256", v.Source.ManifestSHA256}, {"Dataset SHA-256", v.Source.DatasetSHA256}, {"Canonicalization", "daily-flow-canonical-v1"}, {"Formula", "actual-minus-expected-v1"}, {"Доступно с", v.Available.StartDate}, {"Доступно по", v.Available.EndDate}}
	raw, err := json.Marshal(v.Coverage)
	if err != nil {
		return nil, err
	}
	metadata = append(metadata, [2]string{"Coverage", string(raw)})
	for i, pair := range metadata {
		for j, text := range pair {
			cell, _ := excelize.CoordinatesToCellName(j+1, i+1)
			if err := f.SetCellStr(source, cell, text); err != nil {
				return nil, err
			}
		}
	}
	var buf = dailyFlowBuffer{ctx: ctx}
	if err = f.Write(&buf); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
