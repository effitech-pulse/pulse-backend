package integration

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
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

	"golang.org/x/crypto/bcrypt"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/fileinput"
	"github.com/effitech-pulse/pulse-backend/internal/adapter/jobqueue"
	"github.com/effitech-pulse/pulse-backend/internal/bootstrap"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestFileJobWorkerPersistedIntentWithoutBrowser(t *testing.T) {
	pool, session, store, checks, prepared := fileJobSetup(t)
	accepted, e := store.AcceptFileJob(session, prepared)
	if e != nil {
		t.Fatal(e)
	}
	worker, e := service.NewFileJobWorker(store, checks, "11111111111111111111111111111111")
	if e != nil {
		t.Fatal(e)
	}
	msg := fileJobMessage(t, pool, accepted.ID)
	// No browser principal: runtime authority is the stored owner plus opaque PG lease.
	if e = worker.HandleDelivery(context.Background(), msg); e != nil {
		t.Fatal(e)
	}
	if e = worker.HandleDelivery(context.Background(), msg); e != nil {
		t.Fatal(e)
	}
	result, e := store.FileJobResult(session, 1, accepted.ID)
	if e != nil || result.ID != "q1_"+accepted.ID || result.Result.Verdict != "norm" {
		t.Fatal(e, result.ID)
	}
	var results, attempts, manual int
	if e = pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM file_check_result),(SELECT count(*) FROM file_job_attempt),(SELECT count(*) FROM manual_check)`).Scan(&results, &attempts, &manual); e != nil {
		t.Fatal(e)
	}
	if results != 1 || attempts != 1 || manual != 0 {
		t.Fatal(results, attempts, manual)
	}
}
func TestFileJobWorkerQueuedCancelHasNoResult(t *testing.T) {
	pool, session, store, checks, prepared := fileJobSetup(t)
	accepted, e := store.AcceptFileJob(session, prepared)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.CancelFileJob(session, 1, accepted.ID, accepted.Revision); e != nil {
		t.Fatal(e)
	}
	worker, _ := service.NewFileJobWorker(store, checks, "11111111111111111111111111111111")
	if e = worker.HandleDelivery(context.Background(), fileJobMessage(t, pool, accepted.ID)); e != nil {
		t.Fatal(e)
	}
	var count int
	if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM file_check_result`).Scan(&count); e != nil || count != 0 {
		t.Fatal(e, count)
	}
}
func TestFileJobRabbitWorkerActual(t *testing.T) {
	producer, consumer := os.Getenv("Q1_PRODUCER_AMQP_URL"), os.Getenv("Q1_WORKER_AMQP_URL")
	if producer == "" || consumer == "" {
		t.Skip("isolated Q1 broker credentials required")
	}
	_, session, store, checks, prepared := fileJobSetup(t)
	accepted, e := store.AcceptFileJob(session, prepared)
	if e != nil {
		t.Fatal(e)
	}
	publisher := jobqueue.NewPublisher(producer)
	defer publisher.Close()
	dispatcher, _ := service.NewFileJobDispatcher(store, publisher)
	worker, _ := service.NewFileJobWorker(store, checks, "11111111111111111111111111111111")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- jobqueue.Consume(ctx, consumer, worker) }()
	if e = dispatcher.DispatchOnce(ctx); e != nil {
		t.Fatal(e)
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("job did not complete")
		case <-ticker.C:
			state, e := store.FileJob(session, 1, accepted.ID)
			if e != nil {
				t.Fatal(e)
			}
			if state.State == domain.FileJobSucceeded {
				cancel()
				<-done
				check, e := store.FileJobResult(session, 1, accepted.ID)
				if e != nil || check.Result.NWindows != 9007199254740993 {
					t.Fatal(e)
				}
				return
			}
		}
	}
}

// TestFileJobActualHTTPWorkerRestart uses production binaries plus an already
// supervised isolated strict synthetic ML process. It never mutates old Q0 evidence.
func TestFileJobActualHTTPWorkerRestart(t *testing.T) {
	apiBinary, workerBinary := os.Getenv("Q1_API_BINARY"), os.Getenv("Q1_WORKER_BINARY")
	if apiBinary == "" || workerBinary == "" {
		t.Skip("explicit Q1 production binaries and synthetic fixture runtime required")
	}
	pool, session, _, _, _ := fileJobSetup(t)
	// Production FundScope deliberately requires a complete100-well cohort.
	if _, e := pool.Exec(context.Background(), `INSERT INTO well(ngdu,department,active) SELECT 0,0,true FROM generate_series(2,100);INSERT INTO app_cohort_member(version,well_id,revision) SELECT 'q1-test',id,revision FROM well WHERE id<>1`); e != nil {
		t.Fatal(e)
	}
	if _, e := service.NewFundScope(context.Background(), postgres.New(pool), "q1-test"); e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	if external := os.Getenv("Q1_SANDBOX_ROOT"); external != "" {
		if e := os.MkdirAll(external, 0700); e != nil {
			t.Fatal(e)
		}
		root = external
	}
	ctx := context.Background()
	var pin domain.ExecutionPin
	pinRaw, e := os.ReadFile(os.Getenv("Q1_PIN_PATH"))
	if e != nil || json.Unmarshal(pinRaw, &pin) != nil {
		t.Fatal("synthetic pin unavailable")
	}
	schemaRaw, e := os.ReadFile(filepath.Join(os.Getenv("Q1_FIXTURE_DIR"), "schema.json"))
	if e != nil {
		t.Fatal(e)
	}
	schemaPath := filepath.Join(root, "schema.json")
	if e = os.WriteFile(schemaPath, schemaRaw, 0600); e != nil {
		t.Fatal(e)
	}
	dataset := os.Getenv("Q1_DATASET")
	if dataset == "" {
		dataset = "w1.csv"
	}
	if dataset != "w1.csv" && dataset != "w1-insufficient.csv" {
		t.Fatal("unapproved synthetic dataset")
	}
	fixture, e := os.ReadFile(filepath.Join(os.Getenv("Q1_FIXTURE_DIR"), "datasets", dataset))
	if e != nil {
		t.Fatal(e)
	}
	rows, e := csv.NewReader(bytes.NewReader(fixture)).ReadAll()
	if e != nil {
		t.Fatal(e)
	}
	pulse := -1
	for i, col := range rows[0] {
		if col == "pulse" {
			pulse = i
		}
	}
	if pulse < 0 {
		t.Fatal("missing pulse column")
	}
	for _, row := range rows[1:] {
		row[pulse] = "1"
	}
	buffer := new(bytes.Buffer)
	writer := csv.NewWriter(buffer)
	writer.WriteAll(rows)
	if writer.Error() != nil {
		t.Fatal(writer.Error())
	}
	raw := buffer.Bytes()
	column := "pulse"
	spec := domain.FileParseSpec{Version: "file_input_v1", Format: "csv", Delimiter: ",", HeaderRow: 1, TimeColumn: "time", TimestampLayout: "rfc3339", Timezone: "UTC", WellColumn: &column, WellIdentity: "pulse"}
	for _, channel := range []string{"F_out", "Ia", "CosF", "Ped_load", "Pump_intake_pressure_kgs", "Motor_winding_temperature", "R_izol", "qg", "set_Annular_pressure"} {
		spec.Mappings = append(spec.Mappings, domain.Mapping{Field: channel, Parameter: channel, Scale: 1})
	}
	clientSpec, e := service.FileJobClientSpecJSON(spec)
	if e != nil {
		t.Fatal(e)
	}
	policy := domain.DefaultFileJobPolicy()
	policy.RunID = "q1-actual-process"
	policy.CohortVersion = "q1-test"
	policy.CohortManifestSHA256 = strings.Repeat("a", 64)
	policy.AllowedInputs = []domain.FileJobAllowedInput{{WellID: 1, RawSHA256: domain.FileSHA256(raw), Format: "csv", ClientSpecJSON: clientSpec, ClientSpecSHA256: domain.FileSHA256(clientSpec), ExecutionPin: pin}}
	manifest := bootstrap.SyntheticFileManifest{RunID: policy.RunID, TestSchema: pool.Config().ConnConfig.RuntimeParams["search_path"], AccountIDs: []int64{1}, SchemaPath: schemaPath, SchemaSHA256: domain.FileSHA256(schemaRaw), Policy: policy}
	manifestRaw, e := json.Marshal(manifest)
	if e != nil {
		t.Fatal(e)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	if e = os.WriteFile(manifestPath, manifestRaw, 0600); e != nil {
		t.Fatal(e)
	}
	hash := domain.FileSHA256(manifestRaw)
	if _, e = pool.Exec(ctx, `UPDATE file_job_pilot_guard SET run_id=$1,manifest_sha256=$2`, policy.RunID, hash); e != nil {
		t.Fatal(e)
	}
	const password = "q1 synthetic password 2026"
	passwordHash, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE auth_user SET password_hash=$1,login='q1owner' WHERE id=1`, string(passwordHash)); e != nil {
		t.Fatal(e)
	}
	dbURL, e := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	query := dbURL.Query()
	query.Set("search_path", manifest.TestSchema)
	dbURL.RawQuery = query.Encode()
	appEnv := []string{"DATABASE_URL=" + dbURL.String(), "AUTH_MODE=local", "AUTH_COOKIE_SECURE=false", "APP_SCOPE=cohort", "COHORT_VERSION=q1-test", "ML_AUTORUN_ENABLED=false", "FILE_CHECK_MODE=synthetic", "FILE_CHECK_ALLOW_REAL=false", "FILE_CHECK_MANIFEST=" + manifestPath, "FILE_CHECK_MANIFEST_SHA256=" + hash, "FILE_CHECK_RUN_ID=" + policy.RunID, "ML_SERVICE_TARGET=" + os.Getenv("Q1_ML_TARGET"), "ML_SERVICE_API_KEY=q1-synthetic-key", "FILE_JOB_AMQP_URL=" + os.Getenv("Q1_WORKER_AMQP_URL"), "FILE_JOB_PRODUCER_AMQP_URL=" + os.Getenv("Q1_PRODUCER_AMQP_URL"), "HTTP_ADDR=127.0.0.1:8021", "CORS_ORIGINS=http://127.0.0.1:5179", "GIN_MODE=release"}
	processEnv := q1ProcessEnvironment{appEnv: appEnv}
	start := func(binary, label string) *exec.Cmd {
		t.Helper()
		command := exec.Command(binary)
		command.Env = processEnv.childEnv()
		command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		log, e := os.Create(filepath.Join(root, label+".log"))
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { _ = log.Close() })
		command.Stdout = log
		command.Stderr = log
		if e = command.Start(); e != nil {
			t.Fatal(e)
		}
		pidRaw, _ := json.Marshal(map[string]int{"pid": command.Process.Pid, "pgid": command.Process.Pid})
		if e = os.WriteFile(filepath.Join(root, label+"-process.json"), pidRaw, 0600); e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() {
			if command.ProcessState == nil {
				_ = syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
				ended := make(chan struct{})
				go func() { _ = command.Wait(); close(ended) }()
				select {
				case <-ended:
				case <-time.After(5 * time.Second):
					_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
					<-ended
				}
			}
		})
		return command
	}
	stop := func(command *exec.Cmd) { t.Helper(); _ = command.Process.Kill(); _ = command.Wait() }
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 45 * time.Second}
	base := "http://127.0.0.1:8021/api/v1"
	request := func(method, path, content string, body io.Reader, want int) []byte {
		t.Helper()
		req, e := http.NewRequest(method, base+path, body)
		if e != nil {
			t.Fatal(e)
		}
		if content != "" {
			req.Header.Set("Content-Type", content)
		}
		response, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		value, _ := io.ReadAll(io.LimitReader(response.Body, 3<<20))
		if response.StatusCode != want {
			t.Fatalf("%s %s status %d expected %d reason %s", method, path, response.StatusCode, want, value)
		}
		return value
	}
	ready := func() {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			response, e := client.Get("http://127.0.0.1:8021/health")
			if e == nil {
				_ = response.Body.Close()
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("API readiness failed")
	}
	api := start(apiBinary, "api-before")
	ready()
	request("POST", "/auth/login", "application/json", strings.NewReader(`{"login":"q1owner","password":"`+password+`"}`), 200)
	request("GET", "/file-check-capabilities", "", nil, 200)
	multipartBody := new(bytes.Buffer)
	mw := multipart.NewWriter(multipartBody)
	_ = mw.WriteField("request_id", "q1-actual-restart-request")
	var clientFields map[string]json.RawMessage
	_ = json.Unmarshal(clientSpec, &clientFields)
	delete(clientFields, "schema_sha256")
	wireSpec, _ := json.Marshal(clientFields)
	_ = mw.WriteField("parse_spec", string(wireSpec))
	file, e := mw.CreateFormFile("file", "synthetic.csv")
	if e != nil {
		t.Fatal(e)
	}
	_, _ = file.Write(raw)
	_ = mw.Close()
	requestBytes := append([]byte(nil), multipartBody.Bytes()...)
	acceptedRaw := request("POST", "/wells/1/file-check-jobs", mw.FormDataContentType(), bytes.NewReader(requestBytes), 202)
	var accepted struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(acceptedRaw, &accepted) != nil || accepted.ID == "" {
		t.Fatal("missing accepted ID")
	}
	stop(api)
	worker := start(workerBinary, "worker")
	trustedPolicy := policy
	trustedPolicy.ManifestSHA256 = hash
	trustedPolicy.TestSchema = manifest.TestSchema
	trustedPolicy.AllowedAccountIDs = []int64{1}
	trusted, e := postgres.New(pool).WithFileJobPolicy(trustedPolicy)
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(60 * time.Second)
	complete := false
	for time.Now().Before(deadline) {
		status, e := trusted.FileJob(session, 1, accepted.ID)
		if e != nil {
			t.Fatal(e)
		}
		if status.State == domain.FileJobSucceeded {
			complete = true
			break
		}
		if status.State == domain.FileJobFailed && os.Getenv("Q1_EXPECTED_ML_FAILURE") != "" {
			expected := os.Getenv("Q1_EXPECTED_ML_FAILURE")
			if expected != "bundle_unavailable" && expected != "bundle_corrupt" && expected != "pinned_protocol_unavailable" {
				t.Fatal("invalid expected synthetic ML fault")
			}
			if status.ErrorCode == nil || *status.ErrorCode != "pinned_input_unavailable" {
				code := "<nil>"
				if status.ErrorCode != nil {
					code = *status.ErrorCode
				}
				t.Fatalf("unexpected failure classification state=%s code=%s attempt=%d", status.State, code, status.AttemptCount)
			}
			var count int
			if e = pool.QueryRow(ctx, `SELECT count(*) FROM file_check_result`).Scan(&count); e != nil || count != 0 {
				t.Fatal(e, count)
			}
			t.Logf("actual strict ML fault %s terminal failed, result0, attempt%d", expected, status.AttemptCount)
			return
		}
		if status.State.Terminal() {
			t.Fatalf("unexpected terminal %s code %v", status.State, status.ErrorCode)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !complete {
		t.Fatal("worker did not persist result")
	}
	stop(worker)
	api = start(apiBinary, "api-after")
	ready()
	path := "/wells/1/file-check-jobs/" + accepted.ID
	resultRaw := request("GET", path+"/result", "", nil, 200)
	var result struct {
		Check struct {
			Result domain.UploadResult `json:"result"`
		} `json:"check"`
	}
	if json.Unmarshal(resultRaw, &result) != nil || (dataset == "w1.csv" && result.Check.Result.Verdict == "insufficient") || (dataset == "w1-insufficient.csv" && result.Check.Result.Verdict != "insufficient") {
		t.Fatal("sufficient fixture failed")
	}
	request("GET", path+"/review", "", nil, 200)
	request("PUT", path+"/review", "application/json", strings.NewReader(`{"request_id":"q1-actual-review-request","revision":0,"status":"in_progress","owner":"Synthetic","conclusion":"","comment":"synthetic review"}`), 200)
	duplicate := request("POST", "/wells/1/file-check-jobs", mw.FormDataContentType(), bytes.NewReader(requestBytes), 202)
	var same struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(duplicate, &same)
	if same.ID != accepted.ID {
		t.Fatal("lost202 stable key changed job")
	}
	var results, attempts, manual int
	if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM file_check_result),(SELECT count(*) FROM file_job_attempt),(SELECT count(*) FROM manual_check)`).Scan(&results, &attempts, &manual); e != nil || results != 1 || attempts != 1 || manual != 0 {
		t.Fatal(e, results, attempts, manual)
	}
	if os.Getenv("Q1_KEEP_SANDBOX") == "1" {
		if os.Getenv("Q1_SANDBOX_ROOT") == "" {
			t.Fatal("retained sandbox requires external root")
		}
		start(workerBinary, "worker-browser")
		write := func(name string, value any) {
			encoded, e := json.Marshal(value)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(root, name), encoded, 0600); e != nil {
				t.Fatal(e)
			}
		}
		if e = processEnv.writeArtifact(filepath.Join(root, "env.json")); e != nil {
			t.Fatal(e)
		}
		write("credentials.json", map[string]string{"login": "q1owner", "password": password})
		write("spec.json", clientFields)
		if e = os.WriteFile(filepath.Join(root, "fixture.csv"), raw, 0600); e != nil {
			t.Fatal(e)
		}
		write("ready.json", map[string]any{"base_url": base, "schema": manifest.TestSchema, "job_id": accepted.ID, "manifest": manifestPath, "fixture": filepath.Join(root, "fixture.csv"), "spec": filepath.Join(root, "spec.json"), "env": filepath.Join(root, "env.json"), "credentials": filepath.Join(root, "credentials.json"), "stop_marker": filepath.Join(root, "stop")})
		t.Log("retained synthetic browser sandbox ready")
		for {
			if _, e = os.Stat(filepath.Join(root, "stop")); e == nil {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	t.Logf("actual production binary restart proof: verdict=%s result=%d attempt=%d legacy_manual=%d", result.Check.Result.Verdict, results, attempts, manual)
}

type afterCommitAckLoss struct {
	worker      *service.FileJobWorker
	calls       int
	redelivered chan struct{}
}

func (h *afterCommitAckLoss) HandleDelivery(ctx context.Context, m domain.FileJobMessage) error {
	if e := h.worker.HandleDelivery(ctx, m); e != nil {
		return e
	}
	h.calls++
	if h.calls == 1 {
		return errors.New("injected channel loss after persisted commit")
	}
	if h.calls == 2 {
		close(h.redelivered)
	}
	return nil
}
func TestFileJobRabbitCommitBeforeAckLoss(t *testing.T) {
	producer, consumer := os.Getenv("Q1_PRODUCER_AMQP_URL"), os.Getenv("Q1_WORKER_AMQP_URL")
	if producer == "" || consumer == "" {
		t.Skip("isolated broker required")
	}
	pool, session, store, checks, prepared := fileJobSetup(t)
	accepted, e := store.AcceptFileJob(session, prepared)
	if e != nil {
		t.Fatal(e)
	}
	publisher := jobqueue.NewPublisher(producer)
	defer publisher.Close()
	dispatcher, _ := service.NewFileJobDispatcher(store, publisher)
	worker, _ := service.NewFileJobWorker(store, checks, strings.Repeat("a", 32))
	h := &afterCommitAckLoss{worker: worker, redelivered: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- jobqueue.Consume(ctx, consumer, h) }()
	if e = dispatcher.DispatchOnce(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-h.redelivered:
	case <-ctx.Done():
		t.Fatal("persisted message did not redeliver")
	}
	cancel()
	<-done
	var results, attempts int
	if e = pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM file_check_result),(SELECT count(*) FROM file_job_attempt)`).Scan(&results, &attempts); e != nil || results != 1 || attempts != 1 {
		t.Fatal(e, results, attempts)
	}
	if state, e := store.FileJob(session, 1, accepted.ID); e != nil || state.State != domain.FileJobSucceeded {
		t.Fatal(e, state.State)
	}
}
func TestFileJobRabbitKnownPoisonQuarantinesBeforeAck(t *testing.T) {
	producer, consumer := os.Getenv("Q1_PRODUCER_AMQP_URL"), os.Getenv("Q1_WORKER_AMQP_URL")
	if producer == "" || consumer == "" {
		t.Skip("isolated broker required")
	}
	pool, session, store, checks, prepared := fileJobSetup(t)
	accepted, e := store.AcceptFileJob(session, prepared)
	if e != nil {
		t.Fatal(e)
	}
	msg := fileJobMessage(t, pool, accepted.ID)
	msg.JobID = strings.Repeat("f", 32)
	publisher := jobqueue.NewPublisher(producer)
	defer publisher.Close()
	worker, _ := service.NewFileJobWorker(store, checks, strings.Repeat("a", 32))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- jobqueue.Consume(ctx, consumer, worker) }()
	if outcome, e := publisher.Publish(ctx, msg); e != nil || outcome != "confirmed" {
		t.Fatal(outcome, e)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("known poison not quarantined")
		case <-ticker.C:
			state, e := store.FileJob(session, 1, accepted.ID)
			if e != nil {
				t.Fatal(e)
			}
			if state.State == domain.FileJobQuarantined {
				cancel()
				<-done
				var attempts int
				if e = pool.QueryRow(context.Background(), `SELECT count(*) FROM file_job_attempt`).Scan(&attempts); e != nil || attempts != 0 {
					t.Fatal(e, attempts)
				}
				return
			}
		}
	}
}

type retryJobScorer struct {
	calls   int
	success savedScorer
}

func (s *retryJobScorer) UploadPinned(ctx context.Context, pin domain.ExecutionPin, id domain.PinnedInputIdentity, raw io.Reader) (domain.UploadResult, error) {
	s.calls++
	if s.calls == 1 {
		return domain.UploadResult{}, domain.NewError(domain.Unavailable, "ml_unavailable")
	}
	return s.success.UploadPinned(ctx, pin, id, raw)
}
func TestFileJobWorkerDurableRetryThenPinnedSuccess(t *testing.T) {
	pool, session, store, _, prepared := fileJobSetup(t)
	scorer := &retryJobScorer{}
	checks, e := service.NewFileChecksWithBuildInfo(store, store, savedScope{store}, fileinput.Reader{}, scorer, savedSchema(t), savedPin(), domain.DefaultFileInputLimits(), savedFixtureBuildInfo(t))
	if e != nil {
		t.Fatal(e)
	}
	accepted, e := store.AcceptFileJob(session, prepared)
	if e != nil {
		t.Fatal(e)
	}
	worker, _ := service.NewFileJobWorker(store, checks, strings.Repeat("a", 32))
	first := fileJobMessage(t, pool, accepted.ID)
	if e = worker.HandleDelivery(context.Background(), first); e != nil {
		t.Fatal(e)
	}
	state, e := store.FileJob(session, 1, accepted.ID)
	if e != nil || state.State != domain.FileJobRetryWait || state.NextAttemptAt == nil {
		t.Fatal(e, state)
	}
	if e = worker.HandleDelivery(context.Background(), first); e != nil {
		t.Fatal(e)
	}
	if scorer.calls != 1 {
		t.Fatal("stale retry delivery ran compute")
	}
	if _, e = pool.Exec(context.Background(), `UPDATE file_job SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE id=$1`, accepted.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = store.ReconcileFileJobs(context.Background(), 8); e != nil {
		t.Fatal(e)
	}
	second := fileJobMessage(t, pool, accepted.ID)
	if second.Generation != 2 {
		t.Fatal(second.Generation)
	}
	if e = worker.HandleDelivery(context.Background(), second); e != nil {
		t.Fatal(e)
	}
	var results, attempts, outbox int
	if e = pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM file_check_result),(SELECT count(*) FROM file_job_attempt),(SELECT count(*) FROM file_job_outbox)`).Scan(&results, &attempts, &outbox); e != nil || results != 1 || attempts != 2 || outbox != 2 {
		t.Fatal(e, results, attempts, outbox)
	}
}

// q1ProcessEnvironment keeps inherited execution environment out of retained artifacts.
type q1ProcessEnvironment struct{ appEnv []string }

func (e q1ProcessEnvironment) childEnv() []string { return append(os.Environ(), e.appEnv...) }
func (e q1ProcessEnvironment) writeArtifact(path string) error {
	raw, err := json.Marshal(e.appEnv)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0600)
}
func TestFileJobRetainedEnvironmentExcludesHostSecrets(t *testing.T) {
	const sentinelKey = "Q1_TEST_HOST_SECRET_SENTINEL"
	const sentinel = "test-only-must-not-retain"
	t.Setenv(sentinelKey, sentinel)
	environment := q1ProcessEnvironment{appEnv: []string{"FILE_CHECK_MODE=synthetic", "HTTP_ADDR=127.0.0.1:8021"}}
	inherited := false
	for _, entry := range environment.childEnv() {
		if entry == sentinelKey+"="+sentinel {
			inherited = true
		}
	}
	if !inherited {
		t.Fatal("fixture must exercise distinct inherited child env")
	}
	path := filepath.Join(t.TempDir(), "env.json")
	if err := environment.writeArtifact(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(sentinelKey)) || bytes.Contains(raw, []byte(sentinel)) {
		t.Fatal("host environment escaped into retained artifact")
	}
	var saved []string
	if err = json.Unmarshal(raw, &saved); err != nil || len(saved) != 2 || saved[0] != "FILE_CHECK_MODE=synthetic" || saved[1] != "HTTP_ADDR=127.0.0.1:8021" {
		t.Fatal("retained environment differs from explicit application values")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private artifact permissions changed")
	}
}
