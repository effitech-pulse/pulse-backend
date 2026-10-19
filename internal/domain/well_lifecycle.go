package domain

import "time"

type WellLifecycleInput struct {
	Revision int64  `json:"revision"`
	Reason   string `json:"reason"`
}
type WellEvent struct {
	Revision  int64       `json:"revision"`
	CreatedAt time.Time   `json:"created_at"`
	Kind      string      `json:"kind"`
	Actor     ReviewActor `json:"actor"`
	Reason    string      `json:"reason"`
	Before    Well        `json:"before"`
	After     Well        `json:"after"`
}
type WellHistory struct {
	Events  []WellEvent `json:"events"`
	HasMore bool        `json:"has_more"`
}
