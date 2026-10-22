package domain

import "time"

// Cohort описывает неизменяемую версию пользовательского фонда.
type Cohort struct {
	Version        string `json:"cohort_version"`
	ManifestSHA256 string `json:"-"`
}
type CohortOutcome struct {
	Diagnostics    *CohortDiagnostics `json:"diagnostics"`
	Provenance     *Provenance        `json:"provenance"`
	DecisionRule   *DecisionRule      `json:"decision_rule"`
	InputWatermark *time.Time         `json:"input_watermark"`
	AlertID        *int64             `json:"alert_id"`
	AlertStatus    *string            `json:"alert_status"`
	RunID          *int64             `json:"run_id,omitempty"`
	SnapshotID     *string            `json:"snapshot_id,omitempty"`
	AsOf           *time.Time         `json:"as_of,omitempty"`
	CohortVersion  string             `json:"cohort_version,omitempty"`
	AppWellID      int64              `json:"app_well_id"`
	Status         string             `json:"status"`
	ReasonCode     string             `json:"reason_code"`
	Result         *UploadResult      `json:"result"`
	InputSHA256    string             `json:"input_sha256,omitempty"`
	Bounds         *TelemetryBounds   `json:"bounds"`
	Resolution     string             `json:"resolution"`
	Metadata       Well               `json:"metadata"`
}
type CohortCoverage struct {
	MemberCount       int `json:"member_count"`
	VisibleCount      int `json:"visible_count"`
	ScoredCount       int `json:"scored_count"`
	ArchivedCount     int `json:"archived_count"`
	InactiveCount     int `json:"inactive_count"`
	InsufficientCount int `json:"insufficient_count"`
	ErrorCount        int `json:"error_count"`
}
type CohortSnapshot struct {
	Execution           *CohortExecutionIdentity `json:"execution,omitempty"`
	Profile             string                   `json:"profile"`
	CohortVersion       string                   `json:"cohort_version"`
	SnapshotID          *string                  `json:"snapshot_id"`
	RunID               *int64                   `json:"run_id"`
	AsOf                *time.Time               `json:"as_of"`
	Count               int                      `json:"count"`
	Running             bool                     `json:"running"`
	Outcomes            []CohortOutcome          `json:"outcomes"`
	Coverage            CohortCoverage           `json:"coverage"`
	ProvenanceStatus    string                   `json:"provenance_status"`
	EvaluationStatus    string                   `json:"evaluation_status"`
	OutcomeCompleteness string                   `json:"outcome_completeness"`
	ManifestSHA256      string                   `json:"-"`
}

func (s *CohortSnapshot) Recount() {
	c := CohortCoverage{MemberCount: len(s.Outcomes), VisibleCount: len(s.Outcomes)}
	for _, o := range s.Outcomes {
		switch o.Status {
		case "scored":
			c.ScoredCount++
		case "archived":
			c.ArchivedCount++
		case "inactive":
			c.InactiveCount++
		case "insufficient":
			c.InsufficientCount++
		case "error":
			c.ErrorCount++
		}
	}
	s.Count = len(s.Outcomes)
	s.Coverage = c
	s.EvaluationStatus = "evaluated"
	if c.ScoredCount == 0 {
		s.EvaluationStatus = "no_scored_members"
	}
}

// CohortRiskList добавляет фильтруемое представление оценённых участников к полному составу.
type CohortRiskList struct {
	CohortSnapshot
	Scored []CohortOutcome `json:"scored"`
}
type CohortAlert struct {
	AppWellID int64  `json:"app_well_id"`
	ID        int64  `json:"id"`
	Status    string `json:"status"`
}

// CohortDiagnostics сохраняет причину неполной оценки без фиктивного риска.
type CohortDiagnostics struct {
	Verdict             string `json:"verdict"`
	NWindows            int64  `json:"n_windows"`
	WindowsRunning      int64  `json:"windows_running"`
	ChannelsUsed        int64  `json:"channels_used"`
	FeaturesUnavailable int64  `json:"features_unavailable"`
}
