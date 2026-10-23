package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func reviewFixture(t *testing.T) *application {
	t.Helper()
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	app.http.Reviews = service.NewReviews(app.store)
	h := app.Handler()
	call(t, h, "POST", "/api/v1/wells", `{"unitek_id":42,"ngdu":0,"department":0}`, 201)
	call(t, h, "POST", "/api/v1/wells", `{"ngdu":0,"department":0}`, 201)
	_, err := app.store.SaveManualCheck(legacyResearchFixtureContext(), domain.ManualCheck{ID: "check_001", WellID: 1, RequestID: "request_001", Start: mustDate(t, "2025-01-01T00:00:00Z"), End: mustDate(t, "2025-02-01T00:00:00Z"), CreatedAt: mustDate(t, "2025-02-01T00:00:00Z"), PointsCount: 1, InputSHA256: "input", Result: domain.UploadResult{Verdict: "watch"}})
	if err != nil {
		t.Fatal(err)
	}
	return app
}
func reviewBody(request string, revision int64, status, owner, conclusion, comment string) string {
	raw, _ := json.Marshal(domain.ReviewInput{RequestID: request, Revision: revision, Status: status, Owner: owner, Conclusion: conclusion, Comment: comment})
	return string(raw)
}
func TestReviewWorkflowVersioningAuditAndCrossWellIsolation(t *testing.T) {
	app := reviewFixture(t)
	h := app.Handler()
	path := "/api/v1/wells/1/checks/check_001/review"
	empty := call(t, h, "GET", path, "", 200).(map[string]any)
	if empty["state"].(map[string]any)["revision"] != float64(0) || len(empty["events"].([]any)) != 0 {
		t.Fatal(empty)
	}
	first := reviewBody("review-request-01", 0, "in_progress", "Технолог 1", "", "")
	saved := call(t, h, "PUT", path, first, 200).(map[string]any)
	event := saved["events"].([]any)[0].(map[string]any)
	actor := event["actor"].(map[string]any)
	if actor["name"] != "Локальный сеанс" || actor["authenticated"] != false || event["before"].(map[string]any)["status"] != "new" {
		t.Fatal(saved)
	}
	call(t, h, "PUT", path, reviewBody("review-request-02", 0, "in_progress", "Другой", "", ""), 409)
	call(t, h, "PUT", path, reviewBody("review-request-03", 1, "closed", "Технолог 1", "confirmed", ""), 422)
	closed := call(t, h, "PUT", path, reviewBody("review-request-04", 1, "closed", "Технолог 1", "confirmed", "Параметры проверены"), 200).(map[string]any)
	if closed["state"].(map[string]any)["revision"] != float64(2) {
		t.Fatal(closed)
	}
	// Повтор старого ключа не открывает уже завершённую обработку заново.
	replay := call(t, h, "PUT", path, first, 200).(map[string]any)
	if replay["state"].(map[string]any)["status"] != "closed" || len(replay["events"].([]any)) != 2 {
		t.Fatal(replay)
	}
	call(t, h, "PUT", path, reviewBody("review-request-04", 1, "closed", "Технолог 1", "not_confirmed", "Другая операция"), 409)
	call(t, h, "PUT", path, reviewBody("review-request-05", 2, "new", "", "", ""), 422)
	reopened := call(t, h, "PUT", path, reviewBody("review-request-06", 2, "new", "", "", "Поступили новые данные"), 200).(map[string]any)
	events := reopened["events"].([]any)
	if len(events) != 3 || events[0].(map[string]any)["before"].(map[string]any)["conclusion"] != "confirmed" {
		t.Fatal(reopened)
	}
	call(t, h, "GET", "/api/v1/wells/2/checks/check_001/review", "", 404)
	call(t, h, "PUT", "/api/v1/wells/2/checks/check_001/review", first, 404)
	// Пересоздание сервиса сохраняет историю; ответ модели остаётся неизменённым.
	app.http.Reviews = service.NewReviews(app.store)
	again := call(t, app.Handler(), "GET", path, "", 200).(map[string]any)
	if len(again["events"].([]any)) != 3 {
		t.Fatal(again)
	}
	check, err := app.store.ManualCheck(context.Background(), 1, "check_001")
	if err != nil || check.Result.Verdict != "watch" {
		t.Fatal(check, err)
	}
}
func TestConcurrentReviewHasOneWinnerAndHistoryIsPaged(t *testing.T) {
	app := reviewFixture(t)
	h := app.Handler()
	path := "/api/v1/wells/1/checks/check_001/review"
	var group sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			req := httptest.NewRequest("PUT", path, strings.NewReader(reviewBody(fmt.Sprintf("concurrent-key-%02d", i), 0, "in_progress", "Технолог", "", "")))
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			codes <- rr.Code
		}(i)
	}
	group.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatal(counts)
	}
	for revision := int64(1); revision <= 52; revision++ {
		_, err := app.http.Reviews.Update(context.Background(), "check", "check_001", ptr(int64(1)), domain.ReviewInput{RequestID: fmt.Sprintf("history-request-%03d", revision), Revision: revision, Status: "in_progress", Owner: "Технолог", Comment: "Контроль"}, domain.ReviewActor{Name: "Тест"})
		if err != nil {
			t.Fatal(err)
		}
	}
	first := call(t, h, "GET", path, "", 200).(map[string]any)
	if first["has_more"] != true || len(first["events"].([]any)) != 50 {
		t.Fatal(first)
	}
	last := first["events"].([]any)[49].(map[string]any)["revision"].(float64)
	second := call(t, h, "GET", fmt.Sprintf("%s?before=%.0f", path, last), "", 200).(map[string]any)
	if second["has_more"] != false || len(second["events"].([]any)) != 3 {
		t.Fatal(second)
	}
}
func TestAlertReviewAndLegacyStatusCommitOneHistory(t *testing.T) {
	app := reviewFixture(t)
	h := app.Handler()
	call(t, h, "POST", "/api/v1/ml/wells/refresh", "", 200)
	app.Wait()
	alerts, err := app.store.LatestAlerts(context.Background())
	if err != nil || len(alerts) != 1 {
		t.Fatal(alerts, err)
	}
	id := alerts[0].ID
	path := fmt.Sprintf("/api/v1/ml/alerts/%d/review", id)
	call(t, h, "PUT", path, reviewBody("alert-review-0001", 0, "closed", "Технолог", "not_confirmed", "Проверено на установке"), 200)
	state, err := app.store.ModelState(context.Background())
	if err != nil || state.AlertsFalse != 1 || state.AlertsReviewed != 1 {
		t.Fatal(state, err)
	}
	call(t, h, "PATCH", fmt.Sprintf("/api/v1/ml/alerts/%d", id), `{"status":"acknowledged"}`, 200)
	review := call(t, h, "GET", path, "", 200).(map[string]any)
	if len(review["events"].([]any)) != 2 || review["state"].(map[string]any)["status"] != "in_progress" {
		t.Fatal(review)
	}
	call(t, h, "PUT", path, reviewBody("alert-review-0002", 2, "closed", "Технолог", "confirmed", "Осложнение подтверждено"), 200)
	alerts, err = app.store.LatestAlerts(context.Background())
	if err != nil || alerts[0].Status != "confirmed" {
		t.Fatal(alerts, err)
	}
	state, err = app.store.ModelState(context.Background())
	if err != nil || state.AlertsFalse != 0 || state.AlertsReviewed != 1 {
		t.Fatal(state, err)
	}
	wells, err := app.ml.Wells(domain.WithSystemPrincipal(context.Background()), nil, false)
	if err != nil || len(wells.Wells) != 1 || wells.Wells[0].Tier == nil || *wells.Wells[0].Tier != "watch" || wells.Wells[0].DataOk {
		t.Fatal(wells, err)
	}
}
