package postgres

import (
	"context"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// WithExecutionProfile копирует проверенный bootstrap профиль; по умолчанию raw compatibility закрыта.
func (r *Repository) WithExecutionProfile(authMode, appScope string) *Repository {
	copy := *r
	copy.legacyResearchUnbound = authMode == "legacy" && appScope == "research"
	return &copy
}
func (r *Repository) legacyResearchUnboundAllowed(ctx context.Context, kind domain.ExecutionActorKind, a domain.AuthorizationSnapshot, permission domain.Permission) bool {
	return r != nil && r.legacyResearchUnbound && r.fileJobPolicy == nil && r.cohortJobPolicy == nil && r.collectionJobPolicy == nil &&
		kind == domain.ExecutionLegacyHTTP && domain.ExecutionActor(ctx) == domain.ExecutionLegacyHTTP &&
		a.FullApplicationScope() && a.Require(permission) == nil
}
