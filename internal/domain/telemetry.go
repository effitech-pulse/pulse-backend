package domain

import "time"

// Mapping описывает преобразование внешнего измерения до агрегации.
type Mapping struct {
	Field     string  `json:"field"`
	Parameter string  `json:"parameter"`
	Unit      string  `json:"unit"`
	Scale     float64 `json:"scale"`
	Offset    float64 `json:"offset"`
	Missing   string  `json:"missing"`
	IsSet     bool    `json:"is_set"`
}

type TelemetrySource struct {
	WellIdentity    string     `json:"well_identity,omitempty"`
	ProfileVersion  int        `json:"profile_version"`
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Kind            string     `json:"kind"`
	Status          string     `json:"status"`
	IntervalSeconds int        `json:"interval_seconds"`
	Endpoint        string     `json:"endpoint"`
	TableName       string     `json:"table_name"`
	RecordsPath     string     `json:"records_path"`
	WellField       string     `json:"well_field"`
	TimeField       string     `json:"time_field"`
	Mappings        []Mapping  `json:"mappings"`
	LastReceived    *time.Time `json:"last_received"`
	LastMeasured    *time.Time `json:"last_measured"`
	Error           string     `json:"error"`
	EncryptedSecret string     `json:"-"`
}

type SourceInput struct {
	TelemetrySource
	Secret *string `json:"secret,omitempty"`
}

type Measurement struct {
	WellID    string              `json:"well_id"`
	Timestamp time.Time           `json:"timestamp"`
	Values    map[string]*float64 `json:"values"`
	Quality   string              `json:"quality,omitempty"`
}

type CollectionLog struct {
	Timestamp  time.Time `json:"timestamp"`
	SourceID   string    `json:"source_id"`
	Accepted   int       `json:"accepted"`
	Duplicates int       `json:"duplicates"`
	Rejected   int       `json:"rejected"`
	Message    string    `json:"message"`
}

type TelemetryResult struct {
	Points     []Measurement `json:"points"`
	Truncated  bool          `json:"truncated"`
	Resolution string        `json:"resolution,omitempty"`
}

type TelemetryBounds struct {
	Start *time.Time `json:"start"`
	End   *time.Time `json:"end"`
}

type TelemetrySourceLease struct {
	SourceID       string
	Token          string
	Fence          int64
	ProfileVersion int
	Until          time.Time
}
type TelemetryWrite struct {
	SourceID       string
	ProfileVersion int                   // 0 only for manual
	Lease          *TelemetrySourceLease // required iff Release=true
	Points         []Measurement
	Rejected       int
	Message        string
	Release        bool
}
