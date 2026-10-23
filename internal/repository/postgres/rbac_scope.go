package postgres

import (
	"context"

	sq "github.com/Masterminds/squirrel"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// visibleWellPredicate применяется до LIMIT/агрегации; alias задаёт только код репозитория.
func visibleWellPredicate(a domain.AuthorizationSnapshot, alias string) sq.Sqlizer {
	if a.Require() != nil {
		return sq.Expr("FALSE")
	}
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	terms := sq.And{sq.Eq{prefix + "fund_type": a.LegacyScope.Funds}}
	if a.Organization == nil {
		if a.LegacyScope.NGDUs != nil {
			terms = append(terms, sq.Eq{prefix + "ngdu": *a.LegacyScope.NGDUs})
		}
		if a.LegacyScope.Departments != nil {
			terms = append(terms, sq.Eq{prefix + "department": *a.LegacyScope.Departments})
		}
		return terms
	}
	switch a.Organization.Kind {
	case "organization":
	case "ngdu":
		terms = append(terms, sq.Eq{prefix + "ngdu": *a.Organization.NGDUID})
	case "department":
		terms = append(terms, sq.Eq{prefix + "ngdu": *a.Organization.NGDUID, prefix + "department": *a.Organization.DepartmentID})
	default:
		return sq.Expr("FALSE")
	}
	return terms
}
func contextWellPredicate(ctx context.Context, alias string) sq.Sqlizer {
	p, ok := domain.CurrentPrincipal(ctx)
	if ok && p.Authorization != nil {
		return visibleWellPredicate(*p.Authorization, alias)
	}
	if domain.RBACEnforced(ctx) {
		return sq.Expr("FALSE")
	}
	return sq.Expr("TRUE")
}
func linkedWellPredicate(ctx context.Context, column string, global bool) sq.Sqlizer {
	if !domain.RBACEnforced(ctx) {
		return sq.Expr("TRUE")
	}
	q := sqlBuilder.Select("scope_w.id").From("well scope_w").Where(contextWellPredicate(ctx, "scope_w"))
	bound := sq.Expr(column+" IN (?)", q)
	p, ok := domain.CurrentPrincipal(ctx)
	if global && ok && p.Authorization != nil && p.Authorization.FullApplicationScope() {
		return sq.Or{bound, sq.Eq{column: nil}}
	}
	return bound
}

// currentScopeAuthorization сериализует authority с изменением графа до commit.
func currentScopeAuthorization(ctx context.Context, tx db, permission domain.Permission) (domain.AuthorizationSnapshot, error) {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || !p.Authenticated || p.Authorization == nil || p.AccountID == nil {
		return domain.AuthorizationSnapshot{}, domain.NewError(domain.Forbidden, "authorization required")
	}
	if _, err := execute(ctx, tx, sqlBuilder.Select("id").From("auth_user").Where(sq.Eq{"id": *p.AccountID}).Suffix("FOR SHARE")); err != nil {
		return domain.AuthorizationSnapshot{}, err
	}
	a, err := authorization(ctx, tx, *p.AccountID)
	if err != nil {
		return a, err
	}
	if !a.Active || a.Epoch != p.Authorization.Epoch {
		return a, domain.NewError(domain.Unauthorized, "Сеанс истёк")
	}
	return a, a.Require(permission)
}
func requireScopedWellTx(ctx context.Context, tx db, permission domain.Permission, id int64) (domain.Well, error) {
	if !domain.RBACEnforced(ctx) {
		return one[domain.Well](ctx, tx, sqlBuilder.Select(wellColumns).From("well").Where(sq.Eq{"id": id}).Suffix("FOR SHARE"))
	}
	a, err := currentScopeAuthorization(ctx, tx, permission)
	if err != nil {
		return domain.Well{}, err
	}
	w, err := one[domain.Well](ctx, tx, sqlBuilder.Select(wellColumns).From("well").Where(sq.Eq{"id": id}).Suffix("FOR SHARE"))
	if err == nil && !a.AllowsWell(w) {
		return domain.Well{}, domain.NewError(domain.Missing, "Скважина не найдена")
	}
	return w, err
}
