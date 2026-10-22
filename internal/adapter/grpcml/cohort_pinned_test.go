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
	"google.golang.org/protobuf/proto"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type cohortPinnedUploader interface {
	UploadCohortPinned(context.Context, domain.ExecutionPin, domain.PinnedInputIdentity, io.Reader) (domain.UploadResult, error)
}

func cohortUpload(t *testing.T, client *GRPCClient) cohortPinnedUploader {
	t.Helper()
	uploader, ok := any(client).(cohortPinnedUploader)
	if !ok {
		t.Fatal("cohort pinned entrypoint missing")
	}
	return uploader
}

func cohortIdentity(raw string) domain.PinnedInputIdentity {
	return domain.PinnedInputIdentity{InputSnapshotID: strings.Repeat("a", 32), CanonicalSHA256: domain.FileSHA256([]byte(raw)), FingerprintSHA256: domain.FileSHA256([]byte("cohort")), CanonicalBytes: int64(len(raw)), SourceKind: "cohort_snapshot"}
}

func TestPinnedEntrypointsRejectWrongKindBeforeRPC(t *testing.T) {
	client := &GRPCClient{} // Any RPC attempt would panic: validation must happen first.
	cohort := cohortUpload(t, client)
	for _, tc := range []struct {
		name   string
		upload func(context.Context, domain.ExecutionPin, domain.PinnedInputIdentity, io.Reader) (domain.UploadResult, error)
		kinds  []string
	}{
		{"cohort", cohort.UploadCohortPinned, []string{"uploaded_file", "unknown", ""}},
		{"file", client.UploadPinned, []string{"cohort_snapshot", "unknown", ""}},
		{"file_job", client.UploadFileJobPinned, []string{"cohort_snapshot", "unknown", ""}},
	} {
		for _, kind := range tc.kinds {
			t.Run(tc.name+"/"+kind, func(t *testing.T) {
				id := cohortIdentity("x")
				id.SourceKind = kind
				_, err := tc.upload(context.Background(), pinFixture(), id, strings.NewReader("x"))
				var de *domain.Error
				if !errors.As(err, &de) || de.Kind != domain.Invalid || de.Message != "invalid_pinned_input" {
					t.Fatalf("wrong source accepted: %v", err)
				}
			})
		}
	}
}

type cohortPinnedServer struct {
	pb.UnimplementedMLServiceServer
	header  chan *pb.PinnedUploadHeader
	content chan string
	legacy  chan struct{}
	code    codes.Code
	reason  string
}

func (s *cohortPinnedServer) Upload(grpc.ClientStreamingServer[pb.UploadChunk, pb.UploadResponse]) error {
	s.legacy <- struct{}{}
	return status.Error(codes.Internal, "legacy fallback")
}
func (s *cohortPinnedServer) UploadPinned(stream grpc.ClientStreamingServer[pb.PinnedUploadChunk, pb.UploadResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	s.header <- first.GetHeader()
	var raw strings.Builder
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		raw.Write(chunk.GetContent())
	}
	s.content <- raw.String()
	if s.code != codes.OK {
		return status.Error(s.code, s.reason)
	}
	return stream.SendAndClose(&pb.UploadResponse{NWindows: 17})
}
func newCohortPinnedClient(t *testing.T, fixture *cohortPinnedServer) *GRPCClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	pb.RegisterMLServiceServer(server, fixture)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &GRPCClient{conn: conn, stub: pb.NewMLServiceClient(conn), timeout: time.Second, key: "synthetic"}
}
func TestCohortPinnedTransportAndSafeErrors(t *testing.T) {
	for _, tc := range []struct {
		name         string
		code         codes.Code
		reason, want string
		kind         domain.Kind
	}{
		{name: "success"},
		{"busy", codes.ResourceExhausted, "execution_busy", "execution_busy", domain.Limited},
		{"unknown_limit", codes.ResourceExhausted, "private limit", "pinned_resource_limit", domain.Limited},
		{"near_busy", codes.ResourceExhausted, "execution_busy /private", "pinned_resource_limit", domain.Limited},
		{"wrong_status", codes.InvalidArgument, "execution_busy", "invalid_pinned_input", domain.Invalid},
		{"unsupported", codes.Unimplemented, "private", "pinned_protocol_unavailable", domain.Unavailable},
		{"runtime", codes.FailedPrecondition, "runtime_mismatch", "runtime_mismatch", domain.Unavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &cohortPinnedServer{header: make(chan *pb.PinnedUploadHeader, 1), content: make(chan string, 1), legacy: make(chan struct{}, 1), code: tc.code, reason: tc.reason}
			client := newCohortPinnedClient(t, fixture)
			raw := "timestamp,F_out\n2026-01-01T00:00:00Z,0\n"
			id := cohortIdentity(raw)
			out, err := cohortUpload(t, client).UploadCohortPinned(context.Background(), pinFixture(), id, strings.NewReader(raw))
			if tc.code == codes.OK {
				if err != nil || out.NWindows != 17 {
					t.Fatalf("result: %+v %v", out, err)
				}
			} else {
				var de *domain.Error
				if !errors.As(err, &de) || de.Kind != tc.kind || de.Message != tc.want || !de.Plain || status.Code(err) != tc.code || strings.Contains(err.Error(), "private") {
					t.Fatalf("unsafe error: %v", err)
				}
			}
			header := <-fixture.header
			if header == nil || !proto.Equal(header.Pin, pinToProto(pinFixture())) || header.Input.SourceKind != "cohort_snapshot" || header.Input.InputSnapshotId != id.InputSnapshotID || header.Input.CanonicalSha256 != id.CanonicalSHA256 || header.Input.FingerprintSha256 != id.FingerprintSHA256 || header.Input.CanonicalBytes != id.CanonicalBytes {
				t.Fatalf("cohort header lost: %v", header)
			}
			if got := <-fixture.content; got != raw {
				t.Fatalf("canonical content changed: %q", got)
			}
			select {
			case <-fixture.legacy:
				t.Fatal("legacy fallback")
			default:
			}
		})
	}
}

func TestCohortPinnedPreservesStrictInputValidation(t *testing.T) {
	for _, name := range []string{"pin", "nil_reader", "snapshot", "fingerprint", "declared_hash", "empty", "declared_cap"} {
		t.Run(name, func(t *testing.T) {
			client := &GRPCClient{} // Invalid metadata must fail before opening the RPC.
			pin := pinFixture()
			id := cohortIdentity("x")
			var reader io.Reader = strings.NewReader("x")
			switch name {
			case "pin":
				pin.RuntimeManifestSHA256 = ""
			case "nil_reader":
				reader = nil
			case "snapshot":
				id.InputSnapshotID = ""
			case "fingerprint":
				id.FingerprintSHA256 = "invalid"
			case "declared_hash":
				id.CanonicalSHA256 = "invalid"
			case "empty":
				id.CanonicalBytes = 0
			case "declared_cap":
				id.CanonicalBytes = (50 << 20) + 1
			}
			_, err := cohortUpload(t, client).UploadCohortPinned(context.Background(), pin, id, reader)
			var de *domain.Error
			if !errors.As(err, &de) || de.Kind != domain.Invalid {
				t.Fatalf("invalid input accepted: %v", err)
			}
		})
	}
	for _, name := range []string{"hash", "short", "long", "cancel"} {
		t.Run(name, func(t *testing.T) {
			fixture := &cohortPinnedServer{header: make(chan *pb.PinnedUploadHeader, 1), content: make(chan string, 1), legacy: make(chan struct{}, 1)}
			client := newCohortPinnedClient(t, fixture)
			raw := strings.Repeat("x", (1<<20)+1)
			id := cohortIdentity(raw)
			ctx := context.Background()
			switch name {
			case "hash":
				id.CanonicalSHA256 = domain.FileSHA256([]byte("wrong"))
			case "short":
				id.CanonicalBytes++
			case "long":
				id.CanonicalBytes--
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := cohortUpload(t, client).UploadCohortPinned(ctx, pinFixture(), id, strings.NewReader(raw))
			if name == "cancel" {
				if err == nil {
					t.Fatal("cancel accepted")
				}
				return
			}
			want := "canonical_hash_mismatch"
			if name == "long" {
				want = "canonical_length_mismatch"
			}
			var de *domain.Error
			if !errors.As(err, &de) || de.Kind != domain.Invalid || de.Message != want {
				t.Fatalf("want %s, got %v", want, err)
			}
		})
	}
}
