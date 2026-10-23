package postgres

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Account{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = lockAccessGraph(ctx, tx); err != nil {
		return domain.Account{}, err
	}
	actorID := int64(0)
	var before accessGraph
	if bootstrap {
		count, e := one[struct {
			Count int `json:"count"`
		}](ctx, tx, sqlBuilder.Select("count(*) AS count").From("auth_user"))
		if e != nil {
			return domain.Account{}, e
		}
		if count.Count != 0 {
			return domain.Account{}, domain.NewError(domain.Duplicate, "Первый администратор уже создан")
		}
		a.Role = "admin"
		a.Active = true
		if err = restoreBootstrapRole(ctx, tx); err != nil {
			return domain.Account{}, err
		}
	} else {
		actorID, err = accessActorID(ctx)
		if err != nil {
			return domain.Account{}, err
		}
		before, err = readAccessGraph(ctx, tx, actorID, "users.create")
		if err != nil {
			return domain.Account{}, err
		}
	}
	role, err := one[domain.AccessRole](ctx, tx, sqlBuilder.Select("id,name,description,builtin_key,revision").From("auth_role").Where(sq.Eq{"builtin_key": a.Role}))
	if err != nil {
		return domain.Account{}, accessInvalid()
	}
	scope, err := json.Marshal(a.Scope)
	if err != nil {
		return domain.Account{}, err
	}
	org := legacyOrganization(a)
	rawOrg, err := organizationJSON(org)
	if err != nil {
		return domain.Account{}, err
	}
	if !bootstrap {
		keys, e := list[struct {
			Key domain.Permission `json:"permission_key"`
		}](ctx, tx, sqlBuilder.Select("permission_key").From("auth_role_permission").Where(sq.Eq{"role_id": role.ID}))
		if e != nil {
			return domain.Account{}, e
		}
		for _, k := range keys {
			role.PermissionKeys = append(role.PermissionKeys, k.Key)
		}
		proposed := authorizationRow{Account: a, Organization: org, Grants: []accessRoleGrant{{Role: role}}}.snapshot()
		if !before.Actor.CanManageAccount(proposed) || !before.Actor.CanGrant(role.PermissionKeys) {
			return domain.Account{}, accessDenied()
		}
		if e = before.Actor.Require("users.roles.assign"); e != nil {
			return domain.Account{}, e
		}
	}
	out, err := mutate[domain.Account](ctx, tx, sqlBuilder.Insert("auth_user").Columns("login", "name", "password_hash", "role", "scope", "active", "organization_scope").Values(a.Login, a.Name, hash, a.Role, scope, a.Active, rawOrg).Suffix("RETURNING "+accountColumns))
	if err != nil {
		return domain.Account{}, err
	}
	if err = replaceAccessLinks(ctx, tx, "auth_user_role", "user_id", out.ID, "role_id", []int64{role.ID}); err != nil {
		return domain.Account{}, err
	}
	if err = bumpAccessGraph(ctx, tx); err != nil {
		return domain.Account{}, err
	}
	if bootstrap {
		actorID = out.ID
		before = accessGraph{Accounts: []authorizationRow{}, Roles: []domain.AccessRole{}, Groups: []domain.AccessGroup{}, Members: []groupMember{}}
	}
	after, err := readAccessGraphUnchecked(ctx, tx, actorID)
	if err != nil {
		return domain.Account{}, err
	}
	if err = ensureAccessManager(after); err != nil {
		return domain.Account{}, err
	}
	if err = writeAccessEvent(ctx, tx, actorID, before, after, []int64{out.ID}, domain.AccessChange{}); err != nil {
		return domain.Account{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return domain.Account{}, err
	}
	return out, nil
}
func (r *Repository) UpdateAccount(ctx context.Context, a domain.Account, hash *string) (domain.Account, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return domain.Account{}, e
	}
	defer tx.Rollback(ctx)
	if _, e = lockAccessGraph(ctx, tx); e != nil {
		return domain.Account{}, e
	}
	actorID, e := accessActorID(ctx)
	if e != nil {
		return domain.Account{}, e
	}
	before, e := readAccessGraph(ctx, tx, actorID, "users.update")
	if e != nil {
		return domain.Account{}, e
	}
	idx := before.accountIndex(a.ID)
	if idx < 0 || !before.Actor.CanManageAccount(before.Accounts[idx].snapshot()) {
		return domain.Account{}, domain.NewError(domain.Missing, "Пользователь не найден")
	}
	old := before.Accounts[idx]
	if old.Account.Revision != a.Revision {
		return domain.Account{}, accessConflict()
	}
	if len(directAccessRoles(old)) != 1 || len(before.userGroups(a.ID)) != 0 {
		return domain.Account{}, domain.NewError(domain.Duplicate, "legacy_access_write_unsupported")
	}
	roleIdx := -1
	for i, role := range before.Roles {
		if role.BuiltinKey != nil && *role.BuiltinKey == a.Role {
			roleIdx = i
			break
		}
	}
	if roleIdx < 0 {
		return domain.Account{}, accessInvalid()
	}
	role := before.Roles[roleIdx]
	if !before.Actor.CanGrant(role.PermissionKeys) {
		return domain.Account{}, accessDenied()
	}
	if !slices.Equal(directAccessRoles(old), []int64{role.ID}) {
		if e = before.Actor.Require("users.roles.assign"); e != nil {
			return domain.Account{}, e
		}
	}
	org := legacyOrganization(a)
	if !reflect.DeepEqual(old.Account.Scope, a.Scope) || !reflect.DeepEqual(old.Organization, org) {
		if e = before.Actor.Require("users.scope.assign"); e != nil {
			return domain.Account{}, e
		}
	}
	proposed := cloneAccessGraph(before)
	proposed.Accounts[idx].Account = a
	proposed.Accounts[idx].Organization = org
	proposed.Accounts[idx].Grants = []accessRoleGrant{{Role: role}}
	if !before.Actor.CanManageAccount(proposed.Accounts[idx].snapshot()) {
		return domain.Account{}, accessDenied()
	}
	if e = ensureAccessManager(proposed); e != nil {
		return domain.Account{}, e
	}
	if e = lockAccessUsers(ctx, tx, []int64{a.ID}); e != nil {
		return domain.Account{}, e
	}
	scope, e := json.Marshal(a.Scope)
	if e != nil {
		return domain.Account{}, e
	}
	rawOrg, e := organizationJSON(org)
	if e != nil {
		return domain.Account{}, e
	}
	changes := sq.Eq{"login": a.Login, "name": a.Name, "role": a.Role, "scope": scope, "active": a.Active, "organization_scope": rawOrg}
	if hash != nil {
		changes["password_hash"] = *hash
	}
	if _, e = execute(ctx, tx, sqlBuilder.Update("auth_user").SetMap(changes).Where(sq.Eq{"id": a.ID})); e != nil {
		return domain.Account{}, e
	}
	if e = replaceAccessLinks(ctx, tx, "auth_user_role", "user_id", a.ID, "role_id", []int64{role.ID}); e != nil {
		return domain.Account{}, e
	}
	if e = bumpAccessUsers(ctx, tx, []int64{a.ID}); e != nil {
		return domain.Account{}, e
	}
	if e = bumpAccessGraph(ctx, tx); e != nil {
		return domain.Account{}, e
	}
	after, e := readAccessGraphUnchecked(ctx, tx, actorID)
	if e != nil {
		return domain.Account{}, e
	}
	if e = writeAccessEvent(ctx, tx, actorID, before, after, []int64{a.ID}, domain.AccessChange{}); e != nil {
		return domain.Account{}, e
	}
	out := after.Accounts[after.accountIndex(a.ID)].Account
	if e = tx.Commit(ctx); e != nil {
		return domain.Account{}, e
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
	if _, err = execute(ctx, tx, sqlBuilder.Insert("auth_session").Columns("token_hash", "user_id", "expires_at", "authz_epoch").Values(hash, id, expires, sq.Expr("(SELECT authz_epoch FROM auth_user WHERE id=?)", id))); err != nil {
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
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = lockAccessGraph(ctx, tx); e != nil {
		return e
	}
	actorID, e := accessActorID(ctx)
	if e != nil {
		return e
	}
	before, e := readAccessGraph(ctx, tx, actorID, "users.update")
	if e != nil {
		return e
	}
	idx := before.accountIndex(id)
	if idx < 0 || !before.Actor.CanManageAccount(before.Accounts[idx].snapshot()) {
		return domain.NewError(domain.Missing, "Пользователь не найден")
	}
	if e = lockAccessUsers(ctx, tx, []int64{id}); e != nil {
		return e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Insert("auth_identity").Columns("provider_id", "subject", "user_id").Values(provider, subject, id)); e != nil {
		return e
	}
	if e = bumpAccessUsers(ctx, tx, []int64{id}); e != nil {
		return e
	}
	if e = bumpAccessGraph(ctx, tx); e != nil {
		return e
	}
	after, e := readAccessGraphUnchecked(ctx, tx, actorID)
	if e != nil {
		return e
	}
	if e = writeAccessEvent(ctx, tx, actorID, before, after, []int64{id}, domain.AccessChange{}); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
