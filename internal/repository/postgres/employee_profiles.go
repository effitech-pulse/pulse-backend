package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func managedProfile(ctx context.Context, conn db, id int64) (domain.ManagedProfile, error) {
	return one[domain.ManagedProfile](ctx, conn, sqlBuilder.Select("profile_revision::text AS revision", "job_title", `jsonb_build_object('organization',workplace_organization,'division',workplace_division,'department',workplace_department) AS workplace`, "manager_account_id::text AS manager_id").From("auth_user").Where(sq.Eq{"id": id}))
}
func profileProjection() sq.SelectBuilder {
	return sqlBuilder.Select("u.id::text AS id", "u.name", "u.active", "u.job_title", `jsonb_build_object('organization',u.workplace_organization,'division',u.workplace_division,'department',u.workplace_department) AS workplace`, `CASE WHEN m.id IS NULL THEN NULL ELSE jsonb_build_object('id',m.id::text,'name',m.name,'job_title',m.job_title,'active',m.active) END AS manager`).From("auth_user u").LeftJoin("auth_user m ON m.id=u.manager_account_id")
}
func (r *Repository) withPeople(ctx context.Context, actor int64, fn func(db) error) error {
	tx, e := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	a, e := authorization(ctx, tx, actor)
	if e != nil {
		return e
	}
	if !a.Active {
		return domain.NewError(domain.Unauthorized, "Войдите в систему")
	}
	if p, ok := domain.CurrentPrincipal(ctx); ok {
		if !p.Authenticated || p.AccountID == nil || *p.AccountID != actor || p.Mechanism != "session" {
			return domain.NewError(domain.Forbidden, "Требуется персональный вход")
		}
		if p.Authorization != nil && p.Authorization.Epoch != a.Epoch {
			return domain.NewError(domain.Unauthorized, "Полномочия изменены; войдите снова")
		}
	}
	if e = fn(tx); e != nil {
		return e
	}
	return tx.Commit(ctx)
}

type peopleCursor struct {
	Last     int64  `json:"last"`
	Search   string `json:"search_sha256"`
	Inactive bool   `json:"inactive"`
}

func (r *Repository) ListPeople(ctx context.Context, actor int64, q domain.PeopleQuery) (out domain.PeoplePage, e error) {
	q.Search = strings.TrimSpace(q.Search)
	if q.Limit == 0 {
		q.Limit = 25
	}
	invalid := func() error {
		return domain.NewError(domain.Invalid, "Некорректный запрос справочника")
	}
	if !utf8.ValidString(q.Search) || utf8.RuneCountInString(q.Search) > 200 || q.Limit < 1 || q.Limit > 100 || len(q.Cursor) > 1024 {
		return out, invalid()
	}
	filterHash := sha256.Sum256([]byte(q.Search))
	searchFingerprint := hex.EncodeToString(filterHash[:])
	last := int64(0)
	if q.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
		var c peopleCursor
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err != nil || decoder.Decode(&c) != nil || decoder.Decode(new(any)) != io.EOF || c.Last < 1 || c.Search != searchFingerprint || c.Inactive != q.IncludeInactive {
			return out, invalid()
		}
		last = c.Last
	}
	e = r.withPeople(ctx, actor, func(conn db) error {
		query := profileProjection().Where(sq.Gt{"u.id": last}).OrderBy("u.id").Limit(uint64(q.Limit + 1))
		if !q.IncludeInactive {
			query = query.Where(sq.Eq{"u.active": true})
		}
		if q.Search != "" {
			pattern := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q.Search) + "%"
			terms := sq.Or{}
			for _, col := range []string{"u.name", "u.job_title", "u.workplace_organization", "u.workplace_division", "u.workplace_department"} {
				terms = append(terms, sq.ILike{col: pattern})
			}
			query = query.Where(terms)
		}
		items, err := list[domain.EmployeeProfile](ctx, conn, query)
		if err != nil {
			return err
		}
		out.Items = items
		if out.Items == nil {
			out.Items = []domain.EmployeeProfile{}
		}
		if len(items) > q.Limit {
			out.Items = items[:q.Limit]
			raw, _ := json.Marshal(peopleCursor{out.Items[q.Limit-1].ID, searchFingerprint, q.IncludeInactive})
			v := base64.RawURLEncoding.EncodeToString(raw)
			out.NextCursor = &v
		}
		return nil
	})
	return
}
func (r *Repository) EmployeeProfile(ctx context.Context, actor, target int64) (out domain.EmployeeProfile, e error) {
	e = r.withPeople(ctx, actor, func(conn db) error {
		var err error
		out, err = one[domain.EmployeeProfile](ctx, conn, profileProjection().Where(sq.Eq{"u.id": target}))
		return err
	})
	return
}
func evaluateProfile(ctx context.Context, conn db, u *domain.UserAccessChange) (*domain.ProfileChangeSummary, bool, error) {
	if u == nil {
		return nil, false, nil
	}
	var before *domain.NamedProfile
	old := domain.ManagedProfile{Revision: 1}
	if u.ID != nil {
		p, e := managedProfile(ctx, conn, *u.ID)
		if e != nil {
			return nil, false, e
		}
		old = p
		a, e := one[domain.Account](ctx, conn, sqlBuilder.Select("name").From("auth_user").Where(sq.Eq{"id": *u.ID}))
		if e != nil {
			return nil, false, e
		}
		before = &domain.NamedProfile{Name: a.Name, Profile: old}
	}
	next := old
	if u.Profile.Present {
		next = service.NormalizeProfile(u.Profile.Value)
		if u.ID != nil && next.Revision != old.Revision {
			return nil, false, domain.NewError(domain.Duplicate, "profile_revision_conflict")
		}
		if next.ManagerID != nil {
			// Each step is bounded by an existing account and the visited set rejects malformed cycles.
			seen := map[int64]bool{}
			if u.ID != nil {
				seen[*u.ID] = true
			}
			id := *next.ManagerID
			for {
				if seen[id] {
					return nil, false, domain.NewError(domain.Invalid, "Цикл руководителей недопустим")
				}
				seen[id] = true
				rows, e := list[struct {
					Manager *int64 `json:"manager"`
				}](ctx, conn, sqlBuilder.Select("manager_account_id AS manager").From("auth_user").Where(sq.Eq{"id": id}))
				if e != nil {
					return nil, false, e
				}
				if len(rows) != 1 {
					return nil, false, domain.NewError(domain.Invalid, "Руководитель не найден")
				}
				if rows[0].Manager == nil {
					break
				}
				id = *rows[0].Manager
			}
		}
	}
	next.Revision = old.Revision
	changed := !reflect.DeepEqual(old, next)
	if u.ID == nil {
		next.Revision = 1
		changed = u.Profile.Present
	} else if changed {
		next.Revision++
	}
	if !u.Profile.Present && u.ID != nil {
		return nil, false, nil
	}
	return &domain.ProfileChangeSummary{Before: before, After: domain.NamedProfile{Name: u.Name, Profile: next}}, changed, nil
}
func writeProfile(ctx context.Context, tx pgx.Tx, id int64, p domain.ManagedProfile) error {
	_, e := execute(ctx, tx, sqlBuilder.Update("auth_user").SetMap(sq.Eq{"job_title": p.JobTitle, "workplace_organization": p.Workplace.Organization, "workplace_division": p.Workplace.Division, "workplace_department": p.Workplace.Department, "manager_account_id": p.ManagerID, "profile_revision": p.Revision}).Where(sq.Eq{"id": id}))
	return e
}

// Profile-only classification is opt-in; omitted profile keeps accepted legacy semantics.
func metadataOnly(g accessGraph, u *domain.UserAccessChange) bool {
	if u == nil || u.ID == nil || !u.Profile.Present || u.ResetPassword {
		return false
	}
	i := g.accountIndex(*u.ID)
	if i < 0 {
		return false
	}
	a := g.Accounts[i]
	return a.Account.Login == u.Login && a.Account.Name == u.Name && a.Account.Active == u.Active && sameIDSet(directAccessRoles(a), u.RoleIDs) && sameIDSet(g.userGroups(*u.ID), u.GroupIDs) && (!u.Organization.Present || reflect.DeepEqual(a.Organization, &u.Organization.Value))
}

// ValidateEmployeeProfiles проверяет готовность API без DDL и без изменения activation.
func (r *Repository) ValidateEmployeeProfiles(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var ready bool
	e := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM backend_schema_migration WHERE version=17) AND (SELECT count(*)=6 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='auth_user' AND column_name IN ('job_title','workplace_organization','workplace_division','workplace_department','manager_account_id','profile_revision'))`).Scan(&ready)
	if e != nil {
		return e
	}
	if !ready {
		return domain.NewError(domain.Unavailable, "employee_profiles_schema_required")
	}
	return nil
}
