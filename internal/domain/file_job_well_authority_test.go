package domain

import (
	"testing"
	"time"
)

func TestFileJobWellAuthorityValueIdentity(t *testing.T) {
	external := int64(-9007199254740993)
	zero := int64(0)
	instant := time.Date(2026, 1, 2, 3, 4, 5, 123000, time.FixedZone("offset", 3*3600))
	w := Well{ID: 1, Revision: 2, FundType: "operational", NGDU: -1, Department: 0, Active: true, ArchivedAt: &instant, UnitekID: &external, ArmitsID: &zero}
	a, err := FileJobWellAuthorityFrom(w)
	if err != nil {
		t.Fatal(err)
	}
	equal := w
	copyID := external
	utc := instant.UTC()
	equal.UnitekID = &copyID
	equal.ArchivedAt = &utc
	if !a.Matches(equal) {
		t.Fatal("equal value/instant rejected")
	}
	external = 7
	instant = instant.Add(time.Second)
	if a.Matches(w) {
		t.Fatal("snapshot aliases caller")
	}
	equal.Revision++
	if a.Matches(equal) {
		t.Fatal("ABA revision accepted")
	}
	equal.Revision--
	equal.ArmitsID = nil
	if a.Matches(equal) {
		t.Fatal("null conflated with zero")
	}
}

func TestFileJobWellAuthorityFieldMatrix(t *testing.T) {
	base := Well{ID: 1, Revision: 1, FundType: "operational", NGDU: -1, Department: 0, Active: true}
	a, err := FileJobWellAuthorityFrom(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"id", "revision", "fund", "ngdu", "department", "active", "archived", "unitek", "armits"} {
		t.Run(kind, func(t *testing.T) {
			w := base
			external := int64(0)
			now := time.Now().UTC()
			switch kind {
			case "id":
				w.ID++
			case "revision":
				w.Revision++
			case "fund":
				w.FundType = "presentation"
			case "ngdu":
				w.NGDU = 1
			case "department":
				w.Department = 1
			case "active":
				w.Active = false
			case "archived":
				w.ArchivedAt = &now
			case "unitek":
				w.UnitekID = &external
			case "armits":
				w.ArmitsID = &external
			}
			if a.Matches(w) {
				t.Fatal("changed identity accepted", kind)
			}
		})
	}
	if !a.Matches(base) {
		t.Fatal("unchanged identity rejected")
	}
}
