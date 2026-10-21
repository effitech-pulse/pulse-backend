package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/fileinput"
	"github.com/effitech-pulse/pulse-backend/internal/adapter/parameterfile"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/migrations"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

type savedScope struct{ store *postgres.Repository }

func (s savedScope) Members(ctx context.Context) (domain.Cohort, []domain.Well, error) {
	return domain.Cohort{}, nil, nil
}
func (s savedScope) RequireWell(ctx context.Context, id int64) (domain.Well, error) {
	return s.store.Well(ctx, id)
}
func (s savedScope) RequireUnitek(ctx context.Context, id int64) (domain.Well, error) {
	return domain.Well{}, domain.NewError(domain.Missing, "missing")
}

type savedScorer struct {
	calls       atomic.Int64
	expectedPin domain.ExecutionPin
	content     string
}

func (s *savedScorer) UploadPinned(ctx context.Context, pin domain.ExecutionPin, id domain.PinnedInputIdentity, reader io.Reader) (domain.UploadResult, error) {
	s.calls.Add(1)
	raw, e := io.ReadAll(reader)
	if e != nil {
		return domain.UploadResult{}, e
	}
	if domain.FileSHA256(raw) != id.CanonicalSHA256 {
		return domain.UploadResult{}, fmt.Errorf("canonical hash mismatch")
	}
	if s.content != "" && s.content != string(raw) {
		return domain.UploadResult{}, fmt.Errorf("source bytes changed")
	}
	if s.expectedPin.BundleSHA256 != "" && pin.BundleSHA256 != s.expectedPin.BundleSHA256 {
		return domain.UploadResult{}, fmt.Errorf("default pin changed")
	}
	return domain.UploadResult{Verdict: "norm", Filename: id.CanonicalSHA256, NWindows: 9007199254740993, Timeline: []domain.TimelinePoint{{RawScore: ptr(0.), WatchFired: ptr(false)}}, ModelName: &pin.ModelName, ModelVersion: &pin.BundleSHA256, DecisionRule: pin.DecisionRule, Provenance: &domain.Provenance{InputSnapshotID: &id.InputSnapshotID, ContextVersion: &pin.ContextVersion, InputSHA256: &id.CanonicalSHA256, FeatureRecipeSHA256: &pin.FeatureRecipeSHA256, FeatureSchemaSHA256: &pin.FeatureSchemaSHA256, SourceManifestSHA256: &pin.SourceManifestSHA256, RuntimeManifestSHA256: &pin.RuntimeManifestSHA256}}, nil
}
func savedPin() domain.ExecutionPin {
	h := domain.FileSHA256([]byte("q0-fixed-model"))
	return domain.ExecutionPin{Version: "upload_pin_v1", ModelName: "q0-fixed-model", BundleSHA256: h, ContextVersion: "legacy_v1", DecisionRule: &domain.DecisionRule{ContextVersion: "legacy_v1", PolicyID: "q0", RuleSHA256: h, Watch: &domain.ThresholdRule{Scale: "raw", Comparison: ">", Threshold: .5}}, FeatureRecipeSHA256: h, FeatureSchemaSHA256: h, SourceManifestSHA256: h, RuntimeManifestSHA256: h}
}

const savedRaw = "time,id,f,i,r,e,q,s\n2026-01-01T00:00:00Z,9007199254740993,0,0,9999,999.9,0,12\n2026-01-02T00:00:00Z,9007199254740993,50,30,50,3,0,13\n"

func savedSchema(t testing.TB) domain.TelemetrySchemaSnapshot {
	t.Helper()
	schema, e := parameterfile.ParseTelemetrySchema([]byte(`{"tags":[{"alias":"4x010D F_out","displayname":"Частота","server_parameter":{"is_set":false,"coef_k":1,"coef_b":0}},{"alias":"Ia","server_parameter":{"is_set":false,"coef_k":1,"coef_b":0}},{"alias":"R_izol","server_parameter":{"is_set":false,"coef_k":1,"coef_b":0}},{"alias":"energy_Input_energy_reactive_total_kW","server_parameter":{"is_set":false,"coef_k":1,"coef_b":0}},{"alias":"set_pressure","server_parameter":{"is_set":true,"coef_k":1,"coef_b":0}}]}`))
	if e != nil {
		t.Fatal(e)
	}
	return schema
}
func savedSpec() domain.FileParseSpec {
	well := "id"
	return domain.FileParseSpec{Version: "file_input_v1", Format: "csv", Delimiter: ",", HeaderRow: 1, TimeColumn: "time", TimestampLayout: "rfc3339", Timezone: "UTC", WellColumn: &well, WellIdentity: "unitek", Mappings: []domain.Mapping{{Field: "f", Parameter: "4x010D F_out", Scale: 1}, {Field: "i", Parameter: "Ia", Scale: 1}, {Field: "r", Parameter: "R_izol", Scale: 1}, {Field: "e", Parameter: "energy_Input_energy_reactive_total_kW", Scale: 1}, {Field: "q", Parameter: "qg", Scale: 1}, {Field: "s", Parameter: "set_pressure", Scale: 1}}}
}
func savedService(t testing.TB, pool *pgxpool.Pool, scorer *savedScorer, pin domain.ExecutionPin, l domain.FileInputLimits) *service.FileChecks {
	t.Helper()
	store := postgres.New(pool)
	s, e := service.NewFileChecksWithBuildInfo(store, store, savedScope{store}, fileinput.Reader{}, scorer, savedSchema(t), pin, l, savedFixtureBuildInfo(t))
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func savedSetup(t *testing.T) (*pgxpool.Pool, context.Context, *service.FileChecks, *savedScorer) {
	t.Helper()
	pool := testDB(t)
	ctx := context.Background()
	if _, e := pool.Exec(ctx, `INSERT INTO well(unitek_id,armits_id,ngdu,department,active) VALUES(9007199254740993,42,0,0,true);INSERT INTO auth_user(login,name,password_hash,role,scope) VALUES('q0','q0','unused','technologist','{"funds":["operational"]}')`); e != nil {
		t.Fatal(e)
	}
	store := postgres.New(pool)
	account, e := store.Account(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	ctx = domain.WithPrincipal(ctx, domain.AccountPrincipal(account))
	scorer := &savedScorer{expectedPin: savedPin()}
	return pool, ctx, savedService(t, pool, scorer, savedPin(), domain.DefaultFileInputLimits()), scorer
}
func prepareSaved(t *testing.T, s *service.FileChecks, ctx context.Context, key string) domain.PreparedFileInput {
	t.Helper()
	v, e := s.PrepareFileInput(ctx, 1, key, savedSpec(), strings.NewReader(savedRaw))
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestFileInputPGExactSnapshotIdempotencyAndImmutable(t *testing.T) {
	pool, ctx, s, scorer := savedSetup(t)
	v := prepareSaved(t, s, ctx, "q0-input-request01")
	if !strings.Contains(string(v.Canonical), "2026-01-01T00:00:00Z,0,0,,,0\n") || strings.Contains(string(v.Canonical), "set_pressure") {
		t.Fatal("marker/zero/set semantics")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); out, e := s.AcceptFileInput(ctx, v); errs <- e; ids <- out.ID }()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	for id := range ids {
		if id != v.ID {
			t.Fatal("multiple winners")
		}
	}
	var count int
	var budget int64
	if e := pool.QueryRow(ctx, "SELECT retained_count,retained_bytes FROM file_input_budget WHERE budget_key='global'").Scan(&count, &budget); e != nil || count != 1 || budget <= v.RawBytes+v.CanonicalBytes {
		t.Fatalf("budget %d/%d %v", count, budget, e)
	}
	store := postgres.New(pool)
	actual, e := store.StoredFileInput(ctx, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(actual.Raw, v.Raw) || !bytes.Equal(actual.Canonical, v.Canonical) || !bytes.Equal(actual.IdentityJSON, v.IdentityJSON) {
		t.Fatal("snapshot changed")
	}
	mismatch := v
	mismatch.ID = strings.Repeat("b", 32)
	mismatch.ExecutionPin = savedPin()
	mismatch.ExecutionPin.ModelName = "different"
	mismatch.IdentityJSON, _ = domain.FileInputIdentityJSON(mismatch.FileInputSummary)
	mismatch.FingerprintSHA256 = domain.FileSHA256(mismatch.IdentityJSON)
	if _, e = store.AcceptFileInput(ctx, mismatch); e == nil {
		t.Fatal("concurrent fingerprint conflict missed")
	}
	result, e := s.ComputeStoredInput(ctx, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	first, e := s.CommitStoredResult(ctx, result)
	if e != nil {
		t.Fatal(e)
	}
	result.Result.Verdict = "changed"
	again, e := s.CommitStoredResult(ctx, result)
	if e != nil || again.Result.Verdict != first.Result.Verdict {
		t.Fatal("immutable result overwritten", e)
	}
	restart := savedService(t, pool, scorer, savedPin(), domain.DefaultFileInputLimits())
	got, e := restart.ComputeStoredInput(ctx, v.ID)
	if e != nil || got.Result.NWindows != 9007199254740993 || scorer.calls.Load() != 1 || got.Result.Timeline[0].WatchFired == nil || *got.Result.Timeline[0].WatchFired {
		t.Fatalf("retry %v %#v calls=%d", e, got, scorer.calls.Load())
	}
	for _, table := range []string{"file_input", "file_input_payload", "file_check_result"} {
		for _, verb := range []string{"UPDATE " + table + " SET " + map[string]string{"file_input": "id=id", "file_input_payload": "input_id=input_id", "file_check_result": "input_id=input_id"}[table], "DELETE FROM " + table} {
			if _, e = pool.Exec(ctx, verb); e == nil {
				t.Fatal("immutable trigger bypass", verb)
			}
		}
	}
	if _, e = pool.Exec(ctx, "ALTER TABLE file_input_payload RENAME TO unavailable_payload"); e != nil {
		t.Fatal(e)
	}
	if _, e = store.FileInputRequest(ctx, 1, v.RequestKey); e != nil {
		t.Fatal("request reads payload", e)
	}
	if _, e = store.FileInputSummary(ctx, v.ID); e != nil {
		t.Fatal("summary reads payload", e)
	}
	if _, e = store.FileInputSummaries(ctx, 1, ""); e != nil {
		t.Fatal("list reads payload", e)
	}
	if _, e = store.StoredFileResult(ctx, v.ID); e != nil {
		t.Fatal("result reads payload", e)
	}
}
func TestFileInputQuotaRaceAndFailedTransaction(t *testing.T) {
	pool, ctx, _, scorer := savedSetup(t)
	l := domain.DefaultFileInputLimits()
	l.Inputs = 1
	s := savedService(t, pool, scorer, savedPin(), l)
	a := prepareSaved(t, s, ctx, "q0-quota-request01")
	b := prepareSaved(t, s, ctx, "q0-quota-request02")
	// Failure after metadata insert must also roll back the budget/account row.
	if _, e := pool.Exec(ctx, `CREATE FUNCTION fail_q0_payload() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'synthetic_failure';END$$;CREATE TRIGGER fail_q0 BEFORE INSERT ON file_input_payload FOR EACH ROW EXECUTE FUNCTION fail_q0_payload()`); e != nil {
		t.Fatal(e)
	}
	if _, e := s.AcceptFileInput(ctx, a); e == nil {
		t.Fatal("failed tx succeeded")
	}
	var count int
	if e := pool.QueryRow(ctx, "SELECT retained_count FROM file_input_budget WHERE budget_key='global'").Scan(&count); e != nil || count != 0 {
		t.Fatal("failed tx charged", count, e)
	}
	if _, e := pool.Exec(ctx, "DROP TRIGGER fail_q0 ON file_input_payload"); e != nil {
		t.Fatal(e)
	}
	errs := make(chan error, 2)
	go func() { _, e := s.AcceptFileInput(ctx, a); errs <- e }()
	go func() { _, e := s.AcceptFileInput(ctx, b); errs <- e }()
	success := 0
	for i := 0; i < 2; i++ {
		if e := <-errs; e == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("quota race winners", success)
	}
	if e := pool.QueryRow(ctx, "SELECT retained_count FROM file_input_budget WHERE budget_key='global'").Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
}
func TestFileInputPayloadCorruptionAndMissingNoRPC(t *testing.T) {
	for _, mode := range []string{"corrupt", "missing"} {
		t.Run(mode, func(t *testing.T) {
			pool, ctx, s, scorer := savedSetup(t)
			v := prepareSaved(t, s, ctx, "q0-corrupt-request")
			if _, e := s.AcceptFileInput(ctx, v); e != nil {
				t.Fatal(e)
			}
			if _, e := pool.Exec(ctx, "ALTER TABLE file_input_payload DISABLE TRIGGER file_input_immutable"); e != nil {
				t.Fatal(e)
			}
			query := "UPDATE file_input_payload SET raw_bytes=set_byte(raw_bytes,0,0)"
			if mode == "missing" {
				query = "DELETE FROM file_input_payload"
			}
			if _, e := pool.Exec(ctx, query); e != nil {
				t.Fatal(e)
			}
			if _, e := s.ComputeStoredInput(ctx, v.ID); e == nil || scorer.calls.Load() != 0 {
				t.Fatal("corrupt bytes reached RPC", e)
			}
		})
	}
}
func TestFileInputRechecksActorScopeAndWell(t *testing.T) {
	pool, ctx, s, scorer := savedSetup(t)
	v := prepareSaved(t, s, ctx, "q0-access-request1")
	if _, e := s.AcceptFileInput(ctx, v); e != nil {
		t.Fatal(e)
	}
	for _, change := range []string{"UPDATE auth_user SET role='viewer'", "UPDATE auth_user SET active=false", "UPDATE auth_user SET scope='{\"funds\":[\"presentation\"]}'", "UPDATE well SET active=false", "UPDATE well SET archived_at=now()"} {
		if _, e := pool.Exec(ctx, change); e != nil {
			t.Fatal(e)
		}
		if _, e := s.ComputeStoredInput(ctx, v.ID); e == nil {
			t.Fatal("revoked execution", change)
		}
		if _, e := pool.Exec(ctx, `UPDATE auth_user SET role='technologist',active=true,scope='{"funds":["operational"]}';UPDATE well SET active=true,archived_at=NULL`); e != nil {
			t.Fatal(e)
		}
	}
	if scorer.calls.Load() != 0 {
		t.Fatal("revocation reached ML")
	}
	if _, e := pool.Exec(ctx, `INSERT INTO auth_user(login,name,password_hash,role,scope) VALUES('sibling','sibling','unused','admin','{"funds":["operational"]}')`); e != nil {
		t.Fatal(e)
	}
	a, e := postgres.New(pool).Account(ctx, 2)
	if e != nil {
		t.Fatal(e)
	}
	sibling := domain.WithPrincipal(ctx, domain.AccountPrincipal(a))
	if _, e = s.FileInputSummary(sibling, v.ID); e == nil {
		t.Fatal("sibling opened input")
	}
	if _, e = s.StoredFileResult(sibling, v.ID); e == nil {
		t.Fatal("sibling opened result")
	}
}
func TestFileInputXLSXRawCanonicalPersist(t *testing.T) {
	pool, ctx, s, _ := savedSetup(t)
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetSheetRow("Sheet1", "A1", &[]any{"time", "id", "f", "i", "r", "e", "q", "s"})
	_ = f.SetSheetRow("Sheet1", "A2", &[]any{"2026-01-01T00:00:00Z", "9007199254740993", 0, 0, 9999, 999.9, 0, 12})
	raw, e := f.WriteToBuffer()
	if e != nil {
		t.Fatal(e)
	}
	spec := savedSpec()
	spec.Format = "xlsx"
	spec.Delimiter = ""
	spec.Sheet = "Sheet1"
	v, e := s.PrepareFileInput(ctx, 1, "q0-xlsx-request01", spec, bytes.NewReader(raw.Bytes()))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AcceptFileInput(ctx, v); e != nil {
		t.Fatal(e)
	}
	got, e := postgres.New(pool).StoredFileInput(ctx, v.ID)
	if e != nil || !bytes.Equal(got.Raw, raw.Bytes()) || !bytes.Equal(got.Canonical, v.Canonical) {
		t.Fatal("xlsx snapshot", e)
	}
}

// TestFileInputCrashChild is an explicit proof process; it is never a worker.
func TestFileInputCrashChild(t *testing.T) {
	mode := os.Getenv("Q0_CHILD_MODE")
	if mode == "" {
		return
	}
	ctx := context.Background()
	cfg, e := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = os.Getenv("Q0_CHILD_SCHEMA")
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Close()
	store := postgres.New(pool)
	a, e := store.Account(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	ctx = domain.WithPrincipal(ctx, domain.AccountPrincipal(a))
	s := savedService(t, pool, &savedScorer{expectedPin: savedPin()}, savedPin(), domain.DefaultFileInputLimits())
	id := os.Getenv("Q0_CHILD_ID")
	if mode == "accept" {
		raw, e := os.Open(os.Getenv("Q0_CHILD_FILE"))
		if e != nil {
			t.Fatal(e)
		}
		defer raw.Close()
		v, e := s.PrepareFileInput(ctx, 1, "q0-process-request", savedSpec(), raw)
		if e != nil {
			t.Fatal(e)
		}
		out, e := s.AcceptFileInput(ctx, v)
		if e != nil {
			t.Fatal(e)
		}
		id = out.ID
	} else {
		result, e := s.ComputeStoredInput(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.CommitStoredResult(ctx, result); e != nil {
			t.Fatal(e)
		}
	}
	fmt.Println("Q0_COMMITTED " + id)
	for {
		time.Sleep(time.Hour)
	}
}
func runSavedChild(t *testing.T, pool *pgxpool.Pool, mode, path, id string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestFileInputCrashChild$", "-test.v")
	cmd.Env = append(os.Environ(), "Q0_CHILD_MODE="+mode, "Q0_CHILD_SCHEMA="+pool.Config().ConnConfig.RuntimeParams["search_path"], "Q0_CHILD_FILE="+path, "Q0_CHILD_ID="+id)
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(stdout)
	line := make(chan string, 1)
	go func() {
		for scanner.Scan() {
			text := scanner.Text()
			if strings.HasPrefix(text, "Q0_COMMITTED ") {
				line <- strings.TrimPrefix(text, "Q0_COMMITTED ")
				return
			}
		}
		line <- ""
	}()
	select {
	case result := <-line:
		if result == "" {
			t.Fatal("child failed", stderr.String())
		}
		return result
	case <-time.After(20 * time.Second):
		t.Fatal("child timeout")
		return ""
	}
}
func TestFileInputKillAfterCommitAndFreshServiceReplay(t *testing.T) {
	pool, ctx, _, scorer := savedSetup(t)
	path := filepath.Join(t.TempDir(), "original.csv")
	if e := os.WriteFile(path, []byte(savedRaw), 0600); e != nil {
		t.Fatal(e)
	}
	id := runSavedChild(t, pool, "accept", path, "")
	if e := os.WriteFile(path, []byte("changed source"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(path); e != nil {
		t.Fatal(e)
	}
	t.Setenv("ML_MODEL_NAME", "changed-default")
	pin := savedPin()
	pin.ModelName = "changed-default"
	pin.BundleSHA256 = domain.FileSHA256([]byte("changed-default"))
	fresh := savedService(t, pool, scorer, pin, domain.DefaultFileInputLimits())
	result, e := fresh.ComputeStoredInput(ctx, id)
	if e != nil || scorer.calls.Load() != 1 || result.ExecutionPin.BundleSHA256 != savedPin().BundleSHA256 {
		t.Fatal("fresh replay used mutable default", e)
	}
	runSavedChild(t, pool, "result", "", id)
	scorer.calls.Store(0)
	got, e := savedService(t, pool, scorer, pin, domain.DefaultFileInputLimits()).ComputeStoredInput(ctx, id)
	if e != nil || got.Result.Verdict != "norm" || scorer.calls.Load() != 0 {
		t.Fatal("committed result recomputed", e)
	}
}
func TestFileInputFreshAndRepeatMigration(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	if e := migrations.Apply(ctx, pool); e != nil {
		t.Fatal(e)
	}
	var version int
	if e := pool.QueryRow(ctx, "SELECT max(version) FROM backend_schema_migration").Scan(&version); e != nil || version != migrations.CurrentVersion {
		t.Fatal(version, e)
	}
}

func TestFileInputRejectsResultIdentityMismatch(t *testing.T) {
	_, ctx, s, _ := savedSetup(t)
	v := prepareSaved(t, s, ctx, "q0-result-request1")
	if _, e := s.AcceptFileInput(ctx, v); e != nil {
		t.Fatal(e)
	}
	result, e := s.ComputeStoredInput(ctx, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	result.Result.ModelVersion = ptr("sha256:wrong")
	if _, e = s.CommitStoredResult(ctx, result); e == nil {
		t.Fatal("wrong actual model result accepted")
	}
}

func TestFileInputMappingAndStrictRows(t *testing.T) {
	_, ctx, s, _ := savedSetup(t)
	for _, name := range []string{"unknown", "duplicate", "setpoint_spoof", "zero_missing", "scale", "unit", "bad_number", "bad_time", "other_well_bad_time", "duplicate_time", "schema_target", "dat_zam"} {
		t.Run(name, func(t *testing.T) {
			spec := savedSpec()
			raw := savedRaw
			wantFailure := true
			switch name {
			case "unknown":
				spec.Mappings[0].Parameter = "unknown"
			case "duplicate":
				spec.Mappings[1].Parameter = "F_out"
			case "setpoint_spoof":
				spec.Mappings[5].IsSet = false
				wantFailure = false
			case "zero_missing":
				spec.Mappings[0].Missing = "0"
			case "scale":
				spec.Mappings[0].Scale = 2
			case "unit":
				spec.Mappings[0].Unit = "invented"
			case "bad_number":
				raw = strings.Replace(raw, ",50,30,", ",NaN,30,", 1)
			case "bad_time":
				raw = strings.Replace(raw, "2026-01-01T00:00:00Z", "bad-time", 1)
			case "other_well_bad_time":
				raw = strings.Replace(raw, "2026-01-01T00:00:00Z,9007199254740993", "bad-time,9007199254740994", 1)
			case "duplicate_time":
				raw = strings.Replace(raw, "2026-01-02T00:00:00Z", "2026-01-01T00:00:00Z", 1)
			case "schema_target":
				spec.SchemaSHA256 = domain.FileSHA256([]byte("client-spoof"))
				wantFailure = false
			case "dat_zam":
				spec.Mappings[0].Parameter = "dat_zam"
			}
			v, e := s.PrepareFileInput(ctx, 1, "q0-mapping-"+strings.ReplaceAll(name, "_", "-"), spec, strings.NewReader(raw))
			if wantFailure {
				if e == nil {
					t.Fatal("invalid file accepted")
				}
				if name == "other_well_bad_time" && !strings.Contains(e.Error(), "multiple_wells_not_supported") {
					t.Fatal("row IDs not validated first", e)
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				if strings.Contains(string(v.Canonical), "set_pressure") || v.ParseSpec.SchemaSHA256 != savedSchema(t).SHA256 {
					t.Fatal("client overrode server schema")
				}
			}
		})
	}
}
func TestFileInputCanonicalRawSpecAndQuotaByteCaps(t *testing.T) {
	pool, ctx, s, scorer := savedSetup(t)
	v := prepareSaved(t, s, ctx, "q0-bounds-initial")
	l := domain.DefaultFileInputLimits()
	l.CanonicalBytes = v.CanonicalBytes
	l.RawBytes = int64(len(savedRaw))
	exact := savedService(t, pool, scorer, savedPin(), l)
	if _, e := exact.PrepareFileInput(ctx, 1, "q0-bounds-exact01", savedSpec(), strings.NewReader(savedRaw)); e != nil {
		t.Fatal("exact cap failed", e)
	}
	for _, name := range []string{"canonical", "raw", "spec"} {
		t.Run(name, func(t *testing.T) {
			small := l
			switch name {
			case "canonical":
				small.CanonicalBytes--
			case "raw":
				small.RawBytes--
			case "spec":
				small.SpecBytes = 1
			}
			limited := savedService(t, pool, scorer, savedPin(), small)
			if _, e := limited.PrepareFileInput(ctx, 1, "q0-bounds-"+name+"-01", savedSpec(), strings.NewReader(savedRaw)); e == nil {
				t.Fatal("size cap bypass")
			}
		})
	}
	// Exact retained/account byte caps include immutable parse identity and spec.
	specJSON, _ := json.Marshal(v.ParseSpec)
	charge := v.RawBytes + v.CanonicalBytes + int64(len(v.IdentityJSON)+len(specJSON))
	for _, name := range []string{"global", "account"} {
		t.Run(name, func(t *testing.T) {
			localPool, localCtx, _, localScorer := savedSetup(t)
			small := domain.DefaultFileInputLimits()
			if name == "global" {
				small.RetainedBytes = charge
			} else {
				small.AccountBytes = charge
			}
			var limited *service.FileChecks
			var input domain.PreparedFileInput
			for i := 0; i < 5; i++ {
				limited = savedService(t, localPool, localScorer, savedPin(), small)
				input = prepareSaved(t, limited, localCtx, "q0-byte-quota-001")
				a, _ := json.Marshal(input.ParseSpec)
				actual := input.RawBytes + input.CanonicalBytes + int64(len(input.IdentityJSON)+len(a))
				if (name == "global" && small.RetainedBytes == actual) || (name == "account" && small.AccountBytes == actual) {
					break
				}
				if name == "global" {
					small.RetainedBytes = actual
				} else {
					small.AccountBytes = actual
				}
				if i == 4 {
					t.Fatal("exact quota fixture did not converge")
				}
			}
			// A one-byte deficit must reject without charging admission.
			deficit := small
			if name == "global" {
				deficit.RetainedBytes--
			} else {
				deficit.AccountBytes--
			}
			tooSmall := savedService(t, localPool, localScorer, savedPin(), deficit)
			rejected := prepareSaved(t, tooSmall, localCtx, "q0-byte-quota-001")
			if _, e := tooSmall.AcceptFileInput(localCtx, rejected); e == nil {
				t.Fatal("quota +1 accepted")
			}

			if _, e := limited.AcceptFileInput(localCtx, input); e != nil {
				t.Fatal("exact quota rejected", e)
			}
			next := prepareSaved(t, limited, localCtx, "q0-byte-quota-002")
			if _, e := limited.AcceptFileInput(localCtx, next); e == nil {
				t.Fatal("quota bytes bypass")
			}
		})
	}
}

func TestFileInputResultSizeAndExactSpecCaps(t *testing.T) {
	pool, ctx, s, scorer := savedSetup(t)
	v := prepareSaved(t, s, ctx, "q0-resultsize-001")
	if _, e := s.AcceptFileInput(ctx, v); e != nil {
		t.Fatal(e)
	}
	result, e := s.ComputeStoredInput(ctx, v.ID)
	if e != nil {
		t.Fatal(e)
	}
	serialized, e := json.Marshal(result.Result)
	if e != nil {
		t.Fatal(e)
	}
	l := domain.DefaultFileInputLimits()
	l.ResultBytes = len(serialized) - 1
	limited := savedService(t, pool, scorer, savedPin(), l)
	if _, e = limited.ComputeStoredInput(ctx, v.ID); e == nil {
		t.Fatal("result +1 accepted")
	}
	l.ResultBytes++
	exact := savedService(t, pool, scorer, savedPin(), l)
	out, e := exact.ComputeStoredInput(ctx, v.ID)
	if e != nil {
		t.Fatal("exact result cap", e)
	}
	if _, e = exact.CommitStoredResult(ctx, out); e != nil {
		t.Fatal(e)
	}
	clientJSON, e := json.Marshal(savedSpec())
	if e != nil {
		t.Fatal(e)
	}
	l = domain.DefaultFileInputLimits()
	l.SpecBytes = len(clientJSON)
	exact = savedService(t, pool, scorer, savedPin(), l)
	if _, e = exact.PrepareFileInput(ctx, 1, "q0-exactspec-0001", savedSpec(), strings.NewReader(savedRaw)); e != nil {
		t.Fatal("exact spec cap", e)
	}
	l.SpecBytes--
	limited = savedService(t, pool, scorer, savedPin(), l)
	if _, e = limited.PrepareFileInput(ctx, 1, "q0-exactspec-0002", savedSpec(), strings.NewReader(savedRaw)); e == nil {
		t.Fatal("spec +1 accepted")
	}
}

func TestFileInputRequestNamespaceConflictsAndSiblingHistory(t *testing.T) {
	pool, ctx, s, _ := savedSetup(t)
	v := prepareSaved(t, s, ctx, "q0-key-scope-0001")
	if _, e := s.AcceptFileInput(ctx, v); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO well(unitek_id,armits_id,ngdu,department,active) VALUES(3,4,0,0,true);INSERT INTO auth_user(login,name,password_hash,role,scope) VALUES('other','other','unused','technologist','{"funds":["operational"]}')`); e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"raw", "mapping", "timezone", "well"} {
		t.Run(kind, func(t *testing.T) {
			spec := savedSpec()
			raw := savedRaw
			well := int64(1)
			switch kind {
			case "raw":
				raw += "\n"
			case "mapping":
				spec.Mappings[0].Field = "changed"
			case "timezone":
				spec.Timezone = "Etc/UTC"
			case "well":
				well = 2
			}
			_, e := s.PrepareFileInput(ctx, well, v.RequestKey, spec, strings.NewReader(raw))
			var de *domain.Error
			if !errors.As(e, &de) || de.Kind != domain.Duplicate {
				t.Fatal("request namespace was not committed", kind, e)
			}
		})
	}
	account, e := postgres.New(pool).Account(ctx, 2)
	if e != nil {
		t.Fatal(e)
	}
	sibling := domain.WithPrincipal(ctx, domain.AccountPrincipal(account))
	own := prepareSaved(t, s, sibling, v.RequestKey)
	if own.ID == v.ID {
		t.Fatal("sibling key shared snapshot")
	}
	if _, e = s.AcceptFileInput(sibling, own); e != nil {
		t.Fatal(e)
	}
	next := prepareSaved(t, s, ctx, "q0-key-scope-0002")
	if _, e = s.AcceptFileInput(ctx, next); e != nil {
		t.Fatal(e)
	}
	history, e := s.FileInputSummaries(ctx, 1, "")
	if e != nil || len(history) != 2 {
		t.Fatal("history leaked sibling", len(history), e)
	}
	older, e := s.FileInputSummaries(ctx, 1, history[0].ID)
	if e != nil || len(older) != 1 || older[0].ID != history[1].ID {
		t.Fatal("keyset history failed", older, e)
	}
}

func TestFileInputMappedTargetCap(t *testing.T) {
	pool, ctx, _, scorer := savedSetup(t)
	l := domain.DefaultFileInputLimits()
	l.Mappings = len(savedSpec().Mappings)
	s := savedService(t, pool, scorer, savedPin(), l)
	if _, e := s.PrepareFileInput(ctx, 1, "q0-mapping-cap001", savedSpec(), strings.NewReader(savedRaw)); e != nil {
		t.Fatal("exact mapped targets cap failed", e)
	}
	l.Mappings--
	s = savedService(t, pool, scorer, savedPin(), l)
	if _, e := s.PrepareFileInput(ctx, 1, "q0-mapping-cap002", savedSpec(), strings.NewReader(savedRaw)); e == nil {
		t.Fatal("mapped targets +1 accepted")
	}
}

// Fixture metadata is explicit test injection: test binaries omit dependency BuildInfo.
func savedFixtureBuildInfo(t testing.TB) *debug.BuildInfo {
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

type savedParserReader struct {
	fileinput.Reader
	sourceSHA string
	calls     *atomic.Int64
}

func (r savedParserReader) ParserSources() []domain.FileParserPart {
	parts := r.Reader.ParserSources()
	if r.sourceSHA != "" {
		parts[0].SHA256 = r.sourceSHA
	}
	return parts
}
func (r savedParserReader) Read(ctx context.Context, raw io.Reader, spec domain.FileParseSpec, l domain.FileInputLimits) (domain.FileInputRows, error) {
	if r.calls != nil {
		r.calls.Add(1)
	}
	return r.Reader.Read(ctx, raw, spec, l)
}
func TestFileInputParserManifestRaceReplayAndLegacyV1(t *testing.T) {
	pool, ctx, _, scorer := savedSetup(t)
	store := postgres.New(pool)
	create := func(reader savedParserReader) *service.FileChecks {
		s, e := service.NewFileChecksWithBuildInfo(store, store, savedScope{store}, reader, scorer, savedSchema(t), savedPin(), domain.DefaultFileInputLimits(), savedFixtureBuildInfo(t))
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	a := create(savedParserReader{})
	b := create(savedParserReader{sourceSHA: domain.FileSHA256([]byte("different trusted parser bytes"))})
	first := prepareSaved(t, a, ctx, "q0-parser-race-001")
	second := prepareSaved(t, b, ctx, "q0-parser-race-001")
	if !bytes.Equal(first.Canonical, second.Canonical) || first.RequestCommitmentSHA256 != second.RequestCommitmentSHA256 || first.ParseRecipeSHA256 == second.ParseRecipeSHA256 || first.FingerprintSHA256 == second.FingerprintSHA256 {
		t.Fatal("parser fixture did not isolate implementation identity")
	}
	errs := make(chan error, 2)
	go func() { _, e := a.AcceptFileInput(ctx, first); errs <- e }()
	go func() { _, e := b.AcceptFileInput(ctx, second); errs <- e }()
	success, conflict := 0, 0
	for i := 0; i < 2; i++ {
		e := <-errs
		if e == nil {
			success++
			continue
		}
		var de *domain.Error
		if errors.As(e, &de) && de.Kind == domain.Duplicate {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("manifest race", success, conflict)
	}
	winner, e := store.FileInputRequest(ctx, 1, first.RequestKey)
	if e != nil {
		t.Fatal(e)
	}
	var count int
	if e = pool.QueryRow(ctx, "SELECT retained_count FROM file_input_budget WHERE budget_key='global'").Scan(&count); e != nil || count != 1 {
		t.Fatal("manifest race charged twice", count, e)
	}
	calls := &atomic.Int64{}
	newer := create(savedParserReader{sourceSHA: domain.FileSHA256([]byte("next parser bytes")), calls: calls})
	repeat := prepareSaved(t, newer, ctx, first.RequestKey)
	if repeat.ID != winner.ID || repeat.ParseRecipeSHA256 != winner.ParseRecipeSHA256 || calls.Load() != 0 {
		t.Fatal("existing key re-normalized under newer build")
	}
	var recipeBytes, identityBytes int
	if e = pool.QueryRow(ctx, "SELECT octet_length(parse_recipe::text),octet_length(parse_identity_bytes) FROM file_input WHERE id=$1", winner.ID).Scan(&recipeBytes, &identityBytes); e != nil || recipeBytes > 8192 || identityBytes > 65536 {
		t.Fatal("stored manifest cap", recipeBytes, identityBytes, e)
	}
	t.Logf("PG recipe=%d identity=%d bytes", recipeBytes, identityBytes)
	// A historical v1 fixture is metadata/saved-result readable, never newly accepted or executed.
	legacy := prepareSaved(t, newer, ctx, "q0-parser-legacy1")
	legacy.ID = strings.Repeat("d", 32)
	legacy.ParseRecipe.Version = "file_parser_v1"
	legacy.ParseRecipe.Implementation = nil
	legacy.ParseRecipe.SchemaSHA256 = ""
	legacy.ParseRecipe.ResolvedConfigSHA256 = ""
	recipe, _ := json.Marshal(legacy.ParseRecipe)
	legacy.ParseRecipeSHA256 = domain.FileSHA256(recipe)
	legacy.IdentityJSON, _ = domain.FileInputIdentityJSON(legacy.FileInputSummary)
	legacy.FingerprintSHA256 = domain.FileSHA256(legacy.IdentityJSON)
	if _, e = newer.AcceptFileInput(ctx, legacy); e == nil {
		t.Fatal("new v1 accepted")
	}
	_, e = pool.Exec(ctx, `INSERT INTO file_input(id,well_id,actor_account_id,actor,operation_kind,request_key,source_kind,created_at,captured_at,request_commitment_sha256,fingerprint_sha256,raw_sha256,canonical_sha256,parse_recipe_sha256,schema_sha256,parse_spec,parse_identity_bytes,execution_pin,parse_recipe,raw_length,canonical_length,rows_count,columns_count,ignored_columns)
 SELECT $1,well_id,actor_account_id,actor,operation_kind,$2,source_kind,created_at,captured_at,request_commitment_sha256,$3,raw_sha256,canonical_sha256,$4,schema_sha256,parse_spec,$5,execution_pin,$6,raw_length,canonical_length,rows_count,columns_count,ignored_columns FROM file_input WHERE id=$7`, legacy.ID, legacy.RequestKey, legacy.FingerprintSHA256, legacy.ParseRecipeSHA256, legacy.IdentityJSON, recipe, winner.ID)
	if e != nil {
		t.Fatal(e)
	}
	got, e := newer.FileInputSummary(ctx, legacy.ID)
	if e != nil || got.ParseRecipe.Version != "file_parser_v1" {
		t.Fatal("legacy metadata unavailable", e)
	}
	if _, e = newer.ComputeStoredInput(ctx, legacy.ID); e == nil {
		t.Fatal("legacy v1 unverified execution allowed")
	}
	result, e := newer.ComputeStoredInput(ctx, winner.ID)
	if e != nil {
		t.Fatal(e)
	}
	result.InputID = legacy.ID
	result.FingerprintSHA256 = legacy.FingerprintSHA256
	result.Result.Provenance.InputSnapshotID = &legacy.ID
	if _, e = newer.CommitStoredResult(ctx, result); e == nil {
		t.Fatal("new v1 result commit allowed")
	}
	payload, _ := json.Marshal(result.Result)
	pin, _ := json.Marshal(result.ExecutionPin)
	if _, e = pool.Exec(ctx, "INSERT INTO file_check_result(input_id,fingerprint_sha256,execution_pin,computed_at,result) VALUES($1,$2,$3,$4,$5)", legacy.ID, legacy.FingerprintSHA256, pin, result.ComputedAt, payload); e != nil {
		t.Fatal(e)
	}
	before := scorer.calls.Load()
	old, e := newer.ComputeStoredInput(ctx, legacy.ID)
	if e != nil || old.Result.Verdict != "norm" || scorer.calls.Load() != before {
		t.Fatal("legacy saved result read recomputed", e)
	}
}
