// Package domain содержит типы и ошибки предметной области без инфраструктуры.
package domain

import "time"

type Well struct {
	Revision    int64      `json:"revision"`
	ArchivedAt  *time.Time `json:"archived_at"`
	Responsible *string    `json:"responsible"`
	FundType    string     `json:"fund_type"`
	UnitekID    *int64     `json:"unitek_id"`
	ArmitsID    *int64     `json:"armits_id"`
	WellNumber  *string    `json:"well_number"`
	NGDU        int64      `json:"ngdu"`
	Department  int64      `json:"department"`
	Active      bool       `json:"active"`
	ID          int64      `json:"id"`
}

type Reference struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Incident struct {
	FailureType string `json:"failure_type"`
	FailureDate string `json:"failure_date"`
	DaysBefore  int64  `json:"days_before"`
	ID          int64  `json:"id"`
	WellID      int64  `json:"well_id"`
	CreatedAt   string `json:"created_at"`
}

type Complication struct {
	ID          int64   `json:"id"`
	WellID      *int64  `json:"well_id"`
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Category    *string `json:"category"`
	Description *string `json:"description"`
	Active      bool    `json:"active"`
	CreatedAt   string  `json:"created_at"`
}

type Prediction struct {
	ID             int64          `json:"id"`
	WellID         int64          `json:"well_id"`
	Status         string         `json:"status"`
	Parameters     map[string]any `json:"parameters"`
	ProcessingDate string         `json:"processing_date"`
	CreatedAt      string         `json:"created_at"`
}

type RunEvent struct {
	ID            int64           `json:"id"`
	DagID         string          `json:"dag_id"`
	RunID         string          `json:"run_id"`
	ExecutionDate string          `json:"execution_date"`
	WellID        *int64          `json:"well_id"`
	NGDU          *int64          `json:"ngdu"`
	Department    *int64          `json:"department"`
	StatusType    string          `json:"status_type"`
	Severity      string          `json:"severity"`
	Source        *string         `json:"source"`
	Message       *string         `json:"message"`
	Parameters    *map[string]any `json:"parameters"`
	ArtifactsPath *string         `json:"artifacts_path"`
	CreatedAt     string          `json:"created_at"`
}

type AuditEntry struct {
	ID         int64   `json:"id"`
	Ts         string  `json:"ts"`
	Actor      *string `json:"actor"`
	Method     string  `json:"method"`
	Path       string  `json:"path"`
	StatusCode int64   `json:"status_code"`
}

type Parameter struct {
	Field   string   `json:"field"`
	Name    string   `json:"name"`
	Aliases []string `json:"aliases"`
}

type IncidentStats struct {
	Total   int64        `json:"total"`
	ByType  []TypeCount  `json:"by_type"`
	ByMonth []MonthCount `json:"by_month"`
}

type TypeCount struct {
	FailureType string `json:"failure_type"`
	Count       int64  `json:"count"`
}

type MonthCount struct {
	Month string `json:"month"`
	Count int64  `json:"count"`
}

type Classification struct {
	WellID         int64   `json:"well_id"`
	Classification *string `json:"classification"`
}
