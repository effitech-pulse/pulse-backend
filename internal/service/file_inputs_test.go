package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/fileinput"
	"github.com/effitech-pulse/pulse-backend/internal/adapter/parameterfile"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type fileAccounts struct{ a domain.Account }

func (f *fileAccounts) Account(context.Context, int64) (domain.Account, error) { return f.a, nil }

type fileScope struct{ well domain.Well }

func (f *fileScope) Members(context.Context) (domain.Cohort, []domain.Well, error) {
	return domain.Cohort{}, []domain.Well{f.well}, nil
}
func (f *fileScope) RequireWell(context.Context, int64) (domain.Well, error)   { return f.well, nil }
func (f *fileScope) RequireUnitek(context.Context, int64) (domain.Well, error) { return f.well, nil }

type fileRows struct{ calls int }

func (f *fileRows) Read(context.Context, io.Reader, domain.FileParseSpec, domain.FileInputLimits) (domain.FileInputRows, error) {
	f.calls++
	return domain.FileInputRows{Columns: []string{"time", "f"}, Rows: [][]string{{"2026-01-01T00:00:00Z", "0"}}}, nil
}

type fileScorer struct{}

func (fileScorer) UploadPinned(context.Context, domain.ExecutionPin, domain.PinnedInputIdentity, io.Reader) (domain.UploadResult, error) {
	return domain.UploadResult{}, nil
}

type fileStore struct{ v domain.PreparedFileInput }

func (f *fileStore) FileInputRequest(context.Context, int64, string) (domain.FileInputSummary, error) {
	if f.v.ID == "" {
		return domain.FileInputSummary{}, domain.NewError(domain.Missing, "missing")
	}
	return f.v.FileInputSummary, nil
}
func (f *fileStore) FileInputSummary(context.Context, string) (domain.FileInputSummary, error) {
	return f.v.FileInputSummary, nil
}
func (f *fileStore) FileInputSummaries(context.Context, int64, string) ([]domain.FileInputSummary, error) {
	return []domain.FileInputSummary{f.v.FileInputSummary}, nil
}
func (f *fileStore) AcceptFileInput(_ context.Context, v domain.PreparedFileInput) (domain.FileInputSummary, error) {
	f.v = v
	return v.FileInputSummary, nil
}
func (f *fileStore) StoredFileInput(context.Context, string) (domain.PreparedFileInput, error) {
	return f.v, nil
}
func (f *fileStore) StoredFileResult(context.Context, string) (domain.StoredFileResult, error) {
	return domain.StoredFileResult{}, domain.NewError(domain.Missing, "missing")
}
func (f *fileStore) CommitStoredResult(_ context.Context, v domain.StoredFileResult) (domain.StoredFileResult, error) {
	return v, nil
}
func testFilePin() domain.ExecutionPin {
	h := domain.FileSHA256([]byte("test"))
	return domain.ExecutionPin{Version: "upload_pin_v1", ModelName: "synthetic", BundleSHA256: h, ContextVersion: "legacy_v1", DecisionRule: &domain.DecisionRule{ContextVersion: "legacy_v1", PolicyID: "test", RuleSHA256: h, Watch: &domain.ThresholdRule{Scale: "probability", Comparison: ">", Threshold: .5}}, FeatureRecipeSHA256: h, FeatureSchemaSHA256: h, SourceManifestSHA256: h, RuntimeManifestSHA256: h}
}
func TestFileInputAuthoritativeActorAndReplay(t *testing.T) {
	a := &fileAccounts{domain.Account{ID: 1, Active: true, Role: "technologist", Scope: domain.AccessScope{Funds: []string{"operational"}}}}
	store := &fileStore{}
	rows := &fileRows{}
	rawSchema := []byte("test")
	s, e := newFileChecksFixture(t, store, a, &fileScope{domain.Well{ID: 2, Active: true, FundType: "operational"}}, rows, fileScorer{}, domain.TelemetrySchemaSnapshot{Bytes: rawSchema, SHA256: domain.FileSHA256(rawSchema), Channels: []domain.TelemetryChannel{{Canonical: "F_out", Scale: 1}}}, testFilePin(), domain.DefaultFileInputLimits())
	if e != nil {
		t.Fatal(e)
	}
	spec := domain.FileParseSpec{Version: "file_input_v1", Format: "csv", Delimiter: ",", HeaderRow: 1, TimeColumn: "time", TimestampLayout: "rfc3339", Timezone: "UTC", Mappings: []domain.Mapping{{Field: "f", Parameter: "F_out", Scale: 1}}}
	if _, e = s.PrepareFileInput(context.Background(), 2, "abcdefghijklmnop", spec, strings.NewReader("raw")); e == nil {
		t.Fatal("missing session accepted")
	}
	ctx := domain.WithPrincipal(context.Background(), domain.AccountPrincipal(a.a))
	v, e := s.PrepareFileInput(ctx, 2, "abcdefghijklmnop", spec, strings.NewReader("raw"))
	if e != nil {
		t.Fatal(e)
	}
	if string(v.Canonical) != "timestamp,F_out\n2026-01-01T00:00:00Z,0\n" {
		t.Fatal(string(v.Canonical))
	}
	if _, e = s.AcceptFileInput(ctx, v); e != nil {
		t.Fatal(e)
	}
	s.pin.ModelName = "changed-default"
	repeat, e := s.PrepareFileInput(ctx, 2, "abcdefghijklmnop", spec, strings.NewReader("raw"))
	if e != nil || repeat.ID != v.ID || rows.calls != 1 || repeat.ExecutionPin.ModelName != "synthetic" {
		t.Fatalf("replay %v %#v calls=%d", e, repeat, rows.calls)
	}
	if _, e = s.PrepareFileInput(ctx, 2, "abcdefghijklmnop", spec, strings.NewReader("changed")); e == nil {
		t.Fatal("changed bytes accepted")
	}
	a.a.Role = "viewer"
	if _, e = s.ComputeStoredInput(ctx, v.ID); e == nil {
		t.Fatal("revoked role accepted")
	}
}

func TestFileInputTimestampDSTAndExcel(t *testing.T) {
	loc, e := time.LoadLocation("America/New_York")
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []string{"2026-03-08 02:30:00", "2026-11-01 01:30:00"} {
		if _, e := parseFileTimestamp(v, "2006-01-02 15:04:05", loc, false); e == nil {
			t.Fatal("DST ambiguity accepted", v)
		}
	}
	ts, e := parseFileTimestamp("2026-03-08 03:30:00", "2006-01-02 15:04:05", loc, false)
	if e != nil || ts.Format(time.RFC3339) != "2026-03-08T07:30:00Z" {
		t.Fatal(ts, e)
	}
	for _, tc := range []struct {
		serial   string
		date1904 bool
		want     string
	}{{"1", false, "1900-01-01T00:00:00Z"}, {"61", false, "1900-03-01T00:00:00Z"}, {"0", true, "1904-01-01T00:00:00Z"}} {
		ts, e := parseFileTimestamp(tc.serial, "excel_serial", time.UTC, tc.date1904)
		if e != nil || ts.Format(time.RFC3339) != tc.want {
			t.Fatal(tc, ts, e)
		}
	}
	if _, e := parseFileTimestamp("60", "excel_serial", time.UTC, false); e == nil {
		t.Fatal("Excel fake leap day accepted")
	}
}

type blockedFileRaw struct {
	started, release chan struct{}
	done             bool
}

func (b *blockedFileRaw) Read(p []byte) (int, error) {
	if b.done {
		return 0, io.EOF
	}
	b.done = true
	close(b.started)
	<-b.release
	return copy(p, "raw"), io.EOF
}
func TestFileInputNormalizationHasNoWaitingQueue(t *testing.T) {
	account := domain.Account{ID: 1, Active: true, Role: "technologist", Scope: domain.AccessScope{Funds: []string{"operational"}}}
	ctx := domain.WithPrincipal(context.Background(), domain.AccountPrincipal(account))
	schemaBytes := []byte("fixture")
	s, e := newFileChecksFixture(t, &fileStore{}, &fileAccounts{account}, &fileScope{domain.Well{ID: 2, Active: true, FundType: "operational"}}, &fileRows{}, fileScorer{}, domain.TelemetrySchemaSnapshot{Bytes: schemaBytes, SHA256: domain.FileSHA256(schemaBytes), Channels: []domain.TelemetryChannel{{Canonical: "F_out", Scale: 1}}}, testFilePin(), domain.DefaultFileInputLimits())
	if e != nil {
		t.Fatal(e)
	}
	spec := domain.FileParseSpec{Version: "file_input_v1", Format: "csv", Delimiter: ",", HeaderRow: 1, TimeColumn: "time", TimestampLayout: "rfc3339", Timezone: "UTC", Mappings: []domain.Mapping{{Field: "f", Parameter: "F_out", Scale: 1}}}
	blocked := &blockedFileRaw{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, e := s.PrepareFileInput(ctx, 2, "queue-request-0001", spec, blocked); done <- e }()
	<-blocked.started
	_, e = s.PrepareFileInput(ctx, 2, "queue-request-0002", spec, strings.NewReader("raw"))
	var de *domain.Error
	if !errors.As(e, &de) || de.Kind != domain.Limited {
		t.Fatal("normalization waited or bypassed slot", e)
	}
	close(blocked.release)
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}

func TestFileInputValidationClockIsCaptured(t *testing.T) {
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	records := []map[string]any{{"well": "1", "time": fixed.Add(2 * time.Hour), "f": 0}}
	source := domain.TelemetrySource{WellField: "well", TimeField: "time", Mappings: []domain.Mapping{{Field: "f", Parameter: "F_out", Scale: 1}}}
	if points, rejected := MapTelemetryAt(records, source, fixed); len(points) != 0 || rejected != 1 {
		t.Fatal("fixed future guard ignored")
	}
	if points, rejected := MapTelemetryAt(records, source, fixed.Add(2*time.Hour)); len(points) != 1 || rejected != 0 {
		t.Fatal("explicit asof not used")
	}
}

func TestFileInputProtectsAllDeclaredFlowAndCurrentZeros(t *testing.T) {
	account := domain.Account{ID: 1, Active: true, Role: "technologist", Scope: domain.AccessScope{Funds: []string{"operational"}}}
	ctx := domain.WithPrincipal(context.Background(), domain.AccountPrincipal(account))
	schemaBytes := []byte("fixture")
	s, e := newFileChecksFixture(t, &fileStore{}, &fileAccounts{account}, &fileScope{domain.Well{ID: 2, Active: true, FundType: "operational"}}, &fileRows{}, fileScorer{}, domain.TelemetrySchemaSnapshot{Bytes: schemaBytes, SHA256: domain.FileSHA256(schemaBytes), Channels: []domain.TelemetryChannel{{Canonical: "Pump_discharge_flow", Scale: 1}, {Canonical: "Current_motor_max", Scale: 1}}}, testFilePin(), domain.DefaultFileInputLimits())
	if e != nil {
		t.Fatal(e)
	}
	for _, target := range []string{"Pump_discharge_flow", "Current_motor_max"} {
		spec := domain.FileParseSpec{Version: "file_input_v1", Format: "csv", Delimiter: ",", HeaderRow: 1, TimeColumn: "time", TimestampLayout: "rfc3339", Timezone: "UTC", Mappings: []domain.Mapping{{Field: "f", Parameter: target, Scale: 1, Missing: "0"}}}
		if _, e := s.PrepareFileInput(ctx, 2, "q0-validzero-0001", spec, strings.NewReader("raw")); e == nil {
			t.Fatal("valid measured zero declared missing", target)
		}
	}
}

func TestFileInputMarkerZeroTolerance(t *testing.T) {
	account := domain.Account{ID: 1, Active: true, Role: "technologist", Scope: domain.AccessScope{Funds: []string{"operational"}}}
	ctx := domain.WithPrincipal(context.Background(), domain.AccountPrincipal(account))
	schemaBytes := []byte("fixture")
	channels := []domain.TelemetryChannel{{Canonical: "F_out", Scale: 1}, {Canonical: "Ia", Scale: 1}, {Canonical: "Pump_discharge_flow", Scale: 1}, {Canonical: "qg", Scale: 1}}
	s, e := newFileChecksFixture(t, &fileStore{}, &fileAccounts{account}, &fileScope{domain.Well{ID: 2, Active: true, FundType: "operational"}}, fileinput.Reader{}, fileScorer{}, domain.TelemetrySchemaSnapshot{Bytes: schemaBytes, SHA256: domain.FileSHA256(schemaBytes), Channels: channels}, testFilePin(), domain.DefaultFileInputLimits())
	if e != nil {
		t.Fatal(e)
	}
	outside := math.Nextafter(1e-6, math.Inf(1))
	for _, channel := range []string{"F_out", "Ia", "Pump_discharge_flow", "qg"} {
		for _, marker := range []float64{0, 1e-7, -1e-7, 1e-6, -1e-6, outside, -outside} {
			t.Run(channel+"/"+strconv.FormatFloat(marker, 'g', -1, 64), func(t *testing.T) {
				spec := domain.FileParseSpec{Version: "file_input_v1", Format: "csv", Delimiter: ",", HeaderRow: 1, TimeColumn: "time", TimestampLayout: "rfc3339", Timezone: "UTC", Mappings: []domain.Mapping{{Field: "f", Parameter: channel, Scale: 1, Missing: strconv.FormatFloat(marker, 'g', -1, 64)}}}
				v, e := s.PrepareFileInput(ctx, 2, "marker-tolerance-001", spec, strings.NewReader("time,f\n2026-01-01T00:00:00Z,0\n"))
				if math.Abs(marker) <= 1e-6 {
					if e == nil {
						t.Fatal("marker swallowed valid zero", string(v.Canonical))
					}
					return
				}
				if e != nil {
					t.Fatal("outside-tolerance marker rejected", e)
				}
				want := "timestamp," + channel + "\n2026-01-01T00:00:00Z,0\n"
				if string(v.Canonical) != want {
					t.Fatalf("valid zero lost: %q", v.Canonical)
				}
			})
		}
	}
}

func TestFileInputDateOnlyDSTMidnight(t *testing.T) {
	loc, e := time.LoadLocation("America/New_York")
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct{ date, utc string }{{"2026-03-09", "2026-03-09T04:00:00Z"}, {"2026-11-01", "2026-11-01T04:00:00Z"}, {"2026-03-08", "2026-03-08T05:00:00Z"}, {"2026-11-02", "2026-11-02T05:00:00Z"}} {
		t.Run(tc.date, func(t *testing.T) {
			got, e := parseFileTimestamp(tc.date, "2006-01-02", loc, false)
			if e != nil || got.Format(time.RFC3339Nano) != tc.utc {
				t.Fatalf("midnight %v %v, want%s", got, e, tc.utc)
			}
		})
	}
}

func TestFileInputParserBuildMetadataFailsClosed(t *testing.T) {
	account := domain.Account{ID: 1, Active: true, Role: "technologist", Scope: domain.AccessScope{Funds: []string{"operational"}}}
	schemaBytes := []byte("fixture")
	schema := domain.TelemetrySchemaSnapshot{Bytes: schemaBytes, SHA256: domain.FileSHA256(schemaBytes), Channels: []domain.TelemetryChannel{{Canonical: "F_out", Scale: 1}}, DecoderSources: parameterfile.TelemetrySchemaSources()}
	_, e := NewFileChecksWithBuildInfo(&fileStore{}, &fileAccounts{account}, &fileScope{domain.Well{ID: 2, Active: true, FundType: "operational"}}, fileinput.Reader{}, fileScorer{}, schema, testFilePin(), domain.DefaultFileInputLimits(), nil)
	if e == nil || !strings.Contains(e.Error(), "parser_build_identity_unavailable") {
		t.Fatal("missing build identity did not fail closed", e)
	}
}

func (f *fileRows) ParserSources() []domain.FileParserPart {
	return (fileinput.Reader{}).ParserSources()
}
func newFileChecksFixture(t testing.TB, store FileInputStore, accounts FileInputAccounts, scope FundScope, reader FileInputReader, scorer PinnedTelemetryScorer, schema domain.TelemetrySchemaSnapshot, pin domain.ExecutionPin, limits domain.FileInputLimits) (*FileChecks, error) {
	t.Helper()
	if schema.DecoderSources == nil {
		schema.DecoderSources = parameterfile.TelemetrySchemaSources()
	}
	return NewFileChecksWithBuildInfo(store, accounts, scope, reader, scorer, schema, pin, limits, fileFixtureBuildInfo(t))
}

// Fixture metadata is explicit test injection: test binaries omit dependency BuildInfo.
func fileFixtureBuildInfo(t testing.TB) *debug.BuildInfo {
	t.Helper()
	mod, e := os.ReadFile("../../go.mod")
	if e != nil {
		t.Fatal(e)
	}
	sum, e := os.ReadFile("../../go.sum")
	if e != nil {
		t.Fatal(e)
	}
	selected := map[string]string{}
	for _, line := range strings.Split(string(mod), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasPrefix(fields[1], "v") {
			selected[fields[0]] = fields[1]
		}
	}
	sums := map[string]string{}
	for _, line := range strings.Split(string(sum), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 {
			sums[fields[0]+" "+fields[1]] = fields[2]
		}
	}
	info := &debug.BuildInfo{GoVersion: runtime.Version()}
	for _, path := range domain.FileParserModulePaths() {
		version := selected[path]
		hash := sums[path+" "+version]
		if version == "" || hash == "" {
			t.Fatal("missing explicit fixture module", path)
		}
		info.Deps = append(info.Deps, &debug.Module{Path: path, Version: version, Sum: hash})
	}
	return info
}

type parserManifestReader struct {
	fileinput.Reader
	parts []domain.FileParserPart
}

func (r parserManifestReader) ParserSources() []domain.FileParserPart {
	if r.parts == nil {
		return r.Reader.ParserSources()
	}
	return append([]domain.FileParserPart(nil), r.parts...)
}
func parserTestSetup(t *testing.T, reader FileInputReader, schema domain.TelemetrySchemaSnapshot, info *debug.BuildInfo) (*FileChecks, context.Context, error) {
	t.Helper()
	a := domain.Account{ID: 1, Active: true, Role: "technologist", Scope: domain.AccessScope{Funds: []string{"operational"}}}
	s, e := NewFileChecksWithBuildInfo(&fileStore{}, &fileAccounts{a}, &fileScope{domain.Well{ID: 2, Active: true, FundType: "operational"}}, reader, fileScorer{}, schema, testFilePin(), domain.DefaultFileInputLimits(), info)
	return s, domain.WithPrincipal(context.Background(), domain.AccountPrincipal(a)), e
}
func parserTestSchema(t *testing.T) domain.TelemetrySchemaSnapshot {
	t.Helper()
	s, e := parameterfile.ParseTelemetrySchema([]byte(`{"tags":[{"alias":"F_out","server_parameter":{"is_set":false,"coef_k":1,"coef_b":0}}]}`))
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func parserTestPrepare(t *testing.T, s *FileChecks, ctx context.Context) domain.PreparedFileInput {
	t.Helper()
	spec := domain.FileParseSpec{Version: "file_input_v1", Format: "csv", Delimiter: ",", HeaderRow: 1, TimeColumn: "time", TimestampLayout: "rfc3339", Timezone: "UTC", Mappings: []domain.Mapping{{Field: "f", Parameter: "F_out"}}}
	v, e := s.PrepareFileInput(ctx, 2, "parser-identity-001", spec, strings.NewReader("time,f\n2026-01-01T00:00:00Z,0\n"))
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestFileInputParserIdentityChangesWithAuthoritativeManifest(t *testing.T) {
	schema := parserTestSchema(t)
	base, ctx, e := parserTestSetup(t, fileinput.Reader{}, schema, fileFixtureBuildInfo(t))
	if e != nil {
		t.Fatal(e)
	}
	original := parserTestPrepare(t, base, ctx)
	for _, change := range []string{"reader", "decoder", "schema", "config", "module"} {
		t.Run(change, func(t *testing.T) {
			schema := parserTestSchema(t)
			reader := parserManifestReader{}
			info := fileFixtureBuildInfo(t)
			switch change {
			case "reader":
				reader.parts = reader.Reader.ParserSources()
				reader.parts[0].SHA256 = domain.FileSHA256([]byte("new trusted reader bytes"))
			case "decoder":
				schema.DecoderSources[0].SHA256 = domain.FileSHA256([]byte("new trusted decoder bytes"))
			case "schema":
				schema.Bytes = append(schema.Bytes, ' ')
				schema.SHA256 = domain.FileSHA256(schema.Bytes)
			case "config":
				schema.Channels[0].Scale = 2
			case "module":
				info.Deps[0].Version = "v1.0.1"
			}
			s, ctx, e := parserTestSetup(t, reader, schema, info)
			if e != nil {
				t.Fatal(e)
			}
			v := parserTestPrepare(t, s, ctx)
			if string(v.Canonical) != string(original.Canonical) || v.RequestCommitmentSHA256 != original.RequestCommitmentSHA256 {
				t.Fatal("fixture changed raw/options/canonical")
			}
			if v.ParseRecipeSHA256 == original.ParseRecipeSHA256 || v.FingerprintSHA256 == original.FingerprintSHA256 {
				t.Fatal("authoritative identity omitted", change)
			}
		})
	}
	recipe, _ := json.Marshal(original.ParseRecipe)
	if len(recipe) > 8192 || len(original.IdentityJSON) > 65536 || original.ParseRecipe.Version != "file_parser_v2" || len(original.ParseRecipe.Implementation.Modules) != 9 {
		t.Fatal("recipe cap/manifest incomplete")
	}
	t.Logf("compact recipe=%d identity=%d bytes", len(recipe), len(original.IdentityJSON))
	// Injected caller mutation after construction cannot rewrite the pinned implementation.
	info := fileFixtureBuildInfo(t)
	copySchema := parserTestSchema(t)
	s, ctx, e := parserTestSetup(t, fileinput.Reader{}, copySchema, info)
	if e != nil {
		t.Fatal(e)
	}
	info.Deps[0].Sum = "invalid"
	copySchema.DecoderSources[0].SHA256 = "invalid"
	v := parserTestPrepare(t, s, ctx)
	if v.ParseRecipeSHA256 != original.ParseRecipeSHA256 {
		t.Fatal("constructor retained caller-owned metadata")
	}
}
func TestFileInputParserConfigSnapshotIsolation(t *testing.T) {
	// Reusing caller Channels or nested Aliases would change the canonical value,
	// accepted alias or IsSet semantics without updating the captured config digest.
	for _, change := range []string{"scale", "is_set", "alias"} {
		t.Run(change, func(t *testing.T) {
			schema := parserTestSchema(t)
			schema.Channels[0].Aliases[0] = "frequency"
			s, ctx, err := parserTestSetup(t, fileinput.Reader{}, schema, fileFixtureBuildInfo(t))
			if err != nil {
				t.Fatal(err)
			}
			spec := domain.FileParseSpec{Version: "file_input_v1", Format: "csv", Delimiter: ",", HeaderRow: 1, TimeColumn: "time", TimestampLayout: "rfc3339", Timezone: "UTC", Mappings: []domain.Mapping{{Field: "f", Parameter: "frequency"}}}
			prepare := func(checks *FileChecks) domain.PreparedFileInput {
				t.Helper()
				v, e := checks.PrepareFileInput(ctx, 2, "parser-config-copy-001", spec, strings.NewReader("time,f\n2026-01-01T00:00:00Z,5\n"))
				if e != nil {
					t.Fatal(e)
				}
				return v
			}
			before := prepare(s)
			if string(before.Canonical) != "timestamp,F_out\n2026-01-01T00:00:00Z,5\n" || before.ParseSpec.Mappings[0].IsSet {
				t.Fatal("invalid original config semantics", string(before.Canonical))
			}
			switch change {
			case "scale":
				schema.Channels[0].Scale = 2
			case "is_set":
				schema.Channels[0].IsSet = true
			case "alias":
				schema.Channels[0].Aliases[0] = "changed_frequency"
			}
			after := prepare(s)
			if string(after.Canonical) != "timestamp,F_out\n2026-01-01T00:00:00Z,5\n" || after.ParseSpec.Mappings[0].IsSet || after.ParseSpec.Mappings[0].Scale != 1 {
				t.Fatal("caller mutation changed retained config semantics", string(after.Canonical), after.ParseSpec)
			}
			if after.ParseRecipeSHA256 != before.ParseRecipeSHA256 || after.ParseRecipe.ResolvedConfigSHA256 != before.ParseRecipe.ResolvedConfigSHA256 || after.FingerprintSHA256 != before.FingerprintSHA256 {
				t.Fatal("caller mutation changed retained config identity")
			}
			if change == "alias" {
				spec.Mappings[0].Parameter = "changed_frequency"
				if _, e := s.PrepareFileInput(ctx, 2, "parser-config-copy-002", spec, strings.NewReader("time,f\n2026-01-01T00:00:00Z,5\n")); e == nil || !strings.Contains(e.Error(), "unknown_mapping_target") {
					t.Fatal("caller alias mutation became authoritative", e)
				}
			}
			if change == "scale" {
				// The same actual config change made before another constructor
				// must still affect normalization and its authoritative manifest.
				changed, _, e := parserTestSetup(t, fileinput.Reader{}, schema, fileFixtureBuildInfo(t))
				if e != nil {
					t.Fatal(e)
				}
				v := prepare(changed)
				if string(v.Canonical) != "timestamp,F_out\n2026-01-01T00:00:00Z,10\n" || v.ParseSpec.Mappings[0].Scale != 2 {
					t.Fatal("new constructor ignored changed config", string(v.Canonical))
				}
				if v.ParseRecipe.ResolvedConfigSHA256 == before.ParseRecipe.ResolvedConfigSHA256 || v.ParseRecipeSHA256 == before.ParseRecipeSHA256 || v.FingerprintSHA256 == before.FingerprintSHA256 {
					t.Fatal("new config omitted from manifest/fingerprint")
				}
			}
		})
	}
}

func TestFileInputParserMetadataValidationAndActualDefault(t *testing.T) {
	for _, bad := range []string{"nil", "missing", "sum", "version", "replacement", "duplicate", "decoder", "reader"} {
		t.Run(bad, func(t *testing.T) {
			info := fileFixtureBuildInfo(t)
			schema := parserTestSchema(t)
			reader := parserManifestReader{}
			switch bad {
			case "nil":
				info = nil
			case "missing":
				info.Deps = info.Deps[1:]
			case "sum":
				info.Deps[0].Sum = "h1:bad"
			case "version":
				info.Deps[0].Version = "(devel)"
			case "replacement":
				info.Deps[0].Replace = &debug.Module{Path: "/private/local", Version: "v1.0.0", Sum: info.Deps[0].Sum}
			case "duplicate":
				info.Deps = append(info.Deps, info.Deps[0])
			case "decoder":
				schema.DecoderSources = nil
			case "reader":
				reader.parts = []domain.FileParserPart{{Name: "adapter/fileinput/reader.go", SHA256: "not-sha"}}
			}
			if _, _, e := parserTestSetup(t, reader, schema, info); e == nil {
				t.Fatal("malformed identity accepted", bad)
			}
		})
	}
	account := domain.Account{ID: 1, Active: true, Role: "technologist"}
	_, e := NewFileChecks(&fileStore{}, &fileAccounts{account}, &fileScope{}, fileinput.Reader{}, fileScorer{}, parserTestSchema(t), testFilePin(), domain.DefaultFileInputLimits())
	if info, ok := debug.ReadBuildInfo(); !ok || len(info.Deps) == 0 {
		if e == nil || !strings.Contains(e.Error(), "parser_build_identity_unavailable") {
			t.Fatal("test binary missing metadata fell back", e)
		}
	}
}

func TestFileInputPreparedParserManifestRemainsServerAuthoritative(t *testing.T) {
	s, ctx, e := parserTestSetup(t, fileinput.Reader{}, parserTestSchema(t), fileFixtureBuildInfo(t))
	if e != nil {
		t.Fatal(e)
	}
	v := parserTestPrepare(t, s, ctx)
	v.ParseRecipe.Implementation.Sources[0].SHA256 = domain.FileSHA256([]byte("forged caller manifest"))
	recipe, _ := json.Marshal(v.ParseRecipe)
	v.ParseRecipeSHA256 = domain.FileSHA256(recipe)
	v.IdentityJSON, _ = domain.FileInputIdentityJSON(v.FileInputSummary)
	v.FingerprintSHA256 = domain.FileSHA256(v.IdentityJSON)
	if _, e = s.AcceptFileInput(ctx, v); e == nil {
		t.Fatal("caller replaced authoritative parser identity")
	}
}

// Golden bytes are captured on accepted a05ef77 before the memory correction.
// Exact source/recipe hashes deliberately are not golden: source identity changes.
func TestFileInputCanonicalBaselineParity(t *testing.T) {
	const rawSchema = `{"tags":[{"alias":"4x010D F_out","displayname":"Частота","server_parameter":{"is_set":false,"coef_k":1,"coef_b":0}},{"alias":"R_izol","server_parameter":{"is_set":false,"coef_k":2,"coef_b":5}},{"alias":"energy_Input_energy_reactive_total_kW","server_parameter":{"is_set":false,"coef_k":2,"coef_b":0}},{"alias":"Vibration_X","server_parameter":{"is_set":false,"coef_k":2,"coef_b":1}},{"alias":"Ia","server_parameter":{"is_set":false,"coef_k":1,"coef_b":0}},{"alias":"set_Annular_pressure","server_parameter":{"is_set":true,"coef_k":1,"coef_b":0}}]}`
	const columns = "timestamp,F_out,Ia,R_izol,Vibration_X,energy_Input_energy_reactive_total_kW\n"
	for _, tc := range []struct {
		name, format, layout, zone string
		rows                       [][]string
		want                       string
	}{
		{"csv_rfc3339", "csv", "rfc3339", "UTC", [][]string{
			{"2026-01-02T00:00:00.123456789+03:00", "9007199254740993", "-0", "9999", "999.9", "-999", "0", "100", "ignored"},
			{"2026-01-01T00:00:00Z", "9007199254740993", "5.5e1", "2", "1.25e2", "1.5", "-0", "0", "ignored"},
			{"2026-01-01T03:00:00+01:00", "9007199254740993", "", "", "0", "-2.5", "0.5", "", "ignored"},
			{"2026-01-01T01:00:00Z", "9007199254740993", "", "", "", "", "", "", "ignored"},
		}, columns + "2026-01-01T00:00:00Z,55,0,9,4,250\n2026-01-01T01:00:00Z,,,,,\n2026-01-01T02:00:00Z,,0.5,,-4,0\n2026-01-01T21:00:00.123456789Z,0,0,,,\n"},
		{"csv_date_only_dst", "csv", "2006-01-02", "America/New_York", [][]string{
			{"2025-11-02", "9007199254740993", "0", "9999", "9999", "-999", "0", "1", "ignored"},
			{"2025-03-10", "9007199254740993", "5", "2", "3", "4", "0", "1", "ignored"},
		}, columns + "2025-03-10T04:00:00Z,5,0,9,9,6\n2025-11-02T04:00:00Z,0,0,,,\n"},
		{"xlsx_date1904", "xlsx", "excel_serial", "UTC", [][]string{
			{"1.5", "9007199254740993", "0", "9999", "999.9", "-999", "0", "1", "ignored"},
			{"0", "9007199254740993", "5", "2", "3", "4", "0", "1", "ignored"},
		}, columns + "1904-01-01T00:00:00Z,5,0,9,9,6\n1904-01-02T12:00:00Z,0,0,,,\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema, err := parameterfile.ParseTelemetrySchema([]byte(rawSchema))
			if err != nil {
				t.Fatal(err)
			}
			a := domain.Account{ID: 1, Active: true, Role: "technologist", Scope: domain.AccessScope{Funds: []string{"operational"}}}
			s, err := newFileChecksFixture(t, &fileStore{}, &fileAccounts{a}, &fileScope{domain.Well{ID: 9007199254740993, Active: true, FundType: "operational"}}, fileinput.Reader{}, fileScorer{}, schema, testFilePin(), domain.DefaultFileInputLimits())
			if err != nil {
				t.Fatal(err)
			}
			wellColumn := "well"
			spec := domain.FileParseSpec{Version: "file_input_v1", Format: tc.format, HeaderRow: 1, TimeColumn: "time", TimestampLayout: tc.layout, Timezone: tc.zone, WellColumn: &wellColumn, WellIdentity: "pulse", Mappings: []domain.Mapping{{Field: "f", Parameter: "Частота"}, {Field: "r", Parameter: "R_izol", Offset: 5}, {Field: "energy", Parameter: "energy_Input_energy_reactive_total_kW"}, {Field: "v", Parameter: "Vibration_X", Offset: 1, Missing: "-999"}, {Field: "i", Parameter: "Ia"}, {Field: "set", Parameter: "set_Annular_pressure"}}}
			header := []string{"time", "well", "f", "r", "energy", "v", "i", "set", "unused"}
			var raw []byte
			if tc.format == "csv" {
				spec.Delimiter = ","
				var b strings.Builder
				b.WriteString(strings.Join(header, ",") + "\n")
				for _, row := range tc.rows {
					b.WriteString(strings.Join(row, ",") + "\n")
				}
				raw = []byte(b.String())
			} else {
				spec.Sheet = "Sheet1"
				f := excelize.NewFile()
				defer f.Close()
				date1904 := true
				if err = f.SetWorkbookProps(&excelize.WorkbookPropsOptions{Date1904: &date1904}); err != nil {
					t.Fatal(err)
				}
				for ri, row := range append([][]string{header}, tc.rows...) {
					for ci, value := range row {
						cell, e := excelize.CoordinatesToCellName(ci+1, ri+1)
						if e != nil {
							t.Fatal(e)
						}
						if e = f.SetCellStr("Sheet1", cell, value); e != nil {
							t.Fatal(e)
						}
					}
				}
				b, e := f.WriteToBuffer()
				if e != nil {
					t.Fatal(e)
				}
				raw = b.Bytes()
			}
			ctx := domain.WithPrincipal(context.Background(), domain.AccountPrincipal(a))
			v, err := s.PrepareFileInput(ctx, 9007199254740993, "canonical-parity-001", spec, bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			if string(v.Canonical) != tc.want {
				t.Fatalf("canonical bytes differ:\ngot  %q\nwant %q", v.Canonical, tc.want)
			}
			if v.Rows != len(tc.rows) || v.Columns != 9 || len(v.IgnoredColumns) != 1 || v.IgnoredColumns[0] != "unused" || v.ParseSpec.Mappings[0].Parameter != "energy_Input_energy_reactive_total_kW" {
				t.Fatal("metadata/order/alias drift", v.FileInputSummary)
			}
			if v.ParseRecipe.Date1904 != (tc.format == "xlsx") {
				t.Fatal("date system drift")
			}
			t.Logf("canonical=%q rows=%d columns=%d ignored=%q recipe=%s fingerprint=%s", v.Canonical, v.Rows, v.Columns, v.IgnoredColumns, v.ParseRecipeSHA256, v.FingerprintSHA256)
		})
	}
}

func TestFileInputCompactNormalizationErrors(t *testing.T) {
	for _, tc := range []struct {
		name, raw, reason string
		set               bool
		max               int64
	}{
		{"foreign_id_first", "time,id,f\ninvalid,2,bad\n2026-01-01T00:00:00Z,3,1\n", "multiple_wells_not_supported", false, 0},
		{"invalid_id_first", "time,id,f\ninvalid,2,bad\n2026-01-01T00:00:00Z,2.0,1\n", "invalid_well_id", false, 0},
		{"duplicate_time", "time,id,f\n2026-01-01T00:00:00Z,2,1\n2026-01-01T01:00:00+01:00,2,2\n", "duplicate_timestamp", false, 0},
		{"future", "time,id,f\n9999-01-01T00:00:00Z,2,1\n", "invalid_telemetry_row", false, 0},
		{"zero_time", "time,id,f\n0001-01-01T00:00:00Z,2,1\n", "invalid_telemetry_row", false, 0},
		{"span", "time,id,f\n2024-01-01T00:00:00Z,2,1\n2025-01-02T00:00:00Z,2,2\n", "timestamp_span_limit", false, 0},
		{"overflow", "time,id,f\n2026-01-01T00:00:00Z,2,1e308\n", "non_finite_measurement", false, 0},
		{"bad_number", "time,id,f\n2026-01-01T00:00:00Z,2,NaN\n", "non_numeric_measurement", false, 0},
		{"literal_null", "time,id,f\n2026-01-01T00:00:00Z,2,null\n", "non_numeric_measurement", false, 0},
		{"all_setpoints", "time,id,f\n2026-01-01T00:00:00Z,2,1\n", "invalid_telemetry_row", true, 0},
		{"empty", "time,id,f\n", "empty_file", false, 0},
		{"canonical_bound", "time,id,f\n2026-01-01T00:00:00Z,2,1\n", "canonical_limit", false, 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			schema := parserTestSchema(t)
			schema.Channels[0].Scale = 2
			schema.Channels[0].IsSet = tc.set
			limits := domain.DefaultFileInputLimits()
			if tc.max != 0 {
				limits.CanonicalBytes = tc.max
			}
			a := domain.Account{ID: 1, Active: true, Role: "technologist", Scope: domain.AccessScope{Funds: []string{"operational"}}}
			s, e := newFileChecksFixture(t, &fileStore{}, &fileAccounts{a}, &fileScope{domain.Well{ID: 2, Active: true, FundType: "operational"}}, fileinput.Reader{}, fileScorer{}, schema, testFilePin(), limits)
			if e != nil {
				t.Fatal(e)
			}
			id := "id"
			spec := domain.FileParseSpec{Version: "file_input_v1", Format: "csv", Delimiter: ",", HeaderRow: 1, TimeColumn: "time", TimestampLayout: "rfc3339", Timezone: "UTC", WellColumn: &id, WellIdentity: "pulse", Mappings: []domain.Mapping{{Field: "f", Parameter: "F_out"}}}
			_, e = s.PrepareFileInput(domain.WithPrincipal(context.Background(), domain.AccountPrincipal(a)), 2, "compact-errors-001", spec, strings.NewReader(tc.raw))
			var de *domain.Error
			if !errors.As(e, &de) || de.Kind != domain.Invalid || e.Error() != tc.reason {
				t.Fatalf("got %v want Invalid/%s", e, tc.reason)
			}
		})
	}
}
