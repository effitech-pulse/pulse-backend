package grpcml

import (
	"context"
	"errors"
	"io"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type fileJobPinnedScorer interface {
	UploadPinned(context.Context, domain.ExecutionPin, domain.PinnedInputIdentity, io.Reader) (domain.UploadResult, error)
}

type fileJobExplicitPinnedScorer interface {
	UploadFileJobPinned(context.Context, domain.ExecutionPin, domain.PinnedInputIdentity, io.Reader) (domain.UploadResult, error)
}

func fileJobPinnedRPCError(err error) error {
	if status.Code(err) == codes.ResourceExhausted && status.Convert(err).Message() == "execution_busy" {
		return &domain.Error{Kind: domain.Limited, Message: "execution_busy", Plain: true, Cause: &safeRPCStatus{code: codes.ResourceExhausted}}
	}
	return pinnedRPCError(err)
}

// FileJobScorer классифицирует retry только для Q1, сохраняя Q0 error behavior.
type FileJobScorer struct{ Scorer fileJobPinnedScorer }

func (s FileJobScorer) UploadPinned(ctx context.Context, pin domain.ExecutionPin, id domain.PinnedInputIdentity, r io.Reader) (domain.UploadResult, error) {
	var out domain.UploadResult
	var e error
	if explicit, ok := s.Scorer.(fileJobExplicitPinnedScorer); ok {
		out, e = explicit.UploadFileJobPinned(ctx, pin, id, r)
	} else {
		out, e = s.Scorer.UploadPinned(ctx, pin, id, r)
	}
	if e == nil {
		return out, nil
	}
	var de *domain.Error
	if !errors.As(e, &de) {
		return out, domain.NewFileJobProblem(domain.Unavailable, "pinned_execution_unavailable")
	}
	reason := de.Message
	switch status.Code(e) {
	case codes.Unavailable:
		reason = "ml_unavailable"
	case codes.DeadlineExceeded:
		reason = "ml_deadline"
	}
	return out, domain.NewFileJobProblem(de.Kind, reason)
}
