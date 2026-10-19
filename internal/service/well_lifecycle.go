package service

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func (c *Catalog) SetWellArchive(ctx context.Context, id int64, in domain.WellLifecycleInput, archive bool) (domain.Well, error) {
	if p, ok := domain.CurrentPrincipal(ctx); ok && p.Role != "admin" {
		return domain.Well{}, domain.NewError(domain.Forbidden, "Архивом управляет администратор")
	}
	if err := scopedWell(ctx, c.scope, id); err != nil {
		return domain.Well{}, err
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if in.Revision < 1 || in.Reason == "" || utf8.RuneCountInString(in.Reason) > 1000 {
		return domain.Well{}, domain.NewError(domain.Invalid, "Укажите версию карточки и причину (до 1000 символов)")
	}
	return c.store.SetWellArchive(ctx, id, &in.Revision, archive, in.Reason)
}
func (c *Catalog) WellHistory(ctx context.Context, id, before int64) (domain.WellHistory, error) {
	if _, err := c.Well(ctx, id); err != nil {
		return domain.WellHistory{}, err
	}
	return c.store.WellHistory(ctx, id, before)
}
