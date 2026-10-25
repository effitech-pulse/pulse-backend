package service

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type EmployeeProfileStore interface {
	OrganizationTreePeople(context.Context, int64, domain.OrganizationTreeQuery) ([]domain.OrganizationTreePerson, error)
	ListPeople(context.Context, int64, domain.PeopleQuery) (domain.PeoplePage, error)
	EmployeeProfile(context.Context, int64, int64) (domain.EmployeeProfile, error)
}

func ValidateProfile(u *domain.UserAccessChange) error {
	if !u.Profile.Present {
		return nil
	}
	bad := func() error {
		return domain.NewError(domain.Invalid, "Некорректный профиль сотрудника")
	}
	p := u.Profile.Value
	if u.Profile.Null || !p.Revision.Present || p.Revision.Null || !p.JobTitle.Present || !p.ManagerID.Present || !p.Workplace.Present || p.Workplace.Null {
		return bad()
	}
	if u.ID == nil && p.Revision.Value != 0 || u.ID != nil && p.Revision.Value < 1 {
		return bad()
	}
	for _, v := range []domain.Field[string]{p.JobTitle, p.Workplace.Value.Organization, p.Workplace.Value.Division, p.Workplace.Value.Department} {
		if !v.Present || !v.Null && (!utf8.ValidString(v.Value) || utf8.RuneCountInString(strings.TrimSpace(v.Value)) > 200) {
			return bad()
		}
	}
	if !p.ManagerID.Null && (p.ManagerID.Value < 1 || u.ID != nil && p.ManagerID.Value == *u.ID) {
		return bad()
	}
	return nil
}
func NormalizeProfile(p domain.ProfileInput) domain.ManagedProfile {
	text := func(f domain.Field[string]) *string {
		v := strings.TrimSpace(f.Value)
		if f.Null || v == "" {
			return nil
		}
		return &v
	}
	var manager *int64
	if !p.ManagerID.Null {
		v := p.ManagerID.Value
		manager = &v
	}
	return domain.ManagedProfile{Revision: p.Revision.Value, JobTitle: text(p.JobTitle), Workplace: domain.Workplace{Organization: text(p.Workplace.Value.Organization), Division: text(p.Workplace.Value.Division), Department: text(p.Workplace.Value.Department)}, ManagerID: manager}
}
func (a *Access) peopleStore(ctx context.Context) (EmployeeProfileStore, int64, error) {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || !p.Authenticated {
		return nil, 0, domain.NewError(domain.Unauthorized, "Войдите в систему")
	}
	if !a.Enabled() || p.AccountID == nil || p.Mechanism != "session" {
		return nil, 0, domain.NewError(domain.Forbidden, "Требуется персональный вход")
	}
	s, ok := a.store.(EmployeeProfileStore)
	if !ok {
		return nil, 0, domain.NewError(domain.Unavailable, "Справочник недоступен")
	}
	return s, *p.AccountID, nil
}
func (a *Access) ListPeople(ctx context.Context, q domain.PeopleQuery) (domain.PeoplePage, error) {
	s, id, e := a.peopleStore(ctx)
	if e != nil {
		return domain.PeoplePage{}, e
	}
	return s.ListPeople(ctx, id, q)
}
func (a *Access) EmployeeProfile(ctx context.Context, target int64) (domain.EmployeeProfile, error) {
	s, id, e := a.peopleStore(ctx)
	if e != nil {
		return domain.EmployeeProfile{}, e
	}
	return s.EmployeeProfile(ctx, id, target)
}
