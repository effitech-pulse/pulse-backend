package service

import (
	"strings"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestEmployeeProfileValidation(t *testing.T) {
	id := int64(1)
	for _, tc := range []struct {
		title string
		valid bool
	}{{strings.Repeat("я", 200), true}, {strings.Repeat("я", 201), false}, {"  Инженер  ", true}} {
		p := domain.ProfileInput{Revision: domain.Field[int64]{Present: true, Value: 1}, JobTitle: domain.Field[string]{Present: true, Value: tc.title}, Workplace: domain.Field[domain.WorkplaceInput]{Present: true, Value: domain.WorkplaceInput{Organization: domain.Field[string]{Present: true, Null: true}, Division: domain.Field[string]{Present: true, Null: true}, Department: domain.Field[string]{Present: true, Null: true}}}, ManagerID: domain.Field[int64]{Present: true, Null: true}}
		c := domain.AccessChange{User: &domain.UserAccessChange{ID: &id, Revision: 1, Login: "valid", Name: "Имя", Profile: domain.Field[domain.ProfileInput]{Present: true, Value: p}}}
		if e := ValidateAccessChange(c); (e == nil) != tc.valid {
			t.Fatalf("valid=%v error=%v", tc.valid, e)
		}
	}
	c := domain.AccessChange{User: &domain.UserAccessChange{ID: &id, Revision: 1, Login: "valid", Name: "Имя", Profile: domain.Field[domain.ProfileInput]{Present: true, Null: true}}}
	if ValidateAccessChange(c) == nil {
		t.Fatal("null profile accepted")
	}
}
