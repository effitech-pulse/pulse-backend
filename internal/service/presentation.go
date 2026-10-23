package service

import (
	"context"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type PresentationStore interface {
	PresentationWells(context.Context) ([]domain.PresentationWell, error)
}
type PresentationSource interface {
	Scenario(context.Context) (domain.PresentationScenario, error)
}
type Presentation struct {
	scope  FundScope
	store  PresentationStore
	source PresentationSource
}

func (s *Presentation) WithScope(scope FundScope) *Presentation { s.scope = scope; return s }

func NewPresentation(store PresentationStore) *Presentation { return &Presentation{store: store} }
func (s *Presentation) Summary(ctx context.Context) (domain.PresentationSummary, error) {
	for _, permission := range []domain.Permission{"wells.read", "risks.read", "incidents.read"} {
		if err := requireDataPermission(ctx, permission); err != nil {
			return domain.PresentationSummary{}, err
		}
	}
	rows, err := s.store.PresentationWells(ctx)
	if err != nil {
		return domain.PresentationSummary{}, err
	}
	members := map[int64]bool{}
	if s.scope != nil {
		_, wells, err := s.scope.Members(ctx)
		if err != nil {
			return domain.PresentationSummary{}, err
		}
		for _, w := range wells {
			members[w.ID] = true
		}
	}
	visible := make([]domain.PresentationWell, 0, len(rows))
	for _, row := range rows {
		if domain.CanReadWell(ctx, row.Well) && (s.scope == nil || members[row.Well.ID]) {
			visible = append(visible, row)
		}
	}
	rows = visible
	out := domain.PresentationSummary{Wells: rows, Count: len(rows)}
	for _, row := range rows {
		if row.LatestCheck == nil {
			continue
		}
		out.Checked++
		switch row.LatestCheck.Verdict {
		case "watch":
			out.Watch++
		case "alert":
			out.Alert++
		case "insufficient":
			out.Insufficient++
		}
	}
	return out, nil
}

func (s *Presentation) WithSource(source PresentationSource) *Presentation {
	s.source = source
	return s
}
func (s *Presentation) Scenario(ctx context.Context) (domain.PresentationScenario, error) {
	if err := requireFullApplicationScope(ctx, "presentation.scenario.read"); err != nil {
		return domain.PresentationScenario{}, err
	}
	if s.source == nil {
		return domain.PresentationScenario{}, domain.NewError(domain.Unavailable, "Pulse Data не настроен")
	}
	return s.source.Scenario(ctx)
}
