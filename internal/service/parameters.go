package service

import (
	"context"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type ParameterSource interface {
	Load(context.Context) ([]domain.Parameter, error)
}

type Parameters struct{ source ParameterSource }

func NewParameters(source ParameterSource) *Parameters {
	return &Parameters{source: source}
}

func (p *Parameters) List(ctx context.Context) ([]domain.Parameter, error) {
	return p.source.Load(ctx)
}
