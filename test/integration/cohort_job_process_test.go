package integration

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/jobqueue"
	"github.com/effitech-pulse/pulse-backend/internal/bootstrap"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// q2Process owns only its child process group. The coordinating test session
// owns ML/PostgreSQL/Rabbit; cleanup never searches or kills by process name.
type q2Process struct {
	command *exec.Cmd
	done    chan error
}

func q2StartProcess(t *testing.T, root, binary, label string, environment []string) *q2Process {
	t.Helper()
	log, err := os.OpenFile(filepath.Join(root, label+".log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary)
	// Explicit application values only: no inherited host credentials or .env.
	command.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + root}, environment...)
	command.Dir = root
	command.Stdout, command.Stderr = log, log
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err = command.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	p := &q2Process{command: command, done: make(chan error, 1)}
	go func() { p.done <- command.Wait(); _ = log.Close() }()
	t.Cleanup(func() { p.stop(t, false) })
	raw, _ := json.Marshal(struct {
		PID  int `json:"pid"`
		PGID int `json:"pgid"`
	}{command.Process.Pid, command.Process.Pid})
	if err = os.WriteFile(filepath.Join(root, label+"-process.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *q2Process) stop(t *testing.T, crash bool) {
	t.Helper()
	if p.command == nil {
		return
	}
	select {
	case <-p.done:
		p.command = nil
		return
	default:
	}
	signal := syscall.SIGTERM
	if crash {
		signal = syscall.SIGKILL
	}
	_ = syscall.Kill(-p.command.Process.Pid, signal)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-p.command.Process.Pid, syscall.SIGKILL)
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			t.Error("owned process did not exit")
		}
	}
	p.command = nil
}

type q2HTTP struct {
	client *http.Client
	base   string
}

func q2NewHTTP(base string) q2HTTP {
	jar, _ := cookiejar.New(nil)
	return q2HTTP{client: &http.Client{Jar: jar, Timeout: 15 * time.Second}, base: base}
}

func (h q2HTTP) request(t *testing.T, method, path, key, body string, want int) []byte {
	t.Helper()
	request, err := http.NewRequest(method, h.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatal("actual HTTP request failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 22<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf("%s %s status %d expected %d", method, path, response.StatusCode, want)
	}
	if strings.Contains(path, "/cohort-jobs") && response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("job response must not be cached")
	}
	return raw
}

func (h q2HTTP) ready(t *testing.T) {
	t.Helper()
	if err := h.waitReady(); err != nil {
		t.Fatal(err)
	}
}

func (h q2HTTP) waitReady() error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		response, err := h.client.Get(strings.TrimSuffix(h.base, "/api/v1") + "/health")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("actual API readiness failed")
}

// TestCohortJobActualHTTPWorkerRestart catches lost durable202 intent,
// accepted-checkpoint recomputation and duplicate publication after process loss.
// Its absence from ordinary CI is a SKIP, never evidence of actual inference.
func TestCohortJobActualHTTPWorkerRestart(t *testing.T) {
	if os.Getenv("Q2_PROCESS_PROOF") != "1" {
		t.Skip("explicit isolated Q2 actual-process runtime required")
	}
	q2ActualProcessProof(t, "restart")
}

// The suspended client still has the real10s RPC deadline. This proves no old
// process effect after physical session loss; it does not manufacture a late
// successful RPC or replace the dedicated-connection transaction fence tests.
func TestCohortJobActualPhysicalStale(t *testing.T) {
	if os.Getenv("Q2_PROCESS_PROOF") != "1" || os.Getenv("Q2_ML_CONTROL_DIR") == "" {
		t.Skip("explicit isolated Q2 ML barrier/process runtime required")
	}
	q2ActualProcessProof(t, "stale")
}

func TestCohortJobActualMaxCatalogueReject(t *testing.T) {
	if os.Getenv("Q2_PROCESS_PROOF") != "1" || os.Getenv("Q2_MAX_INPUT") != "1" {
		t.Skip("explicit isolated Q2 max aggregate profile required")
	}
	q2ActualProcessProof(t, "oversize")
}

// This is a pure resource-fixture preflight, never actual process evidence.
func TestCohortProcessResourceFixtures(t *testing.T) {
	path := os.Getenv("Q2_PIN_PATH")
	if path == "" {
		t.Skip("explicit synthetic Q2 pin required for pure fixture validation")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var pin domain.ExecutionPin
	if json.Unmarshal(raw, &pin) != nil || pin.Validate() != nil {
		t.Fatal("invalid synthetic pin")
	}
	for _, oversize := range []bool{false, true} {
		canonical := q2MaxCanonical(oversize)
		fixture := domain.CohortFixtureManifest{Version: "cohort_fixture_v1", FixtureID: "resource-preflight", CohortVersion: "resource-preflight", CohortManifestSHA256: strings.Repeat("a", 64), AsOf: time.Date(2026, 2, 1, 0, 0, 1, 0, time.UTC), ExecutionPin: pin, CaptureRecipe: domain.CohortCaptureRecipe{Version: "cohort_fixture_capture_v1", Normalization: "none_supplied_canonical", BackendBuildSHA256: "sha256:" + strings.Repeat("1", 64)}}
		for id := int64(1); id <= 100; id++ {
			fixture.Members = append(fixture.Members, domain.CohortFixtureMember{WellID: id, CanonicalSHA256: domain.FileSHA256(canonical), CanonicalBytes: int64(len(canonical))})
		}
		err = q2ValidateFixture(fixture, canonical)
		if oversize {
			var capacity *domain.Error
			if !errors.As(err, &capacity) || capacity.Message != "cohort_input_capacity" {
				t.Fatal("over-cap fixture not rejected for aggregate capacity")
			}
		} else if err != nil || len(canonical)*100 > 24<<20 {
			t.Fatal("bounded max fixture is invalid")
		}
		t.Logf("pure preflight: oversize=%t bytes/member=%d aggregate=%d rows=2000 channels=7", oversize, len(canonical), len(canonical)*100)
	}
}

// An unrelated configuration failure may stop both API launches. That is a
// failed positive control, never evidence that the aggregate capacity worked.
func TestCohortCapacityGateRejectsFailedPositiveControl(t *testing.T) {
	unrelatedConfig := errors.New("unrelated invalid broker configuration")
	if err := q2CheckCapacityRejection(unrelatedConfig, errors.New("process exit1")); err == nil {
		t.Fatal("unrelated positive-control bootstrap failure counted as capacity rejection")
	}
	if err := q2CheckCapacityRejection(nil, nil); err == nil {
		t.Fatal("successful oversized startup counted as capacity rejection")
	}
}

func q2CheckCapacityRejection(validControlErr, oversizeExitErr error) error {
	if validControlErr != nil {
		return errors.New("valid synthetic catalogue control did not become ready")
	}
	if oversizeExitErr == nil {
		return errors.New("oversized catalogue process exited successfully")
	}
	return nil
}

type q2Status struct {
	ID             string `json:"id"`
	RunID          int64  `json:"run_id,string"`
	State          string `json:"state"`
	Revision       int64  `json:"revision"`
	CompletedCount int    `json:"completed_count"`
	ResultURL      string `json:"result_url"`
}

func q2ActualProcessProof(t *testing.T, scenario string) {
	t.Helper()
	for _, key := range []string{"TEST_DATABASE_URL", "Q2_API_BINARY", "Q2_WORKER_BINARY", "Q2_PIN_PATH", "Q2_CANONICAL_PATH", "Q2_ML_TARGET", "Q2_ML_API_KEY", "Q2_ML_COMPUTES_PATH", "Q2_PRODUCER_AMQP_URL", "Q2_WORKER_AMQP_URL", "Q2_SANDBOX_ROOT", "Q2_HTTP_ADDR"} {
		if os.Getenv(key) == "" {
			t.Fatalf("explicit %s required", key)
		}
	}
	root, err := os.MkdirTemp(os.Getenv("Q2_SANDBOX_ROOT"), "process-")
	if err != nil {
		t.Fatal(err)
	}
	pool := testDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	const version = "q2-process-test"
	cohortSHA := strings.Repeat("a", 64)
	if _, err = pool.Exec(ctx, `INSERT INTO well(ngdu,department,active) SELECT 0,0,true FROM generate_series(1,100)`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO app_cohort(version,manifest_sha256) VALUES($1,$2)`, version, cohortSHA); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO app_cohort_member(version,well_id,revision) SELECT $1,id,revision FROM well`, version); err != nil {
		t.Fatal(err)
	}
	const password = "q2 actual synthetic password 2026"
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO auth_user(login,name,password_hash,role,scope) VALUES('q2owner','Q2 synthetic',$1,'admin','{"funds":["operational"]}')`, string(passwordHash)); err != nil {
		t.Fatal(err)
	}
	read := func(path string) []byte {
		t.Helper()
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	computeCount := func() int {
		t.Helper()
		count := 0
		for _, line := range bytes.Split(read(os.Getenv("Q2_ML_COMPUTES_PATH")), []byte("\n")) {
			var event struct {
				Event string `json:"event"`
			}
			if len(line) > 0 && json.Unmarshal(line, &event) == nil && event.Event == "fake_compute_started" {
				count++
			}
		}
		return count
	}
	beforeComputes := computeCount()
	write := func(name string, value any) string {
		t.Helper()
		raw, e := json.Marshal(value)
		if e != nil {
			t.Fatal(e)
		}
		path := filepath.Join(root, name)
		if e = os.WriteFile(path, raw, 0600); e != nil {
			t.Fatal(e)
		}
		return path
	}
	var pin domain.ExecutionPin
	if json.Unmarshal(read(os.Getenv("Q2_PIN_PATH")), &pin) != nil || pin.Validate() != nil {
		t.Fatal("invalid actual synthetic pin")
	}
	canonical := read(os.Getenv("Q2_CANONICAL_PATH"))
	if os.Getenv("Q2_MAX_INPUT") == "1" {
		canonical = q2MaxCanonical(scenario == "oversize")
	}
	if err = os.WriteFile(filepath.Join(root, "canonical.csv"), canonical, 0600); err != nil {
		t.Fatal(err)
	}
	apiBinary := os.Getenv("Q2_API_BINARY")
	workerBinary := os.Getenv("Q2_WORKER_BINARY")
	fixture := domain.CohortFixtureManifest{Version: "cohort_fixture_v1", FixtureID: "q2-all100", CohortVersion: version, CohortManifestSHA256: cohortSHA, AsOf: time.Date(2026, 2, 1, 0, 0, 1, 0, time.UTC), ExecutionPin: pin, CaptureRecipe: domain.CohortCaptureRecipe{Version: "cohort_fixture_capture_v1", Normalization: "none_supplied_canonical", BackendBuildSHA256: domain.FileSHA256(read(apiBinary))}}
	inputs := make([]bootstrap.SyntheticCohortInput, 0, 100)
	for id := int64(1); id <= 100; id++ {
		fixture.Members = append(fixture.Members, domain.CohortFixtureMember{WellID: id, CanonicalSHA256: domain.FileSHA256(canonical), CanonicalBytes: int64(len(canonical))})
		inputs = append(inputs, bootstrap.SyntheticCohortInput{WellID: id, Path: "canonical.csv"})
	}
	fixtureSHA, err := domain.CohortFixtureSHA256(fixture)
	if err != nil {
		t.Fatal(err)
	}
	validationErr := q2ValidateFixture(fixture, canonical)
	if scenario == "oversize" {
		var capacity *domain.Error
		if !errors.As(validationErr, &capacity) || capacity.Message != "cohort_input_capacity" {
			t.Fatal("oversize source is not valid CSV with aggregate-only capacity rejection")
		}
	} else if validationErr != nil {
		t.Fatal("process canonical profile does not satisfy frozen validation")
	}
	// Valid zeros describe a stopped pump, never missing telemetry. Go's CSV
	// writer preserves the frozen C1 header/order; identity uses shared Go hashes.
	records, err := csv.NewReader(bytes.NewReader(canonical)).ReadAll()
	if err != nil || len(records) < 2 {
		t.Fatal("canonical fixture CSV unavailable")
	}
	frequency, current := -1, -1
	for i, name := range records[0] {
		if name == "F_out" {
			frequency = i
		}
		if name == "Ia" {
			current = i
		}
	}
	if frequency < 0 || current < 0 {
		t.Fatal("canonical fixture missing stopped-pump channels")
	}
	for _, row := range records[1:] {
		row[frequency], row[current] = "0", "0"
	}
	var stopped bytes.Buffer
	csvWriter := csv.NewWriter(&stopped)
	csvWriter.WriteAll(records)
	if csvWriter.Error() != nil {
		t.Fatal(csvWriter.Error())
	}
	if err = os.WriteFile(filepath.Join(root, "canonical-stopped.csv"), stopped.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	mixed := fixture
	mixed.FixtureID = "q2-mixed100"
	mixed.Members = append([]domain.CohortFixtureMember(nil), fixture.Members...)
	mixedInputs := append([]bootstrap.SyntheticCohortInput(nil), inputs[:98]...)
	mixed.Members[95].CanonicalSHA256, mixed.Members[95].CanonicalBytes = domain.FileSHA256(stopped.Bytes()), int64(stopped.Len())
	mixedInputs[95].Path = "canonical-stopped.csv"
	for i, reason := range []string{"insufficient_telemetry", "synthetic_source_unavailable"} {
		value := reason
		mixed.Members[98+i] = domain.CohortFixtureMember{WellID: int64(99 + i), NoComputeReason: &value}
	}
	mixedSHA, err := domain.CohortFixtureSHA256(mixed)
	if err != nil {
		t.Fatal(err)
	}
	schema := pool.Config().ConnConfig.RuntimeParams["search_path"]
	manifest := bootstrap.SyntheticCohortManifest{RunID: "q2-actual-process", TestSchema: schema, AllowedAccountIDs: []int64{1}, CohortVersion: version, CohortManifestSHA256: cohortSHA, Fixtures: []bootstrap.SyntheticCohortFixture{{Manifest: fixture, Inputs: inputs}, {Manifest: mixed, Inputs: mixedInputs}}}
	if scenario == "oversize" {
		manifest.Fixtures = manifest.Fixtures[:1]
	}
	manifestPath := write("manifest.json", manifest)
	manifestSHA := domain.FileSHA256(read(manifestPath))
	// This is the dedicated Q2 guard, independent of Q1 file-check tables.
	if _, err = pool.Exec(ctx, `INSERT INTO cohort_job_guard(id,run_id,manifest_sha256,synthetic_only) VALUES(true,$1,$2,true)`, manifest.RunID, manifestSHA); err != nil {
		t.Fatal(err)
	}
	dbURL, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("invalid isolated database URL")
	}
	query := dbURL.Query()
	query.Set("search_path", schema)
	dbURL.RawQuery = query.Encode()
	environment := []string{"DATABASE_URL=" + dbURL.String(), "AUTH_MODE=local", "AUTH_COOKIE_SECURE=false", "APP_SCOPE=cohort", "COHORT_VERSION=" + version, "ML_AUTORUN_ENABLED=false", "FILE_CHECK_MODE=off", "COHORT_JOB_MODE=synthetic", "COHORT_JOB_ALLOW_REAL=false", "COHORT_JOB_MANIFEST=" + manifestPath, "COHORT_JOB_MANIFEST_SHA256=" + manifestSHA, "COHORT_JOB_RUN_ID=" + manifest.RunID, "COHORT_JOB_TEST_SCHEMA=" + schema, "ML_SERVICE_TARGET=127.0.0.1:59998", "ML_SERVICE_API_KEY=" + os.Getenv("Q2_ML_API_KEY"), "COHORT_JOB_ML_TARGET=" + os.Getenv("Q2_ML_TARGET"), "COHORT_JOB_AMQP_URL=" + os.Getenv("Q2_WORKER_AMQP_URL"), "COHORT_JOB_PRODUCER_AMQP_URL=" + os.Getenv("Q2_PRODUCER_AMQP_URL"), "HTTP_ADDR=" + os.Getenv("Q2_HTTP_ADDR"), "GIN_MODE=release"}
	// API producer points at a deliberately absent loopback listener. Acceptance
	// must commit without Rabbit. The later worker gets the dedicated live broker.
	apiEnvironment := append([]string(nil), environment...)
	for i, v := range apiEnvironment {
		if strings.HasPrefix(v, "COHORT_JOB_PRODUCER_AMQP_URL=") {
			apiEnvironment[i] = "COHORT_JOB_PRODUCER_AMQP_URL=amqp://synthetic:synthetic@127.0.0.1:59997/pulse-q2-synthetic"
		}
	}
	httpAPI := q2NewHTTP("http://" + os.Getenv("Q2_HTTP_ADDR") + "/api/v1")
	offEnvironment := append([]string(nil), apiEnvironment...)
	for i, value := range offEnvironment {
		if strings.HasPrefix(value, "COHORT_JOB_MODE=") {
			offEnvironment[i] = "COHORT_JOB_MODE=off"
		}
	}
	offAPI := q2StartProcess(t, root, apiBinary, "api-default-off", offEnvironment)
	httpAPI.ready(t)
	httpAPI.request(t, "POST", "/auth/login", "", `{"login":"q2owner","password":"`+password+`"}`, 200)
	httpAPI.request(t, "POST", "/ml/cohort-jobs", "q2-defaultoff-request", `{"fixture_id":"q2-all100"}`, 503)
	offAPI.stop(t, false)
	if scenario == "oversize" {
		if len(canonical)*100 <= 24<<20 {
			t.Fatal("max+1 fixture did not exceed aggregate cap")
		}
		// Same binary, schema, accounts, pin, fixture ID, paths and broker/ML
		// configuration must first accept a valid size-derived catalogue. Only
		// source bytes and their trusted descriptor/guard identities then change.
		controlCanonical := q2MaxCanonical(false)
		controlFixture := fixture
		controlFixture.Members = append([]domain.CohortFixtureMember(nil), fixture.Members...)
		for i := range controlFixture.Members {
			controlFixture.Members[i].CanonicalSHA256 = domain.FileSHA256(controlCanonical)
			controlFixture.Members[i].CanonicalBytes = int64(len(controlCanonical))
		}
		if err = q2ValidateFixture(controlFixture, controlCanonical); err != nil {
			t.Fatal("valid positive-control catalogue failed pure validation")
		}
		controlManifest := manifest
		controlManifest.Fixtures = []bootstrap.SyntheticCohortFixture{{Manifest: controlFixture, Inputs: inputs}}
		controlPath := write("manifest-valid-control.json", controlManifest)
		controlSHA := domain.FileSHA256(read(controlPath))
		controlEnvironment := append([]string(nil), apiEnvironment...)
		for i, value := range controlEnvironment {
			if strings.HasPrefix(value, "COHORT_JOB_MANIFEST=") {
				controlEnvironment[i] = "COHORT_JOB_MANIFEST=" + controlPath
			}
			if strings.HasPrefix(value, "COHORT_JOB_MANIFEST_SHA256=") {
				controlEnvironment[i] = "COHORT_JOB_MANIFEST_SHA256=" + controlSHA
			}
		}
		if err = os.WriteFile(filepath.Join(root, "canonical.csv"), controlCanonical, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE cohort_job_guard SET manifest_sha256=$1 WHERE id=true AND run_id=$2`, controlSHA, manifest.RunID); err != nil {
			t.Fatal(err)
		}
		controlAPI := q2StartProcess(t, root, apiBinary, "api-cap-valid-control", controlEnvironment)
		controlErr := httpAPI.waitReady()
		controlAPI.stop(t, false)
		if controlErr != nil {
			t.Fatal("valid synthetic catalogue control failed; capacity gate cannot pass")
		}
		if err = os.WriteFile(filepath.Join(root, "canonical.csv"), canonical, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, `UPDATE cohort_job_guard SET manifest_sha256=$1 WHERE id=true AND run_id=$2`, manifestSHA, manifest.RunID); err != nil {
			t.Fatal(err)
		}
		rejected := q2StartProcess(t, root, apiBinary, "api-cap-reject", apiEnvironment)
		select {
		case exitErr := <-rejected.done:
			if err = q2CheckCapacityRejection(controlErr, exitErr); err != nil {
				t.Fatal(err)
			}
			rejected.command = nil
		case <-time.After(20 * time.Second):
			t.Fatal("oversized catalogue was not rejected at startup")
		}
		var jobs int
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM cohort_job`).Scan(&jobs); err != nil || jobs != 0 || computeCount() != beforeComputes {
			t.Fatal("oversized catalogue admitted work or computed")
		}
		write("proof-cap-reject.json", struct {
			CanonicalBytes             int    `json:"canonical_bytes"`
			AggregateBytes             int    `json:"aggregate_bytes"`
			CapBytes                   int    `json:"cap_bytes"`
			Jobs                       int    `json:"jobs"`
			ActualComputes             int    `json:"actual_computes"`
			Boundary                   string `json:"boundary"`
			ValidControlReady          bool   `json:"valid_control_ready"`
			ValidControlManifestSHA256 string `json:"valid_control_manifest_sha256"`
		}{len(canonical), len(canonical) * 100, 24 << 20, jobs, computeCount() - beforeComputes, "API catalogue startup before admission", true, controlSHA})
		t.Log("actual max aggregate catalogue startup rejection:0jobs/0compute")
		return
	}
	api := q2StartProcess(t, root, apiBinary, "api-before", apiEnvironment)
	httpAPI.ready(t)
	httpAPI.request(t, "POST", "/auth/login", "", `{"login":"q2owner","password":"`+password+`"}`, 200)
	var accepted q2Status
	body := `{"fixture_id":"q2-all100"}`
	if json.Unmarshal(httpAPI.request(t, "POST", "/ml/cohort-jobs", "q2-actual-request-01", body, 202), &accepted) != nil || accepted.ID == "" || accepted.RunID <= 0 {
		t.Fatal("durable admission identity absent")
	}
	var checkpoints int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM cohort_job_checkpoint WHERE job_id=$1`, accepted.ID).Scan(&checkpoints); err != nil || checkpoints != 0 {
		t.Fatal("admission computed before worker")
	}
	var frozenMembers, frozenInputs int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM cohort_job_member WHERE job_id=$1),(SELECT count(*) FROM cohort_job_input WHERE job_id=$1)`, accepted.ID).Scan(&frozenMembers, &frozenInputs); err != nil || frozenMembers != 100 || frozenInputs != 100 || computeCount() != beforeComputes {
		t.Fatal("202 did not durably freeze all100 before actual compute")
	}
	api.stop(t, false)
	api = q2StartProcess(t, root, apiBinary, "api-after", apiEnvironment)
	httpAPI.ready(t)
	var duplicate q2Status
	if json.Unmarshal(httpAPI.request(t, "POST", "/ml/cohort-jobs", "q2-actual-request-01", body, 202), &duplicate) != nil || duplicate.ID != accepted.ID {
		t.Fatal("lost202 replay changed durable job")
	}
	// Both immutable in-memory catalogues are loaded. Only original owned source
	// files disappear; the descriptor/policy and installed ML pin stay unchanged.
	if err = os.Remove(filepath.Join(root, "canonical.csv")); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(root, "canonical-stopped.csv")); err != nil {
		t.Fatal(err)
	}
	if scenario == "stale" {
		control := os.Getenv("Q2_ML_CONTROL_DIR")
		if err = os.WriteFile(filepath.Join(control, "arm"), []byte("one synthetic compute\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	worker := q2StartProcess(t, root, workerBinary, "worker-before", environment)
	deadline := time.Now().Add(90 * time.Second)
	var durable []int64
	var contender *q2Process
	if scenario == "stale" {
		worker = q2PhysicalStale(t, pool, ctx, root, workerBinary, environment, worker, accepted.ID)
	} else {
		for time.Now().Before(deadline) {
			if err = pool.QueryRow(ctx, `SELECT count(*) FROM cohort_job_checkpoint WHERE job_id=$1`, accepted.ID).Scan(&checkpoints); err != nil {
				t.Fatal(err)
			}
			if checkpoints >= 5 && checkpoints < 100 {
				break
			}
			if checkpoints == 100 {
				t.Fatal("actual fixture finished before required crash; increase test-only compute delay")
			}
			time.Sleep(25 * time.Millisecond)
		}
		if checkpoints < 5 {
			t.Fatal("no actual accepted checkpoints before crash")
		}
		worker.stop(t, true)
		// Capture the complete durable set after the process has exited; a final
		// checkpoint may have committed between observation and SIGKILL.
		rows, err := pool.Query(ctx, `SELECT well_id FROM cohort_job_checkpoint WHERE job_id=$1 ORDER BY well_id`, accepted.ID)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			durable = append(durable, id)
		}
		rows.Close()
		if rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		worker = q2StartProcess(t, root, workerBinary, "worker-after", environment)
		contender = q2StartProcess(t, root, workerBinary, "worker-duplicate", environment)
		message := domain.CohortJobMessage{Version: 1, Kind: "cohort.ready.v1", JobID: accepted.ID}
		if err = pool.QueryRow(ctx, `SELECT event_id,generation FROM cohort_job_outbox WHERE job_id=$1 ORDER BY generation DESC LIMIT 1`, accepted.ID).Scan(&message.EventID, &message.Generation); err != nil {
			t.Fatal(err)
		}
		publisher := jobqueue.NewCohortPublisher(os.Getenv("Q2_PRODUCER_AMQP_URL"))
		for i := 0; i < 2; i++ {
			outcome, e := publisher.Publish(ctx, message)
			if e != nil || outcome != domain.CohortPublishOutcome("confirmed") {
				publisher.Close()
				t.Fatal("bounded duplicate delivery publish failed")
			}
		}
		publisher.Close()
	}
	deadline = time.Now().Add(150 * time.Second)
	var final q2Status
	for time.Now().Before(deadline) {
		if json.Unmarshal(httpAPI.request(t, "GET", "/ml/cohort-jobs/"+accepted.ID, "", "", 200), &final) != nil {
			t.Fatal("invalid status")
		}
		if final.State == "succeeded" {
			break
		}
		if final.State == "failed" || final.State == "expired" || final.State == "revoked" {
			t.Fatalf("unexpected terminal state %s", final.State)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if final.State != "succeeded" || final.CompletedCount != 100 {
		t.Fatalf("restart did not complete100 state=%s completed=%d", final.State, final.CompletedCount)
	}
	worker.stop(t, false)
	if contender != nil {
		contender.stop(t, false)
	}
	actualComputes := computeCount() - beforeComputes
	var peak int
	for _, line := range bytes.Split(read(os.Getenv("Q2_ML_COMPUTES_PATH")), []byte("\n")) {
		var event struct {
			Peak int `json:"peak"`
		}
		if json.Unmarshal(line, &event) == nil && event.Peak > peak {
			peak = event.Peak
		}
	}
	if peak != 1 {
		t.Fatalf("actual ML peak compute=%d expected1", peak)
	}
	if actualComputes < 100 || actualComputes > 102 {
		t.Fatalf("unexpected actual fake compute count %d; accepted members need100, uncommitted retry budget allows102", actualComputes)
	}
	var claimAttempts, computeStarts int
	if err = pool.QueryRow(ctx, `SELECT attempt_count,compute_starts FROM cohort_job WHERE id=$1`, accepted.ID).Scan(&claimAttempts, &computeStarts); err != nil || claimAttempts > 3 || computeStarts > 102 {
		t.Fatal("actual recovery exceeded attempt/compute budgets")
	}
	if scenario == "stale" && (claimAttempts != 3 || computeStarts != 102) {
		t.Fatalf("physical stale expected3claims/102starts got%d/%d", claimAttempts, computeStarts)
	}
	var snapshots, runs, alerts, recomputed int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM ml_run_snapshot WHERE run_id=$1),(SELECT count(*) FROM ml_run WHERE id=$1),(SELECT count(*) FROM ml_alert WHERE run_id=$1)`, accepted.RunID).Scan(&snapshots, &runs, &alerts); err != nil || snapshots != 1 || runs != 1 || alerts != 0 {
		t.Fatalf("atomic norm publication counts snapshot=%d run=%d alert=%d query_ok=%t", snapshots, runs, alerts, err == nil)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM (SELECT well_id FROM cohort_job_member_attempt WHERE job_id=$1 AND well_id=ANY($2) GROUP BY well_id HAVING count(*)>1) repeated`, accepted.ID, durable).Scan(&recomputed); err != nil || recomputed != 0 {
		t.Fatal("accepted checkpoint was recomputed or query failed")
	}
	var snapshot domain.CohortSnapshot
	path := fmt.Sprintf("/ml/runs/%d/snapshot", accepted.RunID)
	if json.Unmarshal(httpAPI.request(t, "GET", path, "", "", 200), &snapshot) != nil || snapshot.Count != 100 || snapshot.Coverage.ScoredCount != 100 || snapshot.Execution == nil || snapshot.Execution.SourceKind != "cohort_snapshot" {
		t.Fatal("persisted actual100 snapshot identity/coverage mismatch")
	}
	for _, outcome := range snapshot.Outcomes {
		if outcome.Result == nil || outcome.Provenance == nil || outcome.Provenance.InputKind != "cohort_snapshot" || outcome.InputSHA256 != strings.TrimPrefix(domain.FileSHA256(canonical), "sha256:") || (outcome.Provenance.InputSHA256 == nil || *outcome.Provenance.InputSHA256 != domain.FileSHA256(canonical)) {
			t.Fatal("actual outcome lost cohort provenance")
		}
	}
	httpAPI.request(t, "POST", "/ml/cohort-jobs/"+accepted.ID+"/cancel", "", fmt.Sprintf(`{"revision":%d}`, final.Revision), 409)
	write("proof.json", struct {
		JobID                string `json:"job_id"`
		RunID                int64  `json:"run_id"`
		FixtureSHA256        string `json:"fixture_sha256"`
		AcceptedBeforeCrash  int    `json:"accepted_before_crash"`
		RecomputedAccepted   int    `json:"recomputed_accepted"`
		SnapshotCount        int    `json:"snapshot_count"`
		Scored               int    `json:"scored"`
		ActualFakeComputes   int    `json:"actual_fake_computes"`
		ScientificAcceptance string `json:"scientific_acceptance"`
	}{accepted.ID, accepted.RunID, fixtureSHA, len(durable), recomputed, snapshots, 100, actualComputes, "not_assessed"})
	// Lifecycle changes happen only after the first job's publication. The second
	// job captures current metadata while preserving immutable cohort membership.
	if _, err = pool.Exec(ctx, `UPDATE well SET archived_at=clock_timestamp(),revision=revision+1 WHERE id=97;UPDATE well SET active=false,revision=revision+1 WHERE id=98`); err != nil {
		t.Fatal(err)
	}
	var mixedJob q2Status
	if json.Unmarshal(httpAPI.request(t, "POST", "/ml/cohort-jobs", "q2-actual-request-02", `{"fixture_id":"q2-mixed100"}`, 202), &mixedJob) != nil || mixedJob.ID == "" || mixedJob.ID == accepted.ID {
		t.Fatal("mixed admission identity absent")
	}
	assertOldPublished := func() {
		t.Helper()
		var old domain.CohortSnapshot
		if json.Unmarshal(httpAPI.request(t, "GET", "/ml/wells", "", "", 200), &old) != nil || old.RunID == nil || *old.RunID != accepted.RunID || old.Coverage.ScoredCount != 100 {
			t.Fatal("pending job replaced previous published snapshot")
		}
	}
	assertOldPublished()
	mixedBeforeComputes := computeCount()
	worker = q2StartProcess(t, root, workerBinary, "worker-mixed", environment)
	deadline = time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if json.Unmarshal(httpAPI.request(t, "GET", "/ml/cohort-jobs/"+mixedJob.ID, "", "", 200), &final) != nil {
			t.Fatal("invalid mixed status")
		}
		if final.State == "succeeded" {
			break
		}
		if final.State == "failed" || final.State == "expired" || final.State == "revoked" {
			t.Fatalf("unexpected mixed terminal state %s", final.State)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if final.State != "succeeded" || final.CompletedCount != 100 {
		t.Fatal("mixed100 job did not publish complete100")
	}
	worker.stop(t, false)
	var mixedSnapshot domain.CohortSnapshot
	if json.Unmarshal(httpAPI.request(t, "GET", fmt.Sprintf("/ml/runs/%d/snapshot", mixedJob.RunID), "", "", 200), &mixedSnapshot) != nil {
		t.Fatal("invalid mixed snapshot")
	}
	wantCoverage := domain.CohortCoverage{MemberCount: 100, VisibleCount: 100, ScoredCount: 95, ArchivedCount: 1, InactiveCount: 1, InsufficientCount: 2, ErrorCount: 1}
	if mixedSnapshot.Count != 100 || mixedSnapshot.Coverage != wantCoverage || mixedSnapshot.Execution == nil {
		t.Fatalf("mixed100 coverage mismatch: %+v", mixedSnapshot.Coverage)
	}
	seen := map[int64]bool{}
	for _, outcome := range mixedSnapshot.Outcomes {
		id := outcome.AppWellID
		if id < 1 || id > 100 || seen[id] {
			t.Fatal("mixed snapshot lost membership identity")
		}
		seen[id] = true
		wantStatus, wantReason := "scored", "evaluated"
		switch id {
		case 96:
			wantStatus, wantReason = "insufficient", "insufficient_running_windows"
		case 97:
			wantStatus, wantReason = "archived", "archived"
		case 98:
			wantStatus, wantReason = "inactive", "inactive"
		case 99:
			wantStatus, wantReason = "insufficient", "insufficient_telemetry"
		case 100:
			wantStatus, wantReason = "error", "synthetic_source_unavailable"
		}
		if outcome.Status != wantStatus || outcome.ReasonCode != wantReason {
			t.Fatalf("mixed well%d expected %s/%s got %s/%s", id, wantStatus, wantReason, outcome.Status, outcome.ReasonCode)
		}
		if id <= 95 {
			if outcome.Result == nil || outcome.Provenance == nil || outcome.Provenance.InputKind != "cohort_snapshot" || (outcome.Provenance.InputSHA256 == nil || *outcome.Provenance.InputSHA256 != domain.FileSHA256(canonical)) {
				t.Fatal("mixed scored outcome missing actual provenance")
			}
		} else if outcome.Result != nil {
			t.Fatal("mixed non-scored outcome fabricated risk")
		}
		if id == 96 && (outcome.Diagnostics == nil || outcome.Provenance == nil || outcome.Provenance.InputKind != "cohort_snapshot" || (outcome.Provenance.InputSHA256 == nil || *outcome.Provenance.InputSHA256 != domain.FileSHA256(stopped.Bytes()))) {
			t.Fatal("ML insufficient lost diagnostics/provenance")
		}
	}
	if len(seen) != 100 || computeCount()-mixedBeforeComputes < 95 {
		t.Fatal("mixed fixture did not perform actual scored computes")
	}
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM ml_run_snapshot WHERE run_id=$1),(SELECT count(*) FROM ml_run WHERE id=$1),(SELECT count(*) FROM ml_alert WHERE run_id=$1)`, mixedJob.RunID).Scan(&snapshots, &runs, &alerts); err != nil || snapshots != 1 || runs != 1 || alerts != 0 {
		t.Fatal("mixed publication not one norm snapshot/run with zero alerts")
	}
	write("proof-mixed.json", struct {
		JobID                 string                `json:"job_id"`
		RunID                 int64                 `json:"run_id"`
		FixtureSHA256         string                `json:"fixture_sha256"`
		Coverage              domain.CohortCoverage `json:"coverage"`
		ActualFakeComputes    int                   `json:"actual_fake_computes"`
		OldPublishedPreserved bool                  `json:"old_published_preserved"`
	}{mixedJob.ID, mixedJob.RunID, mixedSHA, mixedSnapshot.Coverage, computeCount() - mixedBeforeComputes, true})
	type retention struct {
		Jobs              int    `json:"jobs"`
		Inputs            int    `json:"inputs"`
		Checkpoints       int    `json:"checkpoints"`
		Snapshots         int    `json:"snapshots"`
		SnapshotDigests   string `json:"snapshot_digests"`
		CheckpointDigests string `json:"checkpoint_digests"`
	}
	retained := func() retention {
		t.Helper()
		var value retention
		if e := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM cohort_job),(SELECT count(*) FROM cohort_job_input),(SELECT count(*) FROM cohort_job_checkpoint),(SELECT count(*) FROM ml_run_snapshot),(SELECT coalesce(string_agg(output_sha256,',' ORDER BY run_id),'') FROM ml_run_snapshot),(SELECT coalesce(string_agg(digest_sha256,',' ORDER BY job_id,well_id),'') FROM cohort_job_checkpoint)`).Scan(&value.Jobs, &value.Inputs, &value.Checkpoints, &value.Snapshots, &value.SnapshotDigests, &value.CheckpointDigests); e != nil {
			t.Fatal(e)
		}
		return value
	}
	beforeRollback := retained()
	beforeRollbackComputes := computeCount()
	api.stop(t, false)
	api = q2StartProcess(t, root, apiBinary, "api-rollback-off", offEnvironment)
	httpAPI.ready(t)
	httpAPI.request(t, "POST", "/ml/cohort-jobs", "q2-rollback-request1", `{"fixture_id":"q2-all100"}`, 503)
	httpAPI.request(t, "GET", "/ml/cohort-jobs/"+mixedJob.ID, "", "", 503)
	var afterRollback domain.CohortSnapshot
	if json.Unmarshal(httpAPI.request(t, "GET", fmt.Sprintf("/ml/runs/%d/snapshot", mixedJob.RunID), "", "", 200), &afterRollback) != nil || afterRollback.Coverage != wantCoverage {
		t.Fatal("rollback off lost published snapshot")
	}
	httpAPI.request(t, "GET", "/ml/runs", "", "", 200)
	if retained() != beforeRollback || computeCount() != beforeRollbackComputes {
		t.Fatal("rollback off mutated retained intent/result or invoked ML")
	}
	write("proof-rollback.json", struct {
		Retained            retention `json:"retained"`
		AdmissionOff        bool      `json:"admission_off"`
		ConsumersStopped    bool      `json:"consumers_stopped"`
		ActualComputesAdded int       `json:"actual_computes_added"`
	}{beforeRollback, true, true, computeCount() - beforeRollbackComputes})
	t.Logf("actual Q2 all100 restart proof: accepted_before_crash=%d snapshots=%d alerts=%d accepted_recomputed=%d evidence=%s", len(durable), snapshots, alerts, recomputed, root)
	t.Log("actual Q2 mixed100 proof:95scored/1archived/1inactive/2insufficient/1error; pending old published snapshot retained")
}

func q2MaxCanonical(oversize bool) []byte {
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	_ = writer.Write([]string{"timestamp", "CosF", "F_out", "Ia", "Motor_winding_temperature", "Ped_load", "Pump_intake_pressure_kgs", "qg"})
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	zero := "0.00000000000"
	if oversize {
		zero += "0"
	}
	for i := int64(0); i < 2000; i++ {
		stamp := start.Add(time.Duration(i*(31*24*3600)/1999) * time.Second)
		_ = writer.Write([]string{stamp.Format(time.RFC3339), "0.870000000000", "50.00000000000", "22.00000000000", "92.00000000000", "55.00000000000", "45.00000000000", zero})
	}
	writer.Flush()
	return buffer.Bytes()
}

func q2ValidateFixture(fixture domain.CohortFixtureManifest, canonical []byte) error {
	records, err := csv.NewReader(bytes.NewReader(canonical)).ReadAll()
	if err != nil {
		return err
	}
	start, err := time.Parse(time.RFC3339, records[1][0])
	if err != nil {
		return err
	}
	watermark, err := time.Parse(time.RFC3339, records[len(records)-1][0])
	if err != nil {
		return err
	}
	end := watermark.Add(time.Nanosecond)
	raw, err := domain.CanonicalCohortFixture(fixture)
	if err != nil {
		return err
	}
	prepared := domain.PreparedCohortJob{ActorAccountID: 1, RequestKey: "catalogue_validation", Fixture: fixture, FixtureSHA256: domain.FileSHA256(raw), FixtureJSON: raw}
	prepared.RequestCommitmentSHA256, err = domain.CohortRequestCommitment(1, fixture.FixtureID, prepared.FixtureSHA256)
	if err != nil {
		return err
	}
	for i, member := range fixture.Members {
		prepared.Members = append(prepared.Members, domain.CohortFrozenMember{Ordinal: i + 1, Well: domain.Well{ID: member.WellID, Active: true}, Input: &domain.CohortFrozenInput{Identity: domain.PinnedInputIdentity{SourceKind: "cohort_snapshot", CanonicalSHA256: domain.FileSHA256(canonical), CanonicalBytes: int64(len(canonical))}, Canonical: canonical, Bounds: domain.TelemetryBounds{Start: &start, End: &end}, Watermark: &watermark, Resolution: "supplied_canonical", Rows: len(records) - 1, Columns: 7}})
	}
	return domain.ValidatePreparedCohortJob(prepared)
}

func q2PhysicalStale(t *testing.T, pool *pgxpool.Pool, ctx context.Context, root, binary string, environment []string, old *q2Process, jobID string) *q2Process {
	t.Helper()
	control := os.Getenv("Q2_ML_CONTROL_DIR")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(control, "entered.json")); err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(control, "entered.json")); err != nil {
		t.Fatal("actual ML barrier was not entered")
	}
	enteredRaw, err := os.ReadFile(filepath.Join(control, "entered.json"))
	var entered struct {
		Sequence int `json:"sequence"`
	}
	if err != nil || json.Unmarshal(enteredRaw, &entered) != nil || entered.Sequence < 1 {
		t.Fatal("invalid actual ML barrier identity")
	}
	if err := syscall.Kill(-old.command.Process.Pid, syscall.SIGSTOP); err != nil {
		t.Fatal("could not pause owned worker group")
	}
	resumed := false
	t.Cleanup(func() {
		if !resumed && old.command != nil {
			_ = syscall.Kill(-old.command.Process.Pid, syscall.SIGCONT)
		}
	})
	var backendPID int32
	var fence int64
	var until time.Time
	if err := pool.QueryRow(ctx, `SELECT physical_backend_pid,fence,lease_until FROM cohort_job WHERE id=$1 AND state='running'`, jobID).Scan(&backendPID, &fence, &until); err != nil || backendPID <= 0 {
		t.Fatal("owned physical claim identity missing")
	}
	pausedAt := time.Now()
	deadline = time.Now().Add(55 * time.Second)
	expired := false
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(ctx, `SELECT clock_timestamp()>=$1::timestamptz`, until).Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !expired || time.Since(pausedAt) < 40*time.Second {
		t.Fatal("actual lease did not age naturally")
	}
	var terminated bool
	if err := pool.QueryRow(ctx, `SELECT pg_terminate_backend(a.pid) FROM pg_stat_activity a JOIN cohort_job j ON j.physical_backend_pid=a.pid WHERE j.id=$1 AND a.pid=$2 AND a.pid<>pg_backend_pid() AND EXISTS(SELECT 1 FROM pg_locks l WHERE l.pid=a.pid AND l.locktype='advisory' AND l.objid=74019321 AND l.granted)`, jobID, backendPID).Scan(&terminated); err != nil || !terminated {
		t.Fatal("observed owned physical session was not terminated")
	}
	replacement := q2StartProcess(t, root, binary, "worker-replacement", environment)
	deadline = time.Now().Add(24 * time.Second)
	busy := false
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cohort_job_action WHERE job_id=$1 AND reason_code='execution_busy')`, jobID).Scan(&busy); err != nil {
			t.Fatal(err)
		}
		if busy {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !busy {
		t.Fatal("replacement did not persist execution_busy while old ML execution remained locked")
	}
	var accepted int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM cohort_job_checkpoint WHERE job_id=$1`, jobID).Scan(&accepted); err != nil || accepted != 0 {
		t.Fatal("blocked old compute accepted a checkpoint")
	}
	if err := os.WriteFile(filepath.Join(control, "release"), []byte("release synthetic barrier\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(-old.command.Process.Pid, syscall.SIGCONT); err != nil {
		t.Fatal("could not resume owned old worker")
	}
	resumed = true
	// The old RPC's real10s deadline expired during SIGSTOP. Wait for the actual
	// server computation to leave the barrier before stopping its stale client.
	deadline = time.Now().Add(8 * time.Second)
	finished := false
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(os.Getenv("Q2_ML_COMPUTES_PATH"))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range bytes.Split(raw, []byte("\n")) {
			var event struct {
				Event    string `json:"event"`
				Sequence int    `json:"sequence"`
			}
			if json.Unmarshal(line, &event) == nil && event.Event == "fake_compute_finished" && event.Sequence == entered.Sequence {
				finished = true
				break
			}
		}
		if finished {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !finished {
		t.Fatal("old actual ML computation did not leave barrier")
	}
	old.stop(t, false)
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM cohort_job_checkpoint WHERE job_id=$1 AND fence=$2`, jobID, fence).Scan(&accepted); err != nil || accepted != 0 {
		t.Fatal("stale physical claimant accepted a checkpoint")
	}
	raw, err := json.Marshal(struct {
		JobID                  string  `json:"job_id"`
		OldBackendPID          int32   `json:"old_backend_pid"`
		OldFence               int64   `json:"old_fence"`
		LeaseAgedSeconds       float64 `json:"lease_aged_seconds"`
		ExecutionBusyPersisted bool    `json:"execution_busy_persisted"`
		OldFenceCheckpoints    int     `json:"old_fence_checkpoints"`
		LateRPC                string  `json:"late_rpc"`
	}{jobID, backendPID, fence, time.Since(pausedAt).Seconds(), busy, accepted, "client10s deadline elapsed during pause; no late successful response claimed"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "proof-physical-stale.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return replacement
}
