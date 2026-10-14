package app

import (
	"bytes"
	"context"
	pb "effitech/backend/internal/mlpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"io"
	"net"
	"testing"
	"time"
)

type testServicer struct {
	pb.UnimplementedMLServiceServer
}

func (s *testServicer) GetSnapshot(ctx context.Context, r *pb.SnapshotRequest) (*pb.SnapshotResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if len(md.Get("x-ml-api-key")) != 1 || md.Get("x-ml-api-key")[0] != "test-key" {
		return nil, status.Error(codes.Unauthenticated, "key")
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, status.Error(codes.Internal, "no deadline")
	}
	if r.MinRisk == nil || *r.MinRisk != 0 {
		return nil, status.Error(codes.InvalidArgument, "presence")
	}
	tier := "watch"
	zero := int64(0)
	return &pb.SnapshotResponse{Wells: []*pb.WellRisk{{WellId: 1 << 54, UnitekId: &zero, Tier: &tier, Risk: 0, DataOk: false}}}, nil
}
func (s *testServicer) Explain(ctx context.Context, r *pb.ExplainRequest) (*pb.ExplainResponse, error) {
	if r.WellId == 404 {
		return nil, status.Error(codes.NotFound, "missing")
	}
	if r.WellId == 422 {
		return nil, status.Error(codes.InvalidArgument, "invalid")
	}
	if r.WellId == 500 {
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &pb.ExplainResponse{WellId: r.WellId}, nil
}
func (s *testServicer) Upload(stream grpc.ClientStreamingServer[pb.UploadChunk, pb.UploadResponse]) error {
	total := 0
	filename := ""
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if filename == "" {
			filename = chunk.Filename
		}
		if len(chunk.Content) > 1024*1024 {
			return status.Error(codes.InvalidArgument, "chunk too large")
		}
		total += len(chunk.Content)
	}
	return stream.SendAndClose(&pb.UploadResponse{Filename: filename, NWindows: int64(total), SpanDays: 30})
}
func grpcTestClient(t *testing.T) *GRPCClient {
	t.Helper()
	listener := bufconn.Listen(16 * 1024 * 1024)
	server := grpc.NewServer()
	pb.RegisterMLServiceServer(server, &testServicer{})
	go server.Serve(listener)
	conn, err := grpc.NewClient("passthrough:///memory", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, target string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	client := &GRPCClient{conn: conn, stub: pb.NewMLServiceClient(conn), timeout: time.Second, key: "test-key"}
	t.Cleanup(func() { client.Close(); server.Stop(); listener.Close() })
	return client
}
func TestGRPCPreservesInt64NullZeroAndDeadline(t *testing.T) {
	client := grpcTestClient(t)
	min := 0.
	out, err := client.Snapshot(context.Background(), &min)
	if err != nil {
		t.Fatal(err)
	}
	row := out["wells"].([]any)[0].(map[string]any)
	if row["well_id"] != int64(1<<54) || row["unitek_id"] != int64(0) || row["risk"] != float64(0) || row["data_ok"] != false || row["true_failure_type"] != nil {
		t.Fatal(row)
	}
	for _, n := range []int64{404, 422} {
		_, err = client.Explain(context.Background(), n, 6)
		api, ok := err.(*APIError)
		if !ok || api.Status != int(n) {
			t.Fatal(n, err)
		}
	}
	client.timeout = 10 * time.Millisecond
	_, err = client.Explain(context.Background(), 500, 6)
	api, ok := err.(*APIError)
	if !ok || api.Status != 503 {
		t.Fatal(err)
	}
}
func TestGRPCUploadsFileLargerThanDefaultMessageLimit(t *testing.T) {
	client := grpcTestClient(t)
	client.timeout = 5 * time.Second
	data := bytes.Repeat([]byte("a"), 5*1024*1024)
	out, err := client.Upload(context.Background(), "sample.csv", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if out["n_windows"] != int64(len(data)) || out["span_days"] != int64(30) {
		t.Fatal(out)
	}
}
