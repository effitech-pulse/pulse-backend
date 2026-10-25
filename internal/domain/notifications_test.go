package domain

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestNotificationRecordRejectsMixedTargets(t *testing.T) {
	alert := &NotificationAlertTarget{Well: NotificationWell{ID: 9007199254740993}, RunID: 9007199254740995, AlertID: math.MaxInt64}
	collection := &NotificationCollectionTarget{SourceID: "legacy/a%? кир", CollectionLogID: math.MaxInt64}
	for _, tc := range []struct {
		name       string
		kind       NotificationKind
		code       NotificationCode
		alert      *NotificationAlertTarget
		collection *NotificationCollectionTarget
		valid      bool
	}{
		{"alert", NotificationKindAlertCreated, NotificationCodeAlertCreated, alert, nil, true},
		{"collection", NotificationKindCollectionFailed, NotificationCodeCollectionFailed, nil, collection, true},
		{"mixed alert", NotificationKindAlertCreated, NotificationCodeAlertCreated, alert, collection, false},
		{"mixed collection", NotificationKindCollectionFailed, NotificationCodeCollectionFailed, alert, collection, false},
		{"no target", NotificationKindAlertCreated, NotificationCodeAlertCreated, nil, nil, false},
		{"wrong alert target", NotificationKindAlertCreated, NotificationCodeAlertCreated, nil, collection, false},
		{"wrong collection target", NotificationKindCollectionFailed, NotificationCodeCollectionFailed, alert, nil, false},
		{"wrong code", NotificationKindAlertCreated, NotificationCodeCollectionFailed, alert, nil, false},
		{"unknown code", NotificationKindCollectionFailed, "future", nil, collection, false},
		{"unknown kind", "future", NotificationCodeAlertCreated, alert, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := NotificationRecord{Key: NotificationKey{ID: math.MaxInt64, RecordedAt: time.Now()}, Kind: tc.kind, Code: tc.code, Alert: tc.alert, Collection: tc.collection}
			err := n.Validate()
			if (err == nil) != tc.valid {
				t.Fatalf("Validate() = %v, valid = %v", err, tc.valid)
			}
			if err != nil {
				var e *Error
				if !errors.As(err, &e) || e.Kind != Invalid {
					t.Fatalf("expected typed validation error, got %v", err)
				}
			}
		})
	}
}

func TestNotificationRecordPreservesNullableRiskAndZero(t *testing.T) {
	zero := 0.0
	for _, risk := range []*float64{nil, &zero} {
		n := NotificationRecord{Key: NotificationKey{ID: 1}, Kind: NotificationKindAlertCreated, Code: NotificationCodeAlertCreated, Alert: &NotificationAlertTarget{Well: NotificationWell{ID: 1}, RunID: 2, AlertID: 3, Risk: risk}}
		if err := n.Validate(); err != nil {
			t.Fatal(err)
		}
		if n.Alert.Risk != risk || n.Alert.WindowEnd != nil || n.ReadAt != nil || n.Alert.Well.Active {
			t.Fatal("validation changed nullable or false values")
		}
		if risk != nil && *n.Alert.Risk != 0 {
			t.Fatal("zero risk changed")
		}
	}
}

func TestNotificationLocatorValidation(t *testing.T) {
	for _, id := range []int64{1, 9007199254740993, math.MaxInt64} {
		if err := (NotificationAlertLocator{WellID: id, RunID: id, AlertID: id}).Validate(); err != nil {
			t.Fatalf("exact int64 %d rejected: %v", id, err)
		}
	}
	for _, id := range []int64{0, -1, math.MinInt64} {
		for _, target := range []NotificationAlertLocator{{WellID: id, RunID: 1, AlertID: 1}, {WellID: 1, RunID: id, AlertID: 1}, {WellID: 1, RunID: 1, AlertID: id}} {
			if err := target.Validate(); err == nil {
				t.Fatalf("invalid locator %+v accepted", target)
			}
		}
	}
}

func TestNotificationRecordRejectsNonpositiveIDs(t *testing.T) {
	for _, id := range []int64{0, -1} {
		for _, n := range []NotificationRecord{
			{Key: NotificationKey{ID: id}, Kind: NotificationKindCollectionFailed, Code: NotificationCodeCollectionFailed, Collection: &NotificationCollectionTarget{SourceID: "a", CollectionLogID: 1}},
			{Key: NotificationKey{ID: 1}, Kind: NotificationKindCollectionFailed, Code: NotificationCodeCollectionFailed, Collection: &NotificationCollectionTarget{SourceID: "a", CollectionLogID: id}},
			{Key: NotificationKey{ID: 1}, Kind: NotificationKindAlertCreated, Code: NotificationCodeAlertCreated, Alert: &NotificationAlertTarget{Well: NotificationWell{ID: id}, RunID: 1, AlertID: 1}},
			{Key: NotificationKey{ID: 1}, Kind: NotificationKindAlertCreated, Code: NotificationCodeAlertCreated, Alert: &NotificationAlertTarget{Well: NotificationWell{ID: 1}, RunID: id, AlertID: 1}},
			{Key: NotificationKey{ID: 1}, Kind: NotificationKindAlertCreated, Code: NotificationCodeAlertCreated, Alert: &NotificationAlertTarget{Well: NotificationWell{ID: 1}, RunID: 1, AlertID: id}},
		} {
			if n.Validate() == nil {
				t.Fatalf("nonpositive ID accepted: %+v", n)
			}
		}
	}
}
