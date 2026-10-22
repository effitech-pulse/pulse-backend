package grpcml

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"

	pb "github.com/effitech-pulse/pulse-contracts/gen/go/pulse/ml/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func thresholdToProto(v *domain.ThresholdRule) *pb.ThresholdRule {
	if v == nil {
		return nil
	}
	return &pb.ThresholdRule{Scale: v.Scale, Comparison: v.Comparison, Threshold: v.Threshold, KWindows: v.KWindows, LookbackDays: v.LookbackDays}
}
func ruleToProto(v *domain.DecisionRule) *pb.DecisionRule {
	if v == nil {
		return nil
	}
	return &pb.DecisionRule{ContextVersion: v.ContextVersion, PolicyId: v.PolicyID, RuleSha256: v.RuleSHA256, Watch: thresholdToProto(v.Watch), High: thresholdToProto(v.High), HighEnabled: v.HighEnabled}
}
func pinToProto(v domain.ExecutionPin) *pb.ExecutionPin {
	return &pb.ExecutionPin{Version: v.Version, ModelName: v.ModelName, BundleSha256: v.BundleSHA256, ContextVersion: v.ContextVersion, DecisionRule: ruleToProto(v.DecisionRule), FeatureRecipeSha256: v.FeatureRecipeSHA256, FeatureSchemaSha256: v.FeatureSchemaSHA256, SourceManifestSha256: v.SourceManifestSHA256, RuntimeManifestSha256: v.RuntimeManifestSHA256}
}
func pinnedRPCError(err error) error {
	if err == nil {
		return nil
	}
	kind := domain.Unavailable
	reason := "pinned_execution_unavailable"
	switch status.Code(err) {
	case codes.Unimplemented:
		reason = "pinned_protocol_unavailable"
	case codes.ResourceExhausted:
		kind = domain.Limited
		reason = "pinned_resource_limit"
	case codes.InvalidArgument:
		kind = domain.Invalid
		reason = "invalid_pinned_input"
	case codes.FailedPrecondition:
		switch status.Convert(err).Message() {
		case "bundle_unavailable", "bundle_corrupt", "pin_mismatch", "policy_mismatch", "recipe_mismatch", "runtime_mismatch", "schema_mismatch", "unsupported_context_version", "unsupported_feature_recipe":
			reason = status.Convert(err).Message()
		}
	}
	// Сохраняем код статуса для диагностики без private RPC message.
	return &domain.Error{Kind: kind, Message: reason, Plain: true, Cause: &safeRPCStatus{code: status.Code(err)}}
}

type safeRPCStatus struct{ code codes.Code }

func (e *safeRPCStatus) Error() string              { return e.code.String() }
func (e *safeRPCStatus) GRPCStatus() *status.Status { return status.New(e.code, "") }

// UploadPinned запрещает fallback к legacy Upload, включая UNIMPLEMENTED.
func (c *GRPCClient) UploadPinned(ctx context.Context, pin domain.ExecutionPin, input domain.PinnedInputIdentity, canonical io.Reader) (domain.UploadResult, error) {
	return c.uploadPinned(ctx, pin, input, canonical, "uploaded_file", pinnedRPCError)
}

// UploadFileJobPinned сохраняет точный safe execution_busy только для Q1 worker.
// Это внутренний Go seam; protobuf/SDK и Q0 UploadPinned не меняются.
func (c *GRPCClient) UploadFileJobPinned(ctx context.Context, pin domain.ExecutionPin, input domain.PinnedInputIdentity, canonical io.Reader) (domain.UploadResult, error) {
	return c.uploadPinned(ctx, pin, input, canonical, "uploaded_file", fileJobPinnedRPCError)
}

// UploadCohortPinned принимает только сохранённый cohort snapshot без legacy fallback.
func (c *GRPCClient) UploadCohortPinned(ctx context.Context, pin domain.ExecutionPin, input domain.PinnedInputIdentity, canonical io.Reader) (domain.UploadResult, error) {
	return c.uploadPinned(ctx, pin, input, canonical, "cohort_snapshot", fileJobPinnedRPCError)
}

func (c *GRPCClient) uploadPinned(ctx context.Context, pin domain.ExecutionPin, input domain.PinnedInputIdentity, canonical io.Reader, expectedSourceKind string, mapRPCError func(error) error) (out domain.UploadResult, err error) {
	if err = pin.Validate(); err != nil {
		return out, err
	}
	if canonical == nil || input.InputSnapshotID == "" || !domain.ValidFileSHA256(input.CanonicalSHA256) || !domain.ValidFileSHA256(input.FingerprintSHA256) || input.SourceKind != expectedSourceKind || input.CanonicalBytes < 1 || input.CanonicalBytes > 50<<20 {
		return out, domain.NewError(domain.Invalid, "invalid_pinned_input")
	}
	ctx, finish := c.begin(ctx, "UploadPinned")
	defer func() { finish(err) }()
	stream, err := c.stub.UploadPinned(ctx, grpc.MaxCallRecvMsgSize(2<<20))
	if err != nil {
		return out, mapRPCError(err)
	}
	send := func(chunk *pb.PinnedUploadChunk) error {
		if e := stream.Send(chunk); e != nil {
			_, final := stream.CloseAndRecv()
			if final != nil {
				return mapRPCError(final)
			}
			return mapRPCError(e)
		}
		return nil
	}
	header := &pb.PinnedUploadHeader{Pin: pinToProto(pin), Input: &pb.PinnedInputIdentity{InputSnapshotId: input.InputSnapshotID, CanonicalSha256: input.CanonicalSHA256, FingerprintSha256: input.FingerprintSHA256, CanonicalBytes: input.CanonicalBytes, SourceKind: input.SourceKind}}
	if err = send(&pb.PinnedUploadChunk{Payload: &pb.PinnedUploadChunk_Header{Header: header}}); err != nil {
		return out, err
	}
	hash := sha256.New()
	buf := make([]byte, 1<<20)
	var total int64
	for {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		n, e := canonical.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > input.CanonicalBytes || total > 50<<20 {
				return out, domain.NewError(domain.Invalid, "canonical_length_mismatch")
			}
			_, _ = hash.Write(buf[:n])
			if err = send(&pb.PinnedUploadChunk{Payload: &pb.PinnedUploadChunk_Content{Content: append([]byte(nil), buf[:n]...)}}); err != nil {
				return out, err
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return out, e
		}
		if n == 0 {
			return out, io.ErrNoProgress
		}
	}
	if total != input.CanonicalBytes || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != input.CanonicalSHA256 {
		return out, domain.NewError(domain.Invalid, "canonical_hash_mismatch")
	}
	result, err := stream.CloseAndRecv()
	if err != nil {
		return out, mapRPCError(err)
	}
	return uploadFromProto(result), nil
}
