package postgres

import (
	"context"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestLegacyResearchUnboundPolicy(t *testing.T) {
	legacy := domain.TrustedLegacyPrincipal("verified legacy fixture")
	for _, tc := range []struct {
		name, authMode, appScope string
		principal                domain.Principal
		kind                     domain.ExecutionActorKind
		want                     bool
	}{
		{"verified", "legacy", "research", legacy, domain.ExecutionLegacyHTTP, true},
		{"default", "", "", legacy, domain.ExecutionLegacyHTTP, false},
		{"local", "local", "research", legacy, domain.ExecutionLegacyHTTP, false},
		{"cohort", "legacy", "cohort", legacy, domain.ExecutionLegacyHTTP, false},
		{"missing", "legacy", "research", domain.Principal{}, domain.ExecutionLegacyHTTP, false},
		{"forged", "legacy", "research", domain.Principal{Authenticated: true, Mechanism: "legacy", Authorization: legacy.Authorization}, domain.ExecutionLegacyHTTP, false},
		{"cross-kind", "legacy", "research", legacy, domain.ExecutionSystemCollect, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := New(nil).WithExecutionProfile(tc.authMode, tc.appScope)
			ctx := domain.WithPrincipal(context.Background(), tc.principal)
			a := domain.AuthorizationSnapshot{}
			if tc.principal.Authorization != nil {
				a = *tc.principal.Authorization
			}
			if got := r.legacyResearchUnboundAllowed(ctx, tc.kind, a, "telemetry.import"); got != tc.want {
				t.Fatalf("allowed=%v want=%v", got, tc.want)
			}
		})
	}
	r := New(nil).WithExecutionProfile("legacy", "research")
	ctx := domain.WithPrincipal(context.Background(), legacy)
	a := *legacy.Authorization
	a.Permissions = []domain.Permission{"telemetry.import"}
	if r.legacyResearchUnboundAllowed(ctx, domain.ExecutionLegacyHTTP, a, "telemetry.import") {
		t.Fatal("missing prerequisites accepted")
	}
	a = *legacy.Authorization
	a.LegacyScope.Funds = []string{"operational"}
	if r.legacyResearchUnboundAllowed(ctx, domain.ExecutionLegacyHTTP, a, "telemetry.import") {
		t.Fatal("partial scope accepted")
	}
	r.fileJobPolicy = &domain.FileJobPolicy{}
	if r.legacyResearchUnboundAllowed(ctx, domain.ExecutionLegacyHTTP, *legacy.Authorization, "telemetry.import") {
		t.Fatal("pilot accepted")
	}
}
