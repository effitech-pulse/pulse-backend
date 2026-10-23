package service

import (
	"context"
	"strconv"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// requireDataPermission использует снимок текущего входа; compat сохраняет старый профиль.
func requireDataPermission(ctx context.Context, permission domain.Permission) error {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || !p.Authenticated || p.Authorization == nil {
		return domain.NewError(domain.Forbidden, "authorization required")
	}
	if domain.RBACEnforced(ctx) || !p.CompatibilityAuthority() {
		return domain.RequirePermissions(ctx, permission)
	}
	return nil
}

func requireFullApplicationScope(ctx context.Context, permission domain.Permission) error {
	if err := requireDataPermission(ctx, permission); err != nil {
		return err
	}
	p, ok := domain.CurrentPrincipal(ctx)
	if !domain.RBACEnforced(ctx) && p.CompatibilityAuthority() {
		return nil
	}
	if !ok || p.Authorization == nil || !p.Authorization.FullApplicationScope() {
		return domain.NewError(domain.Forbidden, "Требуется полная область приложения")
	}
	return nil
}

// RequireWell связывает permission и текущую скважину, затем технический фонд.
func (c *Catalog) RequireWell(ctx context.Context, permission domain.Permission, wellID int64) (domain.Well, error) {
	if err := requireDataPermission(ctx, permission); err != nil {
		return domain.Well{}, err
	}
	w, err := c.store.Well(ctx, wellID)
	if err != nil {
		return w, err
	}
	if !domain.CanReadWell(ctx, w) {
		return domain.Well{}, domain.NewError(domain.Missing, "Скважина не найдена")
	}
	if c.scope != nil {
		return c.scope.RequireWell(ctx, wellID)
	}
	return w, nil
}

// RequireWellSet проверяет пакет одним чтением каталога до первого эффекта.
func (c *Catalog) RequireWellSet(ctx context.Context, permission domain.Permission, wellIDs []int64) error {
	if err := requireDataPermission(ctx, permission); err != nil {
		return err
	}
	wells, err := c.Wells(ctx, false)
	if err != nil {
		return err
	}
	allowed := make(map[int64]bool, len(wells))
	for _, w := range wells {
		allowed[w.ID] = true
	}
	for _, id := range wellIDs {
		if !allowed[id] {
			return domain.NewError(domain.Missing, "Скважина не найдена")
		}
	}
	return nil
}

func requireWellValue(ctx context.Context, permission domain.Permission, w domain.Well) error {
	if err := requireDataPermission(ctx, permission); err != nil {
		return err
	}
	if !domain.CanReadWell(ctx, w) {
		return domain.NewError(domain.Missing, "Скважина не найдена")
	}
	return nil
}

// requireTelemetrySet разрешает весь пакет через один текущий каталог.
func (s *Integrations) requireTelemetrySet(ctx context.Context, rawIDs []string) error {
	if !domain.RBACEnforced(ctx) {
		for _, id := range rawIDs {
			if err := s.requireTelemetry(ctx, id); err != nil {
				return err
			}
		}
		return nil
	}
	if err := requireDataPermission(ctx, "telemetry.read"); err != nil {
		return err
	}
	catalog, ok := s.store.(interface {
		Wells(context.Context, bool) ([]domain.Well, error)
	})
	if !ok {
		return domain.NewError(domain.Unavailable, "Справочник скважин не подключён")
	}
	wells, err := catalog.Wells(ctx, false)
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	members := map[int64]bool{}
	if s.scope != nil {
		_, rows, err := s.scope.Members(ctx)
		if err != nil {
			return err
		}
		for _, w := range rows {
			members[w.ID] = true
		}
	}
	for _, w := range wells {
		if !domain.CanReadWell(ctx, w) || s.scope != nil && !members[w.ID] {
			continue
		}
		allowed[PulseTelemetryID(w.ID)] = true
		if w.UnitekID != nil {
			allowed[strconv.FormatInt(*w.UnitekID, 10)] = true
		}
	}
	for _, id := range rawIDs {
		if !allowed[id] {
			return domain.NewError(domain.Missing, "Скважина не найдена")
		}
	}
	return nil
}

// currentTelemetryWell не доверяет внешним привязкам в переданной карточке.
func (s *Integrations) currentTelemetryWell(ctx context.Context, w domain.Well) (domain.Well, error) {
	if domain.RBACEnforced(ctx) {
		resolver, ok := s.store.(interface {
			Well(context.Context, int64) (domain.Well, error)
		})
		if !ok {
			return domain.Well{}, domain.NewError(domain.Unavailable, "Справочник не подключён")
		}
		current, err := resolver.Well(ctx, w.ID)
		if err != nil {
			return domain.Well{}, err
		}
		w = current
	}
	if err := requireWellValue(ctx, "telemetry.read", w); err != nil {
		return domain.Well{}, err
	}
	return w, nil
}
