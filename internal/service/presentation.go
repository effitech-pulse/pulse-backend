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
	rows, err := s.store.PresentationWells(ctx)
	if err != nil {
		return domain.PresentationSummary{}, err
	}
	visible := make([]domain.PresentationWell, 0, len(rows))
	for _, row := range rows {
		if domain.CanReadWell(ctx, row.Well) && scopedWell(ctx, s.scope, row.Well.ID) == nil {
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
	if s.source == nil {
		return domain.PresentationScenario{}, domain.NewError(domain.Unavailable, "Pulse Data не настроен")
	}
	return s.source.Scenario(ctx)
}
