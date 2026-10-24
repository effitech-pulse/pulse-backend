package postgres

import (
	"context"
	"strconv"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func (r *Repository) SessionAccountAccessView(ctx context.Context, hash string, enforce bool) (domain.AccountAccessView, error) {
	q := authorizationQuery().Join("auth_session session ON session.user_id=u.id").Where(sq.Eq{"session.token_hash": hash, "u.active": true}).Where("session.expires_at > now()")
	if enforce {
		q = q.Where("session.authz_epoch=u.authz_epoch")
	}
	row, e := one[authorizationRow](ctx, r.pool, q)
	if e != nil {
		return domain.AccountAccessView{}, e
	}
	return domain.AccountAccessView{Account: row.Account, Authorization: row.snapshot()}, nil
}
func checkedViewActor(ctx context.Context, g accessGraph, id int64) error {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || !p.Authenticated || p.AccountID == nil || *p.AccountID != id || !g.Actor.Active || g.Actor.AccountID != id {
		return domain.NewError(domain.Unauthorized, "Войдите в систему")
	}
	if p.Authorization != nil && p.Authorization.Epoch != g.Actor.Epoch {
		return domain.NewError(domain.Unauthorized, "Сеанс истёк; войдите снова")
	}
	return nil
}
func (r *Repository) withAccountViews(ctx context.Context, actor int64, fn func(db, accessGraph) error) error {
	tx, e := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	g, e := readAccessGraphUnchecked(ctx, tx, actor)
	if e != nil {
		return e
	}
	if e = checkedViewActor(ctx, g, actor); e != nil {
		return e
	}
	if e = fn(tx, g); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (r *Repository) AccountAccessView(ctx context.Context, actor, target int64) (out domain.AccountAccessView, e error) {
	e = r.withAccountViews(ctx, actor, func(conn db, g accessGraph) error {
		v, e := g.accountView(target)
		if e != nil {
			return e
		}
		if actor != target {
			if e = g.Actor.Require("users.read"); e != nil {
				return e
			}
			if !g.Actor.CanManageAccount(v.Authorization) {
				return domain.NewError(domain.Missing, "Пользователь не найден")
			}
		}
		p, e := managedProfile(ctx, conn, target)
		if e != nil {
			return e
		}
		v.Profile = &p
		out = v
		return nil
	})
	return
}
func (r *Repository) ListManagedAccountViews(ctx context.Context, actor int64, q domain.AccessPageQuery) (out domain.AccountAccessViewPage, e error) {
	e = r.withAccountViews(ctx, actor, func(conn db, g accessGraph) error {
		if e := g.Actor.Require("users.read"); e != nil {
			return e
		}
		page, e := accountPage(g, q, "users", nil)
		if e != nil {
			return e
		}
		out = domain.AccountAccessViewPage{Items: []domain.AccountAccessView{}, NextCursor: page.NextCursor, TotalVisible: page.TotalVisible, GraphRevision: page.GraphRevision}
		for _, a := range page.Items {
			v, e := g.accountView(a.ID)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, v)
		}
		return nil
	})
	return
}

func (r *Repository) ListAccessGroupMemberViews(ctx context.Context, actor, groupID int64, q domain.AccessPageQuery) (out domain.AccountAccessViewPage, e error) {
	e = r.withAccountViews(ctx, actor, func(conn db, g accessGraph) error {
		if e := g.Actor.Require("groups.read", "users.read"); e != nil {
			return e
		}
		if _, e := g.group(groupID); e != nil {
			return e
		}
		page, e := accountPage(g, q, "members:"+strconv.FormatInt(groupID, 10), &groupID)
		if e != nil {
			return e
		}
		out = domain.AccountAccessViewPage{Items: []domain.AccountAccessView{}, NextCursor: page.NextCursor, TotalVisible: page.TotalVisible, GraphRevision: page.GraphRevision}
		for _, a := range page.Items {
			v, e := g.accountView(a.ID)
			if e != nil {
				return e
			}
			out.Items = append(out.Items, v)
		}
		return nil
	})
	return
}
