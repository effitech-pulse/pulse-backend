package app

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

func (a *App) referenceTable(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, a.Config.Prefix+"/ngdu") {
		return "ngdu"
	}
	return "department"
}
func (a *App) listReference(w http.ResponseWriter, r *http.Request) error {
	rows, err := queryJSON(r.Context(), a.Pool, "SELECT id,name FROM "+a.referenceTable(r)+" ORDER BY name")
	if err != nil {
		return err
	}
	writeJSON(w, 200, rows)
	return nil
}
func referenceName(raw map[string]any) (string, error) {
	name, ok := raw["name"].(string)
	if !ok || len([]rune(name)) > 255 {
		return "", validation("Некорректный name")
	}
	return name, nil
}
func (a *App) createReference(w http.ResponseWriter, r *http.Request) error {
	var raw map[string]any
	if err := readJSON(r, &raw); err != nil {
		return err
	}
	n, err := intValue(raw["id"])
	if err != nil {
		return err
	}
	name, err := referenceName(raw)
	if err != nil {
		return err
	}
	out, err := mutateRow(r.Context(), a.Pool, "INSERT INTO "+a.referenceTable(r)+"(id,name) VALUES($1,$2) RETURNING id,name", n, name)
	if err != nil {
		return err
	}
	writeJSON(w, 201, out)
	return nil
}
func (a *App) updateReference(w http.ResponseWriter, r *http.Request) error {
	n, err := id(r, "ref_id")
	if err != nil {
		return err
	}
	var raw map[string]any
	if err = readJSON(r, &raw); err != nil {
		return err
	}
	name, err := referenceName(raw)
	if err != nil {
		return err
	}
	out, err := mutateRow(r.Context(), a.Pool, "UPDATE "+a.referenceTable(r)+" SET name=$2 WHERE id=$1 RETURNING id,name", n, name)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}
func (a *App) deleteReference(w http.ResponseWriter, r *http.Request) error {
	n, err := id(r, "ref_id")
	if err != nil {
		return err
	}
	tag, err := a.Pool.Exec(r.Context(), "DELETE FROM "+a.referenceTable(r)+" WHERE id=$1", n)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return notFound("Запись не найдена")
	}
	w.WriteHeader(204)
	return nil
}
func (a *App) incidents(w http.ResponseWriter, r *http.Request) error {
	n, err := id(r, "well_id")
	if err != nil {
		return err
	}
	rows, err := queryJSON(r.Context(), a.Pool, "SELECT id,well_id,failure_type,failure_date,days_before,created_at FROM incident WHERE well_id=$1 ORDER BY failure_date DESC,id DESC", n)
	if err != nil {
		return err
	}
	writeJSON(w, 200, rows)
	return nil
}
func (a *App) createIncident(w http.ResponseWriter, r *http.Request) error {
	n, err := id(r, "well_id")
	if err != nil {
		return err
	}
	var raw map[string]any
	if err = readJSON(r, &raw); err != nil {
		return err
	}
	ftype, ok := raw["failure_type"].(string)
	if !ok || len([]rune(ftype)) > 100 {
		return validation("Некорректный failure_type")
	}
	date, ok := raw["failure_date"].(string)
	if !ok {
		return validation("Некорректная дата отказа")
	}
	if _, err = time.Parse("2006-01-02", date); err != nil {
		return validation("Некорректная дата отказа")
	}
	days := int64(60)
	if v, ok := raw["days_before"]; ok {
		days, err = intValue(v)
		if err != nil {
			return err
		}
	}
	if days < 1 || days > 365 {
		return validation("days_before должен быть в диапазоне 1..365")
	}
	out, err := mutateRow(r.Context(), a.Pool, "INSERT INTO incident(well_id,failure_type,failure_date,days_before) VALUES($1,$2,$3::date,$4) RETURNING id,well_id,failure_type,failure_date,days_before,created_at", n, ftype, date, days)
	if err != nil {
		return err
	}
	writeJSON(w, 201, out)
	return nil
}
func (a *App) deleteIncident(w http.ResponseWriter, r *http.Request) error {
	n, err := id(r, "incident_id")
	if err != nil {
		return err
	}
	tag, err := a.Pool.Exec(r.Context(), "DELETE FROM incident WHERE id=$1", n)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return notFound("Отказ не найден")
	}
	w.WriteHeader(204)
	return nil
}
func (a *App) incidentStats(w http.ResponseWriter, r *http.Request) error {
	byType, err := queryJSON(r.Context(), a.Pool, "SELECT failure_type,count(*) AS count FROM incident GROUP BY failure_type ORDER BY count DESC,failure_type")
	if err != nil {
		return err
	}
	byMonth, err := queryJSON(r.Context(), a.Pool, "SELECT to_char(failure_date,'YYYY-MM') AS month,count(*) AS count FROM incident GROUP BY month ORDER BY month")
	if err != nil {
		return err
	}
	var total int64
	for _, row := range byType {
		n, _ := intValue(row["count"])
		total += n
	}
	writeJSON(w, 200, map[string]any{"total": total, "by_type": byType, "by_month": byMonth})
	return nil
}
func (a *App) predictions(w http.ResponseWriter, r *http.Request) error {
	n, err := id(r, "well_id")
	if err != nil {
		return err
	}
	count, err := limit(r, 100, 1000)
	if err != nil {
		return err
	}
	rows, err := queryJSON(r.Context(), a.Pool, "SELECT id,well_id,status,parameters,processing_date,created_at FROM prediction WHERE well_id=$1 ORDER BY processing_date DESC LIMIT $2", n, count)
	if err != nil {
		return err
	}
	writeJSON(w, 200, rows)
	return nil
}
func cellValue(v any) any {
	if n, ok := v.(json.Number); ok {
		if integer, err := n.Int64(); err == nil {
			return integer
		}
		f, _ := n.Float64()
		return f
	}
	return v
}
func (a *App) exportPredictions(w http.ResponseWriter, r *http.Request) error {
	n, err := id(r, "well_id")
	if err != nil {
		return err
	}
	rows, err := queryJSON(r.Context(), a.Pool, "SELECT status,parameters,processing_date FROM prediction WHERE well_id=$1 ORDER BY processing_date DESC LIMIT 5000", n)
	if err != nil {
		return err
	}
	keys := map[string]bool{}
	for _, row := range rows {
		params, _ := row["parameters"].(map[string]any)
		for key := range params {
			keys[key] = true
		}
	}
	fields := []string{}
	for key := range keys {
		fields = append(fields, key)
	}
	sort.Strings(fields)
	header := append([]string{"processing_date", "status"}, fields...)
	out := [][]any{}
	for _, row := range rows {
		line := []any{row["processing_date"], row["status"]}
		params, _ := row["parameters"].(map[string]any)
		for _, field := range fields {
			v := params[field]
			switch v.(type) {
			case map[string]any, []any:
				encoded, _ := json.Marshal(v)
				v = string(encoded)
			}
			line = append(line, cellValue(v))
		}
		out = append(out, line)
	}
	return writeXLSX(w, "predictions", fmt.Sprintf("well_%d_predictions.xlsx", n), header, out)
}
func (a *App) complications(w http.ResponseWriter, r *http.Request) error {
	n, err := id(r, "well_id")
	if err != nil {
		return err
	}
	rows, err := queryJSON(r.Context(), a.Pool, "SELECT id,well_id,code,name,category,description,active,created_at FROM complication_reference WHERE well_id=$1 AND active ORDER BY created_at DESC", n)
	if err != nil {
		return err
	}
	writeJSON(w, 200, rows)
	return nil
}
func classify(rows []map[string]any) any {
	categories := map[string]bool{}
	for _, row := range rows {
		if category, ok := row["category"].(string); ok && category != "" {
			categories[category] = true
		}
	}
	if len(categories) > 1 {
		return "комбинированный"
	}
	for category := range categories {
		return category
	}
	return nil
}
func (a *App) classification(w http.ResponseWriter, r *http.Request) error {
	n, err := id(r, "well_id")
	if err != nil {
		return err
	}
	rows, err := queryJSON(r.Context(), a.Pool, "SELECT category FROM complication_reference WHERE well_id=$1 AND active", n)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"well_id": n, "classification": classify(rows)})
	return nil
}

const runColumns = "id,dag_id,run_id,execution_date,well_id,ngdu,department,status_type,severity,source,message,parameters,artifacts_path,created_at"

func runFilters(r *http.Request) (string, []any, error) {
	where := []string{}
	args := []any{}
	for _, key := range []string{"dag_id", "well_id", "status_type"} {
		if v, ok := r.URL.Query()[key]; ok && len(v) > 0 {
			var value any = v[0]
			if key == "well_id" {
				n, err := intValue(v[0])
				if err != nil {
					return "", nil, err
				}
				value = n
			}
			args = append(args, value)
			where = append(where, fmt.Sprintf("%s=$%d", key, len(args)))
		}
	}
	if len(where) == 0 {
		return "", args, nil
	}
	return " WHERE " + strings.Join(where, " AND "), args, nil
}
func (a *App) recentRuns(r *http.Request, count int) ([]map[string]any, error) {
	where, args, err := runFilters(r)
	if err != nil {
		return nil, err
	}
	args = append(args, count)
	return queryJSON(r.Context(), a.Pool, "SELECT "+runColumns+" FROM run_archive"+where+fmt.Sprintf(" ORDER BY execution_date DESC LIMIT $%d", len(args)), args...)
}
func (a *App) runs(w http.ResponseWriter, r *http.Request) error {
	count, err := limit(r, 100, 1000)
	if err != nil {
		return err
	}
	rows, err := a.recentRuns(r, count)
	if err != nil {
		return err
	}
	writeJSON(w, 200, rows)
	return nil
}
func (a *App) runEvents(w http.ResponseWriter, r *http.Request) error {
	rows, err := queryJSON(r.Context(), a.Pool, "SELECT "+runColumns+" FROM run_archive WHERE dag_id=$1 AND run_id=$2 ORDER BY created_at", r.PathValue("dag_id"), r.PathValue("run_id"))
	if err != nil {
		return err
	}
	writeJSON(w, 200, rows)
	return nil
}
func (a *App) exportRuns(w http.ResponseWriter, r *http.Request) error {
	rows, err := a.recentRuns(r, 5000)
	if err != nil {
		return err
	}
	header := []string{"execution_date", "dag_id", "run_id", "well_id", "ngdu", "department", "status_type", "severity", "source", "message"}
	out := [][]any{}
	for _, row := range rows {
		line := []any{}
		for _, key := range header {
			line = append(line, cellValue(row[key]))
		}
		out = append(out, line)
	}
	return writeXLSX(w, "runs", "runs.xlsx", header, out)
}
func (a *App) audit(w http.ResponseWriter, r *http.Request) error {
	count, err := limit(r, 200, 1000)
	if err != nil {
		return err
	}
	rows, err := queryJSON(r.Context(), a.Pool, "SELECT id,ts,actor,method,path,status_code FROM action_log ORDER BY ts DESC,id DESC LIMIT $1", count)
	if err != nil {
		return err
	}
	writeJSON(w, 200, rows)
	return nil
}
func (a *App) parameters(w http.ResponseWriter, r *http.Request) error {
	raw, err := os.ReadFile(a.Config.ParameterPath)
	if os.IsNotExist(err) {
		writeJSON(w, 200, []any{})
		return nil
	}
	if err != nil {
		return err
	}
	var data map[string]json.RawMessage
	if err = json.Unmarshal(raw, &data); err != nil {
		return err
	}
	keys := []string{}
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := []map[string]any{}
	for _, key := range keys {
		names := []string{}
		if err = json.Unmarshal(data[key], &names); err != nil {
			var text string
			if json.Unmarshal(data[key], &text) != nil {
				text = string(data[key])
			}
			names = []string{text}
		}
		name := key
		if len(names) > 0 {
			name = names[0]
		}
		if names == nil {
			names = []string{}
		}
		out = append(out, map[string]any{"field": key, "name": name, "aliases": names})
	}
	writeJSON(w, 200, out)
	return nil
}
func (a *App) analyticsStatus(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, 200, map[string]any{"enabled": a.Config.MetabaseURL != "" && a.Config.MetabaseSecret != "", "dashboards": a.Config.Dashboards})
	return nil
}
func (a *App) analyticsEmbed(w http.ResponseWriter, r *http.Request) error {
	if a.Config.MetabaseURL == "" || a.Config.MetabaseSecret == "" {
		return &APIError{502, "analytics_disabled", "Аналитика (Metabase) не настроена", false}
	}
	n, err := id(r, "dashboard_id")
	if err != nil {
		return err
	}
	allowed := false
	for _, dash := range a.Config.Dashboards {
		if dash.ID == n {
			allowed = true
		}
	}
	if !allowed {
		return notFound("Дашборд не разрешён для встраивания")
	}
	token := signJWT(map[string]any{"resource": map[string]any{"dashboard": n}, "params": map[string]any{}, "exp": time.Now().Unix() + a.Config.TokenTTL}, a.Config.MetabaseSecret)
	writeJSON(w, 200, map[string]any{"url": a.Config.MetabaseURL + "/embed/dashboard/" + token + "#bordered=true&titled=true"})
	return nil
}
func signJWT(payload map[string]any, secret string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	raw, _ := json.Marshal(payload)
	body := header + "." + base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
