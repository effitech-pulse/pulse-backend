package service

import (
	"context"
	"errors"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type AccountViewStore interface {
	AccountAccessView(context.Context, int64, int64) (domain.AccountAccessView, error)
	ListManagedAccountViews(context.Context, int64, domain.AccessPageQuery) (domain.AccountAccessViewPage, error)
	SessionAccountAccessView(context.Context, string, bool) (domain.AccountAccessView, error)
}

// WithRBACMode is trusted construction-time wiring. Runtime activation is a separate gate.
func (a *Access) WithRBACMode(mode domain.RBACMode) *Access { a.mode = mode; return a }
func (a *Access) RBACEnforced() bool                        { return a != nil && a.mode == domain.RBACEnforce }
func (a *Access) SessionView(ctx context.Context, token string) (domain.AccountAccessView, error) {
	if token == "" || len(token) > 128 {
		return domain.AccountAccessView{}, domain.NewError(domain.Unauthorized, "Войдите в систему")
	}
	s, ok := a.store.(AccountViewStore)
	if !ok {
		return domain.AccountAccessView{}, domain.NewError(domain.Unavailable, "Представление доступа недоступно")
	}
	v, e := s.SessionAccountAccessView(ctx, sessionDigest(token), a.RBACEnforced())
	var de *domain.Error
	if errors.As(e, &de) && de.Kind == domain.Missing {
		e = domain.NewError(domain.Unauthorized, "Сеанс истёк; войдите снова")
	}
	return v, e
}
func (a *Access) ViewPrincipal(v domain.AccountAccessView) domain.Principal {
	p := domain.AccountPrincipal(v.Account)
	if a.RBACEnforced() {
		p.Role = "viewer"
		if key := domain.LegacyAccessRole(v.Authorization); key != nil {
			p.Role = *key
		}
		p.Authorization = &v.Authorization
	}
	return p
}
func (a *Access) AccountView(ctx context.Context, targetID int64) (domain.AccountAccessView, error) {
	actor, e := managementActor(ctx)
	if e != nil {
		return domain.AccountAccessView{}, e
	}
	s, ok := a.store.(AccountViewStore)
	if !a.Enabled() || !ok {
		return domain.AccountAccessView{}, domain.NewError(domain.Unavailable, "Представление доступа недоступно")
	}
	return s.AccountAccessView(ctx, actor, targetID)
}
func (a *Access) AccountViews(ctx context.Context, q domain.AccessPageQuery) (domain.AccountAccessViewPage, error) {
	actor, e := managementActor(ctx)
	if e != nil {
		return domain.AccountAccessViewPage{}, e
	}
	s, ok := a.store.(AccountViewStore)
	if !a.Enabled() || !ok {
		return domain.AccountAccessViewPage{}, domain.NewError(domain.Unavailable, "Представление доступа недоступно")
	}
	return s.ListManagedAccountViews(ctx, actor, q)
}
func (a *Access) ManagementStore(ctx context.Context) (AccessManagementStore, int64, error) {
	id, e := managementActor(ctx)
	if e != nil {
		return nil, 0, e
	}
	s, ok := a.store.(AccessManagementStore)
	if !a.Enabled() || !ok {
		return nil, 0, domain.NewError(domain.Unavailable, "Управление доступом недоступно")
	}
	return s, id, nil
}

// LegacyAccountViewWriter shares the exact graph transaction with existing compatibility writes.
type LegacyAccountViewWriter interface {
	CreateAccountAccessView(context.Context, domain.Account, string, bool) (domain.AccountAccessView, error)
	UpdateAccountAccessView(context.Context, domain.Account, *string) (domain.AccountAccessView, error)
}

func (a *Access) createAccountView(ctx context.Context, user domain.Account, hash string, bootstrap bool) (domain.AccountAccessView, error) {
	if store, ok := a.store.(LegacyAccountViewWriter); ok {
		return store.CreateAccountAccessView(ctx, user, hash, bootstrap)
	}
	if a.RBACEnforced() {
		return domain.AccountAccessView{}, domain.NewError(domain.Unavailable, "Представление доступа недоступно")
	}
	account, e := a.store.CreateAccount(ctx, user, hash, bootstrap)
	return domain.AccountAccessView{Account: account}, e
}
func (a *Access) updateAccountView(ctx context.Context, user domain.Account, hash *string) (domain.AccountAccessView, error) {
	if store, ok := a.store.(LegacyAccountViewWriter); ok {
		return store.UpdateAccountAccessView(ctx, user, hash)
	}
	if a.RBACEnforced() {
		return domain.AccountAccessView{}, domain.NewError(domain.Unavailable, "Представление доступа недоступно")
	}
	account, e := a.store.UpdateAccount(ctx, user, hash)
	return domain.AccountAccessView{Account: account}, e
}
func (a *Access) CreateView(ctx context.Context, in domain.AccountInput) (domain.AccountAccessView, error) {
	if _, ok := a.store.(LegacyAccountViewWriter); !a.Enabled() || !ok {
		return domain.AccountAccessView{}, domain.NewError(domain.Unavailable, "Представление доступа недоступно")
	}
	if e := a.requireLegacyManager(ctx); e != nil {
		return domain.AccountAccessView{}, e
	}
	return a.createView(domain.WithRBACMode(ctx, a.mode), in, false)
}
func (a *Access) UpdateView(ctx context.Context, id int64, in domain.AccountInput) (domain.AccountAccessView, error) {
	if _, ok := a.store.(LegacyAccountViewWriter); !a.Enabled() || !ok {
		return domain.AccountAccessView{}, domain.NewError(domain.Unavailable, "Представление доступа недоступно")
	}
	return a.updateView(ctx, id, in)
}

// GroupMemberViewStore preserves the coherent projection for membership pages too.
type GroupMemberViewStore interface {
	ListAccessGroupMemberViews(context.Context, int64, int64, domain.AccessPageQuery) (domain.AccountAccessViewPage, error)
}

func (a *Access) GroupMemberViews(ctx context.Context, groupID int64, q domain.AccessPageQuery) (domain.AccountAccessViewPage, error) {
	actor, e := managementActor(ctx)
	if e != nil {
		return domain.AccountAccessViewPage{}, e
	}
	store, ok := a.store.(GroupMemberViewStore)
	if !a.Enabled() || !ok {
		return domain.AccountAccessViewPage{}, domain.NewError(domain.Unavailable, "Представление доступа недоступно")
	}
	return store.ListAccessGroupMemberViews(ctx, actor, groupID, q)
}
