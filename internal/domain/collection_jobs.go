package domain

import "time"

type CollectionJobState string

// Допустимы pending_dispatch, running, retry_wait, succeeded, failed,
// cancelled, revoked, expired, quarantined; последние шесть terminal.

type CollectionCode string

// Закрытый список кодов — architecture.md, раздел HTTP/lifecycle.

type CollectionChannel struct {
	Name           string    `json:"name"`
	Aliases        []string  `json:"aliases"`
	Unit           string    `json:"unit"`
	IsSet          bool      `json:"is_set"`
	Minimum        float64   `json:"minimum"`
	Maximum        float64   `json:"maximum"`
	MissingMarkers []float64 `json:"missing_markers"`
}
type CollectionBinding struct {
	SourceID     string `json:"source_id"`
	WellID       int64  `json:"well_id"`
	ExternalID   string `json:"external_id"`
	IdentityKind string `json:"identity_kind"`
}
type CollectionFixture struct {
	ID             string              `json:"id"`
	Protocol       string              `json:"protocol"` // collection-fixture-v1
	Horizon        time.Time           `json:"horizon"`
	RetentionStart time.Time           `json:"retention_start"`
	Empty          bool                `json:"empty"`
	Channels       []CollectionChannel `json:"channels"`
	Mappings       []Mapping           `json:"mappings"`
}
type CollectionPolicy struct {
	RunID             string              `json:"run_id"`
	TestSchema        string              `json:"test_schema"`
	ManifestSHA256    string              `json:"manifest_sha256"`
	FixtureOrigin     string              `json:"fixture_origin"`
	AllowedAccountIDs []int64             `json:"allowed_account_ids"`
	Bindings          []CollectionBinding `json:"bindings"`
	Fixtures          []CollectionFixture `json:"fixtures"`
}
type CollectionSubmit struct {
	ReplayKey string `json:"replay_key"`
	FixtureID string `json:"fixture_id"`
	SourceID  string `json:"source_id"`
	Start     string `json:"start"`
	End       string `json:"end"`
}
type CollectionRevision struct {
	Revision int64 `json:"revision"`
}
type CollectionWellAuthority struct {
	WellID       int64      `json:"well_id"`
	FundType     string     `json:"fund_type"`
	NGDU         int64      `json:"ngdu"`
	Department   int64      `json:"department"`
	Active       bool       `json:"active"`
	ArchivedAt   *time.Time `json:"archived_at"`
	ExternalID   string     `json:"external_id"`
	IdentityKind string     `json:"identity_kind"`
}
type CollectionIntent struct {
	JobID                string                  `json:"job_id"`
	ActorAccountID       int64                   `json:"actor_account_id"`
	ActorRevision        int64                   `json:"actor_revision"`
	ScopeSHA256          string                  `json:"scope_sha256"`
	RunID                string                  `json:"run_id"`
	ManifestSHA256       string                  `json:"manifest_sha256"`
	Fixture              CollectionFixture       `json:"fixture"`
	Binding              CollectionBinding       `json:"binding"`
	Well                 CollectionWellAuthority `json:"well"`
	SourceProfileVersion int                     `json:"source_profile_version"`
	SourceConfigSHA256   string                  `json:"source_config_sha256"`
	MappingSHA256        string                  `json:"mapping_sha256"`
	BindingSHA256        string                  `json:"binding_sha256"`
	CanonicalVersion     string                  `json:"canonical_version"` // collection-canonical-v1
	SchemaVersion        string                  `json:"schema_version"`    // collection-measurement-v1
	FixtureOrigin        string                  `json:"fixture_origin"`
	OriginalStart        string                  `json:"original_start"`
	OriginalEnd          string                  `json:"original_end"`
	Start                time.Time               `json:"start"`
	End                  time.Time               `json:"end"`
	AcceptedAt           time.Time               `json:"accepted_at"`
	Deadline             time.Time               `json:"deadline"`
	RequestSHA256        string                  `json:"request_sha256"`
}

// Raw bytes и internal identities никогда не сериализуются в HTTP/status/event/log.
type CollectionRawCapture struct {
	Body       []byte
	ReceivedAt time.Time
}
type CollectionProvenance struct {
	SourceID             string `json:"source_id"`
	SourceProfileVersion int    `json:"source_profile_version"`
	SourceConfigSHA256   string `json:"source_config_sha256"`
	FixtureID            string `json:"fixture_id"`
	DataRevision         string `json:"data_revision"`
	BindingSHA256        string `json:"binding_sha256"`
	MappingSHA256        string `json:"mapping_sha256"`
	CanonicalVersion     string `json:"canonical_version"`
	SchemaVersion        string `json:"schema_version"`
}
type CollectionPreparedCapture struct {
	Raw               []byte
	RawSHA256         string
	Canonical         []byte
	CanonicalSHA256   string
	Provenance        CollectionProvenance
	ReceivedAt        time.Time
	RawRows           int
	UniqueRows        int
	RepeatedRows      int
	MarkerValues      int
	NullValues        int
	ExcludedSetpoints int
	Empty             bool
}

// Чистые функции; malformed -> error, markers/null -> nullable value.
type CollectionLease struct {
	JobID              string
	Token              string
	Fence              int64
	Generation         int
	Attempt            int
	PhysicalBackendPID int32
	Until              time.Time
	AttemptDeadline    time.Time
}
type CollectionWork struct {
	Intent  CollectionIntent
	Capture *CollectionPreparedCapture // nil means fetch, non-nil means NEVER fetch
}
type CollectionFailure struct {
	Code      CollectionCode
	Retryable bool
}
type CollectionCaptureStatus struct {
	State             string     `json:"state"` // absent/ready
	RawBytes          int64      `json:"raw_bytes"`
	RawRows           int        `json:"raw_rows"`
	UniqueRows        int        `json:"unique_rows"`
	RepeatedRows      int        `json:"repeated_rows"`
	MarkerValues      int        `json:"marker_values"`
	NullValues        int        `json:"null_values"`
	ExcludedSetpoints int        `json:"excluded_setpoints"`
	CapturedAt        *time.Time `json:"captured_at"`
}
type CollectionWriteStatus struct {
	Checkpoint  bool       `json:"checkpoint"`
	Inserted    int        `json:"inserted"`
	Identical   int        `json:"identical"`
	Rejected    int        `json:"rejected"`
	CommittedAt *time.Time `json:"committed_at"`
}
type CollectionCoverageStatus struct {
	State             string     `json:"state"` // none/complete/complete_empty
	Start             *time.Time `json:"start"`
	End               *time.Time `json:"end"` // exclusive
	DataRevision      *string    `json:"data_revision"`
	MaximumMeasuredAt *time.Time `json:"maximum_measured_at"`
}
type CollectionSourceStatus struct {
	AcceptedProfileVersion int    `json:"accepted_profile_version"`
	CurrentUsable          bool   `json:"current_usable"`
	Availability           string `json:"availability"` // unknown/dense/explicit_empty/unavailable/invalid/conflict
}
type CollectionJobStatus struct {
	ID              string                   `json:"id"`
	State           CollectionJobState       `json:"state"`
	Revision        int64                    `json:"revision"`
	FixtureID       string                   `json:"fixture_id"`
	SourceID        string                   `json:"source_id"`
	WellID          int64                    `json:"well_id"`
	Start           time.Time                `json:"start"`
	End             time.Time                `json:"end"`
	OriginalStart   string                   `json:"original_start"`
	OriginalEnd     string                   `json:"original_end"`
	AcceptedAt      time.Time                `json:"accepted_at"`
	Deadline        time.Time                `json:"deadline"`
	Attempt         int                      `json:"attempt"`
	MaximumAttempts int                      `json:"maximum_attempts"` // always 3
	NextAttemptAt   *time.Time               `json:"next_attempt_at"`
	ErrorCode       *CollectionCode          `json:"error_code"`
	Capture         CollectionCaptureStatus  `json:"capture"`
	Write           CollectionWriteStatus    `json:"write"`
	Coverage        CollectionCoverageStatus `json:"coverage"`
	Source          CollectionSourceStatus   `json:"source"`
}
type CollectionDisposition struct {
	Kind   string // applied/retry/terminal/stale/ignored
	Status CollectionJobStatus
}
type CollectionMessage struct {
	Version    int    `json:"version"`    // 1
	Kind       string `json:"kind"`       // collection.ready.v1
	EventID    string `json:"event_id"`   // opaque32hex
	JobID      string `json:"job_id"`     // opaque32hex
	Generation int    `json:"generation"` // 1..8
}
type CollectionPublishOutcome string // confirmed/returned/nack/unknown
type CollectionPublishLease struct {
	Message CollectionMessage
	Token   string
	Until   time.Time
}

const (
	CollectionPendingDispatch CollectionJobState = "pending_dispatch"
	CollectionRunning         CollectionJobState = "running"
	CollectionRetryWait       CollectionJobState = "retry_wait"
	CollectionSucceeded       CollectionJobState = "succeeded"
	CollectionFailed          CollectionJobState = "failed"
	CollectionCancelled       CollectionJobState = "cancelled"
	CollectionRevoked         CollectionJobState = "revoked"
	CollectionExpired         CollectionJobState = "expired"
	CollectionQuarantined     CollectionJobState = "quarantined"
)

const (
	CollectionCodeInvalidCollectionRequest     CollectionCode = "invalid_collection_request"
	CollectionCodeInvalidPeriod                CollectionCode = "invalid_period"
	CollectionCodeFixtureUnavailable           CollectionCode = "fixture_unavailable"
	CollectionCodeSourceNotAllowed             CollectionCode = "source_not_allowed"
	CollectionCodeSessionRequired              CollectionCode = "session_required"
	CollectionCodeCollectionAccessDenied       CollectionCode = "collection_access_denied"
	CollectionCodeCollectionJobMissing         CollectionCode = "collection_job_missing"
	CollectionCodeReplayConflict               CollectionCode = "replay_conflict"
	CollectionCodeSourceBusy                   CollectionCode = "source_busy"
	CollectionCodeRevisionConflict             CollectionCode = "revision_conflict"
	CollectionCodeCollectionCapacity           CollectionCode = "collection_capacity"
	CollectionCodeCollectionJobsDisabled       CollectionCode = "collection_jobs_disabled"
	CollectionCodeCollectionStoreUnavailable   CollectionCode = "collection_store_unavailable"
	CollectionCodeSourceUnavailable            CollectionCode = "source_unavailable"
	CollectionCodeSourceTimeout                CollectionCode = "source_timeout"
	CollectionCodeInvalidCapture               CollectionCode = "invalid_capture"
	CollectionCodeInputLimit                   CollectionCode = "input_limit"
	CollectionCodeInputConflict                CollectionCode = "input_conflict"
	CollectionCodeMeasurementConflict          CollectionCode = "measurement_conflict"
	CollectionCodeUnverifiableExisting         CollectionCode = "unverifiable_existing"
	CollectionCodeCaptureCorrupt               CollectionCode = "capture_corrupt"
	CollectionCodeAccessRevoked                CollectionCode = "access_revoked"
	CollectionCodeSourceChanged                CollectionCode = "source_changed"
	CollectionCodeBindingChanged               CollectionCode = "binding_changed"
	CollectionCodeAttemptsExhausted            CollectionCode = "attempts_exhausted"
	CollectionCodeJobDeadline                  CollectionCode = "job_deadline"
	CollectionCodeDispatchExhausted            CollectionCode = "dispatch_exhausted"
	CollectionCodeCancelled                    CollectionCode = "cancelled"
	CollectionCodeStaleLease                   CollectionCode = "stale_lease"
	CollectionCodeSchemaRequired               CollectionCode = "schema_required"
	CollectionCodeTelemetryMeasurementConflict CollectionCode = "telemetry_measurement_conflict"
)

const (
	CollectionPublishConfirmed CollectionPublishOutcome = "confirmed"
	CollectionPublishReturned  CollectionPublishOutcome = "returned"
	CollectionPublishNack      CollectionPublishOutcome = "nack"
	CollectionPublishUnknown   CollectionPublishOutcome = "unknown"
)
