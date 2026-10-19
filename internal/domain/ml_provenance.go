package domain

// ThresholdRule сохраняет шкалу и точное правило сравнения до округления риска.
type ThresholdRule struct {
	Scale        string   `json:"scale"`
	Comparison   string   `json:"comparison"`
	Threshold    float64  `json:"threshold"`
	KWindows     int64    `json:"k_windows"`
	LookbackDays *float64 `json:"lookback_days"`
}
type DecisionRule struct {
	ContextVersion string         `json:"context_version"`
	PolicyID       string         `json:"policy_id"`
	RuleSHA256     string         `json:"rule_sha256"`
	Watch          *ThresholdRule `json:"watch"`
	High           *ThresholdRule `json:"high"`
	HighEnabled    bool           `json:"high_enabled"`
}
type DecisionEvidence struct {
	RawScore   *float64 `json:"raw_score"`
	WatchCount *int64   `json:"watch_count"`
	HighCount  *int64   `json:"high_count"`
}
type ManifestEntry struct {
	Name    string  `json:"name"`
	Version *string `json:"version"`
	SHA256  *string `json:"sha256"`
}
type Provenance struct {
	EnvelopeVersion       string          `json:"envelope_version"`
	SnapshotID            string          `json:"snapshot_id"`
	ComputedAt            string          `json:"computed_at"`
	ModelName             *string         `json:"model_name"`
	ModelVersion          *string         `json:"model_version"`
	ModelSavedAt          *string         `json:"model_saved_at"`
	ContextVersion        *string         `json:"context_version"`
	InputSHA256           *string         `json:"input_sha256"`
	InputSnapshotID       *string         `json:"input_snapshot_id"`
	AsOf                  *string         `json:"as_of"`
	FeatureRecipeSHA256   *string         `json:"feature_recipe_sha256"`
	FeatureSchemaSHA256   *string         `json:"feature_schema_sha256"`
	RuntimeManifestSHA256 *string         `json:"runtime_manifest_sha256"`
	SourceManifestSHA256  *string         `json:"source_manifest_sha256"`
	RuntimeManifest       []ManifestEntry `json:"runtime_manifest"`
	SourceManifest        []ManifestEntry `json:"source_manifest"`
	InputKind             string          `json:"input_kind"`
	ContextCompleteness   string          `json:"context_completeness"`
	ProvenanceStatus      string          `json:"provenance_status"`
}
