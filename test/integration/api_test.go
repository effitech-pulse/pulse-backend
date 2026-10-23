package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/parameterfile"
	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/migrations"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
	"github.com/effitech-pulse/pulse-backend/internal/transport/httpapi"
)

type fakeML struct {
	gate    chan struct{}
	started chan struct{}
	once    sync.Once
}

func ptr[T any](value T) *T { return &value }
func (f *fakeML) Snapshot(ctx context.Context, min *float64) (domain.Snapshot, error) {
	rows := []domain.WellRisk{{WellID: 123, UnitekID: ptr(int64(42)), Oilfield: ptr("test"), Risk: .1, Alert: true, Tier: ptr("watch"), DataOk: false, WindowsFired: 5, WindowsTotal: 10, WindowEnd: ptr("2026-01-01T00:00:00")}}
	if min != nil && *min > .1 {
		rows = []domain.WellRisk{}
	}
	return domain.Snapshot{Wells: rows, AlertThreshold: .7, HighThreshold: .9}, nil
}
func (f *fakeML) Score(ctx context.Context) (domain.Snapshot, error) {
	if f.started != nil {
		f.once.Do(func() { close(f.started) })
	}
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return domain.Snapshot{}, ctx.Err()
		}
	}
	return f.Snapshot(ctx, nil)
}
func (f *fakeML) Explain(ctx context.Context, id int64, n int32) (domain.Explanation, error) {
	return domain.Explanation{WellID: id, Contributions: []domain.Contribution{}}, nil
}
func (f *fakeML) History(ctx context.Context, id int64, n int32) (domain.RiskHistory, error) {
	return domain.RiskHistory{WellID: id, Points: []domain.RiskPoint{}}, nil
}
func (f *fakeML) Metrics(ctx context.Context) (domain.ModelMetrics, error) {
	return domain.ModelMetrics{Models: []map[string]any{}}, nil
}
func (f *fakeML) ModelHealth(ctx context.Context, state domain.ModelState) (domain.ModelHealth, error) {
	return domain.ModelHealth{AlertsReviewed: state.AlertsReviewed, AlertsFalse: state.AlertsFalse}, nil
}
func (f *fakeML) Upload(ctx context.Context, name string, r io.Reader) (domain.UploadResult, error) {
	data, _ := io.ReadAll(r)
	return domain.UploadResult{Filename: name, NWindows: int64(len(data))}, nil
}
func testConfig() config.Config {
	return config.Config{AuthMode: "legacy", AppScope: "research", Prefix: "/api/v1", MLTimeout: time.Second, PoolSize: 5, Location: time.UTC, CORS: []string{"http://localhost:3000"}, Dashboards: []config.Dashboard{}}
}

type application struct {
	http  *httpapi.Server
	ml    *service.ML
	store *postgres.Repository
}

func New(c config.Config, pool *pgxpool.Pool, client service.MLClient) *application {
	store := postgres.New(pool).WithExecutionProfile(c.AuthMode, c.AppScope)
	ml := service.NewML(store, store, client, service.RunOptions{Timeout: c.MLTimeout, Location: c.Location})
	var catalog *service.Catalog
	if pool != nil {
		catalog = service.NewCatalog(store)
	}
	dashes := []service.Dashboard{}
	for _, d := range c.Dashboards {
		dashes = append(dashes, service.Dashboard{ID: d.ID, Name: d.Name})
	}
	return &application{store: store, ml: ml, http: &httpapi.Server{Config: c, Catalog: catalog, ML: ml, Parameters: service.NewParameters(parameterfile.Reader{}), Analytics: service.Analytics{URL: c.MetabaseURL, Secret: c.MetabaseSecret, TTL: c.TokenTTL, Dashboards: dashes}}}
}
func (a *application) Handler() http.Handler { return a.http.Handler() }
func (a *application) Wait()                 { a.ml.Wait() }
func (a *application) StartRun(ctx context.Context, trigger string) (bool, error) {
	return a.ml.StartRun(ctx, trigger)
}
func (a *application) running(ctx context.Context) (bool, error) { return a.store.Running(ctx) }
func waitIdle(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	store := postgres.New(pool)
	for i := 0; i < 100; i++ {
		running, err := store.Running(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !running {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run still active")
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
	if err = migrations.Apply(ctx, pool); err != nil {
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
	if len(w.Header().Get("X-Request-ID")) != 32 {
		t.Fatal("missing generated HTTP correlation header")
	}
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
	if len(w.Header().Get("X-Request-ID")) != 32 {
		t.Fatal("missing generated HTTP correlation header")
	}
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatal(w.Code, w.Header())
	}
	r = httptest.NewRequest("GET", "/api/v1/wells/not-int", nil)
	r.Header.Set("X-API-Key", "secret")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if len(w.Header().Get("X-Request-ID")) != 32 {
		t.Fatal("missing generated HTTP correlation header")
	}
	if w.Code != 422 {
		t.Fatal(w.Code)
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
	if len(w.Header().Get("X-Request-ID")) != 32 {
		t.Fatal("missing generated HTTP correlation header")
	}
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var imported domain.ImportResult
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
	ctx := legacyResearchFixtureContext() // intentional legacy research run fixture
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
	waitIdle(t, pool)
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
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM well WHERE unitek_id=42").Scan(&count); err != nil || count != 1 {
		t.Fatal("migration changed existing data", count, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE alembic_version SET version_num='old'"); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, pool); err == nil {
		t.Fatal("unsupported revision accepted")
	}
}
func TestJWTAndClassification(t *testing.T) {
	cfg := testConfig()
	cfg.MetabaseURL = "https://analytics.example"
	cfg.MetabaseSecret = "test-secret"
	cfg.TokenTTL = 3600
	cfg.Dashboards = []config.Dashboard{{ID: 1, Name: "Обзор"}}
	a := New(cfg, nil, &fakeML{})
	h := a.Handler()
	value := call(t, h, "GET", "/api/v1/analytics/dashboards/1/embed", "", 200).(map[string]any)
	if !strings.Contains(value["url"].(string), "/embed/dashboard/") {
		t.Fatal(value)
	}
	call(t, h, "GET", "/api/v1/analytics/dashboards/2/embed", "", 404)
	if service.Classify([]domain.Complication{{Category: nil}}) != nil {
		t.Fatal("empty category")
	}
}

func TestRunFiltersBindAllValues(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	// Кавычка и знаки SQL остаются значением фильтра при сборке вложенного запроса.
	dag := "dag?' OR 1=1 --"
	var wellID int64
	err := pool.QueryRow(ctx, `INSERT INTO well(unitek_id,armits_id,ngdu,department,active)
        VALUES (42,21,1,4,true) RETURNING id`).Scan(&wellID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `
        INSERT INTO run_archive(dag_id, run_id, execution_date, well_id, status_type, severity)
        VALUES ($1, 'matching', now(), $2, 'success', 'info'),
               ($1, 'wrong-status', now(), $2, 'error', 'info'),
               ('other', 'wrong-dag', now(), $2, 'success', 'info')`, dag, wellID)
	if err != nil {
		t.Fatal(err)
	}
	status := "success"
	rows, err := postgres.New(pool).Runs(legacyResearchFixtureContext(), domain.RunFilter{
		DAGID: &dag, WellID: &wellID, StatusType: &status, Limit: 10,
	})
	if err != nil || len(rows) != 1 || rows[0].RunID != "matching" {
		t.Fatalf("filters did not preserve bound values: %+v, %v", rows, err)
	}
}

func TestGinMLRoutesPreservePathParameters(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	defer app.Wait()
	handler := app.Handler()
	well := call(t, handler, "POST", "/api/v1/wells",
		`{"unitek_id":42,"armits_id":21,"ngdu":1,"department":4}`, 201).(map[string]any)
	risk := call(t, handler, "GET", "/api/v1/ml/wells/by-unitek/42", "", 200).(map[string]any)
	if risk["well_id"] != float64(123) || risk["app_well_id"] != well["id"] || risk["tier"] != "watch" || risk["data_ok"] != false {
		t.Fatalf("by-unitek route: %+v", risk)
	}
	for _, action := range []string{"explain", "history"} {
		result := call(t, handler, "GET", "/api/v1/ml/wells/123/"+action, "", 200).(map[string]any)
		if result["well_id"] != float64(123) {
			t.Fatalf("%s route: %+v", action, result)
		}
	}
}
