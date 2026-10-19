package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func ptr[T any](v T) *T {
	return &v
}

type wellStore struct {
	Store
	created domain.Well
}

func (s *wellStore) CreateWell(ctx context.Context, w domain.Well) (domain.Well, error) {
	s.created = w
	w.ID = 12
	return w, nil
}

func TestCreateWellPreservesValidZeroAndDefaults(t *testing.T) {
	store := &wellStore{}
	catalog := NewCatalog(store)
	w, err := catalog.CreateWell(context.Background(), domain.WellInput{UnitekID: ptr(int64(0)), ArmitsID: ptr(int64(21)), NGDU: ptr(int64(0)), Department: ptr(int64(0))})
	if err != nil {
		t.Fatal(err)
	}
	if w.ID != 12 || w.UnitekID == nil || *w.UnitekID != 0 || w.NGDU != 0 || !w.Active {
		t.Fatal(w)
	}
}

func TestCreateRejectsMissingRequiredFieldsBeforeStore(t *testing.T) {
	_, err := NewCatalog(&wellStore{}).CreateWell(context.Background(), domain.WellInput{})
	if err == nil {
		t.Fatal("missing identifiers accepted")
	}
}

func TestPatchDistinguishesMissingNullAndZero(t *testing.T) {
	var p domain.WellPatch
	if err := json.Unmarshal([]byte(`{"ngdu":0,"well_number":null,"active":false}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.UnitekID.Present || !p.NGDU.Present || p.NGDU.Value != 0 || !p.WellNumber.Null || p.Active.Value {
		t.Fatal(p)
	}
	if err := json.Unmarshal([]byte(`{"ngdu":null}`), &p); err != nil {
		t.Fatal(err)
	}
	_, err := NewCatalog(&wellStore{}).PatchWell(context.Background(), 1, p)
	if err == nil {
		t.Fatal("required null accepted")
	}
}

func TestClassificationCollapsesDistinctCategories(t *testing.T) {
	if Classify(nil) != nil {
		t.Fatal("empty classification")
	}
	one := Classify([]domain.Complication{{Category: ptr("slow")}, {Category: ptr("slow")}})
	if one == nil || *one != "slow" {
		t.Fatal(one)
	}
	combined := Classify([]domain.Complication{{Category: ptr("slow")}, {Category: ptr("fast")}})
	if combined == nil || *combined != "комбинированный" {
		t.Fatal(combined)
	}
}

func TestCreateWellWithoutExternalReferences(t *testing.T) {
	w, err := NewCatalog(&wellStore{}).CreateWell(context.Background(), domain.WellInput{NGDU: ptr(int64(0)), Department: ptr(int64(0))})
	if err != nil || w.ID != 12 || w.UnitekID != nil || w.ArmitsID != nil {
		t.Fatal(w, err)
	}
}
