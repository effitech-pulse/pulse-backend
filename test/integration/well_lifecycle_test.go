package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestWellArchivePreservesIdentityDataReviewsAndActiveState(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	app := New(testConfig(), pool, &fakeML{})
	app.http.Reviews = service.NewReviews(app.store).WithCatalog(app.store)
	h := app.Handler()
	call(t, h, "POST", "/api/v1/wells", `{"unitek_id":42,"armits_id":0,"ngdu":0,"department":0,"active":false,"responsible":"Технолог"}`, 201)
	old, err := app.store.Well(ctx, 1)
	if err != nil || old.Revision != 1 {
		t.Fatal(old, err)
	}
	check, err := app.store.SaveManualCheck(ctx, domain.ManualCheck{ID: "keep-check", WellID: 1, RequestID: "keep-check-key", Start: time.Now().Add(-time.Hour), End: time.Now(), CreatedAt: time.Now(), PointsCount: 1, Result: domain.UploadResult{Verdict: "norm"}})
	if err != nil {
		t.Fatal(err)
	}
	call(t, h, "PUT", "/api/v1/wells/1/checks/"+check.ID+"/review", `{"request_id":"keep-well-review-01","revision":0,"status":"new","comment":"Сохраняется в архиве"}`, 200)
	call(t, h, "POST", "/api/v1/wells/1/incidents", `{"failure_type":"test","failure_date":"2026-01-01"}`, 201)
	archived := call(t, h, "POST", "/api/v1/wells/1/archive", `{"revision":1,"reason":"Длительное обслуживание"}`, 200).(map[string]any)
	if archived["archived_at"] == nil || archived["active"] != false || archived["revision"] != float64(2) || archived["responsible"] != "Технолог" || archived["armits_id"] != float64(0) {
		t.Fatal(archived)
	}
	call(t, h, "GET", "/api/v1/wells/1", "", 404)
	call(t, h, "GET", "/api/v1/wells/1?include_archived=true", "", 200)
	current := call(t, h, "GET", "/api/v1/wells?active_only=false", "", 200).([]any)
	if len(current) != 0 {
		t.Fatal(current)
	}
	rows := call(t, h, "GET", "/api/v1/wells?active_only=false&archive=archived", "", 200).([]any)
	if len(rows) != 1 {
		t.Fatal(rows)
	}
	risks := call(t, h, "GET", "/api/v1/ml/wells", "", 200).(map[string]any)
	if risks["count"] != float64(0) {
		t.Fatal(risks)
	}
	// Исторические значения яруса не сбрасываются архивированием.
	risk := call(t, h, "GET", "/api/v1/wells/1/risk", "", 200).(map[string]any)
	if risk["tier"] != "watch" || risk["data_ok"] != false {
		t.Fatal(risk)
	}
	call(t, h, "GET", "/api/v1/wells/1/checks/keep-check/review", "", 200)
	incidents := call(t, h, "GET", "/api/v1/wells/1/incidents", "", 200).([]any)
	if len(incidents) != 1 {
		t.Fatal(incidents)
	}
	stored, err := app.store.ManualCheck(ctx, 1, "keep-check")
	if err != nil || stored.Result.Verdict != "norm" {
		t.Fatal(stored, err)
	}
	call(t, h, "PATCH", "/api/v1/wells/1", `{"revision":2,"well_number":"Нельзя менять в архиве"}`, 409)
	call(t, h, "POST", "/api/v1/wells/1/restore", `{"revision":1,"reason":"Устаревшая форма"}`, 409)
	call(t, h, "POST", "/api/v1/wells/1/restore", `{"revision":2,"reason":"Обслуживание завершено"}`, 200)
	restored, err := app.store.Well(ctx, 1)
	if err != nil || restored.Revision != 3 || restored.ArchivedAt != nil || restored.Active || restored.UnitekID == nil || *restored.UnitekID != 42 {
		t.Fatal(restored, err)
	}
	history := call(t, h, "GET", "/api/v1/wells/1/history", "", 200).(map[string]any)
	if len(history["events"].([]any)) != 2 {
		t.Fatal(history)
	}
	call(t, h, "POST", "/api/v1/wells/1/archive", `{"revision":3,"reason":""}`, 422)
	call(t, h, "GET", "/api/v1/wells?archive=bad", "", 422)
}
func TestWellRevisionSerializesEditsAndArchiveAndPagesHistory(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	h := app.Handler()
	call(t, h, "POST", "/api/v1/wells", `{"ngdu":0,"department":0}`, 201)
	statuses := make(chan int, 2)
	gate := make(chan struct{})
	for _, method := range []string{"PATCH", "POST"} {
		go func(method string) {
			<-gate
			path := "/api/v1/wells/1"
			body := `{"revision":1,"well_number":"Новое имя"}`
			if method == "POST" {
				path += "/archive"
				body = `{"revision":1,"reason":"Параллельная операция"}`
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
			statuses <- w.Code
		}(method)
	}
	close(gate)
	a, b := <-statuses, <-statuses
	if !((a == 200 && b == 409) || (a == 409 && b == 200)) {
		t.Fatal(a, b)
	}
	well, err := app.store.Well(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if well.ArchivedAt != nil {
		call(t, h, "POST", "/api/v1/wells/1/restore", `{"revision":2,"reason":"Продолжение теста"}`, 200)
	}
	well, err = app.store.Well(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 53; i++ {
		call(t, h, "PATCH", "/api/v1/wells/1", fmt.Sprintf(`{"revision":%d,"responsible":"Технолог %d","active":false,"unitek_id":null}`, well.Revision+int64(i), i), 200)
	}
	first := authRequest(t, h, "GET", "/api/v1/wells/1/history", "", nil, 200)
	var history domain.WellHistory
	if err = json.Unmarshal(first.Body.Bytes(), &history); err != nil || len(history.Events) != 50 || !history.HasMore {
		t.Fatal(history, err)
	}
	last := history.Events[49].Revision
	second := authRequest(t, h, "GET", fmt.Sprintf("/api/v1/wells/1/history?before=%d", last), "", nil, 200)
	var older domain.WellHistory
	if err = json.Unmarshal(second.Body.Bytes(), &older); err != nil || len(older.Events) == 0 || older.Events[0].Revision >= last {
		t.Fatal(older, err)
	}
	call(t, h, "PATCH", "/api/v1/wells/1", `{"revision":1,"responsible":"Старая форма"}`, 409)
	// В совместимом PATCH отсутствие revision допускается, но также увеличивает версию и оставляет историю.
	before, _ := app.store.Well(context.Background(), 1)
	call(t, h, "PATCH", "/api/v1/wells/1", `{"responsible":null}`, 200)
	after, _ := app.store.Well(context.Background(), 1)
	if after.Revision != before.Revision+1 || after.Responsible != nil {
		t.Fatal(before, after)
	}
}
