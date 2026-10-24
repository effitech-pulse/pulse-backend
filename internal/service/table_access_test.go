package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestAccessTableCursorBindsScopeQueryAndParent(t *testing.T) {
	id := int64(9)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{AccountID: &id, Authenticated: true, Scope: domain.AccessScope{Funds: []string{"operational"}}})
	raw := `{"version":1,"filters":[{"field":"active","op":"in","values":[false]}],"sort":[{"field":"name","direction":"asc"}]}`
	req, e := ParseAccessTableRequest(ctx, "access_members", 3, raw, 25, "")
	if e != nil {
		t.Fatal(e)
	}
	token, e := AccessTableCursor(req, []json.RawMessage{json.RawMessage(`7`), json.RawMessage(`1`), json.RawMessage(`"members:3"`), json.RawMessage(`5`)})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ParseAccessTableRequest(ctx, "access_members", 3, raw, 25, token); e != nil {
		t.Fatal(e)
	}
	scopeCtx := domain.WithPrincipal(context.Background(), domain.Principal{AccountID: &id, Authenticated: true, Scope: domain.AccessScope{Funds: []string{"presentation"}}})
	for _, v := range []struct {
		ctx    context.Context
		parent int64
		raw    string
		size   int
	}{{scopeCtx, 3, raw, 25}, {ctx, 4, raw, 25}, {ctx, 3, strings.Replace(raw, "false", "true", 1), 25}, {ctx, 3, raw, 26}} {
		if _, e = ParseAccessTableRequest(v.ctx, "access_members", v.parent, v.raw, v.size, token); e == nil {
			t.Fatal("cursor binding accepted mismatch")
		}
	}
}

type accessOrganizationFacetStore struct{ AccessStore }

func (accessOrganizationFacetStore) ListAccessUserFilterOptions(context.Context, int64, string, domain.TableRequest) (domain.AccessFilterOptionsPage, error) {
	return domain.AccessFilterOptionsPage{}, nil
}
func TestAccessTableOrganizationFacetAllowlist(t *testing.T) {
	id := int64(7)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{AccountID: &id, Authenticated: true, Mechanism: "session"})
	a := &Access{store: accessOrganizationFacetStore{}, enabled: true}
	for _, field := range []string{"ngdu_id", "department_id"} {
		if _, err := a.UserFilterOptions(ctx, field, "", 25, ""); err != nil {
			t.Errorf("bounded visible %s facet rejected: %v", field, err)
		}
	}
}
