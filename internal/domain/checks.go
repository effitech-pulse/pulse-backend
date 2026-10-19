package domain

import "time"

// ManualCheck хранит неизменяемый результат ручной проверки телеметрии.
type ManualCheck struct {
	ID          string       `json:"id"`
	WellID      int64        `json:"well_id"`
	RequestID   string       `json:"request_id"`
	Start       time.Time    `json:"start"`
	End         time.Time    `json:"end"`
	CreatedAt   time.Time    `json:"created_at"`
	InputSHA256 string       `json:"input_sha256"`
	PointsCount int          `json:"points_count"`
	Resolution  string       `json:"resolution"`
	Result      UploadResult `json:"result"`
}

type CheckInput struct {
	RequestID string    `json:"request_id"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
}
