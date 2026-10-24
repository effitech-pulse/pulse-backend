package service

import (
	"context"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type tableAuditStore interface {
	AuditTable(context.Context, domain.TableRequest) (domain.TableRows[domain.AuditEntry], error)
}
type tableLogsStore interface {
	CollectionLogsTable(context.Context, domain.TableRequest) (domain.TableRows[domain.TableCollectionLog], error)
}
type tableChecksStore interface {
	ManualChecksTable(context.Context, int64, domain.TableRequest) (domain.TableRows[domain.ManualCheck], error)
}
type tableHistoryStore interface {
	WellHistoryTable(context.Context, int64, domain.TableRequest) (domain.TableRows[domain.TableWellEvent], error)
}
type tableJobsStore interface {
	FileJobsTable(context.Context, int64, domain.TableRequest) (domain.TableRows[domain.FileJobStatus], error)
}

func (c *Catalog) AuditTable(ctx context.Context, raw string, size int, cursor string) (domain.TablePage[domain.AuditEntry], error) {
	if err := requireFullApplicationScope(ctx, "audit.read"); err != nil {
		return domain.TablePage[domain.AuditEntry]{}, err
	}
	req, err := parseTableRequest(ctx, "audit", 0, raw, size, cursor)
	if err != nil {
		return domain.TablePage[domain.AuditEntry]{}, err
	}
	if c.scope != nil {
		return tablePage(req, domain.TableRows[domain.AuditEntry]{}, nil)
	}
	store, ok := c.store.(tableAuditStore)
	if !ok {
		return domain.TablePage[domain.AuditEntry]{}, domain.NewError(domain.Unavailable, "table_query_unavailable")
	}
	rows, err := store.AuditTable(ctx, req)
	return tablePage(req, rows, err)
}
func (s *Integrations) LogsTable(ctx context.Context, raw string, size int, cursor string) (domain.TablePage[domain.TableCollectionLog], error) {
	if err := requireDataPermission(ctx, "integrations.logs.read"); err != nil {
		return domain.TablePage[domain.TableCollectionLog]{}, err
	}
	req, err := parseTableRequest(ctx, "ingestlogs", 0, raw, size, cursor)
	if err != nil {
		return domain.TablePage[domain.TableCollectionLog]{}, err
	}
	if s.scope != nil || domain.RBACEnforced(ctx) && requireFullApplicationScope(ctx, "integrations.logs.read") != nil {
		return tablePage(req, domain.TableRows[domain.TableCollectionLog]{}, nil)
	}
	store, ok := s.store.(tableLogsStore)
	if !ok {
		return domain.TablePage[domain.TableCollectionLog]{}, domain.NewError(domain.Unavailable, "table_query_unavailable")
	}
	rows, err := store.CollectionLogsTable(ctx, req)
	return tablePage(req, rows, err)
}
func (s *Checks) ListTable(ctx context.Context, well int64, raw string, size int, cursor string) (domain.TablePage[domain.ManualCheck], error) {
	if err := requireDataPermission(ctx, "checks.read"); err != nil {
		return domain.TablePage[domain.ManualCheck]{}, err
	}
	if domain.RBACEnforced(ctx) {
		store, ok := s.store.(Store)
		if !ok {
			return domain.TablePage[domain.ManualCheck]{}, domain.NewError(domain.Unavailable, "Справочник не подключён")
		}
		if _, err := NewCatalog(store).WithScope(s.scope).RequireWell(ctx, "checks.read", well); err != nil {
			return domain.TablePage[domain.ManualCheck]{}, err
		}
	}
	if err := scopedWell(ctx, s.scope, well); err != nil {
		return domain.TablePage[domain.ManualCheck]{}, err
	}
	req, err := parseTableRequest(ctx, "manualchecks", well, raw, size, cursor)
	if err != nil {
		return domain.TablePage[domain.ManualCheck]{}, err
	}
	store, ok := s.store.(tableChecksStore)
	if !ok {
		return domain.TablePage[domain.ManualCheck]{}, domain.NewError(domain.Unavailable, "table_query_unavailable")
	}
	rows, err := store.ManualChecksTable(ctx, well, req)
	return tablePage(req, rows, err)
}
func (c *Catalog) WellHistoryTable(ctx context.Context, well int64, raw string, size int, cursor string) (domain.TablePage[domain.TableWellEvent], error) {
	if _, err := c.RequireWell(ctx, "wells.history.read", well); err != nil {
		return domain.TablePage[domain.TableWellEvent]{}, err
	}
	req, err := parseTableRequest(ctx, "wellhistory", well, raw, size, cursor)
	if err != nil {
		return domain.TablePage[domain.TableWellEvent]{}, err
	}
	store, ok := c.store.(tableHistoryStore)
	if !ok {
		return domain.TablePage[domain.TableWellEvent]{}, domain.NewError(domain.Unavailable, "table_query_unavailable")
	}
	rows, err := store.WellHistoryTable(ctx, well, req)
	return tablePage(req, rows, err)
}
func (s *FileJobs) ListTable(ctx context.Context, well int64, raw string, size int, cursor string) (domain.TablePage[domain.FileJobStatus], error) {
	ctx, _, _, err := s.checks.authorize(ctx, well, "filechecks.read")
	if err != nil {
		return domain.TablePage[domain.FileJobStatus]{}, err
	}
	req, err := parseTableRequest(ctx, "filejobs", well, raw, size, cursor)
	if err != nil {
		return domain.TablePage[domain.FileJobStatus]{}, err
	}
	store, ok := s.store.(tableJobsStore)
	if !ok {
		return domain.TablePage[domain.FileJobStatus]{}, domain.NewError(domain.Unavailable, "table_query_unavailable")
	}
	rows, err := store.FileJobsTable(ctx, well, req)
	return tablePage(req, rows, err)
}
