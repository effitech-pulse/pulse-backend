package httpapi

import (
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
	"strconv"
	"time"
)

func eventTime(t time.Time) string { return t.UTC().Format(service.EventTimeLayout) }
func eventTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := eventTime(*t)
	return &s
}
func eventInt(n int64) string { return strconv.FormatInt(n, 10) }
func eventIntPtr(n *int64) *string {
	if n == nil {
		return nil
	}
	s := eventInt(*n)
	return &s
}

type eventManifestEntryDTO struct {
	Name    string  `json:"name"`
	Version *string `json:"version"`
	SHA256  *string `json:"sha256"`
}

func toEventManifestEntryDTO(v domain.ManifestEntry) eventManifestEntryDTO {
	out := eventManifestEntryDTO{}
	out.Name = v.Name
	out.Version = v.Version
	out.SHA256 = v.SHA256
	return out
}

type eventProvenanceDTO struct {
	EnvelopeVersion       string                  `json:"envelope_version"`
	SnapshotID            string                  `json:"snapshot_id"`
	ComputedAt            string                  `json:"computed_at"`
	ModelName             *string                 `json:"model_name"`
	ModelVersion          *string                 `json:"model_version"`
	ModelSavedAt          *string                 `json:"model_saved_at"`
	ContextVersion        *string                 `json:"context_version"`
	InputSHA256           *string                 `json:"input_sha256"`
	InputSnapshotID       *string                 `json:"input_snapshot_id"`
	AsOf                  *string                 `json:"as_of"`
	FeatureRecipeSHA256   *string                 `json:"feature_recipe_sha256"`
	FeatureSchemaSHA256   *string                 `json:"feature_schema_sha256"`
	RuntimeManifestSHA256 *string                 `json:"runtime_manifest_sha256"`
	SourceManifestSHA256  *string                 `json:"source_manifest_sha256"`
	RuntimeManifest       []eventManifestEntryDTO `json:"runtime_manifest"`
	SourceManifest        []eventManifestEntryDTO `json:"source_manifest"`
	InputKind             string                  `json:"input_kind"`
	ContextCompleteness   string                  `json:"context_completeness"`
	ProvenanceStatus      string                  `json:"provenance_status"`
}

func toEventProvenanceDTO(v domain.Provenance) eventProvenanceDTO {
	out := eventProvenanceDTO{}
	out.EnvelopeVersion = v.EnvelopeVersion
	out.SnapshotID = v.SnapshotID
	out.ComputedAt = v.ComputedAt
	out.ModelName = v.ModelName
	out.ModelVersion = v.ModelVersion
	out.ModelSavedAt = v.ModelSavedAt
	out.ContextVersion = v.ContextVersion
	out.InputSHA256 = v.InputSHA256
	out.InputSnapshotID = v.InputSnapshotID
	out.AsOf = v.AsOf
	out.FeatureRecipeSHA256 = v.FeatureRecipeSHA256
	out.FeatureSchemaSHA256 = v.FeatureSchemaSHA256
	out.RuntimeManifestSHA256 = v.RuntimeManifestSHA256
	out.SourceManifestSHA256 = v.SourceManifestSHA256
	out.RuntimeManifest = make([]eventManifestEntryDTO, 0, len(v.RuntimeManifest))
	for _, x := range v.RuntimeManifest {
		out.RuntimeManifest = append(out.RuntimeManifest, toEventManifestEntryDTO(x))
	}
	out.SourceManifest = make([]eventManifestEntryDTO, 0, len(v.SourceManifest))
	for _, x := range v.SourceManifest {
		out.SourceManifest = append(out.SourceManifest, toEventManifestEntryDTO(x))
	}
	out.InputKind = v.InputKind
	out.ContextCompleteness = v.ContextCompleteness
	out.ProvenanceStatus = v.ProvenanceStatus
	return out
}

type eventThresholdRuleDTO struct {
	Scale        string   `json:"scale"`
	Comparison   string   `json:"comparison"`
	Threshold    float64  `json:"threshold"`
	KWindows     string   `json:"k_windows"`
	LookbackDays *float64 `json:"lookback_days"`
}

func toEventThresholdRuleDTO(v domain.ThresholdRule) eventThresholdRuleDTO {
	out := eventThresholdRuleDTO{}
	out.Scale = v.Scale
	out.Comparison = v.Comparison
	out.Threshold = v.Threshold
	out.KWindows = eventInt(v.KWindows)
	out.LookbackDays = v.LookbackDays
	return out
}

type eventDecisionRuleDTO struct {
	ContextVersion string                 `json:"context_version"`
	PolicyID       string                 `json:"policy_id"`
	RuleSHA256     string                 `json:"rule_sha256"`
	Watch          *eventThresholdRuleDTO `json:"watch"`
	High           *eventThresholdRuleDTO `json:"high"`
	HighEnabled    bool                   `json:"high_enabled"`
}

func toEventDecisionRuleDTO(v domain.DecisionRule) eventDecisionRuleDTO {
	out := eventDecisionRuleDTO{}
	out.ContextVersion = v.ContextVersion
	out.PolicyID = v.PolicyID
	out.RuleSHA256 = v.RuleSHA256
	if v.Watch != nil {
		x := toEventThresholdRuleDTO(*v.Watch)
		out.Watch = &x
	}
	if v.High != nil {
		x := toEventThresholdRuleDTO(*v.High)
		out.High = &x
	}
	out.HighEnabled = v.HighEnabled
	return out
}

type eventTimelinePointDTO struct {
	RawScore   *float64 `json:"raw_score"`
	WatchFired *bool    `json:"watch_fired"`
	HighFired  *bool    `json:"high_fired"`
	WindowEnd  string   `json:"window_end"`
	Risk       float64  `json:"risk"`
}

func toEventTimelinePointDTO(v domain.TimelinePoint) eventTimelinePointDTO {
	out := eventTimelinePointDTO{}
	out.RawScore = v.RawScore
	out.WatchFired = v.WatchFired
	out.HighFired = v.HighFired
	out.WindowEnd = v.WindowEnd
	out.Risk = v.Risk
	return out
}

type eventContributionDTO struct {
	ValueSource *string `json:"value_source"`
	Feature     string  `json:"feature"`
	Value       float64 `json:"value"`
	SHAP        float64 `json:"shap"`
	Label       string  `json:"label"`
}

func toEventContributionDTO(v domain.Contribution) eventContributionDTO {
	out := eventContributionDTO{}
	out.ValueSource = v.ValueSource
	out.Feature = v.Feature
	out.Value = v.Value
	out.SHAP = v.SHAP
	out.Label = v.Label
	return out
}

type eventUploadResultDTO struct {
	Provenance          *eventProvenanceDTO     `json:"provenance"`
	DecisionRule        *eventDecisionRuleDTO   `json:"decision_rule"`
	ModelName           *string                 `json:"model_name"`
	ModelVersion        *string                 `json:"model_version"`
	ModelSavedAt        *string                 `json:"model_saved_at"`
	Verdict             *string                 `json:"verdict"`
	NWindows            string                  `json:"n_windows"`
	ChannelsUsed        string                  `json:"channels_used"`
	FeaturesTotal       string                  `json:"features_total"`
	WindowsRunning      string                  `json:"windows_running"`
	FeaturesUnavailable string                  `json:"features_unavailable"`
	WindowsAboveWatch   string                  `json:"windows_above_watch"`
	WindowsAboveAlert   string                  `json:"windows_above_alert"`
	LatestRisk          float64                 `json:"latest_risk"`
	PeakRisk            float64                 `json:"peak_risk"`
	PeakWindowEnd       string                  `json:"peak_window_end"`
	Timeline            []eventTimelinePointDTO `json:"timeline"`
	Contributions       []eventContributionDTO  `json:"contributions"`
	SpanDays            string                  `json:"span_days"`
}

func toEventUploadResultDTO(v domain.UploadResult) eventUploadResultDTO {
	out := eventUploadResultDTO{}
	if v.Provenance != nil {
		x := toEventProvenanceDTO(*v.Provenance)
		out.Provenance = &x
	}
	if v.DecisionRule != nil {
		x := toEventDecisionRuleDTO(*v.DecisionRule)
		out.DecisionRule = &x
	}
	out.ModelName = v.ModelName
	out.ModelVersion = v.ModelVersion
	out.ModelSavedAt = v.ModelSavedAt
	if v.Verdict != "" {
		x := v.Verdict
		out.Verdict = &x
	}
	out.NWindows = eventInt(v.NWindows)
	out.ChannelsUsed = eventInt(v.ChannelsUsed)
	out.FeaturesTotal = eventInt(v.FeaturesTotal)
	out.WindowsRunning = eventInt(v.WindowsRunning)
	out.FeaturesUnavailable = eventInt(v.FeaturesUnavailable)
	out.WindowsAboveWatch = eventInt(v.WindowsAboveWatch)
	out.WindowsAboveAlert = eventInt(v.WindowsAboveAlert)
	out.LatestRisk = v.LatestRisk
	out.PeakRisk = v.PeakRisk
	out.PeakWindowEnd = v.PeakWindowEnd
	out.Timeline = make([]eventTimelinePointDTO, 0, len(v.Timeline))
	for _, x := range v.Timeline {
		out.Timeline = append(out.Timeline, toEventTimelinePointDTO(x))
	}
	out.Contributions = make([]eventContributionDTO, 0, len(v.Contributions))
	for _, x := range v.Contributions {
		out.Contributions = append(out.Contributions, toEventContributionDTO(x))
	}
	out.SpanDays = eventInt(v.SpanDays)
	return out
}

type eventWellDTO struct {
	Revision    string  `json:"revision"`
	ArchivedAt  *string `json:"archived_at"`
	Responsible *string `json:"responsible"`
	FundType    string  `json:"fund_type"`
	UnitekID    *string `json:"unitek_id"`
	ArmitsID    *string `json:"armits_id"`
	WellNumber  *string `json:"well_number"`
	NGDU        string  `json:"ngdu"`
	Department  string  `json:"department"`
	Active      bool    `json:"active"`
	ID          string  `json:"id"`
}

func toEventWellDTO(v domain.Well) eventWellDTO {
	out := eventWellDTO{}
	out.Revision = eventInt(v.Revision)
	out.ArchivedAt = eventTimePtr(v.ArchivedAt)
	out.Responsible = v.Responsible
	out.FundType = v.FundType
	out.UnitekID = eventIntPtr(v.UnitekID)
	out.ArmitsID = eventIntPtr(v.ArmitsID)
	out.WellNumber = v.WellNumber
	out.NGDU = eventInt(v.NGDU)
	out.Department = eventInt(v.Department)
	out.Active = v.Active
	out.ID = eventInt(v.ID)
	return out
}

type eventReviewStateDTO struct {
	Revision   string  `json:"revision"`
	Status     string  `json:"status"`
	Owner      string  `json:"owner"`
	Conclusion string  `json:"conclusion"`
	UpdatedAt  *string `json:"updated_at"`
}

func toEventReviewStateDTO(v domain.ReviewState) eventReviewStateDTO {
	out := eventReviewStateDTO{}
	out.Revision = eventInt(v.Revision)
	out.Status = v.Status
	out.Owner = v.Owner
	out.Conclusion = v.Conclusion
	out.UpdatedAt = eventTimePtr(v.UpdatedAt)
	return out
}

type eventWellHeaderDTO struct {
	ID         string  `json:"id"`
	WellNumber *string `json:"well_number"`
	Active     bool    `json:"active"`
	ArchivedAt *string `json:"archived_at"`
}
type eventTargetDTO struct {
	Kind     string  `json:"kind"`
	ID       string  `json:"id"`
	Revision *string `json:"revision"`
}
type eventActorDTO struct {
	Kind          string  `json:"kind"`
	ID            *string `json:"id"`
	Name          *string `json:"name"`
	Authenticated *bool   `json:"authenticated"`
}
type eventOutcomeDTO struct {
	Verdict      *string `json:"verdict"`
	ReviewStatus *string `json:"review_status"`
	Conclusion   *string `json:"conclusion"`
}
type eventSummaryDTO struct {
	EventID    string             `json:"event_id"`
	EventType  string             `json:"event_type"`
	OccurredAt string             `json:"occurred_at"`
	Well       eventWellHeaderDTO `json:"well"`
	Target     eventTargetDTO     `json:"target"`
	Actor      eventActorDTO      `json:"actor"`
	Outcome    eventOutcomeDTO    `json:"outcome"`
}
type eventPageDTO struct {
	Items      []eventSummaryDTO `json:"items"`
	HasMore    bool              `json:"has_more"`
	NextCursor *string           `json:"next_cursor"`
}

func toEventSummaryDTO(v domain.EventSummary) eventSummaryDTO {
	target := eventTargetDTO{ID: v.Target.Target}
	switch v.Target.Source {
	case "mc", "review.check":
		target.Kind = "check"
	case "review.alert":
		target.Kind = "alert"
	case "well":
		target.Kind = "well"
	}
	if v.Target.Source != "mc" {
		rev := eventInt(v.Target.Revision)
		target.Revision = &rev
	}
	return eventSummaryDTO{v.EventID, v.EventType, eventTime(v.OccurredAt), eventWellHeaderDTO{eventInt(v.Well.ID), v.Well.WellNumber, v.Well.Active, eventTimePtr(v.Well.ArchivedAt)}, target, eventActorDTO{v.Actor.Kind, v.Actor.ID, v.Actor.Name, v.Actor.Authenticated}, eventOutcomeDTO{v.Outcome.Verdict, v.Outcome.ReviewStatus, v.Outcome.Conclusion}}
}

type eventCheckHeaderDTO struct {
	ID        string `json:"id"`
	WellID    string `json:"well_id"`
	CreatedAt string `json:"created_at"`
	Start     string `json:"start"`
	End       string `json:"end"`
}
type eventCheckDetailDTO struct {
	Kind   string               `json:"kind"`
	Check  eventCheckHeaderDTO  `json:"check"`
	Result eventUploadResultDTO `json:"result"`
}
type eventReviewDetailDTO struct {
	Kind       string              `json:"kind"`
	ReviewKind string              `json:"review_kind"`
	Revision   string              `json:"revision"`
	CreatedAt  string              `json:"created_at"`
	Before     eventReviewStateDTO `json:"before"`
	After      eventReviewStateDTO `json:"after"`
	Comment    string              `json:"comment"`
}
type eventWellDetailDTO struct {
	Kind       string       `json:"kind"`
	Revision   string       `json:"revision"`
	CreatedAt  string       `json:"created_at"`
	ChangeKind string       `json:"change_kind"`
	Before     eventWellDTO `json:"before"`
	After      eventWellDTO `json:"after"`
	Reason     string       `json:"reason"`
}

// eventDetailPayload закрывает union для F3: source JSON не реализует этот интерфейс.
type eventDetailPayload interface{ eventPayload() }

func (eventCheckDetailDTO) eventPayload()  {}
func (eventReviewDetailDTO) eventPayload() {}
func (eventWellDetailDTO) eventPayload()   {}

type eventDetailDTO struct {
	Event  eventSummaryDTO    `json:"event"`
	Detail eventDetailPayload `json:"detail"`
}

func toEventDetailDTO(v domain.EventDetail) (eventDetailDTO, error) {
	out := eventDetailDTO{Event: toEventSummaryDTO(v.Event)}
	r := v.Record
	switch v.Event.Target.Source {
	case "mc":
		if r.Check != nil {
			c := r.Check
			result := toEventUploadResultDTO(c.Result)
			result.Verdict = r.Verdict
			out.Detail = eventCheckDetailDTO{"check", eventCheckHeaderDTO{c.ID, eventInt(c.WellID), eventTime(c.CreatedAt), eventTime(c.Start), eventTime(c.End)}, result}
		}
	case "review.check", "review.alert":
		if r.Review != nil {
			x := r.Review
			kind := "check"
			if v.Event.Target.Source == "review.alert" {
				kind = "alert"
			}
			out.Detail = eventReviewDetailDTO{"review", kind, eventInt(x.Revision), eventTime(x.CreatedAt), toEventReviewStateDTO(x.Before), toEventReviewStateDTO(x.After), x.Comment}
		}
	case "well":
		if r.WellRevision != nil {
			x := r.WellRevision
			out.Detail = eventWellDetailDTO{"well", eventInt(x.Revision), eventTime(x.CreatedAt), x.Kind, toEventWellDTO(x.Before), toEventWellDTO(x.After), x.Reason}
		}
	}
	if out.Detail == nil {
		return out, domain.NewError(domain.Unavailable, "История временно недоступна.")
	}
	return out, nil
}
