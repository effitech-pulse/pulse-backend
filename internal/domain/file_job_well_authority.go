package domain

import "time"

// FileJobWellAuthority фиксирует принятую identity отдельно от ML input identity.
type FileJobWellAuthority struct {
	Version    string     `json:"version"`
	WellID     int64      `json:"well_id"`
	Revision   int64      `json:"revision"`
	FundType   string     `json:"fund_type"`
	NGDU       int64      `json:"ngdu"`
	Department int64      `json:"department"`
	Active     bool       `json:"active"`
	ArchivedAt *time.Time `json:"archived_at"`
	UnitekID   *int64     `json:"unitek_id"`
	ArmitsID   *int64     `json:"armits_id"`
}

func FileJobWellAuthorityFrom(w Well) (FileJobWellAuthority, error) {
	if w.ID <= 0 || w.Revision <= 0 || (w.FundType != "operational" && w.FundType != "presentation") {
		return FileJobWellAuthority{}, NewError(Invalid, "invalid_file_job_well_authority")
	}
	a := FileJobWellAuthority{Version: "file_job_well_v1", WellID: w.ID, Revision: w.Revision, FundType: w.FundType, NGDU: w.NGDU, Department: w.Department, Active: w.Active}
	if w.ArchivedAt != nil {
		v := w.ArchivedAt.UTC()
		a.ArchivedAt = &v
	}
	if w.UnitekID != nil {
		v := *w.UnitekID
		a.UnitekID = &v
	}
	if w.ArmitsID != nil {
		v := *w.ArmitsID
		a.ArmitsID = &v
	}
	return a, nil
}
func sameAuthorityID(a, b *int64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
func sameAuthorityTime(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}
func (a FileJobWellAuthority) Matches(w Well) bool {
	return a.Version == "file_job_well_v1" && a.WellID == w.ID && a.Revision == w.Revision && a.FundType == w.FundType && a.NGDU == w.NGDU && a.Department == w.Department && a.Active == w.Active && sameAuthorityTime(a.ArchivedAt, w.ArchivedAt) && sameAuthorityID(a.UnitekID, w.UnitekID) && sameAuthorityID(a.ArmitsID, w.ArmitsID)
}
