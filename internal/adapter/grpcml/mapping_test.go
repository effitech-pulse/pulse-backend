package grpcml

import (
	"testing"

	pb "github.com/effitech-pulse/pulse-contracts/gen/go/pulse/ml/v1"
)

func TestContributionValueSourceKeepsPresenceAndZero(t *testing.T) {
	source := "zero_fallback"
	rows := contributionsFromProto([]*pb.Contribution{{Feature: "a", Value: 0, Shap: 0, ValueSource: &source}, {Feature: "b"}})
	if rows[0].ValueSource == nil || *rows[0].ValueSource != source || rows[0].Value != 0 || rows[1].ValueSource != nil {
		t.Fatal(rows)
	}
}

func TestProvenanceAndDecisionMappingKeepsUnknownAndExplicitZero(t *testing.T) {
	zero := float64(0)
	no := false
	got := uploadFromProto(&pb.UploadResponse{Provenance: &pb.Provenance{EnvelopeVersion: "v1", InputKind: "synthetic", ProvenanceStatus: "partial"}, DecisionRule: &pb.DecisionRule{ContextVersion: "legacy_v0", PolicyId: "legacy_upload_v0", Watch: &pb.ThresholdRule{Scale: "raw", Comparison: ">", KWindows: 1}}, Timeline: []*pb.TimelinePoint{{RawScore: &zero, WatchFired: &no}}})
	if got.Provenance == nil || got.Provenance.ProvenanceStatus != "partial" || got.DecisionRule == nil || got.DecisionRule.Watch.Comparison != ">" || got.Timeline[0].RawScore == nil || *got.Timeline[0].WatchFired {
		t.Fatal(got)
	}
	old := uploadFromProto(&pb.UploadResponse{})
	if old.Provenance != nil || old.DecisionRule != nil {
		t.Fatal("retrofilled old context")
	}
}
