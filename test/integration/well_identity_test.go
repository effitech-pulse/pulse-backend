package integration

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/spreadsheet"
	"github.com/effitech-pulse/pulse-backend/internal/adapter/telemetry"
	"github.com/effitech-pulse/pulse-backend/internal/service"
	"github.com/xuri/excelize/v2"
)

func TestPulseIdentityKeepsLegacySourcesAndCanonicalImportSeparate(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	app.http.Integrations = service.NewIntegrations(app.store, telemetry.Connector{}, telemetry.Secrets{}, &fakeML{})
	h := app.Handler()
	first := call(t, h, "POST", "/api/v1/wells", `{"unitek_id":42,"armits_id":200,"ngdu":0,"department":0}`, 201).(map[string]any)
	second := call(t, h, "POST", "/api/v1/wells", `{"unitek_id":1,"armits_id":201,"ngdu":0,"department":0}`, 201).(map[string]any)
	a, b := int(first["id"].(float64)), int(second["id"].(float64))
	if a != 1 || b != 2 {
		t.Fatal(first, second)
	}
	// Внешний ID 1 принадлежит другой скважине, а не скважине Pulse 1.
	call(t, h, "POST", "/api/v1/telemetry", `{"points":[{"well_id":"42","timestamp":"2025-01-01T00:00:00Z","values":{"Ia":30}},{"well_id":"1","timestamp":"2025-01-01T00:00:00Z","values":{"Ia":77}}]}`, 200)
	read := func(id int) map[string]any {
		return call(t, h, "GET", fmt.Sprintf("/api/v1/wells/%d/telemetry?start=2024-12-31T00:00:00Z&end=2025-01-03T00:00:00Z", id), "", 200).(map[string]any)
	}
	for id, expected := range map[int]float64{a: 30, b: 77} {
		points := read(id)["points"].([]any)
		p := points[0].(map[string]any)
		if len(points) != 1 || p["well_id"] != fmt.Sprint(id) || p["values"].(map[string]any)["Ia"] != expected {
			t.Fatal(points)
		}
	}
	call(t, h, "POST", "/api/v1/telemetry/import", `{"points":[{"well_id":"1","timestamp":"2025-01-02T00:00:00Z","values":{"F_out":0,"R_izol":9999}}]}`, 200)
	bounds := call(t, h, "GET", "/api/v1/wells/1/telemetry/bounds", "", 200).(map[string]any)
	end, _ := time.Parse(time.RFC3339, bounds["end"].(string))
	if !end.Equal(time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatal(bounds)
	}
	risk := call(t, h, "GET", "/api/v1/wells/1/risk", "", 200).(map[string]any)
	if risk["app_well_id"] != float64(1) || risk["well_id"] != float64(123) {
		t.Fatal(risk)
	}
	history := call(t, h, "GET", "/api/v1/wells/1/risk/history", "", 200).(map[string]any)
	if history["well_id"] != float64(123) {
		t.Fatal("history used app ID as model ID", history)
	}
	call(t, h, "PATCH", "/api/v1/wells/1", `{"unitek_id":null,"armits_id":null}`, 200)
	points := read(a)["points"].([]any)
	if len(points) != 1 {
		t.Fatal(points)
	}
	values := points[0].(map[string]any)["values"].(map[string]any)
	if values["F_out"] != float64(0) || values["R_izol"] != nil {
		t.Fatal(values)
	}
	call(t, h, "GET", "/api/v1/wells/1/risk", "", 404)
	call(t, h, "POST", "/api/v1/telemetry/import", `{"points":[{"well_id":"9999","timestamp":"2025-01-01T00:00:00Z","values":{"F_out":50}}]}`, 404)
	third := call(t, h, "POST", "/api/v1/wells", `{"well_number":"P-3","ngdu":0,"department":0}`, 201).(map[string]any)
	if third["unitek_id"] != nil || third["armits_id"] != nil || third["id"] == nil {
		t.Fatal(third)
	}
	call(t, h, "POST", "/api/v1/wells", `{"id":99,"ngdu":0,"department":0}`, 422)
	// Повтор импорта существующего ID Pulse не создаёт скважину и не меняет связь.
	imported := call(t, h, "POST", "/api/v1/wells/bulk", `[{"id":1,"ngdu":0,"department":0}]`, 200).(map[string]any)
	if imported["created"] != float64(0) || imported["skipped"] != float64(1) {
		t.Fatal(imported)
	}
}

func TestPulseRegistryWorkbookRoundtripWithoutExternalIdentifiers(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	h := app.Handler()
	call(t, h, "POST", "/api/v1/wells", `{"ngdu":0,"department":0}`, 201)
	wells, err := app.store.Wells(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := spreadsheet.WritePulseWells(wells)
	if err != nil {
		t.Fatal(err)
	}
	book, err := excelize.OpenReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	dimension, err := book.GetSheetDimension("wells")
	if err != nil || dimension != "A1:D2" {
		t.Fatal(dimension, err)
	}
	rows, err := book.GetRows("wells")
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(rows[0]) != "[PULSE_ID WELL_NUMBER NGDU CEX]" {
		t.Fatal(rows)
	}
	input, failures, err := spreadsheet.ReadWells(bytes.NewReader(raw))
	if err != nil || len(failures) != 0 || len(input) != 1 || input[0].ID == nil || *input[0].ID != wells[0].ID || input[0].UnitekID != nil {
		t.Fatal(input, failures, err)
	}
	result, err := app.http.Catalog.ImportWells(context.Background(), input)
	if err != nil || result.Created != 0 || result.Skipped != 1 {
		t.Fatal(result, err)
	}
}

func TestIntegrationMapsArmitsOrPulseIDBeforeStoring(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	app.http.Integrations = service.NewIntegrations(app.store, telemetry.Connector{}, telemetry.Secrets{})
	h := app.Handler()
	call(t, h, "POST", "/api/v1/wells", `{"armits_id":88,"ngdu":0,"department":0}`, 201)
	for i, identity := range []string{"armits", "pulse"} {
		source := call(t, h, "POST", "/api/v1/integrations", fmt.Sprintf(`{"name":"source","kind":"json_push","well_identity":"%s","interval_seconds":5,"well_field":"well_id","time_field":"timestamp","mappings":[{"field":"f","parameter":"F_out","scale":1}]}`, identity), 200).(map[string]any)
		id := source["id"].(string)
		call(t, h, "PATCH", "/api/v1/integrations/"+id+"/status", `{"status":"running"}`, 200)
		external := 88
		if identity == "pulse" {
			external = 1
		}
		call(t, h, "POST", "/api/v1/integrations/"+id+"/ingest", fmt.Sprintf(`{"records":[{"well_id":%d,"timestamp":"2025-01-0%dT00:00:00Z","f":0}]}`, external, i+1), 200)
		call(t, h, "POST", "/api/v1/integrations/"+id+"/ingest", `{"records":[{"well_id":999,"timestamp":"2025-01-03T00:00:00Z","f":100}]}`, 422)
	}
	result := call(t, h, "GET", "/api/v1/wells/1/telemetry?start=2024-12-31T00:00:00Z&end=2025-01-04T00:00:00Z", "", 200).(map[string]any)
	points := result["points"].([]any)
	if len(points) != 2 {
		t.Fatal(result)
	}
	for _, row := range points {
		p := row.(map[string]any)
		if p["well_id"] != "1" || p["values"].(map[string]any)["F_out"] != float64(0) {
			t.Fatal(p)
		}
	}
}
