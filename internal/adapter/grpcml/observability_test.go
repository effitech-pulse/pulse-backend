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
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/effitech-pulse/pulse-backend/internal/observability"
)

type heldUploadServer struct {
	pb.UnimplementedMLServiceServer
	ready   chan metadata.MD
	release chan struct{}
}

func (s *heldUploadServer) Upload(stream grpc.ClientStreamingServer[pb.UploadChunk, pb.UploadResponse]) error {
	for {
		_, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	md, _ := metadata.FromIncomingContext(stream.Context())
	s.ready <- md
	select {
	case <-s.release:
	case <-stream.Context().Done():
		return stream.Context().Err()
	}
	return stream.SendAndClose(&pb.UploadResponse{Filename: "FILE_SENTINEL"})
}
func TestUploadSpanLivesThroughCloseAndRecv(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "cancel"}[cancelled], func(t *testing.T) {
			exp := tracetest.NewInMemoryExporter()
			p := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
			defer p.Shutdown(context.Background())
			old := otel.GetTracerProvider()
			otel.SetTracerProvider(p)
			defer otel.SetTracerProvider(old)
			srv := &heldUploadServer{ready: make(chan metadata.MD, 1), release: make(chan struct{})}
			listener := bufconn.Listen(1024 * 1024)
			server := grpc.NewServer()
			pb.RegisterMLServiceServer(server, srv)
			go server.Serve(listener)
			defer server.Stop()
			conn, err := grpc.NewClient("passthrough:///memory", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			client := &GRPCClient{conn: conn, stub: pb.NewMLServiceClient(conn), timeout: 5 * time.Second, key: "test-key"}
			ctx, parent := observability.Start(observability.WithRequestID(context.Background(), "request_fixture_1234"), "test.parent", trace.SpanKindServer)
			defer parent.End()
			ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("baggage", "TOKEN_SENTINEL", "custom-safe", "preserved"))
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := client.Upload(ctx, "FILE_SENTINEL", strings.NewReader("DATA_SENTINEL")); done <- err }()
			var md metadata.MD
			select {
			case md = <-srv.ready:
			case <-time.After(3 * time.Second):
				t.Fatal("upload did not reach CloseAndRecv")
			}
			if len(exp.GetSpans()) != 0 {
				t.Fatal("stream span ended before response")
			}
			if len(md.Get("traceparent")) != 1 || len(md.Get("x-request-id")) != 1 || md.Get("x-request-id")[0] != "request_fixture_1234" || len(md.Get("baggage")) != 0 || md.Get("x-ml-api-key")[0] != "test-key" || md.Get("custom-safe")[0] != "preserved" {
				t.Fatalf("missing/unsafe propagation: keys=%v", "redacted")
			}
			if cancelled {
				cancel()
			} else {
				close(srv.release)
			}
			select {
			case err = <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("terminal stream did not return")
			}
			if (err != nil) != cancelled {
				t.Fatal("result changed", err)
			}
			spans := exp.GetSpans()
			if len(spans) != 1 || spans[0].Parent.SpanID() != parent.SpanContext().SpanID() || spans[0].SpanKind != trace.SpanKindClient {
				t.Fatalf("bad completed spans: %+v", spans)
			}
		})
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("READ_SECRET") }

type oversizedReader struct{ remaining int }

func (r *oversizedReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.remaining)
	r.remaining -= n
	return n, nil
}
func TestUploadTerminalReaderErrorsEndOneSpan(t *testing.T) {
	for _, reader := range []io.Reader{failingReader{}, &oversizedReader{remaining: 51 * 1024 * 1024}} {
		exp := tracetest.NewInMemoryExporter()
		p := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
		old := otel.GetTracerProvider()
		otel.SetTracerProvider(p)
		client := grpcTestClient(t)
		client.timeout = 10 * time.Second
		_, err := client.Upload(context.Background(), "FILE_SECRET", reader)
		spans := exp.GetSpans()
		if err == nil || len(spans) != 1 {
			t.Fatalf("terminal error=%v spans=%d", err, len(spans))
		}
		for _, a := range spans[0].Attributes {
			if strings.Contains(a.Value.AsString(), "SECRET") {
				t.Fatal("secret span attribute")
			}
		}
		otel.SetTracerProvider(old)
		_ = p.Shutdown(context.Background())
	}
}

type rejectingUploadServer struct {
	pb.UnimplementedMLServiceServer
}

func (rejectingUploadServer) Upload(grpc.ClientStreamingServer[pb.UploadChunk, pb.UploadResponse]) error {
	return status.Error(codes.InvalidArgument, "REMOTE_SECRET")
}
func TestRejectedUploadEndsOneErrorSpan(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	p := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(p)
	defer otel.SetTracerProvider(old)
	defer p.Shutdown(context.Background())
	client := grpcTestClient(t, rejectingUploadServer{})
	_, err := client.Upload(context.Background(), "FILE_SECRET", strings.NewReader(strings.Repeat("x", 5*1024*1024)))
	spans := exp.GetSpans()
	if err == nil || len(spans) != 1 || spans[0].Status.Code != otelcodes.Error {
		t.Fatal("remote rejection left span open")
	}
	for _, a := range spans[0].Attributes {
		if strings.Contains(a.Value.AsString(), "SECRET") {
			t.Fatal("remote detail leaked")
		}
	}
}
