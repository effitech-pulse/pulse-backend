package domain

type WellRisk struct {
	Decision            *DecisionEvidence `json:"decision"`
	WellID              int64             `json:"well_id"`
	UnitekID            *int64            `json:"unitek_id"`
	Oilfield            *string           `json:"oilfield"`
	Risk                float64           `json:"risk"`
	Alert               bool              `json:"alert"`
	Tier                *string           `json:"tier"`
	WindowsFired        int64             `json:"windows_fired"`
	WindowsTotal        int64             `json:"windows_total"`
	DataOk              bool              `json:"data_ok"`
	RuleFindings        []string          `json:"rule_findings"`
	WindowEnd           *string           `json:"window_end"`
	TrueFailureType     *int64            `json:"true_failure_type"`
	TrueFailureTypeName *string           `json:"true_failure_type_name"`
	TrueFailureAt       *string           `json:"true_failure_at"`
	AppWellID           *int64            `json:"app_well_id"`
	AlertID             *int64            `json:"alert_id"`
	AlertStatus         *string           `json:"alert_status"`
}

type Snapshot struct {
	Provenance     *Provenance   `json:"provenance"`
	DecisionRule   *DecisionRule `json:"decision_rule"`
	Wells          []WellRisk    `json:"wells"`
	AlertThreshold float64       `json:"alert_threshold"`
	HighThreshold  float64       `json:"high_threshold"`
	DriftVerdict   *string       `json:"drift_verdict"`
	DriftPsiMax    *float64      `json:"drift_psi_max"`
	DriftTop       *string       `json:"drift_top"`
}

type Contribution struct {
	ValueSource *string `json:"value_source"`
	Feature     string  `json:"feature"`
	Value       float64 `json:"value"`
	SHAP        float64 `json:"shap"`
	Label       string  `json:"label"`
}

type Explanation struct {
	WellID        int64          `json:"well_id"`
	Contributions []Contribution `json:"contributions"`
}

type RiskPoint struct {
	RawScore   *float64 `json:"raw_score"`
	WatchFired *bool    `json:"watch_fired"`
	HighFired  *bool    `json:"high_fired"`
	WindowEnd  string   `json:"window_end"`
	Risk       float64  `json:"risk"`
	Fired      bool     `json:"fired"`
}

type RiskHistory struct {
	AccumulatedWatchCount *int64        `json:"accumulated_watch_count"`
	AccumulatedHighCount  *int64        `json:"accumulated_high_count"`
	Provenance            *Provenance   `json:"provenance"`
	DecisionRule          *DecisionRule `json:"decision_rule"`
	WellID                int64         `json:"well_id"`
	Threshold             float64       `json:"threshold"`
	HighThreshold         float64       `json:"high_threshold"`
	WindowsTotal          int64         `json:"windows_total"`
	WindowsFired          int64         `json:"windows_fired"`
	Points                []RiskPoint   `json:"points"`
}

type ModelMetrics struct {
	Models []map[string]any `json:"models"`
}

type TimelinePoint struct {
	RawScore   *float64 `json:"raw_score"`
	WatchFired *bool    `json:"watch_fired"`
	HighFired  *bool    `json:"high_fired"`
	WindowEnd  string   `json:"window_end"`
	Risk       float64  `json:"risk"`
}

type UploadResult struct {
	Provenance          *Provenance     `json:"provenance"`
	DecisionRule        *DecisionRule   `json:"decision_rule"`
	ModelName           *string         `json:"model_name"`
	ModelVersion        *string         `json:"model_version"`
	ModelSavedAt        *string         `json:"model_saved_at"`
	Verdict             string          `json:"verdict"`
	NWindows            int64           `json:"n_windows"`
	ChannelsUsed        int64           `json:"channels_used"`
	FeaturesTotal       int64           `json:"features_total"`
	WindowsRunning      int64           `json:"windows_running"`
	FeaturesUnavailable int64           `json:"features_unavailable"`
	WindowsAboveWatch   int64           `json:"windows_above_watch"`
	WindowsAboveAlert   int64           `json:"windows_above_alert"`
	LatestRisk          float64         `json:"latest_risk"`
	PeakRisk            float64         `json:"peak_risk"`
	PeakWindowEnd       string          `json:"peak_window_end"`
	Timeline            []TimelinePoint `json:"timeline"`
	Contributions       []Contribution  `json:"contributions"`
	Filename            string          `json:"filename"`
	SpanDays            int64           `json:"span_days"`
}

type ModelState struct {
	DriftVerdict   *string  `json:"drift_verdict"`
	DriftPsiMax    *float64 `json:"drift_psi_max"`
	AlertsReviewed int64    `json:"alerts_reviewed"`
	AlertsFalse    int64    `json:"alerts_false"`
}

type ModelHealth struct {
	Urgency        string   `json:"urgency"`
	Needed         bool     `json:"needed"`
	Reasons        []string `json:"reasons"`
	DriftVerdict   *string  `json:"drift_verdict"`
	AlertsReviewed int64    `json:"alerts_reviewed"`
	AlertsFalse    int64    `json:"alerts_false"`
	FalseAlertRate *float64 `json:"false_alert_rate"`
	ModelAgeDays   *float64 `json:"model_age_days"`
}

type RiskList struct {
	Profile        string     `json:"profile"`
	Count          int        `json:"count"`
	AlertThreshold float64    `json:"alert_threshold"`
	HighThreshold  float64    `json:"high_threshold"`
	Running        bool       `json:"running"`
	Wells          []WellRisk `json:"wells"`
}

type MLRun struct {
	Profile           *string     `json:"profile"`
	CohortVersion     *string     `json:"cohort_version"`
	SnapshotID        *string     `json:"snapshot_id"`
	Provenance        *Provenance `json:"provenance"`
	FailureReasonCode *string     `json:"failure_reason_code"`
	ProvenanceStatus  *string     `json:"provenance_status"`
	ID                int64       `json:"id"`
	StartedAt         string      `json:"started_at"`
	FinishedAt        *string     `json:"finished_at"`
	Status            string      `json:"status"`
	Trigger           string      `json:"trigger"`
	WellsScored       *int64      `json:"wells_scored"`
	AlertsCount       *int64      `json:"alerts_count"`
	Error             *string     `json:"error"`
	DriftVerdict      *string     `json:"drift_verdict"`
	DriftPSIMax       *float64    `json:"drift_psi_max"`
	DriftTop          *string     `json:"drift_top"`
}

type RunList struct {
	Running bool    `json:"running"`
	Runs    []MLRun `json:"runs"`
}

type Alert struct {
	ID       int64  `json:"id"`
	UnitekID *int64 `json:"unitek_id"`
	Status   string `json:"status"`
}
