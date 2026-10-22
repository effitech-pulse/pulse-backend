package grpcml

import (
	"context"
	"errors"
	"io"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type cohortErrorScorer struct{ err error }

func (s cohortErrorScorer) UploadCohortPinned(context.Context, domain.ExecutionPin, domain.PinnedInputIdentity, io.Reader) (domain.UploadResult, error) {
	return domain.UploadResult{}, s.err
}
func TestCohortJobScorerSafeTransientBoundary(t *testing.T) {
	for _, tc := range []struct {
		code          codes.Code
		message, want string
	}{
		{codes.Unavailable, "private", "ml_unavailable"}, {codes.DeadlineExceeded, "private", "ml_deadline"}, {codes.ResourceExhausted, "execution_busy", "execution_busy"}, {codes.ResourceExhausted, "execution_busy /private", "pinned_resource_limit"}, {codes.InvalidArgument, "execution_busy", "invalid_pinned_input"}, {codes.Unimplemented, "private", "pinned_protocol_unavailable"},
	} {
		t.Run(tc.want+tc.message, func(t *testing.T) {
			scorer := CohortJobScorer{Scorer: cohortErrorScorer{fileJobPinnedRPCError(status.Error(tc.code, tc.message))}}
			_, err := scorer.UploadCohortPinned(context.Background(), pinFixture(), domain.PinnedInputIdentity{}, nil)
			var de *domain.Error
			if !errors.As(err, &de) || de.Message != tc.want {
				t.Fatalf("wrong safe classification: %v", err)
			}
		})
	}
}
