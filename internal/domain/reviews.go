package domain

import "time"

// ReviewActor задаётся сервером; имя из тела запроса не является авторизацией.
type ReviewActor struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Authenticated bool   `json:"authenticated"`
}
type ReviewState struct {
	Revision   int64      `json:"revision"`
	Status     string     `json:"status"`
	Owner      string     `json:"owner"`
	Conclusion string     `json:"conclusion"`
	UpdatedAt  *time.Time `json:"updated_at"`
}
type ReviewTarget struct {
	Kind         string
	ID           string
	WellID       *int64
	FileJobID    *string
	CheckID      *string
	AlertID      *int64
	LegacyStatus string
	Initial      ReviewState
}
type ReviewInput struct {
	RequestID  string `json:"request_id"`
	Revision   int64  `json:"revision"`
	Status     string `json:"status"`
	Owner      string `json:"owner"`
	Conclusion string `json:"conclusion"`
	Comment    string `json:"comment"`
}
type ReviewEvent struct {
	Revision    int64       `json:"revision"`
	CreatedAt   time.Time   `json:"created_at"`
	Actor       ReviewActor `json:"actor"`
	Comment     string      `json:"comment"`
	Before      ReviewState `json:"before"`
	After       ReviewState `json:"after"`
	Fingerprint string      `json:"-"`
}
type Review struct {
	State   ReviewState   `json:"state"`
	Events  []ReviewEvent `json:"events"`
	HasMore bool          `json:"has_more"`
}
