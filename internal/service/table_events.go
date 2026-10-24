package service

import (
	"context"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type tableEventsStore interface {
	EventsTable(context.Context, domain.EventReadScope, domain.TableRequest) (domain.TableRows[domain.EventRecord], error)
}

func (s *Events) ListTable(ctx context.Context, raw string, size int, cursor string) (domain.TablePage[domain.EventSummary], error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	scope, err := s.readScope(ctx)
	if err != nil {
		return domain.TablePage[domain.EventSummary]{}, err
	}
	req, err := parseTableRequest(ctx, "events", 0, raw, size, cursor, eventHash(scope))
	if err != nil {
		return domain.TablePage[domain.EventSummary]{}, err
	}
	store, ok := s.store.(tableEventsStore)
	if !ok {
		return domain.TablePage[domain.EventSummary]{}, domain.NewError(domain.Unavailable, "table_query_unavailable")
	}
	rows, err := store.EventsTable(ctx, scope, req)
	if err != nil {
		return domain.TablePage[domain.EventSummary]{}, eventStoreError(err)
	}
	summaries := domain.TableRows[domain.EventSummary]{Keys: rows.Keys, HasMore: rows.HasMore, Items: make([]domain.EventSummary, 0, len(rows.Items))}
	for _, record := range rows.Items {
		summary, e := s.summary(record)
		if e != nil {
			return domain.TablePage[domain.EventSummary]{}, e
		}
		summaries.Items = append(summaries.Items, summary)
	}
	return tablePage(req, summaries, nil)
}
