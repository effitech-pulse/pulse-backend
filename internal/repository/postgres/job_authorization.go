package postgres

import (
	"context"
	"errors"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// authorizationTx удерживает user row до значимого эффекта; graph lock не берётся.
func authorizationTx(ctx context.Context, tx pgx.Tx, accountID int64) (domain.AuthorizationSnapshot, error) {
	var id int64
	err := tx.QueryRow(ctx, "SELECT id FROM auth_user WHERE id=$1 FOR SHARE", accountID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AuthorizationSnapshot{}, domain.NewError(domain.Missing, "access_revoked")
	}
	if err != nil {
		return domain.AuthorizationSnapshot{}, err
	}
	a, err := authorization(ctx, tx, id)
	if isMissing(err) {
		return a, domain.NewError(domain.Unavailable, "rbac_state_unavailable")
	}
	return a, err
}
func requireJobAuthorizationTx(ctx context.Context, tx pgx.Tx, saved domain.JobAuthorization, permission domain.Permission) (domain.AuthorizationSnapshot, error) {
	if err := saved.Validate(); err != nil {
		return domain.AuthorizationSnapshot{}, err
	}
	a, err := authorizationTx(ctx, tx, saved.AccountID)
	if err != nil {
		return a, err
	}
	if !a.Active || a.Epoch != saved.Epoch {
		return a, domain.NewError(domain.Forbidden, "access_revoked")
	}
	return a, a.Require(permission)
}
func authorityDenied(err error) bool {
	var de *domain.Error
	return errors.As(err, &de) && (de.Kind == domain.Forbidden || de.Kind == domain.Unauthorized || de.Kind == domain.Missing)
}

// jobAuthority отделяет session/current read от strict persisted compute epoch.
func jobAuthority(ctx context.Context, tx pgx.Tx, id int64, saved *domain.JobAuthorization, permission domain.Permission, execution bool) (domain.AuthorizationSnapshot, error) {
	if saved != nil && saved.AccountID != id {
		return domain.AuthorizationSnapshot{}, domain.NewError(domain.Forbidden, "access_revoked")
	}
	a, err := authorizationTx(ctx, tx, id)
	if err != nil {
		return a, err
	}
	if !a.Active {
		return a, domain.NewError(domain.Forbidden, "access_revoked")
	}
	if saved != nil {
		if err = saved.Validate(); err != nil {
			return a, err
		}
		if a.Epoch != saved.Epoch {
			return a, domain.NewError(domain.Forbidden, "access_revoked")
		}
	}
	if !execution {
		p, ok := domain.CurrentPrincipal(ctx)
		if !ok || !p.Authenticated || p.AccountID == nil || *p.AccountID != id || p.Authorization == nil || p.Authorization.AccountID != id || p.Authorization.Epoch != a.Epoch {
			return a, domain.NewError(domain.Forbidden, "access_revoked")
		}
	}
	if domain.RBACEnforced(ctx) {
		if execution && saved == nil {
			return a, domain.NewError(domain.Forbidden, "access_revoked")
		}

	} else {
		account, e := one[domain.Account](ctx, tx, sqlBuilder.Select(accountColumns).From("auth_user").Where(sq.Eq{"id": id}))
		if e != nil {
			return a, e
		}
		p := domain.AccountPrincipal(account)
		p.Authorization = &a
		p = domain.TrustedCompatibilityPrincipal(p)
		a = *p.Authorization
		switch permission {
		case "cohortjobs.run", "cohortjobs.read", "cohortjobs.cancel", "collectionjobs.run", "collectionjobs.read", "collectionjobs.cancel":
			a.LegacyScope = account.Scope
			a.Organization = nil
		}
	}
	return a, a.Require(permission)
}
func admissionAuthorization(ctx context.Context, tx pgx.Tx, id int64, permission domain.Permission) (*domain.JobAuthorization, error) {
	a, err := jobAuthority(ctx, tx, id, nil, permission, false)
	if err != nil {
		return nil, err
	}
	v := domain.NewJobAuthorization(a)
	return &v, v.Validate()
}

// executionAuthorityTx принимает только persisted discriminator и явную server policy.
func executionAuthorityTx(ctx context.Context, tx pgx.Tx, kind domain.ExecutionActorKind, saved *domain.JobAuthorization, permission domain.Permission, system domain.ExecutionActorKind) (domain.AuthorizationSnapshot, error) {
	if kind == domain.ExecutionAccount {
		p, ok := domain.CurrentPrincipal(ctx)
		if saved == nil || !ok || domain.ExecutionActor(ctx) != domain.ExecutionAccount || p.AccountID == nil || *p.AccountID != saved.AccountID {
			return domain.AuthorizationSnapshot{}, domain.NewError(domain.Forbidden, "access_revoked")
		}
		return jobAuthority(ctx, tx, saved.AccountID, saved, permission, true)
	}
	p, ok := domain.CurrentPrincipal(ctx)
	actual := domain.ExecutionActor(ctx)
	if !ok || p.Authorization == nil || kind != actual || (kind != system && kind != domain.ExecutionLegacyHTTP) {
		return domain.AuthorizationSnapshot{}, domain.NewError(domain.Forbidden, "access_revoked")
	}
	if err := p.Authorization.Require(permission); err != nil {
		return domain.AuthorizationSnapshot{}, err
	}
	return *p.Authorization, nil
}
func beginExecutionTx(ctx context.Context, tx pgx.Tx, permission domain.Permission, system domain.ExecutionActorKind) (domain.ExecutionActorKind, *domain.JobAuthorization, error) {
	kind := domain.ExecutionActor(ctx)
	if kind == domain.ExecutionAccount {
		p, _ := domain.CurrentPrincipal(ctx)
		saved, err := admissionAuthorization(ctx, tx, *p.AccountID, permission)
		return kind, saved, err
	}
	_, err := executionAuthorityTx(ctx, tx, kind, nil, permission, system)
	return kind, nil, err
}
func runAuthorityTx(ctx context.Context, tx pgx.Tx, id int64) (domain.AuthorizationSnapshot, error) {
	frame, err := one[struct {
		Kind          domain.ExecutionActorKind `json:"execution_actor"`
		Authorization *domain.JobAuthorization  `json:"actor_authorization"`
	}](ctx, tx, sqlBuilder.Select("execution_actor", "actor_authorization").From("ml_run").Where(sq.Eq{"id": id}))
	if err != nil {
		return domain.AuthorizationSnapshot{}, err
	}
	a, err := executionAuthorityTx(ctx, tx, frame.Kind, frame.Authorization, "runs.start", domain.ExecutionSystemML)
	if err == nil && !a.FullApplicationScope() {
		err = domain.NewError(domain.Forbidden, "access_revoked")
	}
	return a, err
}
