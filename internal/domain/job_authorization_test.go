package domain

import (
	"context"
	"testing"
)

func TestSchedulerActionPrerequisites(t *testing.T) {
	for _, tc := range []struct {
		kind ExecutionActorKind
		key  Permission
	}{{ExecutionSystemML, "runs.start"}, {ExecutionSystemCollect, "integrations.collect"}} {
		ctx := WithSchedulerPrincipal(context.Background(), tc.kind, AccessScope{Funds: []string{"operational", "presentation"}})
		p, _ := CurrentPrincipal(ctx)
		if p.Authorization.Require(tc.key) != nil {
			t.Fatalf("scheduler %s lacks action prerequisites", tc.kind)
		}
		if ExecutionActor(ctx) != tc.kind {
			t.Fatal("actor kind lost")
		}
	}
}
func TestJobAuthorizationCopiesAuditRoles(t *testing.T) {
	key := "viewer"
	a := AuthorizationSnapshot{AccountID: 9007199254740993, Epoch: 2, Assignments: AccessAssignments{Roles: []AccessRoleSummary{{ID: 5, Name: "custom", Revision: 3, BuiltinKey: &key}}}}
	saved := NewJobAuthorization(a)
	key = "admin"
	a.Assignments.Roles[0].Name = "changed"
	if saved.Validate() != nil || saved.AccountID != 9007199254740993 || saved.Roles[0].Name != "custom" || *saved.Roles[0].BuiltinKey != "viewer" {
		t.Fatal("audit aliases mutable graph", saved)
	}
}
