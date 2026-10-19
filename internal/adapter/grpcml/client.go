package grpcml

import (
	"context"
	"io"
	"time"

	pb "github.com/effitech-pulse/pulse-contracts/gen/go/pulse/ml/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type GRPCClient struct {
	conn    *grpc.ClientConn
	stub    pb.MLServiceClient
	timeout time.Duration
	key     string
}

func New(c config.Config) (*GRPCClient, error) {
	conn, err := grpc.NewClient(
		c.MLTarget,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(16*1024*1024),
			grpc.MaxCallSendMsgSize(16*1024*1024),
		),
	)
	if err != nil {
		return nil, err
	}
	return &GRPCClient{conn: conn, stub: pb.NewMLServiceClient(conn), timeout: c.MLTimeout, key: c.MLKey}, nil
}

func (c *GRPCClient) Close() error {
	return c.conn.Close()
}

func (c *GRPCClient) context(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx = metadata.AppendToOutgoingContext(ctx, "x-ml-api-key", c.key)
	return context.WithTimeout(ctx, c.timeout)
}

func rpcError(err error) error {
	if err == nil {
		return nil
	}
	kind := domain.Unavailable
	message := "ML-сервис недоступен"
	switch status.Code(err) {
	case codes.NotFound:
		kind = domain.Missing
	case codes.InvalidArgument:
		kind = domain.Invalid
	case codes.AlreadyExists:
		kind = domain.Duplicate
	}
	if kind != domain.Unavailable {
		message = status.Convert(err).Message()
	}
	return &domain.Error{Kind: kind, Message: message, Plain: true, Cause: err}
}

func (c *GRPCClient) Snapshot(ctx context.Context, min *float64) (domain.Snapshot, error) {
	ctx, cancel := c.context(ctx)
	defer cancel()
	result, err := c.stub.GetSnapshot(ctx, &pb.SnapshotRequest{MinRisk: min})
	if err != nil {
		return domain.Snapshot{}, rpcError(err)
	}
	return snapshotFromProto(result), nil
}

func (c *GRPCClient) Score(ctx context.Context) (domain.Snapshot, error) {
	ctx, cancel := c.context(ctx)
	defer cancel()
	result, err := c.stub.Score(ctx, &emptypb.Empty{})
	if err != nil {
		return domain.Snapshot{}, rpcError(err)
	}
	return snapshotFromProto(result), nil
}

func (c *GRPCClient) Explain(ctx context.Context, id int64, top int32) (domain.Explanation, error) {
	ctx, cancel := c.context(ctx)
	defer cancel()
	result, err := c.stub.Explain(ctx, &pb.ExplainRequest{WellId: id, TopN: &top})
	if err != nil {
		return domain.Explanation{}, rpcError(err)
	}
	return explanationFromProto(result), nil
}

func (c *GRPCClient) History(ctx context.Context, id int64, count int32) (domain.RiskHistory, error) {
	ctx, cancel := c.context(ctx)
	defer cancel()
	result, err := c.stub.History(ctx, &pb.HistoryRequest{WellId: id, Limit: &count})
	if err != nil {
		return domain.RiskHistory{}, rpcError(err)
	}
	return historyFromProto(result), nil
}

func (c *GRPCClient) Metrics(ctx context.Context) (domain.ModelMetrics, error) {
	ctx, cancel := c.context(ctx)
	defer cancel()
	result, err := c.stub.Metrics(ctx, &emptypb.Empty{})
	if err != nil {
		return domain.ModelMetrics{}, rpcError(err)
	}
	return metricsFromProto(result), nil
}

func (c *GRPCClient) ModelHealth(ctx context.Context, state domain.ModelState) (domain.ModelHealth, error) {
	ctx, cancel := c.context(ctx)
	defer cancel()
	req := &pb.ModelHealthRequest{DriftVerdict: state.DriftVerdict, DriftPsiMax: state.DriftPsiMax, AlertsReviewed: state.AlertsReviewed, AlertsFalse: state.AlertsFalse}
	result, err := c.stub.ModelHealth(ctx, req)
	if err != nil {
		return domain.ModelHealth{}, rpcError(err)
	}
	return healthFromProto(result), nil
}

func (c *GRPCClient) Upload(ctx context.Context, name string, input io.Reader) (domain.UploadResult, error) {
	ctx, cancel := c.context(ctx)
	defer cancel()
	stream, err := c.stub.Upload(ctx)
	if err != nil {
		return domain.UploadResult{}, rpcError(err)
	}
	send := func(chunk *pb.UploadChunk) error {
		if err := stream.Send(chunk); err != nil {
			_, final := stream.CloseAndRecv()
			if final != nil {
				return rpcError(final)
			}
			return rpcError(err)
		}
		return nil
	}
	if err = send(&pb.UploadChunk{Filename: name}); err != nil {
		return domain.UploadResult{}, err
	}
	buf := make([]byte, 1024*1024)
	total := 0
	for {
		n, readErr := input.Read(buf)
		if n > 0 {
			total += n
			if total > 50*1024*1024 {
				return domain.UploadResult{}, &domain.Error{Kind: domain.Invalid, Message: "Файл больше 50 МБ", Plain: true}
			}
			if err = send(&pb.UploadChunk{Content: append([]byte(nil), buf[:n]...)}); err != nil {
				return domain.UploadResult{}, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return domain.UploadResult{}, readErr
		}
	}
	result, err := stream.CloseAndRecv()
	if err != nil {
		return domain.UploadResult{}, rpcError(err)
	}
	return uploadFromProto(result), nil
}
