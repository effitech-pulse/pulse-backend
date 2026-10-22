package grpcml

import (
	"context"
	"errors"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type cohortPinnedScorer interface {
	UploadCohortPinned(context.Context, domain.ExecutionPin, domain.PinnedInputIdentity, io.Reader) (domain.UploadResult, error)
}

// CohortJobScorer переводит безопасные transport-коды в finite причины cohort worker.
type CohortJobScorer struct{ Scorer cohortPinnedScorer }

func (s CohortJobScorer) UploadCohortPinned(ctx context.Context, pin domain.ExecutionPin, input domain.PinnedInputIdentity, canonical io.Reader) (domain.UploadResult, error) {
	result, err := s.Scorer.UploadCohortPinned(ctx, pin, input, canonical)
	if err == nil {
		return result, nil
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		return result, domain.NewError(domain.Unavailable, "pinned_execution_unavailable")
	}
	reason := de.Message
	switch status.Code(err) {
	case codes.Unavailable:
		reason = "ml_unavailable"
	case codes.DeadlineExceeded:
		reason = "ml_deadline"
	}
	return result, domain.NewError(de.Kind, reason)
}
