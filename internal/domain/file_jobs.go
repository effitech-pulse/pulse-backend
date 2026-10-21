package domain

import (
	"encoding/json"
	"time"
)

type FileJobState string

const (
	FileJobPendingDispatch FileJobState = "pending_dispatch"
	FileJobQueued          FileJobState = "queued"
	FileJobRunning         FileJobState = "running"
	FileJobRetryWait       FileJobState = "retry_wait"
	FileJobCancelRequested FileJobState = "cancel_requested"
	FileJobQuarantined     FileJobState = "quarantined"
	FileJobSucceeded       FileJobState = "succeeded"
	FileJobFailed          FileJobState = "failed"
	FileJobCancelled       FileJobState = "cancelled"
	FileJobExpired         FileJobState = "expired"
	FileJobRevoked         FileJobState = "revoked"
)

func (s FileJobState) Terminal() bool {
	return s == FileJobSucceeded || s == FileJobFailed || s == FileJobCancelled || s == FileJobExpired || s == FileJobRevoked
}

type FileJobMessage struct {
	Version    int    `json:"version"`
	EventID    string `json:"event_id"`
	JobID      string `json:"job_id"`
	Generation int    `json:"generation"`
}
type FileJobLease struct {
	JobID      string
	Generation int
	Fence      int64
	Token      string
}
type FileJobInputSummary struct {
	Format            string `json:"format"`
	RawBytes          int64  `json:"raw_bytes"`
	CanonicalBytes    int64  `json:"canonical_bytes"`
	Rows              int    `json:"rows"`
	RawSHA256         string `json:"raw_sha256"`
	CanonicalSHA256   string `json:"canonical_sha256"`
	FingerprintSHA256 string `json:"fingerprint_sha256"`
}
type FileJobExecutionSummary struct {
	ModelName      string `json:"model_name"`
	BundleSHA256   string `json:"bundle_sha256"`
	ContextVersion string `json:"context_version"`
}
type FileJobStatus struct {
	Input             FileJobInputSummary     `json:"input"`
	Execution         FileJobExecutionSummary `json:"execution"`
	ID                string                  `json:"id"`
	WellID            int64                   `json:"well_id"`
	State             FileJobState            `json:"state"`
	Revision          int64                   `json:"revision"`
	AttemptCount      int                     `json:"attempt_count"`
	CreatedAt         time.Time               `json:"created_at"`
	UpdatedAt         time.Time               `json:"updated_at"`
	DeadlineAt        time.Time               `json:"deadline_at"`
	StartedAt         *time.Time              `json:"started_at"`
	FinishedAt        *time.Time              `json:"finished_at"`
	NextAttemptAt     *time.Time              `json:"next_attempt_at"`
	CancelRequestedAt *time.Time              `json:"cancel_requested_at"`
	CheckID           *string                 `json:"check_id"`
	ErrorCode         *string                 `json:"error_code"`
}
type FileJobPage struct {
	Items      []FileJobStatus `json:"items"`
	NextBefore *string         `json:"next_before"`
}
type FileSavedCheck struct {
	ID          string       `json:"id"`
	JobID       string       `json:"job_id"`
	WellID      int64        `json:"well_id"`
	SourceKind  string       `json:"source_kind"`
	Start       time.Time    `json:"start"`
	End         time.Time    `json:"end"`
	CreatedAt   time.Time    `json:"created_at"`
	InputSHA256 string       `json:"input_sha256"`
	PointsCount int          `json:"points_count"`
	Resolution  string       `json:"resolution"`
	Result      UploadResult `json:"result"`
}
type FileJobPrepared struct {
	Input       PreparedFileInput
	Start       time.Time
	End         time.Time
	PointsCount int
}
type FileJobFailure struct {
	Code      string
	Retryable bool
}
type FileJobDisposition struct {
	Kind   string
	Status FileJobStatus
}
type FileJobPublishLease struct {
	Message FileJobMessage
	Token   string
}
type FileJobPublishOutcome string

const (
	FileJobPublishConfirmed FileJobPublishOutcome = "confirmed"
	FileJobPublishReturned  FileJobPublishOutcome = "returned"
	FileJobPublishNack      FileJobPublishOutcome = "nack"
	FileJobPublishUnknown   FileJobPublishOutcome = "unknown"
)

// FileJobAllowedInput — доверенная server-owned запись manifest, не клиентский DTO.
type FileJobAllowedInput struct {
	WellID           int64
	RawSHA256        string
	Format           string
	ClientSpecJSON   json.RawMessage
	ClientSpecSHA256 string
	ExecutionPin     ExecutionPin
}
type FileJobPolicy struct {
	TestSchema           string
	AllowedAccountIDs    []int64
	RunID                string
	ManifestSHA256       string
	CohortVersion        string
	CohortManifestSHA256 string
	AllowedInputs        []FileJobAllowedInput
	ActiveGlobal         int
	ActivePerAccount     int
}

func DefaultFileJobPolicy() FileJobPolicy { return FileJobPolicy{ActiveGlobal: 8, ActivePerAccount: 2} }

type FileJobExecution struct {
	Lease FileJobLease
	Input PreparedFileInput
}

// FileJobProblem сохраняет безопасный код и стандартную domain классификацию.
type FileJobProblem struct {
	Code  string
	Cause error
}

func (e *FileJobProblem) Error() string { return e.Code }
func (e *FileJobProblem) Unwrap() error { return e.Cause }
func NewFileJobProblem(kind Kind, code string) error {
	return &FileJobProblem{Code: code, Cause: NewError(kind, code)}
}
