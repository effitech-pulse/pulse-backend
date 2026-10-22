package domain

import "time"

// EventLocator обозначает точное сохранённое основание, а не право доступа.
type EventLocator struct {
	Source, Target string
	Revision       int64
}
type EventKey struct {
	OccurredAt time.Time
	SourceRank int16
	TargetKey  string
	Revision   int64
}
type EventFilter struct {
	Limit    int
	Types    []string
	WellID   *int64
	From, To *time.Time
	Archive  string
}
type EventCohortFence struct {
	Version, Manifest string
	IDs               []int64
}
type EventReadScope struct {
	Principal Principal
	Mode      string
	Cohort    *EventCohortFence
}
type EventReadQuery struct {
	Scope  EventReadScope
	Filter EventFilter
	Before *EventKey
}

// EventActorSnapshot сохраняет отсутствие authenticated отдельно от false.
type EventActorSnapshot struct {
	ID            *string `json:"id"`
	Name          *string `json:"name"`
	Authenticated *bool   `json:"authenticated"`
}
type EventActor struct {
	Kind          string
	ID, Name      *string
	Authenticated *bool
}
type EventOutcome struct{ Verdict, ReviewStatus, Conclusion *string }
type EventRecord struct {
	Key                               EventKey
	Locator                           EventLocator
	Well                              Well
	ActorSnapshot                     *EventActorSnapshot
	Verdict, ReviewStatus, Conclusion *string
	ChangeKind                        string
	Check                             *ManualCheck
	Review                            *ReviewEvent
	WellRevision                      *WellEvent
}
type EventSummary struct {
	EventID, EventType string
	OccurredAt         time.Time
	Well               Well
	Target             EventLocator
	Actor              EventActor
	Outcome            EventOutcome
}
type EventPage struct {
	Items      []EventSummary
	HasMore    bool
	NextCursor *string
}
type EventDetail struct {
	Event  EventSummary
	Record EventRecord
}

// EventReadError добавляет только публичную причину перезапуска курсора.
type EventReadError struct {
	Cause  error
	Detail string
}

func (e *EventReadError) Error() string { return e.Cause.Error() }
func (e *EventReadError) Unwrap() error { return e.Cause }
