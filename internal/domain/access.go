package domain

import (
	"context"
	"slices"
	"time"
)

type AccessScope struct {
	Funds       []string `json:"funds"`
	NGDUs       *[]int64 `json:"ngdus"`
	Departments *[]int64 `json:"departments"`
}
type Account struct {
	ID        int64       `json:"id"`
	Login     string      `json:"login"`
	Name      string      `json:"name"`
	Role      string      `json:"role"`
	Scope     AccessScope `json:"scope"`
	Active    bool        `json:"active"`
	Revision  int64       `json:"revision"`
	CreatedAt time.Time   `json:"created_at"`
	LastLogin *time.Time  `json:"last_login"`
}
type AccountInput struct {
	Login    string      `json:"login"`
	Name     string      `json:"name"`
	Role     string      `json:"role"`
	Scope    AccessScope `json:"scope"`
	Active   *bool       `json:"active"`
	Revision int64       `json:"revision"`
	Password *string     `json:"password"`
}
type Principal struct {
	Authorization *AuthorizationSnapshot `json:"authorization,omitempty"`
	AccountID     *int64                 `json:"account_id"`
	Name          string                 `json:"name"`
	Role          string                 `json:"role"`
	Scope         AccessScope            `json:"scope"`
	Authenticated bool                   `json:"authenticated"`
	Mechanism     string                 `json:"mechanism"`
}

func (p Principal) Allows(well Well) bool {
	if p.Authorization != nil {
		return p.Authorization.AllowsWell(well)
	}
	if p.Role == "admin" {
		return true
	}
	kind := well.FundType
	if kind == "" {
		kind = "operational"
	}
	return slices.Contains(p.Scope.Funds, kind) && (p.Scope.NGDUs == nil || slices.Contains(*p.Scope.NGDUs, well.NGDU)) && (p.Scope.Departments == nil || slices.Contains(*p.Scope.Departments, well.Department))
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}
func CurrentPrincipal(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}
func CanReadWell(ctx context.Context, w Well) bool {
	p, ok := CurrentPrincipal(ctx)
	return !ok || p.Allows(w)
}
func AccountPrincipal(a Account) Principal {
	return Principal{AccountID: &a.ID, Name: a.Name, Role: a.Role, Scope: a.Scope, Authenticated: true, Mechanism: "session"}
}
