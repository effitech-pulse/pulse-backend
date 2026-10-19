package domain

import "encoding/json"

// Field сохраняет различие отсутствующего ключа, null и валидного нуля.
type Field[T any] struct {
	Present bool
	Null    bool
	Value   T
}

func (f *Field[T]) UnmarshalJSON(raw []byte) error {
	f.Present = true
	f.Null = string(raw) == "null"
	if f.Null {
		return nil
	}
	return json.Unmarshal(raw, &f.Value)
}

type WellInput struct {
	Responsible *string     `json:"responsible"`
	FundType    *string     `json:"fund_type"`
	ID          *int64      `json:"id"`
	UnitekID    *int64      `json:"unitek_id"`
	ArmitsID    *int64      `json:"armits_id"`
	WellNumber  *string     `json:"well_number"`
	NGDU        *int64      `json:"ngdu"`
	Department  *int64      `json:"department"`
	Active      Field[bool] `json:"active"`
}

type WellPatch struct {
	Revision    Field[int64]  `json:"revision"`
	Responsible Field[string] `json:"responsible"`
	UnitekID    Field[int64]  `json:"unitek_id"`
	ArmitsID    Field[int64]  `json:"armits_id"`
	WellNumber  Field[string] `json:"well_number"`
	NGDU        Field[int64]  `json:"ngdu"`
	Department  Field[int64]  `json:"department"`
	Active      Field[bool]   `json:"active"`
}

type ReferenceInput struct {
	ID   *int64  `json:"id"`
	Name *string `json:"name"`
}

type IncidentInput struct {
	FailureType *string `json:"failure_type"`
	FailureDate *string `json:"failure_date"`
	DaysBefore  *int64  `json:"days_before"`
}

type ImportResult struct {
	Created int      `json:"created"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors"`
}

type RunFilter struct {
	DAGID      *string
	WellID     *int64
	StatusType *string
	Limit      int
}

type ReferenceKind string

const (
	NGDUReference       ReferenceKind = "ngdu"
	DepartmentReference ReferenceKind = "department"
)
