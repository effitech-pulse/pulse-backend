package domain

import "encoding/json"

// TableQuery задаёт условия для полного разрешённого набора до пагинации.
type TableQuery struct {
	Version int           `json:"version"`
	Filters []TableFilter `json:"filters"`
	Sort    []TableSort   `json:"sort"`
}
type TableFilter struct {
	Field  string            `json:"field"`
	Op     string            `json:"op"`
	Value  *string           `json:"value,omitempty"`
	Values []json.RawMessage `json:"values,omitempty"`
	From   json.RawMessage   `json:"from,omitempty"`
	To     json.RawMessage   `json:"to,omitempty"`
}
type TableSort struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}
type TableRequest struct {
	Query       TableQuery
	PageSize    int
	After       []json.RawMessage
	Fingerprint string
}
type TableRows[T any] struct {
	Items   []T
	Keys    []json.RawMessage
	HasMore bool
}
type TablePage[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

// TableCollectionLog добавляет стабильный ключ только opt-in представлению журнала.
type TableCollectionLog struct {
	CollectionLog
	ID int64 `json:"id,string"`
}
type TableWellEvent struct {
	WellEvent
	Changes string `json:"changes"`
}
