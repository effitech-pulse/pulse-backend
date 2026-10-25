package domain

// DailyFlowQuery задаёт включительный календарный интервал и точную версию экрана.
type DailyFlowQuery struct{ StartDate, EndDate, DatasetVersion, ViewID string }
type DailyFlowSourceRow struct {
	Date               string `json:"date"`
	ActualHundredths   *int64 `json:"actual_hundredths"`
	ExpectedHundredths *int64 `json:"expected_hundredths"`
	RecordState        string `json:"record_state"`
}
type DailyFlowMember struct {
	WellID       int64  `json:"well_id"`
	UnitekID     int64  `json:"unitek_id"`
	ArmitsID     int64  `json:"armits_id"`
	Ordinal      int    `json:"ordinal"`
	ScenarioCode string `json:"scenario_code"`
}
type DailyFlowProfile struct {
	DatasetVersion       string            `json:"dataset_version"`
	RecipeVersion        string            `json:"recipe_version"`
	Seed                 string            `json:"seed"`
	Canonicalization     string            `json:"canonicalization"`
	CohortVersion        string            `json:"cohort_version"`
	CohortManifestSHA256 string            `json:"cohort_manifest_sha256"`
	StartDate            string            `json:"start_date"`
	EndDate              string            `json:"end_date"`
	Members              []DailyFlowMember `json:"members"`
	ManifestSHA256       string            `json:"-"`
	DatasetSHA256        string            `json:"-"`
}
type DailyFlowRange struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}
type DailyFlowCoverage struct {
	CalendarDays          int `json:"calendar_days"`
	RecordedDays          int `json:"recorded_days"`
	ActualDays            int `json:"actual_days"`
	ExpectedDays          int `json:"expected_days"`
	PairedDays            int `json:"paired_days"`
	ComparablePercentDays int `json:"comparable_percent_days"`
}
type DailyFlowSource struct {
	Kind               string `json:"kind"`
	Synthetic          bool   `json:"synthetic"`
	Label              string `json:"label"`
	ExpectedDefinition string `json:"expected_definition"`
	ScenarioCode       string `json:"scenario_code"`
	RecipeVersion      string `json:"recipe_version"`
	ManifestSHA256     string `json:"manifest_sha256"`
	DatasetSHA256      string `json:"dataset_sha256"`
}
type DailyFlowRow struct {
	Date             string   `json:"date"`
	Actual           *float64 `json:"actual"`
	Expected         *float64 `json:"expected"`
	Delta            *float64 `json:"delta"`
	DeltaPercent     *float64 `json:"delta_percent"`
	RecordState      string   `json:"record_state"`
	ComparisonStatus string   `json:"comparison_status"`
}
type DailyFlowView struct {
	Enabled          bool              `json:"enabled"`
	SchemaVersion    string            `json:"schema_version"`
	WellID           string            `json:"well_id"`
	StartDate        string            `json:"start_date"`
	EndDate          string            `json:"end_date"`
	CalendarTimezone string            `json:"calendar_timezone"`
	Quantity         string            `json:"quantity"`
	Unit             string            `json:"unit"`
	DatasetVersion   string            `json:"dataset_version"`
	ViewID           string            `json:"view_id"`
	Source           DailyFlowSource   `json:"source"`
	Available        DailyFlowRange    `json:"available"`
	Coverage         DailyFlowCoverage `json:"coverage"`
	Rows             []DailyFlowRow    `json:"rows"`
}
type DailyFlowResult struct {
	Enabled bool
	View    *DailyFlowView
}
type DailyFlowExport struct {
	Filename string
	Bytes    []byte
}
