package postgres

import (
	"context"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func (r *Repository) PeopleTable(ctx context.Context, actor int64, req domain.TableRequest) (out domain.TableRows[domain.EmployeeProfile], err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = r.withPeople(ctx, actor, func(conn db) error {
		var e error
		out, e = tableRows[domain.EmployeeProfile](ctx, conn, profileProjection(), "people", req)
		return e
	})
	return
}
