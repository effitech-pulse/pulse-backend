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
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type pinnedServer struct {
	pb.UnimplementedMLServiceServer
	calls  int
	legacy int
	fail   codes.Code
	large  bool
	t      *testing.T
}

func (s *pinnedServer) Upload(grpc.ClientStreamingServer[pb.UploadChunk, pb.UploadResponse]) error {
	s.legacy++
	return status.Error(codes.Internal, "must not fallback")
}
func (s *pinnedServer) UploadPinned(stream grpc.ClientStreamingServer[pb.PinnedUploadChunk, pb.UploadResponse]) error {
	s.calls++
	if s.fail != codes.OK {
		return status.Error(s.fail, "private /unsafe/path")
	}
	md, _ := metadata.FromIncomingContext(stream.Context())
	if len(md.Get("x-ml-api-key")) != 1 || md.Get("x-ml-api-key")[0] != "synthetic" {
		s.t.Error("auth metadata lost")
	}
	h, e := stream.Recv()
	if e != nil {
		return e
	}
	if h.GetHeader() == nil || h.GetHeader().Pin.DecisionRule.HighEnabled || h.GetHeader().Pin.DecisionRule.High != nil {
		return status.Error(codes.InvalidArgument, "bad header")
	}
	var data []byte
	for {
		chunk, e := stream.Recv()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if chunk.GetHeader() != nil || len(chunk.GetContent()) > 1<<20 {
			return status.Error(codes.InvalidArgument, "bad chunk")
		}
		data = append(data, chunk.GetContent()...)
	}
	if domain.FileSHA256(data) != h.GetHeader().Input.CanonicalSha256 {
		return status.Error(codes.InvalidArgument, "input_hash_mismatch")
	}
	zero := 0.
	no := false
	if s.large {
		return stream.SendAndClose(&pb.UploadResponse{Filename: strings.Repeat("x", 2<<20)})
	}
	return stream.SendAndClose(&pb.UploadResponse{NWindows: 9007199254740993, Timeline: []*pb.TimelinePoint{{RawScore: &zero, WatchFired: &no}}, DecisionRule: h.GetHeader().Pin.DecisionRule})
}
func pinFixture() domain.ExecutionPin {
	h := domain.FileSHA256([]byte("fixture"))
	return domain.ExecutionPin{Version: "upload_pin_v1", ModelName: "fixture", BundleSHA256: h, ContextVersion: "legacy_v1", DecisionRule: &domain.DecisionRule{ContextVersion: "legacy_v1", PolicyID: "fixture", RuleSHA256: h, Watch: &domain.ThresholdRule{Scale: "raw", Comparison: ">", Threshold: 0}}, FeatureRecipeSHA256: h, FeatureSchemaSHA256: h, SourceManifestSHA256: h, RuntimeManifestSHA256: h}
}
func TestPinnedRealTransportAndNoFallback(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	fixture := &pinnedServer{t: t}
	pb.RegisterMLServiceServer(srv, fixture)
	go srv.Serve(listener)
	defer srv.Stop()
	conn, e := grpc.NewClient("passthrough:///fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	client := &GRPCClient{conn: conn, stub: pb.NewMLServiceClient(conn), timeout: time.Second, key: "synthetic"}
	content := "timestamp,F_out\n2026-01-01T00:00:00Z,0\n"
	identity := domain.PinnedInputIdentity{InputSnapshotID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CanonicalSHA256: domain.FileSHA256([]byte(content)), FingerprintSHA256: domain.FileSHA256([]byte("id")), CanonicalBytes: int64(len(content)), SourceKind: "uploaded_file"}
	out, e := client.UploadPinned(context.Background(), pinFixture(), identity, strings.NewReader(content))
	if e != nil {
		t.Fatal(e)
	}
	if out.NWindows != 9007199254740993 || out.Timeline[0].RawScore == nil || *out.Timeline[0].RawScore != 0 || out.Timeline[0].WatchFired == nil || *out.Timeline[0].WatchFired {
		t.Fatal("typed semantics lost")
	}
	fixture.large = true
	if _, e = client.UploadPinned(context.Background(), pinFixture(), identity, strings.NewReader(content)); e == nil {
		t.Fatal("oversized protobuf result accepted")
	}
	fixture.large = false
	fixture.fail = codes.Unimplemented
	_, e = client.UploadPinned(context.Background(), pinFixture(), identity, strings.NewReader(content))
	if e == nil || !strings.Contains(e.Error(), "pinned_protocol_unavailable") || strings.Contains(e.Error(), "unsafe") || fixture.legacy != 0 {
		t.Fatalf("unsafe fallback/error %v", e)
	}
}

func TestPinnedSafeErrors(t *testing.T) {
	for _, tc := range []struct {
		code   codes.Code
		kind   domain.Kind
		reason string
	}{{codes.Unimplemented, domain.Unavailable, "pinned_protocol_unavailable"}, {codes.ResourceExhausted, domain.Limited, "pinned_resource_limit"}, {codes.InvalidArgument, domain.Invalid, "invalid_pinned_input"}, {codes.FailedPrecondition, domain.Unavailable, "pinned_execution_unavailable"}, {codes.Canceled, domain.Unavailable, "pinned_execution_unavailable"}} {
		e := pinnedRPCError(status.Error(tc.code, "secret /private/path"))
		var de *domain.Error
		if !errors.As(e, &de) || de.Kind != tc.kind || de.Message != tc.reason || strings.Contains(e.Error(), "secret") || status.Code(e) != tc.code {
			t.Fatalf("unsafe mapping: %#v %v", tc, e)
		}
	}
	if e := pinnedRPCError(status.Error(codes.FailedPrecondition, "runtime_mismatch")); !strings.Contains(e.Error(), "runtime_mismatch") {
		t.Fatal(e)
	}
}
func TestPinnedContentBoundsAndCancellation(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	fixture := &pinnedServer{t: t}
	pb.RegisterMLServiceServer(server, fixture)
	go server.Serve(listener)
	defer server.Stop()
	conn, e := grpc.NewClient("passthrough:///fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	client := &GRPCClient{conn: conn, stub: pb.NewMLServiceClient(conn), timeout: time.Second, key: "synthetic"}
	for _, name := range []string{"hash", "length", "declared_cap", "cancel"} {
		t.Run(name, func(t *testing.T) {
			raw := strings.Repeat("x", (1<<20)+1)
			id := domain.PinnedInputIdentity{InputSnapshotID: strings.Repeat("c", 32), CanonicalSHA256: domain.FileSHA256([]byte(raw)), FingerprintSHA256: domain.FileSHA256([]byte("fingerprint")), CanonicalBytes: int64(len(raw)), SourceKind: "uploaded_file"}
			ctx := context.Background()
			switch name {
			case "hash":
				id.CanonicalSHA256 = domain.FileSHA256([]byte("wrong"))
			case "length":
				id.CanonicalBytes--
			case "declared_cap":
				id.CanonicalBytes = (50 << 20) + 1
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, e := client.UploadPinned(ctx, pinFixture(), id, strings.NewReader(raw)); e == nil {
				t.Fatal("invalid content/cancel accepted")
			}
		})
	}
	if fixture.legacy != 0 {
		t.Fatal("legacy fallback")
	}
}
