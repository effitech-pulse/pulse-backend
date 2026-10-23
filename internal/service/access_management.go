package service

import (
	"context"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func managementActor(ctx context.Context) (int64, error) {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || !p.Authenticated || p.AccountID == nil {
		return 0, domain.NewError(domain.Unauthorized, "Войдите в систему")
	}
	return *p.AccountID, nil
}

// ValidateAccessChange проверяет форму изменения без чтения полномочий и без пароля.
func ValidateAccessChange(c domain.AccessChange) error {
	invalid := func() error {
		return domain.NewError(domain.Invalid, "Некорректное изменение доступа")
	}
	n := 0
	if c.User != nil {
		n++
	}
	if c.Role != nil {
		n++
	}
	if c.Group != nil {
		n++
	}
	if n != 1 {
		return invalid()
	}
	name := func(s string) bool {
		return utf8.ValidString(s) && utf8.RuneCountInString(s) >= 1 && utf8.RuneCountInString(s) <= 120 && strings.TrimSpace(s) != ""
	}
	ids := func(v []int64, max int) bool {
		if len(v) > max {
			return false
		}
		seen := map[int64]bool{}
		for _, id := range v {
			if id < 1 || seen[id] {
				return false
			}
			seen[id] = true
		}
		return true
	}
	if u := c.User; u != nil {
		if !name(u.Name) || !accountLogin.MatchString(u.Login) || !ids(u.RoleIDs, 100) || !ids(u.GroupIDs, 100) || (u.ID != nil && (*u.ID < 1 || u.Revision < 1)) {
			return invalid()
		}
		if u.Organization.Present {
			s := u.Organization.Value
			if u.Organization.Null {
				return invalid()
			}
			switch s.Kind {
			case "none", "organization":
				if s.NGDUID != nil || s.DepartmentID != nil {
					return invalid()
				}
			case "ngdu":
				if s.NGDUID == nil || s.DepartmentID != nil {
					return invalid()
				}
			case "department":
				if s.NGDUID == nil || s.DepartmentID == nil {
					return invalid()
				}
			default:
				return invalid()
			}
		}
		if u.ID == nil && (!u.Organization.Present || u.Organization.Value.Kind == "none") {
			return invalid()
		}
	}
	if r := c.Role; r != nil {
		if (r.ID != nil && (*r.ID < 1 || r.Revision < 1)) || (r.ID == nil && r.Delete) || (!r.Delete && (!name(r.Name) || utf8.RuneCountInString(r.Description) > 2000)) {
			return invalid()
		}
		known := map[domain.Permission]bool{}
		for _, d := range domain.PermissionCatalog() {
			known[d.Key] = true
		}
		seen := map[domain.Permission]bool{}
		for _, k := range r.PermissionKeys {
			if !known[k] || seen[k] {
				return invalid()
			}
			seen[k] = true
		}
	}
	if g := c.Group; g != nil {
		if (g.ID != nil && (*g.ID < 1 || g.Revision < 1)) || (g.ID == nil && g.Delete) || (!g.Delete && (!name(g.Name) || utf8.RuneCountInString(g.Description) > 2000)) || !ids(g.RoleIDs, 100) || !ids(g.MemberIDs, 5000) {
			return invalid()
		}
	}
	return nil
}
func (a *Access) Preview(ctx context.Context, c domain.AccessChange, q domain.AccessPageQuery) (domain.AccessPreview, error) {
	if !a.Enabled() {
		return domain.AccessPreview{}, domain.NewError(domain.Unavailable, "Персональный вход не включён")
	}
	if e := ValidateAccessChange(c); e != nil {
		return domain.AccessPreview{}, e
	}
	id, e := managementActor(ctx)
	if e != nil {
		return domain.AccessPreview{}, e
	}
	s, ok := a.store.(AccessManagementStore)
	if !ok {
		return domain.AccessPreview{}, domain.NewError(domain.Unavailable, "Управление доступом недоступно")
	}
	return s.PreviewAccessChange(ctx, id, c, q)
}
func (a *Access) Apply(ctx context.Context, in domain.AccessApply, password *string) (domain.AccessApplyResult, error) {
	if !a.Enabled() {
		return domain.AccessApplyResult{}, domain.NewError(domain.Unavailable, "Персональный вход не включён")
	}
	if e := ValidateAccessChange(in.Change); e != nil {
		return domain.AccessApplyResult{}, e
	}
	if in.GraphRevision < 1 || in.ActorEpoch < 1 || len(in.Fingerprint) != 64 {
		return domain.AccessApplyResult{}, domain.NewError(domain.Invalid, "Нужен предварительный просмотр")
	}
	id, e := managementActor(ctx)
	if e != nil {
		return domain.AccessApplyResult{}, e
	}
	if (password != nil) != (in.Change.User != nil && in.Change.User.ResetPassword) {
		return domain.AccessApplyResult{}, domain.NewError(domain.Invalid, "Пароль не соответствует изменению")
	}
	var hash *string
	if password != nil {
		if e = validatePassword(*password); e != nil {
			return domain.AccessApplyResult{}, e
		}
		b, e := bcrypt.GenerateFromPassword([]byte(*password), 12)
		if e != nil {
			return domain.AccessApplyResult{}, e
		}
		v := string(b)
		hash = &v
	}
	s, ok := a.store.(AccessManagementStore)
	if !ok {
		return domain.AccessApplyResult{}, domain.NewError(domain.Unavailable, "Управление доступом недоступно")
	}
	return s.ApplyAccessChange(ctx, id, in, hash)
}
