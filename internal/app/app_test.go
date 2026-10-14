package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeML struct {
	gate    chan struct{}
	started chan struct{}
	once    sync.Once
}

func (f *fakeML) Snapshot(ctx context.Context, min *float64) (map[string]any, error) {
	rows := []map[string]any{{"well_id": int64(123), "unitek_id": int64(42), "oilfield": "test", "risk": .1, "alert": true, "tier": "watch", "data_ok": false, "windows_fired": int64(5), "windows_total": int64(10), "window_end": "2026-01-01T00:00:00"}}
	if min != nil && *min > .1 {
		rows = []map[string]any{}
	}
	return map[string]any{"wells": rows, "alert_threshold": .7, "high_threshold": .9}, nil
}
func (f *fakeML) Score(ctx context.Context) (map[string]any, error) {
	if f.started != nil {
		f.once.Do(func() { close(f.started) })
	}
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.Snapshot(ctx, nil)
}
func (f *fakeML) Explain(ctx context.Context, id int64, n int32) (map[string]any, error) {
	return map[string]any{"well_id": id, "contributions": []any{}}, nil
}
func (f *fakeML) History(ctx context.Context, id int64, n int32) (map[string]any, error) {
	return map[string]any{"well_id": id, "points": []any{}}, nil
}
func (f *fakeML) Metrics(ctx context.Context) (map[string]any, error) {
	return map[string]any{"models": []any{}}, nil
}
func (f *fakeML) ModelHealth(ctx context.Context, state map[string]any) (map[string]any, error) {
	return state, nil
}
func (f *fakeML) Upload(ctx context.Context, name string, r io.Reader) (map[string]any, error) {
	content, _ := io.ReadAll(r)
	return map[string]any{"filename": name, "n_windows": len(content)}, nil
}
func testConfig() Config {
	return Config{Prefix: "/api/v1", MLTimeout: time.Second, Location: time.UTC, CORS: []string{"http://localhost:3000"}, Dashboards: []Dashboard{}}
}
func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("pulse_test_%d", time.Now().UnixNano())
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close(); _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	if err = Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return pool
}
func call(t *testing.T, h http.Handler, method, path, body string, want int) any {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, w.Code, want, w.Body.String())
	}
	if want == 204 {
		return nil
	}
	var out any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON %s", w.Body.String())
	}
	return out
}
func TestAuthCORSAndValidation(t *testing.T) {
	cfg := testConfig()
	cfg.APIKey = "secret"
	a := New(cfg, nil, &fakeML{})
	h := a.Handler()
	call(t, h, "GET", "/health", "", 200)
	call(t, h, "GET", "/api/v1/parameters", "", 401)
	r := httptest.NewRequest("GET", "/api/v1/parameters", nil)
	r.Header.Set("X-API-Key", "secret")
	r.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatal(w.Code, w.Header())
	}
	r = httptest.NewRequest("GET", "/api/v1/wells/not-int", nil)
	r.Header.Set("X-API-Key", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 422 {
		t.Fatal(w.Code)
	}
}
func TestAllLegacyRoutesRegistered(t *testing.T) {
	a := New(testConfig(), nil, &fakeML{})
	mux := a.routes()
	var doc map[string]any
	if err := json.Unmarshal(openAPI, &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	for path, methods := range paths {
		for method := range methods.(map[string]any) {
			if method == "parameters" {
				continue
			}
			requestPath := path
			for _, key := range []string{"dep_id", "ngdu_id", "well_id", "incident_id", "alert_id", "unitek_id", "dashboard_id", "dag_id", "run_id"} {
				requestPath = strings.ReplaceAll(requestPath, "{"+key+"}", "1")
			}
			r := httptest.NewRequest(strings.ToUpper(method), requestPath, nil)
			_, pattern := mux.Handler(r)
			if pattern == "" {
				t.Fatalf("route missing: %s %s", method, path)
			}
		}
	}
}
func TestWellCRUDAndReference(t *testing.T) {
	pool := testDB(t)
	a := New(testConfig(), pool, &fakeML{})
	defer a.Wait()
	h := a.Handler()
	out := call(t, h, "POST", "/api/v1/wells", `{"unitek_id":42,"armits_id":21,"ngdu":1,"department":4,"well_number":"001"}`, 201).(map[string]any)
	n := int64(out["id"].(float64))
	path := fmt.Sprintf("/api/v1/wells/%d", n)
	call(t, h, "POST", "/api/v1/wells", `{"unitek_id":42,"armits_id":22,"ngdu":1,"department":4}`, 409)
	patched := call(t, h, "PATCH", path, `{"active":false,"well_number":null}`, 200).(map[string]any)
	if patched["active"] != false || patched["well_number"] != nil {
		t.Fatal(patched)
	}
	if len(call(t, h, "GET", "/api/v1/wells", "", 200).([]any)) != 0 {
		t.Fatal("inactive well listed")
	}
	call(t, h, "GET", "/api/v1/wells?active_only=false", "", 200)
	for _, ref := range []string{"ngdu", "departments"} {
		call(t, h, "POST", "/api/v1/"+ref, `{"id":4,"name":"Цех"}`, 201)
		call(t, h, "PUT", "/api/v1/"+ref+"/4", `{"name":"новое"}`, 200)
		call(t, h, "GET", "/api/v1/"+ref, "", 200)
		call(t, h, "DELETE", "/api/v1/"+ref+"/4", "", 204)
	}
	call(t, h, "DELETE", path, "", 204)
	call(t, h, "GET", path, "", 404)
	audit := call(t, h, "GET", "/api/v1/audit", "", 200).([]any)
	if len(audit) < 5 {
		t.Fatal("audit missing", audit)
	}
}
func TestIncidentsPredictionsComplicationsAndRuns(t *testing.T) {
	pool := testDB(t)
	a := New(testConfig(), pool, &fakeML{})
	defer a.Wait()
	h := a.Handler()
	well := call(t, h, "POST", "/api/v1/wells", `{"unitek_id":42,"armits_id":21,"ngdu":1,"department":4}`, 201).(map[string]any)
	n := int64(well["id"].(float64))
	base := fmt.Sprintf("/api/v1/wells/%d", n)
	incident := call(t, h, "POST", base+"/incidents", `{"failure_type":"PARAFFIN","failure_date":"2026-01-02","days_before":14}`, 201).(map[string]any)
	call(t, h, "POST", base+"/incidents", `{"failure_type":"X","failure_date":"bad"}`, 422)
	stats := call(t, h, "GET", "/api/v1/incidents/stats", "", 200).(map[string]any)
	if stats["total"].(float64) != 1 {
		t.Fatal(stats)
	}
	call(t, h, "GET", base+"/incidents", "", 200)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO prediction(well_id,status,parameters,processing_date) VALUES($1,'normal','{"pressure":0,"extra":{"x":1}}','2026-01-01');`, n)
	if err != nil {
		t.Fatal(err)
	}
	call(t, h, "GET", base+"/predictions", "", 200)
	_, err = pool.Exec(ctx, `INSERT INTO complication_reference(well_id,code,name,category) VALUES($1,'a','a','slow'),($1,'b','b','mechanical')`, n)
	if err != nil {
		t.Fatal(err)
	}
	classification := call(t, h, "GET", base+"/classification", "", 200).(map[string]any)
	if classification["classification"] != "комбинированный" {
		t.Fatal(classification)
	}
	call(t, h, "GET", base+"/complications", "", 200)
	_, err = pool.Exec(ctx, `INSERT INTO run_archive(dag_id,run_id,execution_date,well_id,status_type,severity) VALUES('dag','run',now(),$1,'success','info')`, n)
	if err != nil {
		t.Fatal(err)
	}
	call(t, h, "GET", "/api/v1/runs?dag_id=dag&well_id="+fmt.Sprint(n), "", 200)
	call(t, h, "GET", "/api/v1/runs/dag/run", "", 200)
	for _, path := range []string{base + "/predictions/export", "/api/v1/runs/export", "/api/v1/wells/export"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		book, err := excelize.OpenReader(bytes.NewReader(w.Body.Bytes()))
		if err != nil {
			t.Fatal(path, err)
		}
		book.Close()
	}
	call(t, h, "DELETE", fmt.Sprintf("/api/v1/incidents/%v", incident["id"]), "", 204)
}
func TestBulkImportAndRollback(t *testing.T) {
	pool := testDB(t)
	a := New(testConfig(), pool, &fakeML{})
	defer a.Wait()
	h := a.Handler()
	result := call(t, h, "POST", "/api/v1/wells/bulk", `[{"unitek_id":42,"armits_id":21,"ngdu":1,"department":4},{"unitek_id":42,"armits_id":22,"ngdu":1,"department":4}]`, 200).(map[string]any)
	if result["created"].(float64) != 1 || result["skipped"].(float64) != 1 {
		t.Fatal(result)
	}
	book := excelize.NewFile()
	rows := [][]any{{"UNITEK_ID", "ARMITS_ID", "NGDU", "CEX", "WELL_NUMBER"}, {43, 23, 1, 4, "002"}, {"invalid", 24, 1, 4, "003"}}
	for i, row := range rows {
		_ = book.SetSheetRow("Sheet1", fmt.Sprintf("A%d", i+1), &row)
	}
	buf, _ := book.WriteToBuffer()
	book.Close()
	body := &bytes.Buffer{}
	multi := multipart.NewWriter(body)
	file, _ := multi.CreateFormFile("file", "wells.xlsx")
	_, _ = file.Write(buf.Bytes())
	multi.Close()
	r := httptest.NewRequest("POST", "/api/v1/wells/import", body)
	r.Header.Set("Content-Type", multi.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var imported importResult
	_ = json.Unmarshal(w.Body.Bytes(), &imported)
	if imported.Created != 1 || len(imported.Errors) != 1 {
		t.Fatal(imported)
	}
}
func TestRunLockAndFeedbackCarry(t *testing.T) {
	pool := testDB(t)
	fake := &fakeML{gate: make(chan struct{}), started: make(chan struct{})}
	a := New(testConfig(), pool, fake)
	other := New(testConfig(), pool, fake)
	ctx := context.Background()
	started, err := a.StartRun(ctx, "manual")
	if err != nil || !started {
		t.Fatal(started, err)
	}
	select {
	case <-fake.started:
	case <-time.After(time.Second):
		t.Fatal("run did not start")
	}
	if running, err := other.running(ctx); err != nil || !running {
		t.Fatal(running, err)
	}
	if started, err = other.StartRun(ctx, "manual"); err != nil || started {
		t.Fatal("duplicate run allowed", started, err)
	}
	close(fake.gate)
	a.jobs.Wait()
	var status string
	var alertID int64
	if err = pool.QueryRow(ctx, "SELECT id,status FROM ml_alert ORDER BY id DESC LIMIT 1").Scan(&alertID, &status); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, "UPDATE ml_alert SET status='acknowledged' WHERE id=$1", alertID); err != nil {
		t.Fatal(err)
	}
	if started, err = a.StartRun(ctx, "manual"); err != nil || !started {
		t.Fatal(started, err)
	}
	a.Wait()
	if err = pool.QueryRow(ctx, "SELECT status FROM ml_alert ORDER BY id DESC LIMIT 1").Scan(&status); err != nil || status != "acknowledged" {
		t.Fatal(status, err)
	}
	other.Wait()
}
func TestMigrateAdoptsExistingHead(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "INSERT INTO well(unitek_id,armits_id,ngdu,department,active) VALUES(42,21,1,4,true)"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM well WHERE unitek_id=42").Scan(&count); err != nil || count != 1 {
		t.Fatal("migration changed existing data", count, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE alembic_version SET version_num='old'"); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, pool); err == nil {
		t.Fatal("unsupported revision accepted")
	}
}
func TestJWTAndClassification(t *testing.T) {
	cfg := testConfig()
	cfg.MetabaseURL = "https://analytics.example"
	cfg.MetabaseSecret = "test-secret"
	cfg.TokenTTL = 3600
	cfg.Dashboards = []Dashboard{{ID: 1, Name: "Обзор"}}
	a := New(cfg, nil, &fakeML{})
	h := a.Handler()
	value := call(t, h, "GET", "/api/v1/analytics/dashboards/1/embed", "", 200).(map[string]any)
	if !strings.Contains(value["url"].(string), "/embed/dashboard/") {
		t.Fatal(value)
	}
	call(t, h, "GET", "/api/v1/analytics/dashboards/2/embed", "", 404)
	if classify([]map[string]any{{"category": nil}}) != nil {
		t.Fatal("empty category")
	}
}
