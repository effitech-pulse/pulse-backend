package service

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/observability"
)

type RunOptions struct {
	Profile       string
	CohortVersion string
	Timeout       time.Duration
	AutoRun       bool
	Hour          int
	Location      *time.Location
}

type ML struct {
	scope     FundScope
	telemetry CohortTelemetry
	snapshots CohortSnapshotStore
	runs      RunStore
	catalog   Store
	client    MLClient
	options   RunOptions
	mu        sync.Mutex
	closing   bool
	jobs      sync.WaitGroup
}

func NewML(runs RunStore, catalog Store, client MLClient, options RunOptions) *ML {
	if options.Profile == "" {
		options.Profile = "research"
	}
	return &ML{runs: runs, catalog: catalog, client: client, options: options}
}

func (m *ML) StartRun(ctx context.Context, trigger string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return false, domain.NewError(domain.Unavailable, "API останавливается")
	}
	if (m.options.Profile != "research" && m.options.Profile != "cohort") || (m.scope != nil) != (m.options.Profile == "cohort") || (m.options.Profile == "cohort" && m.options.CohortVersion == "") {
		return false, domain.NewError(domain.Unavailable, "Профиль и версия расчёта не настроены")
	}
	lease, acquired, err := m.runs.Reserve(ctx)
	if err != nil || !acquired {
		return acquired, err
	}
	m.jobs.Add(1)
	seed := observability.BackgroundSeed(ctx)
	go m.execute(seed, lease, trigger)
	return true, nil
}

func (m *ML) execute(seed context.Context, lease RunLease, trigger string) {
	defer m.jobs.Done()
	defer lease.Close()
	operationStart := time.Now()
	operation, span := observability.Detached(seed, "ml.run")
	var err error
	defer func() { observability.Complete(operation, span, "operation completed", operationStart, err) }()
	ctx, cancel := context.WithTimeout(operation, m.options.Timeout+30*time.Second)
	defer cancel()
	var id int64
	if m.options.Profile == "cohort" {
		id, err = lease.BeginCohort(ctx, trigger, m.options.CohortVersion)
	} else {
		id, err = lease.Begin(ctx, trigger)
	}
	if err != nil {
		slog.ErrorContext(ctx, "run creation failed", "error_code", "operation_failed")
		return
	}
	if m.scope != nil {
		work, stop := context.WithTimeout(ctx, m.options.Timeout)
		snapshot, scoreErr := ScoreCohort(work, m.scope, m.telemetry, m.client, id, time.Now().UTC())
		stop()
		err = scoreErr
		if err == nil {
			if writer, ok := lease.(CohortRunLease); ok {
				save, finish := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
				err = writer.PersistCohort(save, id, snapshot)
				finish()
			} else {
				err = domain.NewError(domain.Unavailable, "Хранилище cohort snapshot не подключено")
			}
		}
	} else {
		snapshot, scoreErr := m.client.Score(ctx)
		err = scoreErr
		if err == nil {
			err = lease.Persist(ctx, id, snapshot)
		}
	}
	if err != nil {
		slog.ErrorContext(ctx, "ML run failed", "error_code", "operation_failed")
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		message := err.Error()
		if len(message) > 2000 {
			message = message[:2000]
		}
		if err := lease.Fail(finishCtx, id, message); err != nil {
			slog.ErrorContext(ctx, "record run failure", "error_code", "operation_failed")
		}
	}
}

func (m *ML) Wait() {
	m.mu.Lock()
	m.closing = true
	m.mu.Unlock()
	m.jobs.Wait()
}

func (m *ML) Schedule(ctx context.Context) {
	if !m.options.AutoRun {
		return
	}
	for {
		now := time.Now().In(m.options.Location)
		next := time.Date(now.Year(), now.Month(), now.Day(), m.options.Hour, 0, 0, 0, m.options.Location)
		if !next.After(now) {
			next = next.AddDate(0, 0, 1)
		}
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if _, err := m.StartRun(ctx, "scheduled"); err != nil {
				slog.ErrorContext(ctx, "scheduled ML run failed", "error_code", "operation_failed")
			}
		}
	}
}

func (m *ML) enrich(ctx context.Context, rows []domain.WellRisk) error {
	wells, err := m.catalog.Wells(ctx, false)
	if err != nil {
		return err
	}
	ids := map[int64]int64{}
	for _, w := range wells {
		if w.UnitekID != nil {
			ids[*w.UnitekID] = w.ID
		}
	}
	alerts, err := m.runs.LatestAlerts(ctx)
	if err != nil {
		return err
	}
	statuses := map[int64]domain.Alert{}
	for _, a := range alerts {
		if a.UnitekID != nil {
			statuses[*a.UnitekID] = a
		}
	}
	for i := range rows {
		w := &rows[i]
		if w.UnitekID == nil {
			continue
		}
		if id, ok := ids[*w.UnitekID]; ok {
			w.AppWellID = &id
		}
		if a, ok := statuses[*w.UnitekID]; ok {
			w.AlertID = &a.ID
			w.AlertStatus = &a.Status
		}
	}
	return nil
}

func (m *ML) Wells(ctx context.Context, min *float64, alertOnly bool) (domain.RiskList, error) {
	if m.scope != nil {
		return domain.RiskList{}, domain.NewError(domain.Missing, "Используйте cohort outcomes по ID Pulse")
	}
	snapshot, err := m.client.Snapshot(ctx, min)
	if err != nil {
		return domain.RiskList{}, err
	}
	rows := make([]domain.WellRisk, 0, len(snapshot.Wells))
	for _, w := range snapshot.Wells {
		if !alertOnly || w.Alert {
			rows = append(rows, w)
		}
	}
	if err = m.enrich(ctx, rows); err != nil {
		return domain.RiskList{}, err
	}
	if p, ok := domain.CurrentPrincipal(ctx); ok && p.Role != "admin" {
		visible := make([]domain.WellRisk, 0, len(rows))
		for _, row := range rows {
			if row.AppWellID != nil {
				w, e := m.catalog.Well(ctx, *row.AppWellID)
				if e != nil {
					return domain.RiskList{}, e
				}
				if p.Allows(w) {
					visible = append(visible, row)
				}
			}
		}
		rows = visible
	}
	known, err := m.catalog.Wells(ctx, false)
	if err != nil {
		return domain.RiskList{}, err
	}
	archived := map[int64]bool{}
	for _, w := range known {
		if w.ArchivedAt != nil && w.UnitekID != nil {
			archived[*w.UnitekID] = true
		}
	}
	visible := make([]domain.WellRisk, 0, len(rows))
	for _, w := range rows {
		if w.UnitekID == nil || !archived[*w.UnitekID] {
			visible = append(visible, w)
		}
	}
	rows = visible
	running, err := m.runs.Running(ctx)
	return domain.RiskList{
		Profile:        "research",
		Count:          len(rows),
		Wells:          rows,
		AlertThreshold: snapshot.AlertThreshold,
		HighThreshold:  snapshot.HighThreshold,
		Running:        running,
	}, err
}

func (m *ML) ByUnitek(ctx context.Context, id int64) (domain.WellRisk, error) {
	if m.scope != nil {
		return domain.WellRisk{}, domain.NewError(domain.Missing, "Используйте ID Pulse и сохранённый результат")
	}
	snapshot, err := m.client.Snapshot(ctx, nil)
	if err != nil {
		return domain.WellRisk{}, err
	}
	for _, w := range snapshot.Wells {
		if w.UnitekID != nil && *w.UnitekID == id {
			rows := []domain.WellRisk{w}
			if err = m.enrich(ctx, rows); err != nil {
				return domain.WellRisk{}, err
			}
			return rows[0], nil
		}
	}
	return domain.WellRisk{}, &domain.Error{Kind: domain.Missing, Message: "Скважина не найдена в скоринге", Plain: true}
}

func (m *ML) Runs(ctx context.Context, count int) (domain.RunList, error) {
	if m.scope != nil {
		return m.cohortRuns(ctx, count)
	}
	running, err := m.runs.Running(ctx)
	if err != nil {
		return domain.RunList{}, err
	}
	rows, err := m.runs.MLRuns(ctx, count)
	if err != nil {
		return domain.RunList{}, err
	}
	if !running && len(rows) > 0 && rows[0].Status == "running" {
		running, err = m.runs.Running(ctx)
		if err != nil {
			return domain.RunList{}, err
		}
		if !running {
			rows, err = m.runs.MLRuns(ctx, count)
		}
	}
	return domain.RunList{Running: running, Runs: rows}, err
}

func (m *ML) UpdateAlert(ctx context.Context, id int64, status string) (domain.Alert, error) {
	if status != "new" && status != "acknowledged" && status != "false_positive" {
		return domain.Alert{}, &domain.Error{Kind: domain.Invalid, Message: "Недопустимый статус", Plain: true}
	}
	if m.scope != nil {
		resolver, ok := m.runs.(interface {
			ReviewTarget(context.Context, string, string) (domain.ReviewTarget, error)
		})
		if !ok {
			return domain.Alert{}, domain.NewError(domain.Unavailable, "Не подключено хранилище обработки тревог")
		}
		target, err := resolver.ReviewTarget(ctx, "alert", strconv.FormatInt(id, 10))
		if err != nil {
			return domain.Alert{}, err
		}
		if target.WellID == nil {
			return domain.Alert{}, domain.NewError(domain.Missing, "Результат не найден")
		}
		if err = scopedWell(ctx, m.scope, *target.WellID); err != nil {
			return domain.Alert{}, err
		}
	}
	return m.runs.UpdateAlert(ctx, id, status)
}

func (m *ML) Explain(ctx context.Context, id int64, top int32) (domain.Explanation, error) {
	if err := m.checkModelScope(ctx, id); err != nil {
		return domain.Explanation{}, err
	}
	return m.client.Explain(ctx, id, top)
}

func (m *ML) History(ctx context.Context, id int64, count int32) (domain.RiskHistory, error) {
	if err := m.checkModelScope(ctx, id); err != nil {
		return domain.RiskHistory{}, err
	}
	return m.client.History(ctx, id, count)
}

func (m *ML) Metrics(ctx context.Context) (domain.ModelMetrics, error) {
	if m.scope != nil {
		return domain.ModelMetrics{Models: []map[string]any{}}, nil
	}
	return m.client.Metrics(ctx)
}

func (m *ML) Health(ctx context.Context) (domain.ModelHealth, error) {
	if m.scope != nil {
		return domain.ModelHealth{Urgency: "unknown", Reasons: []string{"Оценка готовности исследовательской модели недоступна в пользовательском фонде"}}, nil
	}
	state, err := m.runs.ModelState(ctx)
	if err != nil {
		return domain.ModelHealth{}, err
	}
	return m.client.ModelHealth(ctx, state)
}

func (m *ML) Upload(ctx context.Context, name string, input io.Reader) (domain.UploadResult, error) {
	return m.client.Upload(ctx, name, input)
}

// ByAppWell связывает внутренний ID приложения с исследовательским скорингом.
func (m *ML) ByAppWell(ctx context.Context, id int64) (domain.WellRisk, error) {
	well, err := m.catalog.Well(ctx, id)
	if err != nil {
		return domain.WellRisk{}, err
	}
	if well.UnitekID == nil {
		return domain.WellRisk{}, domain.NewError(domain.Missing, "Скважина ещё не сопоставлена с источником ML")
	}
	return m.ByUnitek(ctx, *well.UnitekID)
}
func (m *ML) HistoryByAppWell(ctx context.Context, id int64, count int32) (domain.RiskHistory, error) {
	risk, err := m.ByAppWell(ctx, id)
	if err != nil {
		return domain.RiskHistory{}, err
	}
	return m.History(ctx, risk.WellID, count)
}

func (m *ML) checkModelScope(ctx context.Context, id int64) error {
	if m.scope != nil {
		return domain.NewError(domain.Missing, "Исследовательский ID недоступен в пользовательском фонде")
	}
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || p.Role == "admin" {
		return nil
	}
	snapshot, err := m.client.Snapshot(ctx, nil)
	if err != nil {
		return err
	}
	for _, row := range snapshot.Wells {
		if row.WellID == id && row.UnitekID != nil {
			wells, err := m.catalog.Wells(ctx, false)
			if err != nil {
				return err
			}
			for _, w := range wells {
				if w.UnitekID != nil && *w.UnitekID == *row.UnitekID && p.Allows(w) {
					return nil
				}
			}
		}
	}
	return domain.NewError(domain.Missing, "Скважина не найдена в скоринге")
}
