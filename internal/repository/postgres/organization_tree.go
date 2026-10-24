package postgres

import (
	"context"
	"errors"

	sq "github.com/Masterminds/squirrel"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

// OrganizationTreePeople читает весь bounded набор в том же RR snapshot, что и actor.
func (r *Repository) OrganizationTreePeople(ctx context.Context, actorID int64, q domain.OrganizationTreeQuery) (rows []domain.OrganizationTreePerson, err error) {
	ctx, cancel := context.WithTimeout(ctx, domain.OrganizationTreeReadTimeout)
	defer cancel()
	q, err = service.NormalizeOrganizationTreeQuery(q)
	if err != nil {
		return nil, err
	}
	err = r.withPeople(ctx, actorID, func(conn db) error {
		query := sqlBuilder.Select("u.id::text AS id", "u.name", "u.job_title", "u.active", `jsonb_build_object('organization',u.workplace_organization,'division',u.workplace_division,'department',u.workplace_department) AS workplace`).From("auth_user u").OrderBy("u.id").Limit(domain.OrganizationTreeEmployeeLimit + 1)
		if !q.IncludeInactive {
			query = query.Where(sq.Eq{"u.active": true})
		}
		if q.Search != "" {
			query = query.Where(peopleSearch(q.Search))
		}
		var e error
		rows, e = list[domain.OrganizationTreePerson](ctx, conn, query)
		return e
	})
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, domain.NewError(domain.Unavailable, "organization_tree_timeout")
	}
	if err != nil {
		return nil, err
	}
	if len(rows) > domain.OrganizationTreeEmployeeLimit {
		return nil, domain.NewError(domain.Unavailable, "organization_tree_too_large")
	}
	if rows == nil {
		rows = []domain.OrganizationTreePerson{}
	}
	return rows, nil
}
