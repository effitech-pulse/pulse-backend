package service

import (
	"context"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type tablePeopleStore interface {
	PeopleTable(context.Context, int64, domain.TableRequest) (domain.TableRows[domain.EmployeeProfile], error)
}

func (a *Access) ListPeopleTable(ctx context.Context, raw string, size int, cursor string) (domain.TablePage[domain.EmployeeProfile], error) {
	s, actor, err := a.peopleStore(ctx)
	if err != nil {
		return domain.TablePage[domain.EmployeeProfile]{}, err
	}
	req, err := parseTableRequest(ctx, "people", 0, raw, size, cursor)
	if err != nil {
		return domain.TablePage[domain.EmployeeProfile]{}, err
	}
	store, ok := s.(tablePeopleStore)
	if !ok {
		return domain.TablePage[domain.EmployeeProfile]{}, domain.NewError(domain.Unavailable, "table_query_unavailable")
	}
	rows, err := store.PeopleTable(ctx, actor, req)
	return tablePage(req, rows, err)
}
