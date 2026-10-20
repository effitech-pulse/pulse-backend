package domain

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"time"
)

// FileParseSpec фиксирует явные клиентские параметры и разрешённую сервером схему.
type FileParseSpec struct {
	Version         string    `json:"version"`
	Format          string    `json:"format"`
	Delimiter       string    `json:"delimiter"`
	Sheet           string    `json:"sheet"`
	HeaderRow       int       `json:"header_row"`
	TimeColumn      string    `json:"time_column"`
	TimestampLayout string    `json:"timestamp_layout"`
	Timezone        string    `json:"timezone"`
	WellColumn      *string   `json:"well_column"`
	WellIdentity    string    `json:"well_identity"`
	Mappings        []Mapping `json:"mappings"`
	SchemaSHA256    string    `json:"schema_sha256"`
}
type ExecutionPin struct {
	Version               string        `json:"version"`
	ModelName             string        `json:"model_name"`
	BundleSHA256          string        `json:"bundle_sha256"`
	ContextVersion        string        `json:"context_version"`
	DecisionRule          *DecisionRule `json:"decision_rule"`
	FeatureRecipeSHA256   string        `json:"feature_recipe_sha256"`
	FeatureSchemaSHA256   string        `json:"feature_schema_sha256"`
	SourceManifestSHA256  string        `json:"source_manifest_sha256"`
	RuntimeManifestSHA256 string        `json:"runtime_manifest_sha256"`
}
type PinnedInputIdentity struct {
	InputSnapshotID   string `json:"input_snapshot_id"`
	CanonicalSHA256   string `json:"canonical_sha256"`
	FingerprintSHA256 string `json:"fingerprint_sha256"`
	CanonicalBytes    int64  `json:"canonical_bytes"`
	SourceKind        string `json:"source_kind"`
}
type TelemetryChannel struct {
	Canonical string   `json:"canonical"`
	Aliases   []string `json:"aliases"`
	Unit      string   `json:"unit"`
	Scale     float64  `json:"scale"`
	Offset    float64  `json:"offset"`
	Missing   string   `json:"missing"`
	IsSet     bool     `json:"is_set"`
}
type TelemetrySchemaSnapshot struct {
	Bytes          []byte             `json:"-"`
	SHA256         string             `json:"sha256"`
	Channels       []TelemetryChannel `json:"channels"`
	DecoderSources []FileParserPart   `json:"decoder_sources"`
}
type FileInputRows struct {
	Columns  []string
	Rows     [][]string
	Date1904 bool
}
type FileInputLimits struct {
	RawBytes       int64         `json:"raw_bytes"`
	CanonicalBytes int64         `json:"canonical_bytes"`
	Rows           int           `json:"rows"`
	Columns        int           `json:"columns"`
	Mappings       int           `json:"mappings"`
	FieldBytes     int           `json:"field_bytes"`
	SpecBytes      int           `json:"spec_bytes"`
	HeaderRow      int           `json:"header_row"`
	ExpandedBytes  int64         `json:"expanded_bytes"`
	EntryBytes     int64         `json:"entry_bytes"`
	Entries        int           `json:"entries"`
	ExpansionRatio int64         `json:"expansion_ratio"`
	TempXMLBytes   int64         `json:"temp_xml_bytes"`
	ResultBytes    int           `json:"result_bytes"`
	Timeout        time.Duration `json:"timeout"`
	RetainedBytes  int64         `json:"retained_bytes"`
	AccountBytes   int64         `json:"account_bytes"`
	Inputs         int64         `json:"inputs"`
}

func DefaultFileInputLimits() FileInputLimits {
	return FileInputLimits{RawBytes: 20 << 20, CanonicalBytes: 50 << 20, Rows: 100000, Columns: 128, Mappings: 64, FieldBytes: 64 << 10, SpecBytes: 64 << 10, HeaderRow: 100, ExpandedBytes: 100 << 20, EntryBytes: 50 << 20, Entries: 512, ExpansionRatio: 100, TempXMLBytes: 8 << 20, ResultBytes: 2 << 20, Timeout: 30 * time.Second, RetainedBytes: 256 << 20, AccountBytes: 128 << 20, Inputs: 16}
}

// Valid допускает уменьшенные тестовые лимиты, но не расширяет synthetic профиль.
func (l FileInputLimits) Valid() bool {
	d := DefaultFileInputLimits()
	a, _ := json.Marshal(l)
	b, _ := json.Marshal(d)
	var x, y map[string]int64
	_ = json.Unmarshal(a, &x)
	_ = json.Unmarshal(b, &y)
	for k, v := range x {
		if v <= 0 || v > y[k] {
			return false
		}
	}
	return true
}

// FileParseRecipe сохраняет параметры reader/date-system вместе с hash.
type FileParseRecipe struct {
	Version              string                    `json:"version"`
	Limits               FileInputLimits           `json:"limits"`
	Date1904             bool                      `json:"date1904"`
	Implementation       *FileParserImplementation `json:"implementation,omitempty"`
	SchemaSHA256         string                    `json:"schema_sha256,omitempty"`
	ResolvedConfigSHA256 string                    `json:"resolved_config_sha256,omitempty"`
}
type FileActorSnapshot struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	Revision int64  `json:"revision"`
}
type FileInputSummary struct {
	ID                      string            `json:"id"`
	PulseWellID             int64             `json:"pulse_well_id"`
	ActorAccountID          int64             `json:"actor_account_id"`
	Actor                   FileActorSnapshot `json:"actor"`
	RequestKey              string            `json:"request_key"`
	SourceKind              string            `json:"source_kind"`
	CreatedAt               time.Time         `json:"created_at"`
	CapturedAt              time.Time         `json:"captured_at"`
	RawSHA256               string            `json:"raw_sha256"`
	CanonicalSHA256         string            `json:"canonical_sha256"`
	RequestCommitmentSHA256 string            `json:"request_commitment_sha256"`
	FingerprintSHA256       string            `json:"fingerprint_sha256"`
	ParseSpec               FileParseSpec     `json:"parse_spec"`
	ParseRecipeSHA256       string            `json:"parse_recipe_sha256"`
	ParseRecipe             FileParseRecipe   `json:"parse_recipe"`
	ExecutionPin            ExecutionPin      `json:"execution_pin"`
	IdentityJSON            []byte            `json:"identity_json"`
	RawBytes                int64             `json:"raw_bytes"`
	CanonicalBytes          int64             `json:"canonical_bytes"`
	Rows                    int               `json:"rows"`
	Columns                 int               `json:"columns"`
	IgnoredColumns          []string          `json:"ignored_columns"`
}
type PreparedFileInput struct {
	FileInputSummary
	Raw       []byte          `json:"-"`
	Canonical []byte          `json:"-"`
	Limits    FileInputLimits `json:"-"`
}
type StoredFileResult struct {
	InputID           string       `json:"input_id"`
	FingerprintSHA256 string       `json:"fingerprint_sha256"`
	ExecutionPin      ExecutionPin `json:"execution_pin"`
	ComputedAt        time.Time    `json:"computed_at"`
	Result            UploadResult `json:"result"`
}

func FileSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

var fileDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func ValidFileSHA256(s string) bool { return fileDigest.MatchString(s) }
func (p ExecutionPin) Validate() error {
	if p.Version != "upload_pin_v1" || p.ModelName == "" || len(p.ModelName) > 200 || p.ContextVersion == "" || p.DecisionRule == nil {
		return NewError(Invalid, "invalid_execution_pin")
	}
	for _, v := range []string{p.BundleSHA256, p.FeatureRecipeSHA256, p.FeatureSchemaSHA256, p.SourceManifestSHA256, p.RuntimeManifestSHA256, p.DecisionRule.RuleSHA256} {
		if !ValidFileSHA256(v) {
			return NewError(Invalid, "invalid_execution_pin")
		}
	}
	r := p.DecisionRule
	if r.ContextVersion != p.ContextVersion || r.PolicyID == "" || r.Watch == nil || (r.HighEnabled && r.High == nil) {
		return NewError(Invalid, "invalid_execution_pin")
	}
	for _, t := range []*ThresholdRule{r.Watch, r.High} {
		if t == nil {
			continue
		}
		if math.IsNaN(t.Threshold) || math.IsInf(t.Threshold, 0) || t.KWindows < 0 || (t.LookbackDays != nil && (math.IsNaN(*t.LookbackDays) || math.IsInf(*t.LookbackDays, 0) || *t.LookbackDays < 0)) || t.Scale == "" || (t.Comparison != ">" && t.Comparison != ">=") {
			return NewError(Invalid, "invalid_execution_pin")
		}
	}
	return nil
}

// ValidateFileInput защищает транзакционный seam от ошибочного caller и повреждения bytes.
func ValidateFileInput(v PreparedFileInput) error {
	if !fileInputID.MatchString(v.ID) || v.PulseWellID <= 0 || v.ActorAccountID <= 0 || !v.Limits.Valid() || v.SourceKind != "uploaded_file" || v.ParseSpec.Version != "file_input_v1" || !fileRequestKey.MatchString(v.RequestKey) || v.CapturedAt.IsZero() || v.CreatedAt.IsZero() {
		return NewError(Invalid, "invalid_file_input")
	}
	if err := v.ExecutionPin.Validate(); err != nil {
		return err
	}
	for _, h := range []string{v.RawSHA256, v.CanonicalSHA256, v.RequestCommitmentSHA256, v.FingerprintSHA256, v.ParseRecipeSHA256, v.ParseSpec.SchemaSHA256} {
		if !ValidFileSHA256(h) {
			return NewError(Invalid, "invalid_file_input")
		}
	}
	if int64(len(v.Raw)) != v.RawBytes || int64(len(v.Canonical)) != v.CanonicalBytes || v.RawBytes <= 0 || v.RawBytes > v.Limits.RawBytes || v.CanonicalBytes <= 0 || v.CanonicalBytes > v.Limits.CanonicalBytes || v.Rows < 1 || v.Rows > v.Limits.Rows || v.Columns < 1 || v.Columns > v.Limits.Columns || len(v.IdentityJSON) > 64<<10 {
		return NewError(Invalid, "invalid_file_input")
	}
	ignored, err := json.Marshal(v.IgnoredColumns)
	if err != nil || len(ignored) > 64<<10 || v.IgnoredColumns == nil {
		return NewError(Invalid, "ignored_columns_limit")
	}
	recipe, err := json.Marshal(v.ParseRecipe)
	if err != nil || len(recipe) > 8192 || v.ParseRecipe.Validate(v.ParseSpec.SchemaSHA256) != nil || v.ParseRecipe.Limits != v.Limits || FileSHA256(recipe) != v.ParseRecipeSHA256 {
		return NewError(Invalid, "invalid_parser_recipe")
	}
	identity, err := FileInputIdentityJSON(v.FileInputSummary)
	if err != nil || !bytes.Equal(identity, v.IdentityJSON) {
		return NewError(Invalid, "invalid_file_identity")
	}
	if FileSHA256(v.Raw) != v.RawSHA256 || FileSHA256(v.Canonical) != v.CanonicalSHA256 || FileSHA256(v.IdentityJSON) != v.FingerprintSHA256 {
		return NewError(Unavailable, "input_corrupt")
	}
	return nil
}

var fileInputID = regexp.MustCompile(`^[0-9a-f]{32}$`)

var fileRequestKey = regexp.MustCompile(`^[A-Za-z0-9_-]{16,80}$`)

// FileInputIdentityJSON сериализует стабильную identity один раз, без JSONB reserialization.
func FileInputIdentityJSON(v FileInputSummary) ([]byte, error) {
	return json.Marshal(struct {
		Version        string          `json:"version"`
		Well           int64           `json:"well"`
		Actor          int64           `json:"actor"`
		Source         string          `json:"source"`
		Commitment     string          `json:"commitment"`
		Raw            string          `json:"raw"`
		Canonical      string          `json:"canonical"`
		Spec           FileParseSpec   `json:"spec"`
		Recipe         string          `json:"recipe"`
		ParseRecipe    FileParseRecipe `json:"parse_recipe"`
		Pin            ExecutionPin    `json:"pin"`
		RawBytes       int64           `json:"raw_bytes"`
		CanonicalBytes int64           `json:"canonical_bytes"`
		Rows           int             `json:"rows"`
		Columns        int             `json:"columns"`
	}{"file_identity_v1", v.PulseWellID, v.ActorAccountID, v.SourceKind, v.RequestCommitmentSHA256, v.RawSHA256, v.CanonicalSHA256, v.ParseSpec, v.ParseRecipeSHA256, v.ParseRecipe, v.ExecutionPin, v.RawBytes, v.CanonicalBytes, v.Rows, v.Columns})
}

// ValidateFileResult проверяет actual identity результата до immutable commit.
func ValidateFileResult(input FileInputSummary, v StoredFileResult) error {
	pinJSON, _ := json.Marshal(input.ExecutionPin)
	actualPinJSON, _ := json.Marshal(v.ExecutionPin)
	if v.InputID != input.ID || v.FingerprintSHA256 != input.FingerprintSHA256 || !bytes.Equal(pinJSON, actualPinJSON) || v.ComputedAt.IsZero() {
		return NewError(Duplicate, "result_identity_conflict")
	}
	result := v.Result
	pin := input.ExecutionPin
	p := result.Provenance
	eq := func(v *string, want string) bool { return v != nil && *v == want }
	expectedRule, _ := json.Marshal(pin.DecisionRule)
	actualRule, _ := json.Marshal(result.DecisionRule)
	if !eq(result.ModelName, pin.ModelName) || !eq(result.ModelVersion, pin.BundleSHA256) || !bytes.Equal(expectedRule, actualRule) || p == nil {
		return NewError(Unavailable, "result_identity_mismatch")
	}
	if !eq(p.InputSnapshotID, input.ID) || !eq(p.InputSHA256, input.CanonicalSHA256) || !eq(p.ContextVersion, pin.ContextVersion) || !eq(p.FeatureRecipeSHA256, pin.FeatureRecipeSHA256) || !eq(p.FeatureSchemaSHA256, pin.FeatureSchemaSHA256) || !eq(p.SourceManifestSHA256, pin.SourceManifestSHA256) || !eq(p.RuntimeManifestSHA256, pin.RuntimeManifestSHA256) {
		return NewError(Unavailable, "result_identity_mismatch")
	}
	return nil
}

// Clone изолирует optional rule pointers от mutation вызывающего кода.
func (p ExecutionPin) Clone() ExecutionPin {
	if p.DecisionRule == nil {
		return p
	}
	rule := *p.DecisionRule
	cloneThreshold := func(value *ThresholdRule) *ThresholdRule {
		if value == nil {
			return nil
		}
		copy := *value
		if value.LookbackDays != nil {
			days := *value.LookbackDays
			copy.LookbackDays = &days
		}
		return &copy
	}
	rule.Watch = cloneThreshold(rule.Watch)
	rule.High = cloneThreshold(rule.High)
	p.DecisionRule = &rule
	return p
}

// FileParserPart идентифицирует реальные embedded bytes без пути на host.
type FileParserPart struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}
type FileParserModule struct {
	Path            string `json:"path"`
	Version         string `json:"version"`
	Sum             string `json:"sum"`
	ReplacementPath string `json:"replacement_path,omitempty"`
}
type FileParserImplementation struct {
	Version   string             `json:"version"`
	Sources   []FileParserPart   `json:"sources"`
	GoVersion string             `json:"go_version"`
	GOOS      string             `json:"goos"`
	GOARCH    string             `json:"goarch"`
	Modules   []FileParserModule `json:"modules"`
}

//go:embed file_inputs.go
var fileParserDomainSource []byte

//go:embed telemetry.go
var fileParserTelemetrySource []byte
var fileParserDomainParts = []FileParserPart{{Name: "domain/file_inputs.go", SHA256: FileSHA256(fileParserDomainSource)}, {Name: "domain/telemetry.go", SHA256: FileSHA256(fileParserTelemetrySource)}}

func FileParserDomainSources() []FileParserPart {
	return append([]FileParserPart(nil), fileParserDomainParts...)
}

// FileParserModulePaths задаёт reviewed external closure reader. При её изменении нужен review.
func FileParserModulePaths() []string {
	return []string{"github.com/richardlehane/mscfb", "github.com/richardlehane/msoleps", "github.com/tiendc/go-deepcopy", "github.com/xuri/efp", "github.com/xuri/excelize/v2", "github.com/xuri/nfp", "golang.org/x/crypto", "golang.org/x/net", "golang.org/x/text"}
}

var parserPartNames = []string{"adapter/fileinput/reader.go", "adapter/parameterfile/telemetry_schema.go", "domain/file_inputs.go", "domain/telemetry.go", "service/file_inputs.go", "service/integrations.go"}
var parserModuleVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)
var parserGoVersion = regexp.MustCompile(`^go[0-9]+\.[0-9]+(?:\.[0-9]+)?$`)
var parserPlatform = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)
var parserModulePath = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~/-]{0,199}$`)

func (v FileParserImplementation) Validate() error {
	fail := func() error { return NewError(Invalid, "invalid_parser_implementation") }
	if v.Version != "file_parser_impl_v1" || !parserGoVersion.MatchString(v.GoVersion) || !parserPlatform.MatchString(v.GOOS) || !parserPlatform.MatchString(v.GOARCH) || len(v.Sources) != len(parserPartNames) {
		return fail()
	}
	for i, part := range v.Sources {
		if part.Name != parserPartNames[i] || !ValidFileSHA256(part.SHA256) {
			return fail()
		}
	}
	paths := FileParserModulePaths()
	if len(v.Modules) != len(paths) {
		return fail()
	}
	for i, module := range v.Modules {
		if module.Path != paths[i] || len(module.Version) > 128 || !parserModuleVersion.MatchString(module.Version) || !strings.HasPrefix(module.Sum, "h1:") {
			return fail()
		}
		decoded, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(module.Sum, "h1:"))
		if e != nil || len(decoded) != 32 || "h1:"+base64.StdEncoding.EncodeToString(decoded) != module.Sum {
			return fail()
		}
		if module.ReplacementPath != "" && (!parserModulePath.MatchString(module.ReplacementPath) || !strings.Contains(module.ReplacementPath, ".") || strings.Contains(module.ReplacementPath, "..")) {
			return fail()
		}
	}
	return nil
}
func (v FileParseRecipe) Validate(schemaSHA string) error {
	if v.Version != "file_parser_v2" || !v.Limits.Valid() || v.Implementation == nil || v.SchemaSHA256 != schemaSHA || !ValidFileSHA256(v.SchemaSHA256) || !ValidFileSHA256(v.ResolvedConfigSHA256) {
		return NewError(Invalid, "invalid_parser_recipe")
	}
	return v.Implementation.Validate()
}
