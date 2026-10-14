package app

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"
)

const runLock int64 = 74019321

func (a *App) running(ctx context.Context) (bool, error) {
	conn, err := a.Pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	var acquired bool
	if err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", runLock).Scan(&acquired); err != nil {
		return false, err
	}
	if acquired {
		_, err = conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", runLock)
	}
	return !acquired, err
}
func unlock(conn *pgxpool.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", runLock); err != nil {
		slog.Error("unlock failed", "error", err)
		raw := conn.Hijack()
		_ = raw.Close(ctx)
		return
	}
	conn.Release()
}

// StartRun резервирует запуск общей блокировкой до возврата HTTP-ответа.
func (a *App) StartRun(ctx context.Context, trigger string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closing {
		return false, &APIError{503, "shutdown", "API останавливается", true}
	}
	conn, err := a.Pool.Acquire(ctx)
	if err != nil {
		return false, err
	}
	var acquired bool
	if err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", runLock).Scan(&acquired); err != nil {
		conn.Release()
		return false, err
	}
	if !acquired {
		conn.Release()
		return false, nil
	}
	a.jobs.Add(1)
	go a.runAndPersist(conn, trigger)
	return true, nil
}
func (a *App) runAndPersist(conn *pgxpool.Conn, trigger string) {
	defer a.jobs.Done()
	defer unlock(conn)
	ctx, cancel := context.WithTimeout(context.Background(), a.Config.MLTimeout+30*time.Second)
	defer cancel()
	var runID int64
	if _, err := conn.Exec(ctx, "UPDATE ml_run SET status='error',finished_at=now(),error='Предыдущий процесс прогона был остановлен' WHERE status='running'"); err != nil {
		slog.Error("stale run cleanup failed", "error", err)
		return
	}
	if err := conn.QueryRow(ctx, "INSERT INTO ml_run(status,trigger,started_at) VALUES('running',$1,now()) RETURNING id", trigger).Scan(&runID); err != nil {
		slog.Error("run creation failed", "error", err)
		return
	}
	payload, err := a.ML.Score(ctx)
	if err == nil {
		err = a.persistResult(ctx, conn, runID, payload)
	}
	if err != nil {
		slog.Error("ML run failed", "run_id", runID, "error", err)
		finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer finishCancel()
		message := err.Error()
		if len(message) > 2000 {
			message = message[:2000]
		}
		if _, recordErr := conn.Exec(finishCtx, "UPDATE ml_run SET status='error',finished_at=now(),error=$2 WHERE id=$1", runID, message); recordErr != nil {
			slog.Error("run error recording failed", "error", recordErr)
		}
	}
}
func wellRows(payload map[string]any) []map[string]any {
	result := []map[string]any{}
	if rows, ok := payload["wells"].([]any); ok {
		for _, v := range rows {
			if row, ok := v.(map[string]any); ok {
				result = append(result, row)
			}
		}
	}
	if rows, ok := payload["wells"].([]map[string]any); ok {
		return rows
	}
	return result
}
func (a *App) persistResult(ctx context.Context, conn *pgxpool.Conn, runID int64, payload map[string]any) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows := wellRows(payload)
	count := 0
	for _, row := range rows {
		alert, _ := row["alert"].(bool)
		if !alert {
			continue
		}
		var unitek any
		if n, err := intValue(row["unitek_id"]); err == nil {
			unitek = n
		}
		wellID, err := intValue(row["well_id"])
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO ml_alert(run_id,ml_well_id,unitek_id,app_well_id,oilfield,risk,window_end,status)
 VALUES($1,$2,$3,(SELECT id FROM well WHERE unitek_id=$3),$4,$5,$6::timestamp,
 COALESCE((SELECT status FROM ml_alert WHERE unitek_id=$3 AND status<>'new' AND run_id=(SELECT id FROM ml_run WHERE status='done' AND id<>$1 ORDER BY id DESC LIMIT 1) LIMIT 1),'new'))`, runID, wellID, unitek, row["oilfield"], row["risk"], row["window_end"])
		if err != nil {
			return err
		}
		count++
	}
	_, err = tx.Exec(ctx, "UPDATE ml_run SET status='done',finished_at=now(),wells_scored=$2,alerts_count=$3,drift_verdict=$4,drift_psi_max=$5,drift_top=$6 WHERE id=$1", runID, len(rows), count, payload["drift_verdict"], payload["drift_psi_max"], payload["drift_top"])
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (a *App) Wait() { a.mu.Lock(); a.closing = true; a.mu.Unlock(); a.jobs.Wait() }
func (a *App) Schedule(ctx context.Context) {
	if !a.Config.AutoRun {
		return
	}
	for {
		now := time.Now().In(a.Config.Location)
		next := time.Date(now.Year(), now.Month(), now.Day(), a.Config.AutoRunHour, 0, 0, 0, a.Config.Location)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if _, err := a.StartRun(ctx, "scheduled"); err != nil {
				slog.Error("scheduled ML run failed", "error", err)
			}
		}
	}
}
func (a *App) enrich(ctx context.Context, rows []map[string]any) error {
	wells, err := queryJSON(ctx, a.Pool, "SELECT id,unitek_id FROM well")
	if err != nil {
		return err
	}
	wellMap := map[int64]any{}
	for _, row := range wells {
		n, _ := intValue(row["unitek_id"])
		wellMap[n] = row["id"]
	}
	alerts, err := queryJSON(ctx, a.Pool, "SELECT id,unitek_id,status FROM ml_alert WHERE run_id=(SELECT id FROM ml_run WHERE status='done' ORDER BY id DESC LIMIT 1)")
	if err != nil {
		return err
	}
	alertMap := map[int64]map[string]any{}
	for _, row := range alerts {
		if n, err := intValue(row["unitek_id"]); err == nil {
			alertMap[n] = row
		}
	}
	for _, row := range rows {
		row["app_well_id"] = nil
		row["alert_id"] = nil
		row["alert_status"] = nil
		if n, err := intValue(row["unitek_id"]); err == nil {
			row["app_well_id"] = wellMap[n]
			if alert := alertMap[n]; alert != nil {
				row["alert_id"] = alert["id"]
				row["alert_status"] = alert["status"]
			}
		}
	}
	return nil
}
func (a *App) mlWells(w http.ResponseWriter, r *http.Request) error {
	var min *float64
	if raw := r.URL.Query().Get("min_risk"); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return validation("Некорректный min_risk")
		}
		min = &value
	}
	alertOnly, err := queryBool(r, "alert_only", false)
	if err != nil {
		return err
	}
	payload, err := a.ML.Snapshot(r.Context(), min)
	if err != nil {
		return err
	}
	rows := []map[string]any{}
	for _, row := range wellRows(payload) {
		if alert, ok := row["alert"].(bool); !alertOnly || (ok && alert) {
			rows = append(rows, row)
		}
	}
	if err = a.enrich(r.Context(), rows); err != nil {
		return err
	}
	running, err := a.running(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"count": len(rows), "wells": rows, "alert_threshold": payload["alert_threshold"], "high_threshold": payload["high_threshold"], "running": running})
	return nil
}
func (a *App) mlByUnitek(w http.ResponseWriter, r *http.Request) error {
	n, err := id(r, "unitek_id")
	if err != nil {
		return err
	}
	payload, err := a.ML.Snapshot(r.Context(), nil)
	if err != nil {
		return err
	}
	for _, row := range wellRows(payload) {
		unitek, err := intValue(row["unitek_id"])
		if err == nil && unitek == n {
			rows := []map[string]any{row}
			if err = a.enrich(r.Context(), rows); err != nil {
				return err
			}
			writeJSON(w, 200, row)
			return nil
		}
	}
	return &APIError{404, "not_found", fmt.Sprintf("Скважина unitek_id=%d не найдена в скоринге", n), true}
}
func (a *App) mlRefresh(w http.ResponseWriter, r *http.Request) error {
	started, err := a.StartRun(r.Context(), "manual")
	if err != nil {
		return err
	}
	if !started {
		return &APIError{409, "conflict", "Прогон уже выполняется", true}
	}
	writeJSON(w, 200, map[string]any{"started": true})
	return nil
}
func (a *App) mlRuns(w http.ResponseWriter, r *http.Request) error {
	count, err := limit(r, 20, 1000)
	if err != nil {
		return err
	}
	// Сначала читаем блокировку: завершившийся между запросами прогон
	// не должен остановить polling frontend со старым status=running.
	running, err := a.running(r.Context())
	if err != nil {
		return err
	}
	query := "SELECT id,started_at,finished_at,status,trigger,wells_scored,alerts_count,error,drift_verdict,drift_psi_max,drift_top FROM ml_run ORDER BY id DESC LIMIT $1"
	rows, err := queryJSON(r.Context(), a.Pool, query, count)
	if err != nil {
		return err
	}
	if !running && len(rows) > 0 && rows[0]["status"] == "running" {
		running, err = a.running(r.Context())
		if err != nil {
			return err
		}
		if !running {
			rows, err = queryJSON(r.Context(), a.Pool, query, count)
			if err != nil {
				return err
			}
		}
	}
	writeJSON(w, 200, map[string]any{"running": running, "runs": rows})
	return nil
}
func (a *App) mlAlertStatus(w http.ResponseWriter, r *http.Request) error {
	n, err := id(r, "alert_id")
	if err != nil {
		return err
	}
	var raw map[string]any
	if err = readJSON(r, &raw); err != nil {
		return err
	}
	s, ok := raw["status"].(string)
	if !ok || (s != "new" && s != "acknowledged" && s != "false_positive") {
		return &APIError{422, "validation_error", "Недопустимый статус", true}
	}
	out, err := mutateRow(r.Context(), a.Pool, "UPDATE ml_alert SET status=$2,updated_at=now() WHERE id=$1 RETURNING id,status", n, s)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}
func (a *App) mlExplain(w http.ResponseWriter, r *http.Request) error {
	n, err := id(r, "well_id")
	if err != nil {
		return err
	}
	top := int64(6)
	if raw := r.URL.Query().Get("top_n"); raw != "" {
		top, err = intValue(raw)
		if err != nil || top < 1 || top > 2147483647 {
			return validation("Некорректный top_n")
		}
	}
	result, err := a.ML.Explain(r.Context(), n, int32(top))
	if err != nil {
		return err
	}
	writeJSON(w, 200, result)
	return nil
}
func (a *App) mlHistory(w http.ResponseWriter, r *http.Request) error {
	n, err := id(r, "well_id")
	if err != nil {
		return err
	}
	count, err := limit(r, 400, 2147483647)
	if err != nil {
		return err
	}
	result, err := a.ML.History(r.Context(), n, int32(count))
	if err != nil {
		return err
	}
	writeJSON(w, 200, result)
	return nil
}
func (a *App) mlMetrics(w http.ResponseWriter, r *http.Request) error {
	out, err := a.ML.Metrics(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}
func (a *App) mlUpload(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 51*1024*1024)
	if err := r.ParseMultipartForm(2 * 1024 * 1024); err != nil {
		return validation("Некорректный файл или файл больше 50 МБ")
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		return validation("Не передан файл")
	}
	defer file.Close()
	if header.Size > 50*1024*1024 {
		return &APIError{422, "validation_error", "Файл больше 50 МБ", true}
	}
	result, err := a.ML.Upload(r.Context(), header.Filename, file)
	if err != nil {
		return err
	}
	writeJSON(w, 200, result)
	return nil
}
func (a *App) mlHealth(w http.ResponseWriter, r *http.Request) error {
	state := map[string]any{"alerts_reviewed": int64(0), "alerts_false": int64(0)}
	rows, err := queryJSON(r.Context(), a.Pool, `SELECT drift_verdict,drift_psi_max,(SELECT count(*) FROM ml_alert WHERE run_id=ml_run.id AND status IN ('acknowledged','false_positive')) AS alerts_reviewed,(SELECT count(*) FROM ml_alert WHERE run_id=ml_run.id AND status='false_positive') AS alerts_false FROM ml_run WHERE status='done' ORDER BY started_at DESC LIMIT 1`)
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		state = rows[0]
		if number, ok := state["drift_psi_max"].(interface{ Float64() (float64, error) }); ok {
			state["drift_psi_max"], _ = number.Float64()
		}
	}
	out, err := a.ML.ModelHealth(r.Context(), state)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

// mlWellAction разрешает пересечение статического by-unitek и числового well_id.
func (a *App) mlWellAction(w http.ResponseWriter, r *http.Request) error {
	segment, action := r.PathValue("segment"), r.PathValue("action")
	if segment == "by-unitek" {
		r.SetPathValue("unitek_id", action)
		return a.mlByUnitek(w, r)
	}
	r.SetPathValue("well_id", segment)
	switch action {
	case "explain":
		return a.mlExplain(w, r)
	case "history":
		return a.mlHistory(w, r)
	default:
		return &APIError{404, "not_found", "Not Found", true}
	}
}
