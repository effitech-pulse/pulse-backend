package domain

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

const PassportSchemaVersion = "equipment-passport-v1"

type PassportID int64
type PassportRevision int64
type PassportDecimal string
type PassportDate string
type PassportSlot string
type PassportAction string
type PassportInstallationStatus string
type PassportSource string
type PassportSnapshotPurpose string

const (
	PassportSlotPump             PassportSlot               = "pump"
	PassportSlotMotor            PassportSlot               = "motor"
	PassportSlotControlStation   PassportSlot               = "control_station"
	PassportActionRegister       PassportAction             = "register"
	PassportActionUpdate         PassportAction             = "update"
	PassportActionReplace        PassportAction             = "replace"
	PassportActionCorrect        PassportAction             = "correct"
	PassportActionClose          PassportAction             = "close"
	PassportActionVoid           PassportAction             = "void"
	PassportStatusCurrent        PassportInstallationStatus = "current"
	PassportStatusClosed         PassportInstallationStatus = "closed"
	PassportStatusVoided         PassportInstallationStatus = "voided"
	PassportSourceManual         PassportSource             = "manual"
	PassportSourceExcel          PassportSource             = "excel"
	PassportPurposeTemplate      PassportSnapshotPurpose    = "template"
	PassportPurposeExportCurrent PassportSnapshotPurpose    = "export_current"
	PassportPurposeExportHistory PassportSnapshotPurpose    = "export_history"
)

var passportIntegerPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

func passportParseInteger(raw []byte, positive bool) (int64, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, fmt.Errorf("passport integer must be a decimal string: %w", err)
	}
	if !passportIntegerPattern.MatchString(s) {
		return 0, fmt.Errorf("invalid passport integer")
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || positive && v == 0 {
		return 0, fmt.Errorf("passport integer out of range")
	}
	return v, nil
}
func (v PassportID) MarshalJSON() ([]byte, error) {
	if v <= 0 {
		return nil, fmt.Errorf("passport ID must be positive")
	}
	return json.Marshal(strconv.FormatInt(int64(v), 10))
}
func (v *PassportID) UnmarshalJSON(raw []byte) error {
	n, e := passportParseInteger(raw, true)
	if e == nil {
		*v = PassportID(n)
	}
	return e
}
func (v PassportRevision) MarshalJSON() ([]byte, error) {
	if v < 0 {
		return nil, fmt.Errorf("passport revision must be nonnegative")
	}
	return json.Marshal(strconv.FormatInt(int64(v), 10))
}
func (v *PassportRevision) UnmarshalJSON(raw []byte) error {
	n, e := passportParseInteger(raw, false)
	if e == nil {
		*v = PassportRevision(n)
	}
	return e
}
func (v *PassportDecimal) UnmarshalJSON(raw []byte) error {
	var s string
	if e := json.Unmarshal(raw, &s); e != nil {
		return e
	}
	d, e := ParsePassportDecimal(s)
	if e == nil {
		*v = d
	}
	return e
}
func (v *PassportDate) UnmarshalJSON(raw []byte) error {
	var s string
	if e := json.Unmarshal(raw, &s); e != nil {
		return e
	}
	d, e := ParsePassportDate(s)
	if e == nil {
		*v = d
	}
	return e
}

// PassportAttributes содержит только паспортные значения; неизвестное — nil.
type PassportAttributes struct {
	Model                       *string          `json:"model"`
	Manufacturer                *string          `json:"manufacturer"`
	SerialNumber                *string          `json:"serial_number"`
	PumpNominalFlowM3Day        *PassportDecimal `json:"pump_nominal_flow_m3_day"`
	PumpNominalHeadM            *PassportDecimal `json:"pump_nominal_head_m"`
	PumpStageCount              *int64           `json:"pump_stage_count"`
	MotorRatedPowerKW           *PassportDecimal `json:"motor_rated_power_kw"`
	MotorRatedVoltageV          *PassportDecimal `json:"motor_rated_voltage_v"`
	MotorRatedCurrentA          *PassportDecimal `json:"motor_rated_current_a"`
	MotorNominalFrequencyHz     *PassportDecimal `json:"motor_nominal_frequency_hz"`
	ControlRatedPowerKW         *PassportDecimal `json:"control_rated_power_kw"`
	ControlRatedVoltageV        *PassportDecimal `json:"control_rated_voltage_v"`
	ControlRatedCurrentA        *PassportDecimal `json:"control_rated_current_a"`
	ControlOutputFrequencyMinHz *PassportDecimal `json:"control_output_frequency_min_hz"`
	ControlOutputFrequencyMaxHz *PassportDecimal `json:"control_output_frequency_max_hz"`
}

type PassportAttributesPatch struct {
	Model                       Field[string]          `json:"model"`
	Manufacturer                Field[string]          `json:"manufacturer"`
	SerialNumber                Field[string]          `json:"serial_number"`
	PumpNominalFlowM3Day        Field[PassportDecimal] `json:"pump_nominal_flow_m3_day"`
	PumpNominalHeadM            Field[PassportDecimal] `json:"pump_nominal_head_m"`
	PumpStageCount              Field[int64]           `json:"pump_stage_count"`
	MotorRatedPowerKW           Field[PassportDecimal] `json:"motor_rated_power_kw"`
	MotorRatedVoltageV          Field[PassportDecimal] `json:"motor_rated_voltage_v"`
	MotorRatedCurrentA          Field[PassportDecimal] `json:"motor_rated_current_a"`
	MotorNominalFrequencyHz     Field[PassportDecimal] `json:"motor_nominal_frequency_hz"`
	ControlRatedPowerKW         Field[PassportDecimal] `json:"control_rated_power_kw"`
	ControlRatedVoltageV        Field[PassportDecimal] `json:"control_rated_voltage_v"`
	ControlRatedCurrentA        Field[PassportDecimal] `json:"control_rated_current_a"`
	ControlOutputFrequencyMinHz Field[PassportDecimal] `json:"control_output_frequency_min_hz"`
	ControlOutputFrequencyMaxHz Field[PassportDecimal] `json:"control_output_frequency_max_hz"`
}

type PassportMutation struct {
	RowKey                   string           `json:"row_key"`
	WellID                   PassportID       `json:"well_id"`
	ExpectedWellRevision     PassportRevision `json:"expected_well_revision"`
	ExpectedPassportRevision PassportRevision `json:"expected_passport_revision"`
	Slot                     PassportSlot     `json:"slot"`
	Action                   PassportAction   `json:"action"`
	ComponentID              *PassportID      `json:"component_id"`
	InstallationID           *PassportID      `json:"installation_id"`
	LinkedInstallationID     *PassportID      `json:"linked_installation_id"`
	PassportAttributesPatch
	InstalledOn     Field[PassportDate] `json:"installed_on"`
	RemovedOn       Field[PassportDate] `json:"removed_on"`
	ReplacementOn   Field[PassportDate] `json:"replacement_on"`
	ChangeReason    string              `json:"change_reason"`
	SourceReference *string             `json:"source_reference"`
}

// MarshalJSON выдаёт плоские value/null/omission, а не внутренний Field.
func (m PassportMutation) MarshalJSON() ([]byte, error) {
	// Типизированные wire-поля сохраняют плоский PATCH без внутренних Field.
	wire := struct {
		RowKey                      string           `json:"row_key"`
		WellID                      PassportID       `json:"well_id"`
		ExpectedWellRevision        PassportRevision `json:"expected_well_revision"`
		ExpectedPassportRevision    PassportRevision `json:"expected_passport_revision"`
		Slot                        PassportSlot     `json:"slot"`
		Action                      PassportAction   `json:"action"`
		ComponentID                 *PassportID      `json:"component_id"`
		InstallationID              *PassportID      `json:"installation_id"`
		LinkedInstallationID        *PassportID      `json:"linked_installation_id"`
		ChangeReason                string           `json:"change_reason"`
		SourceReference             *string          `json:"source_reference"`
		Model                       *json.RawMessage `json:"model,omitempty"`
		Manufacturer                *json.RawMessage `json:"manufacturer,omitempty"`
		SerialNumber                *json.RawMessage `json:"serial_number,omitempty"`
		PumpNominalFlowM3Day        *json.RawMessage `json:"pump_nominal_flow_m3_day,omitempty"`
		PumpNominalHeadM            *json.RawMessage `json:"pump_nominal_head_m,omitempty"`
		PumpStageCount              *json.RawMessage `json:"pump_stage_count,omitempty"`
		MotorRatedPowerKW           *json.RawMessage `json:"motor_rated_power_kw,omitempty"`
		MotorRatedVoltageV          *json.RawMessage `json:"motor_rated_voltage_v,omitempty"`
		MotorRatedCurrentA          *json.RawMessage `json:"motor_rated_current_a,omitempty"`
		MotorNominalFrequencyHz     *json.RawMessage `json:"motor_nominal_frequency_hz,omitempty"`
		ControlRatedPowerKW         *json.RawMessage `json:"control_rated_power_kw,omitempty"`
		ControlRatedVoltageV        *json.RawMessage `json:"control_rated_voltage_v,omitempty"`
		ControlRatedCurrentA        *json.RawMessage `json:"control_rated_current_a,omitempty"`
		ControlOutputFrequencyMinHz *json.RawMessage `json:"control_output_frequency_min_hz,omitempty"`
		ControlOutputFrequencyMaxHz *json.RawMessage `json:"control_output_frequency_max_hz,omitempty"`
		InstalledOn                 *json.RawMessage `json:"installed_on,omitempty"`
		RemovedOn                   *json.RawMessage `json:"removed_on,omitempty"`
		ReplacementOn               *json.RawMessage `json:"replacement_on,omitempty"`
	}{RowKey: m.RowKey, WellID: m.WellID, ExpectedWellRevision: m.ExpectedWellRevision, ExpectedPassportRevision: m.ExpectedPassportRevision, Slot: m.Slot, Action: m.Action, ComponentID: m.ComponentID, InstallationID: m.InstallationID, LinkedInstallationID: m.LinkedInstallationID, ChangeReason: m.ChangeReason, SourceReference: m.SourceReference}
	wire.Model = passportFieldJSON(m.Model)
	wire.Manufacturer = passportFieldJSON(m.Manufacturer)
	wire.SerialNumber = passportFieldJSON(m.SerialNumber)
	wire.PumpNominalFlowM3Day = passportFieldJSON(m.PumpNominalFlowM3Day)
	wire.PumpNominalHeadM = passportFieldJSON(m.PumpNominalHeadM)
	wire.PumpStageCount = passportFieldJSON(m.PumpStageCount)
	wire.MotorRatedPowerKW = passportFieldJSON(m.MotorRatedPowerKW)
	wire.MotorRatedVoltageV = passportFieldJSON(m.MotorRatedVoltageV)
	wire.MotorRatedCurrentA = passportFieldJSON(m.MotorRatedCurrentA)
	wire.MotorNominalFrequencyHz = passportFieldJSON(m.MotorNominalFrequencyHz)
	wire.ControlRatedPowerKW = passportFieldJSON(m.ControlRatedPowerKW)
	wire.ControlRatedVoltageV = passportFieldJSON(m.ControlRatedVoltageV)
	wire.ControlRatedCurrentA = passportFieldJSON(m.ControlRatedCurrentA)
	wire.ControlOutputFrequencyMinHz = passportFieldJSON(m.ControlOutputFrequencyMinHz)
	wire.ControlOutputFrequencyMaxHz = passportFieldJSON(m.ControlOutputFrequencyMaxHz)
	wire.InstalledOn = passportFieldJSON(m.InstalledOn)
	wire.RemovedOn = passportFieldJSON(m.RemovedOn)
	wire.ReplacementOn = passportFieldJSON(m.ReplacementOn)
	return json.Marshal(wire)
}
func passportFieldJSON[T ~string | ~int64](f Field[T]) *json.RawMessage {
	if !f.Present {
		return nil
	}
	raw := json.RawMessage("null")
	if !f.Null {
		raw, _ = json.Marshal(f.Value)
	}
	return &raw
}

type PassportChangeSet struct {
	SchemaVersion string             `json:"schema_version"`
	Rows          []PassportMutation `json:"rows"`
}
type PassportInstallation struct {
	ID            *PassportID                `json:"id"`
	ComponentID   *PassportID                `json:"component_id"`
	Slot          PassportSlot               `json:"slot"`
	Status        PassportInstallationStatus `json:"status"`
	InstalledOn   *PassportDate              `json:"installed_on"`
	RemovedOn     *PassportDate              `json:"removed_on"`
	PredecessorID *PassportID                `json:"predecessor_id"`
	Attributes    PassportAttributes         `json:"attributes"`
}

// PassportChangeProvenance — сохранённое происхождение материального изменения.
type PassportChangeProvenance struct {
	Source           PassportSource     `json:"source"`
	Actor            PassportActor      `json:"actor"`
	RecordedAt       time.Time          `json:"recorded_at"`
	SourceReference  *string            `json:"source_reference"`
	ReceiptID        PassportID         `json:"receipt_id"`
	SourceFileSHA256 *string            `json:"source_file_sha256"`
	TemplateVersion  *string            `json:"template_version"`
	SourceRow        *PassportRowSource `json:"source_row"`
}
type PassportSlotProvenance struct {
	Slot       PassportSlot             `json:"slot"`
	LastChange PassportChangeProvenance `json:"last_change"`
}

type PassportView struct {
	Provenance   []PassportSlotProvenance `json:"provenance"`
	WellID       PassportID               `json:"well_id"`
	WellRevision PassportRevision         `json:"well_revision"`
	PassportID   *PassportID              `json:"passport_id"`
	Revision     PassportRevision         `json:"revision"`
	Slots        []PassportInstallation   `json:"slots"`
}
type PassportBasis struct {
	Well          Well
	PassportID    *PassportID
	Revision      PassportRevision
	Installations []PassportInstallation
}
type PassportDiff struct {
	RowKey string                 `json:"row_key"`
	WellID PassportID             `json:"well_id"`
	Slot   PassportSlot           `json:"slot"`
	Action PassportAction         `json:"action"`
	Before []PassportInstallation `json:"before"`
	After  []PassportInstallation `json:"after"`
}
type PassportIssue struct {
	RowKey   string  `json:"row_key"`
	Field    *string `json:"field"`
	Severity string  `json:"severity"`
	Code     string  `json:"code"`
	Message  string  `json:"message"`
}
type PassportPrepared struct {
	ChangeSet      PassportChangeSet `json:"change_set"`
	Diff           []PassportDiff    `json:"diff"`
	Issues         []PassportIssue   `json:"issues"`
	ChangedCount   int               `json:"changed_count"`
	UnchangedCount int               `json:"unchanged_count"`
}
type PassportRowSource struct {
	RowKey string `json:"row_key"`
	Sheet  string `json:"sheet"`
	Index  int    `json:"index"`
}
type PassportProvenance struct {
	Source           PassportSource      `json:"source"`
	SourceFileSHA256 *string             `json:"source_file_sha256"`
	TemplateVersion  *string             `json:"template_version"`
	Rows             []PassportRowSource `json:"rows"`
}
type PassportPreview struct {
	ID             PassportID        `json:"id"`
	Digest         string            `json:"digest"`
	ExpiresAt      time.Time         `json:"expires_at"`
	Commands       PassportChangeSet `json:"commands"`
	Diff           []PassportDiff    `json:"diff"`
	Issues         []PassportIssue   `json:"issues"`
	ChangedCount   int               `json:"changed_count"`
	UnchangedCount int               `json:"unchanged_count"`
}
type PassportActor struct {
	AccountID PassportID `json:"account_id"`
	Name      string     `json:"name"`
}
type PassportCommitInput struct {
	PreviewID           PassportID
	Digest              string
	AcknowledgeWarnings bool
	IdempotencyKey      string
}
type PassportWellRevision struct {
	WellID         PassportID       `json:"well_id"`
	BeforeRevision PassportRevision `json:"before_revision"`
	AfterRevision  PassportRevision `json:"after_revision"`
}
type PassportReceiptRow struct {
	RowKey         string     `json:"row_key"`
	WellID         PassportID `json:"well_id"`
	ComponentID    PassportID `json:"component_id"`
	InstallationID PassportID `json:"installation_id"`
}
type PassportReceipt struct {
	ID               PassportID             `json:"id"`
	RequestID        string                 `json:"request_id"`
	RecordedAt       time.Time              `json:"recorded_at"`
	Actor            PassportActor          `json:"actor"`
	Source           PassportSource         `json:"source"`
	SourceFileSHA256 *string                `json:"source_file_sha256"`
	ChangedCount     int                    `json:"changed_count"`
	UnchangedCount   int                    `json:"unchanged_count"`
	Wells            []PassportWellRevision `json:"wells"`
	Rows             []PassportReceiptRow   `json:"rows"`
}
type PassportEvent struct {
	ReceiptID        PassportID         `json:"receipt_id"`
	SourceFileSHA256 *string            `json:"source_file_sha256"`
	TemplateVersion  *string            `json:"template_version"`
	SourceRow        *PassportRowSource `json:"source_row"`
	ID               PassportID         `json:"id"`
	WellID           PassportID         `json:"well_id"`
	Revision         PassportRevision   `json:"revision"`
	RowKey           string             `json:"row_key"`
	Action           PassportAction     `json:"action"`
	Slot             PassportSlot       `json:"slot"`
	RecordedAt       time.Time          `json:"recorded_at"`
	Actor            PassportActor      `json:"actor"`
	Source           PassportSource     `json:"source"`
	SourceReference  *string            `json:"source_reference"`
	Reason           string             `json:"reason"`
	Diff             PassportDiff       `json:"diff"`
}
type PassportWorkbook struct {
	Rows []PassportRowSource

	ChangeSet  PassportChangeSet
	FileSHA256 string
}
type PassportExportRequest struct {
	WellIDs []PassportID `json:"well_ids"`
	Mode    string       `json:"mode"`
	Query   TableQuery   `json:"query"`
}
type PassportRowProvenance struct {
	RowKey          string         `json:"row_key"`
	Source          PassportSource `json:"source"`
	ActorID         PassportID     `json:"actor_id"`
	RecordedAt      time.Time      `json:"recorded_at"`
	ReceiptID       PassportID     `json:"receipt_id"`
	SourceReference *string        `json:"source_reference"`
}
type PassportExport struct {
	Mode           string
	Template       bool
	ExportedAt     time.Time
	Current        []PassportView
	History        []PassportEvent
	Provenance     []PassportRowProvenance
	EmptySlotCount int
}
type PassportPolicy struct {
	AppScope      string
	CohortVersion string
}

// PassportError сохраняет feature code и issues без изменения общего mapper.
type PassportError struct {
	Err    *Error
	Code   string
	Issues []PassportIssue
}

func (e *PassportError) Error() string { return e.Err.Error() }
func (e *PassportError) Unwrap() error { return e.Err }

// PassportEnrollmentInput — stdin maintenance intent, не session claims.
type PassportEnrollmentInput struct {
	RequestID               string           `json:"request_id"`
	TargetAccountID         PassportID       `json:"target_account_id"`
	ExpectedAccountRevision PassportRevision `json:"expected_account_revision"`
	ExpectedEpoch           PassportRevision `json:"expected_epoch"`
	ExpectedGraphRevision   PassportRevision `json:"expected_graph_revision"`
	PermissionKeys          []Permission     `json:"permission_keys"`
}
type PassportEnrollmentReceipt struct {
	RequestID       string                `json:"request_id"`
	ActorKind       string                `json:"actor_kind"`
	TargetAccountID PassportID            `json:"target_account_id"`
	Before          AuthorizationSnapshot `json:"before"`
	After           AuthorizationSnapshot `json:"after"`
	Digest          string                `json:"digest"`
	RecordedAt      time.Time             `json:"recorded_at"`
}

func NewPassportError(kind Kind, code, message string, issues []PassportIssue) error {
	return &PassportError{Err: &Error{Kind: kind, Message: message}, Code: code, Issues: issues}
}
