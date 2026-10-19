package service

import (
	"context"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type CohortStore interface {
	CohortMembers(context.Context, string) (domain.Cohort, []domain.Well, error)
}
type FundScope interface {
	Members(context.Context) (domain.Cohort, []domain.Well, error)
	RequireWell(context.Context, int64) (domain.Well, error)
	RequireUnitek(context.Context, int64) (domain.Well, error)
}
type fundScope struct {
	store   CohortStore
	version string
	ids     map[int64]bool
}

func NewFundScope(ctx context.Context, store CohortStore, version string) (FundScope, error) {
	s := &fundScope{store: store, version: version, ids: map[int64]bool{}}
	c, w, err := store.CohortMembers(ctx, version)
	if err != nil {
		return nil, err
	}
	if version == "" || c.Version != version || len(w) != 100 {
		return nil, domain.NewError(domain.Invalid, "Нужна полная версия когорты из 100 скважин")
	}
	external := map[int64]bool{}
	for _, v := range w {
		if v.ID <= 0 || s.ids[v.ID] {
			return nil, domain.NewError(domain.Invalid, "Неоднозначный состав когорты")
		}
		s.ids[v.ID] = true
		if v.UnitekID != nil {
			if external[*v.UnitekID] {
				return nil, domain.NewError(domain.Invalid, "Неоднозначная привязка когорты")
			}
			external[*v.UnitekID] = true
		}
	}
	return s, nil
}
func (s *fundScope) Members(ctx context.Context) (domain.Cohort, []domain.Well, error) {
	c, w, err := s.store.CohortMembers(ctx, s.version)
	if err != nil {
		return c, nil, err
	}
	if len(w) != 100 {
		return c, nil, domain.NewError(domain.Unavailable, "Состав когорты повреждён")
	}
	for _, v := range w {
		if !s.ids[v.ID] {
			return c, nil, domain.NewError(domain.Unavailable, "Состав когорты изменён")
		}
	}
	return c, w, nil
}
func (s *fundScope) RequireWell(ctx context.Context, id int64) (domain.Well, error) {
	if s.ids[id] {
		_, w, err := s.Members(ctx)
		if err != nil {
			return domain.Well{}, err
		}
		for _, v := range w {
			if v.ID == id && domain.CanReadWell(ctx, v) {
				return v, nil
			}
		}
	}
	return domain.Well{}, domain.NewError(domain.Missing, "Скважина не найдена")
}
func (s *fundScope) RequireUnitek(ctx context.Context, id int64) (domain.Well, error) {
	_, w, err := s.Members(ctx)
	if err != nil {
		return domain.Well{}, err
	}
	for _, v := range w {
		if v.UnitekID != nil && *v.UnitekID == id && domain.CanReadWell(ctx, v) {
			return v, nil
		}
	}
	return domain.Well{}, domain.NewError(domain.Missing, "Скважина не найдена")
}
func scopedWell(ctx context.Context, s FundScope, id int64) error {
	if s == nil {
		return nil
	}
	_, err := s.RequireWell(ctx, id)
	return err
}
