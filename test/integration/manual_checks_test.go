package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/telemetry"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

type checkScorer struct {
	calls     int
	content   string
	version   string
	fail      bool
	cancelled bool
	during    func()
}

func (s *checkScorer) Upload(ctx context.Context, name string, r io.Reader) (domain.UploadResult, error) {
	s.calls++
	if s.during != nil {
		s.during()
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return domain.UploadResult{}, err
	}
	s.content = string(raw)
	if s.cancelled && ctx.Err() != nil {
		return domain.UploadResult{}, ctx.Err()
	}
	if s.fail {
		return domain.UploadResult{}, domain.NewError(domain.Unavailable, "Сервис недоступен")
	}
	model := "test-model"
	version := s.version
	return domain.UploadResult{Verdict: "norm", NWindows: 3, ModelName: &model, ModelVersion: &version, Timeline: []domain.TimelinePoint{{WindowEnd: "2025-02-01T00:00:00Z", Risk: 0}}, Contributions: []domain.Contribution{{Feature: "Ia", Value: 0, SHAP: -0.1}}}, nil
}
func TestManualCheckPersistsAndReplaysWithoutRescoring(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	scorer := &checkScorer{version: "sha256:original"}
	setup := func() {
		app.http.Integrations = service.NewIntegrations(app.store, telemetry.Connector{}, telemetry.Secrets{}, scorer)
		app.http.Checks = service.NewChecks(app.store, app.http.Integrations)
	}
	setup()
	h := app.Handler()
	call(t, h, "POST", "/api/v1/wells", `{"ngdu":0,"department":0}`, 201)
	call(t, h, "POST", "/api/v1/wells", `{"ngdu":0,"department":0}`, 201)
	call(t, h, "POST", "/api/v1/telemetry/import", `{"points":[{"well_id":"1","timestamp":"2025-01-01T00:00:00Z","values":{"Ia":0,"F_out":0,"R_izol":9999}},{"well_id":"1","timestamp":"2025-02-01T00:00:00Z","values":{"Ia":30,"F_out":50}}]}`, 200)
	body := `{"request_id":"check-request-0001","start":"2025-01-01T00:00:00Z","end":"2025-02-02T00:00:00Z"}`
	saved := call(t, h, "POST", "/api/v1/wells/1/checks", body, 200).(map[string]any)
	id := saved["id"].(string)
	digest := sha256.Sum256([]byte(scorer.content))
	if saved["well_id"] != float64(1) || saved["points_count"] != float64(2) || saved["input_sha256"] != hex.EncodeToString(digest[:]) || !strings.Contains(scorer.content, "2025-01-01T00:00:00Z,0,0,") {
		t.Fatal(saved, scorer.content)
	}
	scorer.version = "sha256:new"
	setup()
	h = app.Handler() // новое приложение читает тот же сохранённый результат
	again := call(t, h, "GET", "/api/v1/wells/1/checks/"+id, "", 200).(map[string]any)
	if again["result"].(map[string]any)["model_version"] != "sha256:original" {
		t.Fatal(again)
	}
	replay := call(t, h, "POST", "/api/v1/wells/1/checks", body, 200).(map[string]any)
	if replay["id"] != id || scorer.calls != 1 {
		t.Fatal(replay, scorer.calls)
	}
	list := call(t, h, "GET", "/api/v1/wells/1/checks", "", 200).([]any)
	if len(list) != 1 {
		t.Fatal(list)
	}
	call(t, h, "GET", "/api/v1/wells/2/checks/"+id, "", 404)
	call(t, h, "POST", "/api/v1/wells/1/checks", strings.Replace(body, "2025-02-02", "2025-02-03", 1), 409)
	call(t, h, "POST", "/api/v1/wells/1/checks", strings.Replace(body, "check-request-0001", "bad", 1), 422)
	scorer.fail = true
	call(t, h, "POST", "/api/v1/wells/1/checks", strings.Replace(body, "check-request-0001", "check-request-0002", 1), 503)
	list = call(t, h, "GET", "/api/v1/wells/1/checks", "", 200).([]any)
	if len(list) != 1 {
		t.Fatal("failed calculation saved as success", list)
	}
	// История не зависит от перепривязки внешних ID и не меняет ярус скважины.
	call(t, h, "PATCH", "/api/v1/wells/1", `{"unitek_id":42,"armits_id":200}`, 200)
	call(t, h, "GET", "/api/v1/wells/1/checks/"+id, "", 200)
}
func TestManualCheckHistoryPaginationAndIdempotentStorage(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	app.http.Checks = service.NewChecks(app.store, nil)
	h := app.Handler()
	call(t, h, "POST", "/api/v1/wells", `{"ngdu":0,"department":0}`, 201)
	for i := 0; i < 27; i++ {
		// Одинаковое время намеренно: курсор должен учитывать ID.
		v := domain.ManualCheck{ID: fmt.Sprintf("check-%03d", i), WellID: 1, RequestID: fmt.Sprint(i), PointsCount: 1, InputSHA256: "abc", Result: domain.UploadResult{Verdict: "norm"}}
		v.Start = mustDate(t, "2025-01-01T00:00:00Z")
		v.End = mustDate(t, "2025-02-01T00:00:00Z")
		v.CreatedAt = v.End
		if _, err := app.store.SaveManualCheck(legacyResearchFixtureContext(), v); err != nil {
			t.Fatal(err)
		}
		v.ID = "duplicate"
		if _, err := app.store.SaveManualCheck(legacyResearchFixtureContext(), v); err != nil {
			t.Fatal(err)
		}
	}
	first := call(t, h, "GET", "/api/v1/wells/1/checks", "", 200).([]any)
	if len(first) != 25 {
		t.Fatal(len(first))
	}
	id := first[24].(map[string]any)["id"].(string)
	second := call(t, h, "GET", "/api/v1/wells/1/checks?before="+id, "", 200).([]any)
	if len(second) != 2 || second[0].(map[string]any)["id"] == id {
		t.Fatal(second)
	}
}

func mustDate(t *testing.T, value string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestManualCheckCompletesAfterClientDisconnects(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scorer := &checkScorer{cancelled: true, during: cancel}
	app.http.Integrations = service.NewIntegrations(app.store, telemetry.Connector{}, telemetry.Secrets{}, scorer)
	app.http.Checks = service.NewChecks(app.store, app.http.Integrations)
	h := app.Handler()
	call(t, h, "POST", "/api/v1/wells", `{"ngdu":0,"department":0}`, 201)
	call(t, h, "POST", "/api/v1/telemetry/import", `{"points":[{"well_id":"1","timestamp":"2025-01-01T00:00:00Z","values":{"Ia":0}}]}`, 200)
	req := httptest.NewRequest("POST", "/api/v1/wells/1/checks", strings.NewReader(`{"request_id":"disconnect-request","start":"2025-01-01T00:00:00Z","end":"2025-02-02T00:00:00Z"}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	if ctx.Err() == nil || recorder.Code != 200 {
		t.Fatal(recorder.Code, recorder.Body.String(), ctx.Err())
	}
	saved, err := app.store.ManualChecks(context.Background(), 1, "")
	if err != nil || len(saved) != 1 {
		t.Fatal(saved, err)
	}
}
