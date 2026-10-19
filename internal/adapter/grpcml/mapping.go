package grpcml

import (
	pb "github.com/effitech-pulse/pulse-contracts/gen/go/pulse/ml/v1"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// Преобразования используют generated-типы напрямую. Optional-поля остаются
// указателями, целые не проходят через float64/JSON, пустые списки дают [] в HTTP.
func snapshotFromProto(message *pb.SnapshotResponse) domain.Snapshot {
	wells := make([]domain.WellRisk, 0, len(message.GetWells()))
	for _, row := range message.GetWells() {
		var failureType *int64
		if row.TrueFailureType != nil {
			value := int64(*row.TrueFailureType)
			failureType = &value
		}
		wells = append(wells, domain.WellRisk{
			Decision:            decisionFromProto(row.Decision),
			WellID:              row.GetWellId(),
			UnitekID:            row.UnitekId,
			Oilfield:            row.Oilfield,
			Risk:                row.GetRisk(),
			Alert:               row.GetAlert(),
			Tier:                row.Tier,
			WindowsFired:        row.GetWindowsFired(),
			WindowsTotal:        row.GetWindowsTotal(),
			DataOk:              row.GetDataOk(),
			RuleFindings:        append([]string{}, row.GetRuleFindings()...),
			WindowEnd:           row.WindowEnd,
			TrueFailureType:     failureType,
			TrueFailureTypeName: row.TrueFailureTypeName,
			TrueFailureAt:       row.TrueFailureAt,
		})
	}
	return domain.Snapshot{
		Provenance:     provenanceFromProto(message.Provenance),
		DecisionRule:   ruleFromProto(message.DecisionRule),
		Wells:          wells,
		AlertThreshold: message.GetAlertThreshold(),
		HighThreshold:  message.GetHighThreshold(),
		DriftVerdict:   message.DriftVerdict,
		DriftPsiMax:    message.DriftPsiMax,
		DriftTop:       message.DriftTop,
	}
}

func contributionsFromProto(rows []*pb.Contribution) []domain.Contribution {
	out := make([]domain.Contribution, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.Contribution{
			Feature:     row.GetFeature(),
			Value:       row.GetValue(),
			SHAP:        row.GetShap(),
			Label:       row.GetLabel(),
			ValueSource: row.ValueSource,
		})
	}
	return out
}

func explanationFromProto(message *pb.ExplainResponse) domain.Explanation {
	return domain.Explanation{
		WellID:        message.GetWellId(),
		Contributions: contributionsFromProto(message.GetContributions()),
	}
}

func historyFromProto(message *pb.HistoryResponse) domain.RiskHistory {
	points := make([]domain.RiskPoint, 0, len(message.GetPoints()))
	for _, row := range message.GetPoints() {
		points = append(points, domain.RiskPoint{
			RawScore: row.RawScore, WatchFired: row.WatchFired, HighFired: row.HighFired,
			WindowEnd: row.GetWindowEnd(),
			Risk:      row.GetRisk(),
			Fired:     row.GetFired(),
		})
	}
	return domain.RiskHistory{
		Provenance: provenanceFromProto(message.Provenance), DecisionRule: ruleFromProto(message.DecisionRule), AccumulatedWatchCount: message.AccumulatedWatchCount, AccumulatedHighCount: message.AccumulatedHighCount,
		WellID:        message.GetWellId(),
		Threshold:     message.GetThreshold(),
		HighThreshold: message.GetHighThreshold(),
		WindowsTotal:  message.GetWindowsTotal(),
		WindowsFired:  message.GetWindowsFired(),
		Points:        points,
	}
}

func metricsFromProto(message *pb.MetricsResponse) domain.ModelMetrics {
	models := make([]map[string]any, 0, len(message.GetModels()))
	for _, row := range message.GetModels() {
		models = append(models, row.AsMap())
	}
	return domain.ModelMetrics{Models: models}
}

func uploadFromProto(message *pb.UploadResponse) domain.UploadResult {
	timeline := make([]domain.TimelinePoint, 0, len(message.GetTimeline()))
	for _, row := range message.GetTimeline() {
		timeline = append(timeline, domain.TimelinePoint{
			RawScore: row.RawScore, WatchFired: row.WatchFired, HighFired: row.HighFired,
			WindowEnd: row.GetWindowEnd(),
			Risk:      row.GetRisk(),
		})
	}
	return domain.UploadResult{
		Provenance: provenanceFromProto(message.Provenance), DecisionRule: ruleFromProto(message.DecisionRule),
		ModelName:           message.ModelName,
		ModelVersion:        message.ModelVersion,
		ModelSavedAt:        message.ModelSavedAt,
		Verdict:             message.GetVerdict(),
		NWindows:            message.GetNWindows(),
		ChannelsUsed:        int64(message.GetChannelsUsed()),
		FeaturesTotal:       int64(message.GetFeaturesTotal()),
		WindowsRunning:      message.GetWindowsRunning(),
		FeaturesUnavailable: int64(message.GetFeaturesUnavailable()),
		WindowsAboveWatch:   message.GetWindowsAboveWatch(),
		WindowsAboveAlert:   message.GetWindowsAboveAlert(),
		LatestRisk:          message.GetLatestRisk(),
		PeakRisk:            message.GetPeakRisk(),
		PeakWindowEnd:       message.GetPeakWindowEnd(),
		Timeline:            timeline,
		Contributions:       contributionsFromProto(message.GetContributions()),
		Filename:            message.GetFilename(),
		SpanDays:            int64(message.GetSpanDays()),
	}
}

func healthFromProto(message *pb.ModelHealthResponse) domain.ModelHealth {
	return domain.ModelHealth{
		Urgency:        message.GetUrgency(),
		Needed:         message.GetNeeded(),
		Reasons:        append([]string{}, message.GetReasons()...),
		DriftVerdict:   message.DriftVerdict,
		AlertsReviewed: message.GetAlertsReviewed(),
		AlertsFalse:    message.GetAlertsFalse(),
		FalseAlertRate: message.FalseAlertRate,
		ModelAgeDays:   message.ModelAgeDays,
	}
}

func thresholdFromProto(v *pb.ThresholdRule) *domain.ThresholdRule {
	if v == nil {
		return nil
	}
	return &domain.ThresholdRule{Scale: v.Scale, Comparison: v.Comparison, Threshold: v.Threshold, KWindows: v.KWindows, LookbackDays: v.LookbackDays}
}
func ruleFromProto(v *pb.DecisionRule) *domain.DecisionRule {
	if v == nil {
		return nil
	}
	return &domain.DecisionRule{ContextVersion: v.ContextVersion, PolicyID: v.PolicyId, RuleSHA256: v.RuleSha256, Watch: thresholdFromProto(v.Watch), High: thresholdFromProto(v.High), HighEnabled: v.HighEnabled}
}
func decisionFromProto(v *pb.DecisionEvidence) *domain.DecisionEvidence {
	if v == nil {
		return nil
	}
	return &domain.DecisionEvidence{RawScore: v.RawScore, WatchCount: v.WatchCount, HighCount: v.HighCount}
}
func manifestFromProto(rows []*pb.ManifestEntry) []domain.ManifestEntry {
	out := make([]domain.ManifestEntry, 0, len(rows))
	for _, v := range rows {
		out = append(out, domain.ManifestEntry{Name: v.Name, Version: v.Version, SHA256: v.Sha256})
	}
	return out
}
func provenanceFromProto(v *pb.Provenance) *domain.Provenance {
	if v == nil {
		return nil
	}
	return &domain.Provenance{EnvelopeVersion: v.EnvelopeVersion, SnapshotID: v.SnapshotId, ComputedAt: v.ComputedAt, ModelName: v.ModelName, ModelVersion: v.ModelVersion, ModelSavedAt: v.ModelSavedAt, ContextVersion: v.ContextVersion, InputSHA256: v.InputSha256, InputSnapshotID: v.InputSnapshotId, AsOf: v.AsOf, FeatureRecipeSHA256: v.FeatureRecipeSha256, FeatureSchemaSHA256: v.FeatureSchemaSha256, RuntimeManifestSHA256: v.RuntimeManifestSha256, SourceManifestSHA256: v.SourceManifestSha256, RuntimeManifest: manifestFromProto(v.RuntimeManifest), SourceManifest: manifestFromProto(v.SourceManifest), InputKind: v.InputKind, ContextCompleteness: v.ContextCompleteness, ProvenanceStatus: v.ProvenanceStatus}
}
