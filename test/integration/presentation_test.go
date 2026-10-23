package integration

import (
	"context"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/migrations"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestFundFilterKeepsPresentationOutsideOperationalListsAndSummary(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	app.http.Presentation = service.NewPresentation(app.store)
	h := app.Handler()
	regular := call(t, h, "POST", "/api/v1/wells", `{"unitek_id":42,"ngdu":0,"department":0}`, 201).(map[string]any)
	simulated := call(t, h, "POST", "/api/v1/wells", `{"fund_type":"presentation","unitek_id":-900000,"armits_id":-900000,"ngdu":-900,"department":-900,"well_number":"П-001"}`, 201).(map[string]any)
	if regular["fund_type"] != "operational" || simulated["fund_type"] != "presentation" {
		t.Fatal(regular, simulated)
	}
	for fund, id := range map[string]float64{"operational": 1, "presentation": 2} {
		wells := call(t, h, "GET", "/api/v1/wells?fund="+fund, "", 200).([]any)
		if len(wells) != 1 || wells[0].(map[string]any)["id"] != id {
			t.Fatal(fund, wells)
		}
	}
	all := call(t, h, "GET", "/api/v1/wells", "", 200).([]any)
	if len(all) != 2 {
		t.Fatal(all)
	}
	call(t, h, "GET", "/api/v1/wells?fund=other", "", 422)
	call(t, h, "POST", "/api/v1/wells", `{"fund_type":"unknown","ngdu":0,"department":0}`, 422)
	empty := call(t, h, "GET", "/api/v1/presentation/summary", "", 200).(map[string]any)
	if empty["count"] != float64(1) || empty["checked"] != float64(0) || empty["alert"] != float64(0) {
		t.Fatal(empty)
	}
	// Только фактические сохранённые ответы; старое срабатывание не остаётся актуальным после новой нормы.
	for i, v := range []struct {
		well    int64
		verdict string
		date    string
	}{{1, "alert", "2025-02-01T00:00:00Z"}, {2, "alert", "2025-02-01T00:00:00Z"}, {2, "norm", "2025-02-02T00:00:00Z"}} {
		_, err := app.store.SaveManualCheck(legacyResearchFixtureContext(), domain.ManualCheck{ID: string(rune('a' + i)), WellID: v.well, RequestID: string(rune('a' + i)), Start: mustDate(t, "2025-01-01T00:00:00Z"), End: mustDate(t, "2025-02-01T00:00:00Z"), CreatedAt: mustDate(t, v.date), PointsCount: 1, Result: domain.UploadResult{Verdict: v.verdict}})
		if err != nil {
			t.Fatal(err)
		}
	}
	summary := call(t, h, "GET", "/api/v1/presentation/summary", "", 200).(map[string]any)
	if summary["count"] != float64(1) || summary["checked"] != float64(1) || summary["alert"] != float64(0) {
		t.Fatal(summary)
	}
	rows := summary["wells"].([]any)
	if rows[0].(map[string]any)["latest_check"].(map[string]any)["verdict"] != "norm" {
		t.Fatal(rows)
	}
}
func TestFundMigrationOnlyAdoptsReservedSyntheticRange(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	h := app.Handler()
	call(t, h, "POST", "/api/v1/wells", `{"unitek_id":-900000,"ngdu":-900,"department":-900}`, 201)
	call(t, h, "POST", "/api/v1/wells", `{"unitek_id":1234,"ngdu":-900,"department":-900}`, 201)
	if err := migrations.Apply(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	first, err := app.store.Well(legacyResearchFixtureContext(), 1)
	if err != nil || first.FundType != "presentation" {
		t.Fatal(first, err)
	}
	second, err := app.store.Well(legacyResearchFixtureContext(), 2)
	if err != nil || second.FundType != "operational" {
		t.Fatal(second, err)
	}
}
