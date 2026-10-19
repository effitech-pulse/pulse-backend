package service

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func (c *Catalog) FilteredWells(ctx context.Context, active bool, f domain.WellFilter) ([]domain.Well, error) {
	if utf8.RuneCountInString(f.Search) > 120 {
		return nil, domain.NewError(domain.Invalid, "Поиск не длиннее 120 символов")
	}
	if f.Sort != "" && f.Sort != "id_asc" && f.Sort != "id_desc" && f.Sort != "name_asc" && f.Sort != "name_desc" {
		return nil, domain.NewError(domain.Invalid, "Недопустимая сортировка")
	}
	if f.Archive != "" && f.Archive != "current" && f.Archive != "archived" && f.Archive != "all" {
		return nil, domain.NewError(domain.Invalid, "Недопустимый фильтр архива")
	}
	rows, err := c.WellsForFund(ctx, active, f.Fund)
	if err != nil {
		return nil, err
	}
	search := strings.ToLower(strings.TrimSpace(f.Search))
	out := make([]domain.Well, 0, len(rows))
	for _, w := range rows {
		if (f.Archive == "" || f.Archive == "current") && w.ArchivedAt != nil {
			continue
		}
		if f.Archive == "archived" && w.ArchivedAt == nil {
			continue
		}
		if f.NGDU != nil && w.NGDU != *f.NGDU {
			continue
		}
		if f.Department != nil && w.Department != *f.Department {
			continue
		}
		name := ""
		if w.WellNumber != nil {
			name = *w.WellNumber
		}
		if search != "" && !strings.Contains(strings.ToLower(name), search) && !strings.Contains(strconv.FormatInt(w.ID, 10), search) {
			continue
		}
		out = append(out, w)
	}
	if f.Sort != "" {
		slices.SortFunc(out, func(a, b domain.Well) int {
			v := 0
			if strings.HasPrefix(f.Sort, "name_") {
				an, bn := "", ""
				if a.WellNumber != nil {
					an = strings.ToLower(*a.WellNumber)
				}
				if b.WellNumber != nil {
					bn = strings.ToLower(*b.WellNumber)
				}
				v = cmp.Compare(an, bn)
			}
			if v == 0 {
				v = cmp.Compare(a.ID, b.ID)
			}
			if strings.HasSuffix(f.Sort, "_desc") {
				return -v
			}
			return v
		})
	}
	return out, nil
}
func (c *Catalog) FilteredIncidentStats(ctx context.Context, f domain.WellFilter) (domain.IncidentStats, error) {
	rows, err := c.FilteredWells(ctx, false, f)
	if err != nil {
		return domain.IncidentStats{}, err
	}
	ids := make([]int64, 0, len(rows))
	for _, w := range rows {
		if (f.Archive == "" || f.Archive == "current") && w.ArchivedAt != nil {
			continue
		}
		if f.Archive == "archived" && w.ArchivedAt == nil {
			continue
		}
		ids = append(ids, w.ID)
	}
	return c.store.IncidentStatsForWells(ctx, ids)
}
