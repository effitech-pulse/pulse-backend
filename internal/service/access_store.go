package service

import (
	"context"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// AccessManagementStore читает актуальный граф, не меняя legacy login/session seams.
// Все выборки повторно проверяют текущие права actor в согласованном DB snapshot.
type AccessManagementStore interface {
	Authorization(context.Context, int64) (domain.AuthorizationSnapshot, error)
	ListManagedAccounts(context.Context, int64, domain.AccessPageQuery) (domain.AccountPage, error)
	ManagedAccount(context.Context, int64, int64) (domain.Account, error)
	ListAccessRoles(context.Context, int64, domain.AccessPageQuery) (domain.RolePage, error)
	AccessRole(context.Context, int64, int64) (domain.AccessRole, error)
	ListAccessGroups(context.Context, int64, domain.AccessPageQuery) (domain.GroupPage, error)
	AccessGroup(context.Context, int64, int64) (domain.AccessGroup, error)
	AccessGroupMembers(context.Context, int64, int64, domain.AccessPageQuery) (domain.AccountPage, error)
	OrganizationOptions(context.Context, int64) (domain.OrganizationOptions, error)
}
