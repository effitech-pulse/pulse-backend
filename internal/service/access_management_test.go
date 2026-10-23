package service

import (
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"testing"
)

func TestAccessChangeValidation(t *testing.T) {
	id := int64(1)
	valid := domain.AccessChange{User: &domain.UserAccessChange{ID: &id, Revision: 1, Login: "valid", Name: "Имя", RoleIDs: []int64{1, 2}}}
	if e := ValidateAccessChange(valid); e != nil {
		t.Fatal(e)
	}
	cases := []domain.AccessChange{
		{}, {User: valid.User, Role: &domain.RoleAccessChange{Name: "role"}},
		{User: &domain.UserAccessChange{Login: "valid", Name: "Имя"}},
		{User: &domain.UserAccessChange{ID: &id, Revision: 1, Login: "valid", Name: "Имя", RoleIDs: []int64{1, 1}}},
		{User: &domain.UserAccessChange{ID: &id, Revision: 1, Login: "valid", Name: "Имя", Organization: domain.Field[domain.OrganizationScope]{Present: true, Null: true}}},
		{Role: &domain.RoleAccessChange{Name: "role", PermissionKeys: []domain.Permission{"unknown"}}},
		{Role: &domain.RoleAccessChange{Name: "role", PermissionKeys: []domain.Permission{"wells.read", "wells.read"}}},
		{Group: &domain.GroupAccessChange{Name: "group", MemberIDs: []int64{1, 1}}},
	}
	for i, c := range cases {
		if e := ValidateAccessChange(c); e == nil {
			t.Errorf("invalid case %d accepted", i)
		}
	}
}
