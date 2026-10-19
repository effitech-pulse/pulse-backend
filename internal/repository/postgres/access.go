package postgres

import (
	"context"
	"encoding/json"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

const accountColumns = "id,login,name,role,scope,active,revision,created_at,last_login"

func (r *Repository) Accounts(ctx context.Context) ([]domain.Account, error) {
	return list[domain.Account](ctx, r.pool, sqlBuilder.Select(accountColumns).From("auth_user").OrderBy("id"))
}
func (r *Repository) Account(ctx context.Context, id int64) (domain.Account, error) {
	return one[domain.Account](ctx, r.pool, sqlBuilder.Select(accountColumns).From("auth_user").Where(sq.Eq{"id": id}))
}
func (r *Repository) LoginCredential(ctx context.Context, login string) (domain.Account, string, error) {
	query := sqlBuilder.Select("row_to_json(u)", "password_hash").FromSelect(sqlBuilder.Select(accountColumns).From("auth_user").Where(sq.Eq{"login": login}), "u").Join("auth_user secret ON secret.id = u.id")
	sql, args, err := build(query)
	if err != nil {
		return domain.Account{}, "", err
	}
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return domain.Account{}, "", err
	}
	defer rows.Close()
	if !rows.Next() {
		if rows.Err() != nil {
			return domain.Account{}, "", rows.Err()
		}
		return domain.Account{}, "", domain.NewError(domain.Missing, "Учётная запись не найдена")
	}
	var raw []byte
	var hash string
	if err = rows.Scan(&raw, &hash); err != nil {
		return domain.Account{}, "", err
	}
	var account domain.Account
	err = json.Unmarshal(raw, &account)
	return account, hash, err
}
func (r *Repository) CreateAccount(ctx context.Context, a domain.Account, hash string, bootstrap bool) (domain.Account, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Account{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = execute(ctx, tx, sq.Expr("SELECT pg_advisory_xact_lock(74019323)")); err != nil {
		return domain.Account{}, err
	}
	if bootstrap {
		count, err := one[struct {
			Count int `json:"count"`
		}](ctx, tx, sqlBuilder.Select("count(*) AS count").From("auth_user"))
		if err != nil {
			return domain.Account{}, err
		}
		if count.Count != 0 {
			return domain.Account{}, domain.NewError(domain.Duplicate, "Первый администратор уже создан")
		}
	}
	scope, err := json.Marshal(a.Scope)
	if err != nil {
		return domain.Account{}, err
	}
	out, err := mutate[domain.Account](ctx, tx, sqlBuilder.Insert("auth_user").Columns("login", "name", "password_hash", "role", "scope", "active").Values(a.Login, a.Name, hash, a.Role, scope, a.Active).Suffix("RETURNING "+accountColumns))
	if err != nil {
		return domain.Account{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Account{}, err
	}
	return out, nil
}
func (r *Repository) UpdateAccount(ctx context.Context, a domain.Account, hash *string) (domain.Account, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Account{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = execute(ctx, tx, sq.Expr("SELECT pg_advisory_xact_lock(74019323)")); err != nil {
		return domain.Account{}, err
	}
	old, err := one[domain.Account](ctx, tx, sqlBuilder.Select(accountColumns).From("auth_user").Where(sq.Eq{"id": a.ID}).Suffix("FOR UPDATE"))
	if err != nil {
		return domain.Account{}, err
	}
	if old.Revision != a.Revision {
		return domain.Account{}, domain.NewError(domain.Duplicate, "Учётная запись уже изменена; обновите форму")
	}
	if old.Role == "admin" && old.Active && (a.Role != "admin" || !a.Active) {
		count, err := one[struct {
			Count int `json:"count"`
		}](ctx, tx, sqlBuilder.Select("count(*) AS count").From("auth_user").Where(sq.Eq{"role": "admin", "active": true}))
		if err != nil {
			return domain.Account{}, err
		}
		if count.Count < 2 {
			return domain.Account{}, domain.NewError(domain.Invalid, "Нельзя отключить или понизить последнего активного администратора")
		}
	}
	scope, err := json.Marshal(a.Scope)
	if err != nil {
		return domain.Account{}, err
	}
	changes := sq.Eq{"login": a.Login, "name": a.Name, "role": a.Role, "scope": scope, "active": a.Active, "revision": a.Revision + 1}
	if hash != nil {
		changes["password_hash"] = *hash
	}
	out, err := mutate[domain.Account](ctx, tx, sqlBuilder.Update("auth_user").SetMap(changes).Where(sq.Eq{"id": a.ID}).Suffix("RETURNING "+accountColumns))
	if err != nil {
		return domain.Account{}, err
	}
	if _, err = execute(ctx, tx, sqlBuilder.Delete("auth_session").Where(sq.Eq{"user_id": a.ID})); err != nil {
		return domain.Account{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Account{}, err
	}
	return out, nil
}
func (r *Repository) CreateSession(ctx context.Context, id int64, revision int64, hash string, expires time.Time) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Общая блокировка связывает выдачу сеанса с актуальным состоянием аккаунта.
	user, err := one[domain.Account](ctx, tx, sqlBuilder.Select(accountColumns).From("auth_user").Where(sq.Eq{"id": id}).Suffix("FOR UPDATE"))
	if err != nil {
		return err
	}
	if !user.Active || user.Revision != revision {
		return domain.NewError(domain.Unauthorized, "Вход недоступен")
	}
	if _, err = execute(ctx, tx, sqlBuilder.Delete("auth_session").Where(sq.Expr("expires_at <= now()"))); err != nil {
		return err
	}
	if _, err = execute(ctx, tx, sqlBuilder.Insert("auth_session").Columns("token_hash", "user_id", "expires_at").Values(hash, id, expires)); err != nil {
		return err
	}
	if _, err = execute(ctx, tx, sqlBuilder.Update("auth_user").Set("last_login", sq.Expr("now()")).Where(sq.Eq{"id": id})); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *Repository) SessionAccount(ctx context.Context, hash string) (domain.Account, error) {
	query := sqlBuilder.Select("u.id,u.login,u.name,u.role,u.scope,u.active,u.revision,u.created_at,u.last_login").From("auth_session s").Join("auth_user u ON u.id=s.user_id").Where(sq.Eq{"s.token_hash": hash, "u.active": true}).Where("s.expires_at > now()")
	return one[domain.Account](ctx, r.pool, query)
}
func (r *Repository) DeleteSession(ctx context.Context, hash string) error {
	_, err := execute(ctx, r.pool, sqlBuilder.Delete("auth_session").Where(sq.Eq{"token_hash": hash}))
	return err
}

func (r *Repository) AccountForIdentity(ctx context.Context, provider, subject string) (domain.Account, error) {
	query := sqlBuilder.Select("u.id,u.login,u.name,u.role,u.scope,u.active,u.revision,u.created_at,u.last_login").From("auth_identity i").Join("auth_user u ON u.id=i.user_id").Where(sq.Eq{"i.provider_id": provider, "i.subject": subject})
	return one[domain.Account](ctx, r.pool, query)
}
func (r *Repository) BindIdentity(ctx context.Context, provider, subject string, id int64) error {
	_, err := execute(ctx, r.pool, sqlBuilder.Insert("auth_identity").Columns("provider_id", "subject", "user_id").Values(provider, subject, id))
	return err
}
