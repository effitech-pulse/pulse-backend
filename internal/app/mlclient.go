package app

import (
	"context"
	pb "effitech/backend/internal/mlpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/structpb"
	"io"
	"time"
)

// MLAPI задаёт границу вычислений: реализация не загружает модели.
type MLAPI interface {
	Snapshot(context.Context, *float64) (map[string]any, error)
	Score(context.Context) (map[string]any, error)
	Explain(context.Context, int64, int32) (map[string]any, error)
	History(context.Context, int64, int32) (map[string]any, error)
	Metrics(context.Context) (map[string]any, error)
	ModelHealth(context.Context, map[string]any) (map[string]any, error)
	Upload(context.Context, string, io.Reader) (map[string]any, error)
}
type GRPCClient struct {
	conn    *grpc.ClientConn
	stub    pb.MLServiceClient
	timeout time.Duration
	key     string
}

func NewGRPCClient(c Config) (*GRPCClient, error) {
	conn, err := grpc.NewClient(c.MLTarget, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16*1024*1024), grpc.MaxCallSendMsgSize(16*1024*1024)))
	if err != nil {
		return nil, err
	}
	return &GRPCClient{conn: conn, stub: pb.NewMLServiceClient(conn), timeout: c.MLTimeout, key: c.MLKey}, nil
}
func (c *GRPCClient) Close() error { return c.conn.Close() }
func (c *GRPCClient) context(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx = metadata.AppendToOutgoingContext(ctx, "x-ml-api-key", c.key)
	return context.WithTimeout(ctx, c.timeout)
}
func rpcError(err error) error {
	if err == nil {
		return nil
	}
	httpStatus := 503
	message := "ML-сервис недоступен"
	switch status.Code(err) {
	case codes.NotFound:
		httpStatus = 404
	case codes.InvalidArgument:
		httpStatus = 422
	case codes.AlreadyExists:
		httpStatus = 409
	}
	if httpStatus != 503 {
		message = status.Convert(err).Message()
	}
	return &APIError{httpStatus, "ml_error", message, true}
}
func protoMap(message proto.Message) map[string]any {
	if s, ok := message.(*structpb.Struct); ok {
		return s.AsMap()
	}
	m := message.ProtoReflect()
	out := map[string]any{}
	fields := m.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		v := m.Get(fd)
		key := string(fd.Name())
		if fd.IsList() {
			items := []any{}
			list := v.List()
			for j := 0; j < list.Len(); j++ {
				items = append(items, protoValue(fd, list.Get(j)))
			}
			out[key] = items
		} else if fd.HasPresence() && !m.Has(fd) {
			out[key] = nil
		} else {
			out[key] = protoValue(fd, v)
		}
	}
	return out
}
func protoValue(fd protoreflect.FieldDescriptor, v protoreflect.Value) any {
	switch fd.Kind() {
	case protoreflect.MessageKind:
		return protoMap(v.Message().Interface())
	case protoreflect.BoolKind:
		return v.Bool()
	case protoreflect.StringKind:
		return v.String()
	case protoreflect.DoubleKind, protoreflect.FloatKind:
		return v.Float()
	case protoreflect.Int32Kind, protoreflect.Int64Kind, protoreflect.Sint32Kind, protoreflect.Sint64Kind:
		return v.Int()
	default:
		return v.Interface()
	}
}
func (c *GRPCClient) Snapshot(ctx context.Context, min *float64) (map[string]any, error) {
	ctx, cancel := c.context(ctx)
	defer cancel()
	result, err := c.stub.GetSnapshot(ctx, &pb.SnapshotRequest{MinRisk: min})
	if err != nil {
		return nil, rpcError(err)
	}
	return protoMap(result), nil
}
func (c *GRPCClient) Score(ctx context.Context) (map[string]any, error) {
	ctx, cancel := c.context(ctx)
	defer cancel()
	result, err := c.stub.Score(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, rpcError(err)
	}
	return protoMap(result), nil
}
func (c *GRPCClient) Explain(ctx context.Context, id int64, top int32) (map[string]any, error) {
	ctx, cancel := c.context(ctx)
	defer cancel()
	result, err := c.stub.Explain(ctx, &pb.ExplainRequest{WellId: id, TopN: &top})
	if err != nil {
		return nil, rpcError(err)
	}
	return protoMap(result), nil
}
func (c *GRPCClient) History(ctx context.Context, id int64, count int32) (map[string]any, error) {
	ctx, cancel := c.context(ctx)
	defer cancel()
	result, err := c.stub.History(ctx, &pb.HistoryRequest{WellId: id, Limit: &count})
	if err != nil {
		return nil, rpcError(err)
	}
	return protoMap(result), nil
}
func (c *GRPCClient) Metrics(ctx context.Context) (map[string]any, error) {
	ctx, cancel := c.context(ctx)
	defer cancel()
	result, err := c.stub.Metrics(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, rpcError(err)
	}
	return protoMap(result), nil
}
func (c *GRPCClient) ModelHealth(ctx context.Context, state map[string]any) (map[string]any, error) {
	ctx, cancel := c.context(ctx)
	defer cancel()
	request := &pb.ModelHealthRequest{}
	if v, ok := state["drift_verdict"].(string); ok {
		request.DriftVerdict = &v
	}
	if v, ok := state["drift_psi_max"].(float64); ok {
		request.DriftPsiMax = &v
	}
	request.AlertsReviewed, _ = intValue(state["alerts_reviewed"])
	request.AlertsFalse, _ = intValue(state["alerts_false"])
	result, err := c.stub.ModelHealth(ctx, request)
	if err != nil {
		return nil, rpcError(err)
	}
	return protoMap(result), nil
}
func (c *GRPCClient) Upload(ctx context.Context, name string, input io.Reader) (map[string]any, error) {
	ctx, cancel := c.context(ctx)
	defer cancel()
	stream, err := c.stub.Upload(ctx)
	if err != nil {
		return nil, rpcError(err)
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
		return nil, err
	}
	buf := make([]byte, 1024*1024)
	total := 0
	for {
		n, readErr := input.Read(buf)
		if n > 0 {
			total += n
			if total > 50*1024*1024 {
				return nil, &APIError{422, "validation_error", "Файл больше 50 МБ", true}
			}
			if err = send(&pb.UploadChunk{Content: buf[:n]}); err != nil {
				return nil, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	result, err := stream.CloseAndRecv()
	if err != nil {
		return nil, rpcError(err)
	}
	return protoMap(result), nil
}
