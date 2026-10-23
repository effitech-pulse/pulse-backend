package service

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// internalFixtureContext represents intentional trusted in-process legacy fixture work.
func internalFixtureContext() context.Context {
	return domain.WithSystemPrincipal(context.Background())
}

func TestMissingDataAuthorityDenied(t *testing.T) {
	c := NewCatalog(&wellStore{})
	for _, ctx := range []context.Context{context.Background(), domain.WithPrincipal(context.Background(), domain.Principal{Authenticated: true, Role: "admin"}), domain.WithRBACMode(context.Background(), domain.RBACEnforce), domain.WithPrincipal(context.Background(), domain.Principal{Authenticated: true, Authorization: &domain.AuthorizationSnapshot{Active: true}})} {
		if _, err := c.Wells(ctx, false); err == nil {
			t.Fatal("missing snapshot accepted")
		}
	}
}

type deniedScopeML struct {
	MLClient
	calls int
}

func (m *deniedScopeML) Upload(context.Context, string, io.Reader) (domain.UploadResult, error) {
	m.calls++
	return domain.UploadResult{}, nil
}

type deniedScopeConnector struct{ calls int }

func (s *deniedScopeConnector) Fetch(context.Context, domain.TelemetrySource, string) ([]map[string]any, error) {
	s.calls++
	return nil, nil
}
func TestRBACGlobalEffectsDeniedBeforeRPC(t *testing.T) {
	ngdu := int64(1)
	a := domain.AuthorizationSnapshot{Active: true, Permissions: domain.BuiltinAccessPermissions("admin"), Organization: &domain.OrganizationScope{Kind: "ngdu", NGDUID: &ngdu}, LegacyScope: domain.AccessScope{Funds: []string{"operational", "presentation"}}}
	ctx := domain.WithRBACMode(domain.WithPrincipal(context.Background(), domain.Principal{Authenticated: true, Authorization: &a, Role: "admin"}), domain.RBACEnforce)
	client := &deniedScopeML{}
	m := NewML(nil, nil, client, RunOptions{})
	if _, err := m.Upload(ctx, "private.csv", strings.NewReader("raw")); err == nil {
		t.Fatal("scoped upload accepted")
	}
	if _, err := m.StartRun(ctx, "manual"); err == nil {
		t.Fatal("scoped whole-fund run accepted")
	}
	connector := &deniedScopeConnector{}
	integrations := NewIntegrations(nil, connector, nil)
	if _, err := integrations.Check(ctx, domain.SourceInput{}); err == nil {
		t.Fatal("scoped raw source check accepted")
	}
	if _, err := integrations.Collect(ctx, "source", false); err == nil {
		t.Fatal("scoped collection accepted")
	}
	if client.calls != 0 || connector.calls != 0 {
		t.Fatal("denied effect reached external boundary")
	}
}
