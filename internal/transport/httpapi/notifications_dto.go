package httpapi

import (
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func notificationText(s string) string {
	r := []rune(s)
	if len(r) > 256 {
		return string(r[:256])
	}
	return s
}
func notificationTextPtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := notificationText(*s)
	return &v
}
func notificationTime(t time.Time) string { return t.UTC().Format(service.NotificationTimeLayout) }
func notificationTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := notificationTime(*t)
	return &v
}
func notificationOptional(s string) *string {
	if s == "" {
		return nil
	}
	v := notificationText(s)
	return &v
}

type notificationWellDTO struct {
	ID         string  `json:"id"`
	Number     *string `json:"number"`
	Active     bool    `json:"active"`
	ArchivedAt *string `json:"archived_at"`
}
type notificationTargetDTO interface{ notificationTarget() }
type notificationAlertTargetDTO struct {
	Kind      string              `json:"kind"`
	Well      notificationWellDTO `json:"well"`
	RunID     string              `json:"run_id"`
	AlertID   string              `json:"alert_id"`
	Risk      *float64            `json:"risk"`
	WindowEnd *string             `json:"window_end"`
}

func (notificationAlertTargetDTO) notificationTarget() {}

type notificationCollectionTargetDTO struct {
	Kind            string `json:"kind"`
	SourceID        string `json:"source_id"`
	SourceName      string `json:"source_name"`
	CollectionLogID string `json:"collection_log_id"`
}

func (notificationCollectionTargetDTO) notificationTarget() {}

type notificationItemDTO struct {
	ID         string                  `json:"notification_id"`
	Kind       domain.NotificationKind `json:"kind"`
	Code       domain.NotificationCode `json:"code"`
	Text       string                  `json:"text"`
	RecordedAt string                  `json:"recorded_at"`
	ReadAt     *string                 `json:"read_at"`
	Target     notificationTargetDTO   `json:"target"`
}
type notificationPageDTO struct {
	Items      []notificationItemDTO `json:"items"`
	NextCursor *string               `json:"next_cursor"`
}
type notificationCapabilitiesDTO struct {
	Enabled  bool                        `json:"enabled"`
	Reason   string                      `json:"reason"`
	Families []domain.NotificationFamily `json:"families"`
}
type notificationCountDTO struct {
	Value  int  `json:"value"`
	Capped bool `json:"capped"`
}
type notificationReadDTO struct {
	ID     string `json:"notification_id"`
	ReadAt string `json:"read_at"`
}

func notificationAlertTarget(v domain.NotificationAlertTarget) (notificationAlertTargetDTO, error) {
	if (domain.NotificationAlertLocator{WellID: v.Well.ID, RunID: v.RunID, AlertID: v.AlertID}).Validate() != nil || (v.Risk != nil && (math.IsNaN(*v.Risk) || math.IsInf(*v.Risk, 0))) {
		return notificationAlertTargetDTO{}, notificationHTTPUnavailable()
	}
	return notificationAlertTargetDTO{"alert", notificationWellDTO{strconv.FormatInt(v.Well.ID, 10), notificationTextPtr(v.Well.Number), v.Well.Active, notificationTimePtr(v.Well.ArchivedAt)}, strconv.FormatInt(v.RunID, 10), strconv.FormatInt(v.AlertID, 10), v.Risk, notificationTimePtr(v.WindowEnd)}, nil
}
func notificationItem(v domain.NotificationRecord) (notificationItemDTO, error) {
	if v.Validate() != nil {
		return notificationItemDTO{}, notificationHTTPUnavailable()
	}
	id, e := service.EncodeNotificationID(v.Key.ID)
	if e != nil {
		return notificationItemDTO{}, notificationHTTPUnavailable()
	}
	out := notificationItemDTO{ID: id, Kind: v.Kind, Code: v.Code, RecordedAt: notificationTime(v.Key.RecordedAt), ReadAt: notificationTimePtr(v.ReadAt)}
	switch v.Kind {
	case domain.NotificationKindAlertCreated:
		out.Text = "Тревога нового прогона"
		out.Target, e = notificationAlertTarget(*v.Alert)
	case domain.NotificationKindCollectionFailed:
		out.Text = "Попытка сбора завершилась ошибкой"
		c := v.Collection
		if c.SourceID == "" || !notificationSourceURLFits(c.SourceID) {
			return out, notificationHTTPUnavailable()
		}
		out.Target = notificationCollectionTargetDTO{"collection", c.SourceID, notificationText(c.SourceName), strconv.FormatInt(c.CollectionLogID, 10)}
	}
	return out, e
}

type notificationRunDTO struct {
	ID         string  `json:"id"`
	Profile    string  `json:"profile"`
	StartedAt  string  `json:"started_at"`
	FinishedAt *string `json:"finished_at"`
}
type notificationSavedResultDTO struct {
	Verdict      string  `json:"verdict"`
	ModelName    *string `json:"model_name"`
	ModelVersion *string `json:"model_version"`
}
type notificationAlertContextDTO struct {
	Target      notificationAlertTargetDTO  `json:"target"`
	Run         notificationRunDTO          `json:"run"`
	AlertStatus string                      `json:"alert_status"`
	SnapshotID  *string                     `json:"snapshot_id"`
	SavedResult *notificationSavedResultDTO `json:"saved_result"`
}

func notificationAlertContext(v domain.NotificationAlertContext) (notificationAlertContextDTO, error) {
	target, e := notificationAlertTarget(v.Target)
	out := notificationAlertContextDTO{Target: target, Run: notificationRunDTO{strconv.FormatInt(v.Target.RunID, 10), v.RunProfile, notificationTime(v.RunStartedAt), notificationTimePtr(v.RunFinishedAt)}, AlertStatus: v.AlertStatus, SnapshotID: v.SnapshotID}
	if e != nil || !slices.Contains([]string{"research", "cohort"}, v.RunProfile) {
		return out, notificationHTTPUnavailable()
	}
	if !slices.Contains([]string{"new", "confirmed", "false_positive", "unknown"}, out.AlertStatus) {
		out.AlertStatus = "unknown"
	}
	if v.SavedResult != nil {
		r := v.SavedResult
		if !slices.Contains([]string{"watch", "alert"}, r.Verdict) {
			return out, notificationHTTPUnavailable()
		}
		out.SavedResult = &notificationSavedResultDTO{r.Verdict, notificationOptional(r.ModelName), notificationOptional(r.ModelVersion)}
	}
	return out, nil
}

type notificationSourceDTO struct {
	ID              string                   `json:"id"`
	Name            string                   `json:"name"`
	Kind            string                   `json:"kind"`
	Status          string                   `json:"status"`
	IntervalSeconds int                      `json:"interval_seconds"`
	LastReceived    *string                  `json:"last_received"`
	LastMeasured    *string                  `json:"last_measured"`
	ErrorCode       *domain.NotificationCode `json:"error_code"`
}

func notificationSource(v domain.NotificationSourceOverview) (notificationSourceDTO, error) {
	out := notificationSourceDTO{v.ID, notificationText(v.Name), v.Kind, v.Status, v.IntervalSeconds, notificationTimePtr(v.LastReceived), notificationTimePtr(v.LastMeasured), v.ErrorCode}
	if !notificationSourceURLFits(v.ID) {
		return out, notificationHTTPUnavailable()
	}
	if !slices.Contains([]string{"json_pull", "json_push", "clickhouse", "postgres", "mysql", "file"}, v.Kind) {
		out.Kind = "unknown"
	}
	if !slices.Contains([]string{"draft", "running", "paused", "error"}, v.Status) {
		out.Status = "unknown"
	}
	if out.ErrorCode != nil && *out.ErrorCode != domain.NotificationCodeCollectionFailed {
		return out, notificationHTTPUnavailable()
	}
	return out, nil
}
