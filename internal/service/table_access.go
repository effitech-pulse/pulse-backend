package service

import (
	"context"
	"encoding/json"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// ParseAccessTableRequest проверяет opt-in selectors до чтения графа доступа.
func ParseAccessTableRequest(ctx context.Context, dataset string, parent int64, raw string, size int, cursor string) (domain.TableRequest, error) {
	return parseTableRequest(ctx, dataset, parent, raw, size, cursor)
}

// AccessTableCursor подписывает позицию внутри уже проверенного полного графа.
func AccessTableCursor(req domain.TableRequest, keys []json.RawMessage) (string, error) {
	raw, err := json.Marshal(keys)
	if err != nil {
		return "", err
	}
	return encodeTableCursor(req, raw)
}

type accessUserFilterOptionsStore interface {
	ListAccessUserFilterOptions(context.Context, int64, string, domain.TableRequest) (domain.AccessFilterOptionsPage, error)
}

// UserFilterOptions читает bounded сводки назначений; определения ролей сюда не входят.
func (a *Access) UserFilterOptions(ctx context.Context, field, search string, size int, cursor string) (domain.AccessFilterOptionsPage, error) {
	out := domain.AccessFilterOptionsPage{}
	dataset := "access_role_options"
	switch field {
	case "role_ids":
	case "group_ids":
		dataset = "access_group_options"
	case "ngdu_id":
		dataset = "access_ngdu_options"
	case "department_id":
		dataset = "access_department_options"
	default:
		return out, tableInvalid()
	}
	actor, e := managementActor(ctx)
	if e != nil {
		return out, e
	}
	store, ok := a.store.(accessUserFilterOptionsStore)
	if !a.Enabled() || !ok {
		return out, domain.NewError(domain.Unavailable, "Управление доступом недоступно")
	}
	raw, e := json.Marshal(domain.TableQuery{Version: 1, Filters: []domain.TableFilter{{Field: "name", Op: "contains", Value: &search}}, Sort: []domain.TableSort{{Field: "name", Direction: "asc"}}})
	if e != nil {
		return out, e
	}
	req, e := parseTableRequest(ctx, dataset, 0, string(raw), size, cursor)
	if e != nil {
		return out, e
	}
	return store.ListAccessUserFilterOptions(ctx, actor, field, req)
}

type accessGroupFilterOptionsStore interface {
	ListAccessGroupFilterOptions(context.Context, int64, domain.TableRequest) (domain.AccessFilterOptionsPage, error)
}

// GroupFilterOptions возвращает только ID ролей, уже раскрытые в разрешённых группах.
func (a *Access) GroupFilterOptions(ctx context.Context, field, search string, size int, cursor string) (domain.AccessFilterOptionsPage, error) {
	out := domain.AccessFilterOptionsPage{}
	if field != "role_ids" {
		return out, tableInvalid()
	}
	actor, e := managementActor(ctx)
	if e != nil {
		return out, e
	}
	store, ok := a.store.(accessGroupFilterOptionsStore)
	if !a.Enabled() || !ok {
		return out, domain.NewError(domain.Unavailable, "Управление доступом недоступно")
	}
	raw, e := json.Marshal(domain.TableQuery{Version: 1, Filters: []domain.TableFilter{{Field: "name", Op: "contains", Value: &search}}, Sort: []domain.TableSort{{Field: "name", Direction: "asc"}}})
	if e != nil {
		return out, e
	}
	req, e := parseTableRequest(ctx, "access_group_role_options", 0, string(raw), size, cursor)
	if e != nil {
		return out, e
	}
	return store.ListAccessGroupFilterOptions(ctx, actor, req)
}
