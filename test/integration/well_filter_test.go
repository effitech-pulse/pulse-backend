package integration

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/xuri/excelize/v2"
)

func TestWellFiltersShareSelectionWithExportAndIncidentStats(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	h := app.Handler()
	for _, body := range []string{`{"ngdu":0,"department":0,"well_number":"Бета"}`, `{"ngdu":0,"department":1,"well_number":"Альфа"}`, `{"ngdu":1,"department":0,"well_number":"Альфа"}`, `{"ngdu":2147483647,"department":0,"well_number":"Большой код"}`, `{"fund_type":"presentation","ngdu":0,"department":0,"well_number":"Альфа"}`} {
		call(t, h, "POST", "/api/v1/wells", body, 201)
	}
	for _, path := range []string{"/api/v1/wells/1/incidents", "/api/v1/wells/2/incidents", "/api/v1/wells/3/incidents", "/api/v1/wells/5/incidents"} {
		call(t, h, "POST", path, `{"failure_type":"test","failure_date":"2026-01-01"}`, 201)
	}
	cases := []struct {
		query string
		ids   []int64
	}{{"fund=operational&ngdu=0&sort=name_asc", []int64{2, 1}}, {"fund=operational&ngdu=0&department=0", []int64{1}}, {"ngdu=2147483647", []int64{4}}, {"fund=operational&search=альфа", []int64{2, 3}}, {"fund=presentation", []int64{5}}, {"ngdu=-900", []int64{}}, {"ngdu=9223372036854775806", []int64{}}}
	for _, tc := range cases {
		w := authRequest(t, h, "GET", "/api/v1/wells?"+tc.query, "", nil, 200)
		var rows []domain.Well
		if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		if len(rows) != len(tc.ids) {
			t.Fatal(tc.query, rows)
		}
		for i, id := range tc.ids {
			if rows[i].ID != id {
				t.Fatal(tc.query, rows)
			}
		}
	}
	stats := call(t, h, "GET", "/api/v1/incidents/stats?fund=operational&ngdu=0&department=0", "", 200).(map[string]any)
	if stats["total"] != float64(1) {
		t.Fatal(stats)
	}
	r := httptest.NewRequest("GET", "/api/v1/wells/export?identity=pulse&fund=operational&ngdu=0&sort=name_asc", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	book, err := excelize.OpenReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	rows, err := book.GetRows(book.GetSheetName(0))
	if err != nil || len(rows) != 3 || rows[1][0] != "2" || rows[2][0] != "1" {
		t.Fatal(rows, err)
	}
	for _, query := range []string{"sort=arbitrary", "ngdu=abc", "department=9223372036854775808"} {
		call(t, h, "GET", "/api/v1/wells?"+query, "", 422)
	}
}
