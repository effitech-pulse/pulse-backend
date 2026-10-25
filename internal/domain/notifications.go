package domain

import "time"

// NotificationKind задаёт закрытый тип опубликованного факта.
type NotificationKind string

const (
	NotificationKindAlertCreated     NotificationKind = "alert.created.v1"
	NotificationKindCollectionFailed NotificationKind = "collection.failed.v1"
)

// NotificationFamily определяет семейство с производными правами доступа.
type NotificationFamily string

const (
	NotificationFamilyAlerts     NotificationFamily = "alerts"
	NotificationFamilyCollection NotificationFamily = "collection"
)

// NotificationState задаёт фильтр персональной ленты.
type NotificationState string

const (
	NotificationStateUnread NotificationState = "unread"
	NotificationStateAll    NotificationState = "all"
)

// NotificationCode выбирает безопасное сообщение без исходного payload.
type NotificationCode string

const (
	NotificationCodeAlertCreated     NotificationCode = "alert_created"
	NotificationCodeCollectionFailed NotificationCode = "collection_failed"
)

// CollectionOutcome отличает подтверждённый исход от неизвестной истории.
type CollectionOutcome string

const (
	CollectionOutcomeHistoricalUnknown CollectionOutcome = "historical_unknown"
	CollectionOutcomeCompleted         CollectionOutcome = "completed"
	CollectionOutcomeFailed            CollectionOutcome = "failed"
)

type NotificationActivation struct {
	ID          string
	ActivatedAt time.Time
}

type NotificationKey struct {
	RecordedAt time.Time
	ID         int64
}

type NotificationCohortFence struct {
	Version        string
	ManifestSHA256 string
	WellIDs        []int64
}

type NotificationScope struct {
	Principal Principal
	Mode      string
	Cohort    *NotificationCohortFence
}

type NotificationBoundary struct {
	AccountID           int64
	Epoch               int64
	GraphRevision       int64
	ActivationID        string
	AuthorizationSHA256 string
	CohortSHA256        string
}

type NotificationPosition struct {
	Key      NotificationKey
	Boundary NotificationBoundary
}

type NotificationQuery struct {
	Scope  NotificationScope
	State  NotificationState
	Limit  int
	Before *NotificationPosition
}

type NotificationWell struct {
	ID         int64
	Number     *string
	Active     bool
	ArchivedAt *time.Time
}

type NotificationAlertTarget struct {
	Well      NotificationWell
	RunID     int64
	AlertID   int64
	Risk      *float64
	WindowEnd *time.Time
}

type NotificationCollectionTarget struct {
	SourceID        string
	SourceName      string
	CollectionLogID int64
}

// NotificationRecord содержит ровно одну цель, соответствующую Kind и Code.
// Проверка структуры не предоставляет права доступа к цели.
type NotificationRecord struct {
	Key        NotificationKey
	Kind       NotificationKind
	Code       NotificationCode
	ReadAt     *time.Time
	Alert      *NotificationAlertTarget
	Collection *NotificationCollectionTarget
}

// Validate проверяет закрытое семейство и точные положительные ID.
func (n NotificationRecord) Validate() error {
	if n.Key.ID <= 0 {
		return NewError(Invalid, "Некорректный идентификатор уведомления.")
	}
	switch n.Kind {
	case NotificationKindAlertCreated:
		if n.Code != NotificationCodeAlertCreated || n.Alert == nil || n.Collection != nil {
			return NewError(Invalid, "Некорректная цель уведомления о тревоге.")
		}
		return (NotificationAlertLocator{WellID: n.Alert.Well.ID, RunID: n.Alert.RunID, AlertID: n.Alert.AlertID}).Validate()
	case NotificationKindCollectionFailed:
		if n.Code != NotificationCodeCollectionFailed || n.Collection == nil || n.Alert != nil {
			return NewError(Invalid, "Некорректная цель уведомления о сборе.")
		}
		if n.Collection.CollectionLogID <= 0 {
			return NewError(Invalid, "Некорректный идентификатор попытки сбора.")
		}
		return nil
	default:
		return NewError(Invalid, "Неизвестный тип уведомления.")
	}
}

type NotificationCapabilities struct {
	Enabled  bool
	Reason   string
	Families []NotificationFamily
}

type NotificationRecords struct {
	Items    []NotificationRecord
	Boundary NotificationBoundary
}

type NotificationPage struct {
	Items      []NotificationRecord
	NextCursor *string
}

type NotificationCount struct {
	Value  int
	Capped bool
}

type NotificationRead struct {
	ID     int64
	ReadAt time.Time
}

// NotificationAlertLocator указывает на исходную сохранённую тревогу прогона.
type NotificationAlertLocator struct {
	WellID  int64
	RunID   int64
	AlertID int64
}

// Validate проверяет значения int64 без сужения и преобразования через float.
func (t NotificationAlertLocator) Validate() error {
	if t.WellID <= 0 || t.RunID <= 0 || t.AlertID <= 0 {
		return NewError(Invalid, "Некорректный идентификатор сохранённой тревоги.")
	}
	return nil
}

type NotificationSavedResult struct {
	Verdict      string
	ModelName    string
	ModelVersion string
}

type NotificationAlertContext struct {
	Target        NotificationAlertTarget
	RunProfile    string
	RunStartedAt  time.Time
	RunFinishedAt *time.Time
	AlertStatus   string
	SnapshotID    *string
	SavedResult   *NotificationSavedResult
}

// NotificationSourceOverview содержит только разрешённые текущие поля источника.
type NotificationSourceOverview struct {
	ID              string
	Name            string
	Kind            string
	Status          string
	IntervalSeconds int
	LastReceived    *time.Time
	LastMeasured    *time.Time
	ErrorCode       *NotificationCode
}
