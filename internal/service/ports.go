// Package service реализует сценарии приложения через интерфейсы адаптеров.
package service

import (
	"context"
	"io"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type Store interface {
	Ping(context.Context) error
	Wells(context.Context, bool) ([]domain.Well, error)
	Well(context.Context, int64) (domain.Well, error)
	CreateWell(context.Context, domain.Well) (domain.Well, error)
	PatchWell(context.Context, int64, domain.WellPatch) (domain.Well, error)
	DeleteWell(context.Context, int64) error
	SetWellArchive(context.Context, int64, *int64, bool, string) (domain.Well, error)
	WellHistory(context.Context, int64, int64) (domain.WellHistory, error)
	ImportWells(context.Context, []domain.Well) (domain.ImportResult, error)
	References(context.Context, domain.ReferenceKind) ([]domain.Reference, error)
	CreateReference(context.Context, domain.ReferenceKind, int64, string) (domain.Reference, error)
	UpdateReference(context.Context, domain.ReferenceKind, int64, string) (domain.Reference, error)
	DeleteReference(context.Context, domain.ReferenceKind, int64) error
	Incidents(context.Context, int64) ([]domain.Incident, error)
	CreateIncident(context.Context, domain.Incident) (domain.Incident, error)
	DeleteIncident(context.Context, int64) error
	IncidentStatsForWells(context.Context, []int64) (domain.IncidentStats, error)
	IncidentStats(context.Context) (domain.IncidentStats, error)
	Predictions(context.Context, int64, int) ([]domain.Prediction, error)
	Complications(context.Context, int64) ([]domain.Complication, error)
	Runs(context.Context, domain.RunFilter) ([]domain.RunEvent, error)
	RunEvents(context.Context, string, string) ([]domain.RunEvent, error)
	Audit(context.Context, int) ([]domain.AuditEntry, error)
	RecordAudit(context.Context, string, string, string, int) error
}

type RunLease interface {
	Begin(context.Context, string) (int64, error)
	BeginCohort(context.Context, string, string) (int64, error)
	Persist(context.Context, int64, domain.Snapshot) error
	Fail(context.Context, int64, string) error
	Close()
}

type RunStore interface {
	Reserve(context.Context) (RunLease, bool, error)
	Running(context.Context) (bool, error)
	MLRuns(context.Context, int) ([]domain.MLRun, error)
	LatestAlerts(context.Context) ([]domain.Alert, error)
	UpdateAlert(context.Context, int64, string) (domain.Alert, error)
	ModelState(context.Context) (domain.ModelState, error)
}

type MLClient interface {
	Snapshot(context.Context, *float64) (domain.Snapshot, error)
	Score(context.Context) (domain.Snapshot, error)
	Explain(context.Context, int64, int32) (domain.Explanation, error)
	History(context.Context, int64, int32) (domain.RiskHistory, error)
	Metrics(context.Context) (domain.ModelMetrics, error)
	ModelHealth(context.Context, domain.ModelState) (domain.ModelHealth, error)
	Upload(context.Context, string, io.Reader) (domain.UploadResult, error)
}

// ScopedRunStore применяет текущий состав фонда до pagination событий.
type ScopedRunStore interface {
	RunsForWells(context.Context, domain.RunFilter, []int64) ([]domain.RunEvent, error)
	RunEventsForWells(context.Context, string, string, []int64) ([]domain.RunEvent, error)
}
