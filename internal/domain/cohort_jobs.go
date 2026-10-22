package domain

import "time"

type CohortJobState string

const (
	CohortJobPendingDispatch CohortJobState = "pending_dispatch"
	CohortJobQueued          CohortJobState = "queued"
	CohortJobRunning         CohortJobState = "running"
	CohortJobRetryWait       CohortJobState = "retry_wait"
	CohortJobCancelRequested CohortJobState = "cancel_requested"
	CohortJobQuarantined     CohortJobState = "quarantined"
	CohortJobSucceeded       CohortJobState = "succeeded"
	CohortJobFailed          CohortJobState = "failed"
	CohortJobCancelled       CohortJobState = "cancelled"
	CohortJobExpired         CohortJobState = "expired"
	CohortJobRevoked         CohortJobState = "revoked"
)

func (s CohortJobState) Terminal() bool {
	switch s {
	case CohortJobSucceeded, CohortJobFailed, CohortJobCancelled, CohortJobExpired, CohortJobRevoked:
		return true
	}
	return false
}

type CohortCaptureRecipe struct {
	Version            string `json:"version"`       // cohort_fixture_capture_v1
	Normalization      string `json:"normalization"` // none_supplied_canonical
	BackendBuildSHA256 string `json:"backend_build_sha256"`
}
type CohortFrozenInput struct {
	Identity   PinnedInputIdentity `json:"identity"`
	Canonical  []byte              `json:"-"`
	Bounds     TelemetryBounds     `json:"bounds"`
	Watermark  *time.Time          `json:"watermark"`
	Resolution string              `json:"resolution"`
	Rows       int                 `json:"rows"`
	Columns    int                 `json:"columns"`
}
type CohortFrozenMember struct {
	Ordinal         int                `json:"ordinal"`
	Well            Well               `json:"well"`
	Input           *CohortFrozenInput `json:"input"`
	NoComputeReason *string            `json:"no_compute_reason"`
}

// Trusted fixture source manifest is independently hashed before DB lifecycle overlay.
// Non-input marker allowed here only insufficient_telemetry/synthetic_source_unavailable.
// DB frozen lifecycle takes precedence and may replace a fixture input with archived/inactive marker.
type CohortFixtureMember struct {
	WellID          int64   `json:"well_id"`
	CanonicalSHA256 string  `json:"canonical_sha256,omitempty"`
	CanonicalBytes  int64   `json:"canonical_bytes,omitempty"`
	NoComputeReason *string `json:"no_compute_reason"`
}
type CohortFixtureManifest struct {
	Version              string                `json:"version"` // cohort_fixture_v1
	FixtureID            string                `json:"fixture_id"`
	CohortVersion        string                `json:"cohort_version"`
	CohortManifestSHA256 string                `json:"cohort_manifest_sha256"`
	AsOf                 time.Time             `json:"as_of"`
	ExecutionPin         ExecutionPin          `json:"execution_pin"`
	CaptureRecipe        CohortCaptureRecipe   `json:"capture_recipe"`
	Members              []CohortFixtureMember `json:"members"`
}
type PreparedCohortJob struct {
	ActorAccountID          int64
	RequestKey              string
	RequestCommitmentSHA256 string
	Fixture                 CohortFixtureManifest
	FixtureSHA256           string
	FixtureJSON             []byte               // exact canonical JSON of typed Fixture; policy SHA allowlist
	Members                 []CohortFrozenMember //100, input bytes validated against Fixture before lifecycle overlay
}
type CohortFixturePermit struct {
	FixtureID     string `json:"fixture_id"`
	FixtureSHA256 string `json:"fixture_sha256"`
}
type CohortJobPolicy struct {
	RunID                string                `json:"run_id"`
	TestSchema           string                `json:"test_schema"`
	ManifestSHA256       string                `json:"manifest_sha256"`
	CohortVersion        string                `json:"cohort_version"`
	CohortManifestSHA256 string                `json:"cohort_manifest_sha256"`
	AllowedAccountIDs    []int64               `json:"allowed_account_ids"`
	Fixtures             []CohortFixturePermit `json:"fixtures"` //<=2
}
type CohortJobStatus struct {
	ID             string
	RunID          int64
	State          CohortJobState
	Revision       int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeadlineAt     time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
	NextAttemptAt  *time.Time
	ErrorCode      *string
	AttemptCount   int
	CompletedCount int
	Counts         CohortCoverage
	SnapshotID     string
	AsOf           time.Time
	CohortVersion  string
	// URL derivation belongs to HTTP; no actor/private input/lease fields.
}
type CohortJobLease struct {
	JobID              string
	EventID            string
	Generation         int
	Fence              int64
	Token              string
	DeadlineAt         time.Time
	Attempt            int
	PhysicalBackendPID int32
}
type CohortMemberExecution struct {
	JobID               string
	Member              CohortFrozenMember
	ExecutionPin        ExecutionPin
	ExecutionPinSHA256  string
	InputManifestSHA256 string
	Attempt             int //0 for no-compute marker
}
type CohortCheckpoint struct {
	JobID                  string
	WellID                 int64
	InputFingerprintSHA256 string //empty for no-compute marker
	ExecutionPinSHA256     string
	InputManifestSHA256    string
	Outcome                CohortOutcome
	DigestSHA256           string
}
type CohortJobFailure struct {
	Code      string
	Retryable bool
}
type CohortJobDisposition struct {
	Kind   string
	Status CohortJobStatus
} //ack|stale

type CohortJobMessage struct {
	Version    int    `json:"version"`
	Kind       string `json:"kind"`
	EventID    string `json:"event_id"`
	JobID      string `json:"job_id"`
	Generation int    `json:"generation"`
}
type CohortPublishLease struct {
	Message    CohortJobMessage
	Token      string
	LeaseUntil time.Time
	Attempt    int
}
type CohortPublishOutcome string //confirmed|returned|nack|unknown

type CohortExecutionIdentity struct {
	Version              string       `json:"version"`     //cohort_execution_v1
	SourceKind           string       `json:"source_kind"` //cohort_snapshot
	CohortManifestSHA256 string       `json:"cohort_manifest_sha256"`
	InputManifestSHA256  string       `json:"input_manifest_sha256"`
	ExecutionPinSHA256   string       `json:"execution_pin_sha256"`
	ExecutionPin         ExecutionPin `json:"execution_pin"`
	ReplayStatus         string       `json:"replay_status"` //immutable_inputs_pinned
}
