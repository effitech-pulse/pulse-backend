package grpcml

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	pb "github.com/effitech-pulse/pulse-contracts/gen/go/pulse/ml/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type jobErrorScorer struct{ err error }

func (s jobErrorScorer) UploadPinned(context.Context, domain.ExecutionPin, domain.PinnedInputIdentity, io.Reader) (domain.UploadResult, error) {
	return domain.UploadResult{}, pinnedRPCError(s.err)
}
func TestFileJobScorerOnlyRetriesSafeTransientCodes(t *testing.T) {
	for _, tc := range []struct {
		code codes.Code
		want string
	}{{codes.Unavailable, "ml_unavailable"}, {codes.DeadlineExceeded, "ml_deadline"}, {codes.FailedPrecondition, "pinned_execution_unavailable"}, {codes.Unimplemented, "pinned_protocol_unavailable"}} {
		s := FileJobScorer{Scorer: jobErrorScorer{status.Error(tc.code, "private RPC content")}}
		_, e := s.UploadPinned(context.Background(), domain.ExecutionPin{}, domain.PinnedInputIdentity{}, strings.NewReader(""))
		if e == nil || e.Error() == "private RPC content" {
			t.Fatal(e)
		}
		var de *domain.Error
		if !errors.As(e, &de) || de.Message != tc.want || de.Plain {
			t.Fatalf("code %s error %v", tc.code, e)
		}
	}
}

type fileJobFailureServer struct {
	pb.UnimplementedMLServiceServer
	code   codes.Code
	reason string
}

func (s fileJobFailureServer) UploadPinned(stream grpc.ClientStreamingServer[pb.PinnedUploadChunk, pb.UploadResponse]) error {
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return status.Error(s.code, s.reason)
}
func TestFileJobExecutionBusyTransportPreservesQ0(t *testing.T) {
	for _, tc := range []struct {
		name           string
		code           codes.Code
		reason, q1, q0 string
		kind           domain.Kind
	}{
		{"busy", codes.ResourceExhausted, "execution_busy", "execution_busy", "pinned_resource_limit", domain.Limited},
		{"input_limit", codes.ResourceExhausted, "input_too_large", "pinned_resource_limit", "pinned_resource_limit", domain.Limited},
		{"result_limit", codes.ResourceExhausted, "result_too_large", "pinned_resource_limit", "pinned_resource_limit", domain.Limited},
		{"private_near_match", codes.ResourceExhausted, "execution_busy /private/unsafe", "pinned_resource_limit", "pinned_resource_limit", domain.Limited},
		{"same_word_wrong_status", codes.InvalidArgument, "execution_busy", "invalid_pinned_input", "invalid_pinned_input", domain.Invalid},
		{"missing_bundle", codes.FailedPrecondition, "bundle_unavailable", "bundle_unavailable", "bundle_unavailable", domain.Unavailable},
		{"corrupt_bundle", codes.FailedPrecondition, "bundle_corrupt", "bundle_corrupt", "bundle_corrupt", domain.Unavailable},
		{"unimplemented", codes.Unimplemented, "execution_busy", "pinned_protocol_unavailable", "pinned_protocol_unavailable", domain.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener := bufconn.Listen(1 << 20)
			server := grpc.NewServer()
			pb.RegisterMLServiceServer(server, fileJobFailureServer{code: tc.code, reason: tc.reason})
			go server.Serve(listener)
			defer server.Stop()
			conn, err := grpc.NewClient("passthrough:///fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			client := &GRPCClient{conn: conn, stub: pb.NewMLServiceClient(conn), timeout: time.Second, key: "synthetic"}
			raw := "timestamp,F_out\n2026-01-01T00:00:00Z,0\n"
			id := domain.PinnedInputIdentity{InputSnapshotID: strings.Repeat("a", 32), CanonicalSHA256: domain.FileSHA256([]byte(raw)), FingerprintSHA256: domain.FileSHA256([]byte("identity")), CanonicalBytes: int64(len(raw)), SourceKind: "uploaded_file"}
			_, err = client.UploadPinned(context.Background(), pinFixture(), id, strings.NewReader(raw))
			var legacy *domain.Error
			if !errors.As(err, &legacy) || legacy.Kind != tc.kind || legacy.Message != tc.q0 || !legacy.Plain || status.Code(err) != tc.code || strings.Contains(err.Error(), "private") {
				t.Fatalf("Q0 behavior changed: %v", err)
			}
			_, err = client.UploadFileJobPinned(context.Background(), pinFixture(), id, strings.NewReader(raw))
			var explicit *domain.Error
			if !errors.As(err, &explicit) || explicit.Kind != tc.kind || explicit.Message != tc.q1 || status.Code(err) != tc.code || strings.Contains(err.Error(), "private") {
				t.Fatalf("Q1 raw error mapping lost safe status: %v", err)
			}
			scorer := FileJobScorer{Scorer: client}
			_, err = scorer.UploadPinned(context.Background(), pinFixture(), id, strings.NewReader(raw))
			var actual *domain.Error
			if !errors.As(err, &actual) || actual.Kind != tc.kind || actual.Message != tc.q1 || actual.Plain || strings.Contains(err.Error(), "private") {
				t.Fatalf("Q1 expected %s actual %v", tc.q1, err)
			}
		})
	}
}
