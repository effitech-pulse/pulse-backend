package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

type cohortHTTPStore struct {
	service.CohortJobStore
	calls  int
	status domain.CohortJobStatus
	err    error
}

func (s *cohortHTTPStore) CohortJobStatus(context.Context, string) (domain.CohortJobStatus, error) {
	s.calls++
	return s.status, s.err
}
func (s *cohortHTTPStore) CancelCohortJob(context.Context, string, int64) (domain.CohortJobStatus, error) {
	s.calls++
	return s.status, s.err
}
func (s *cohortHTTPStore) AcceptCohortJob(context.Context, domain.PreparedCohortJob) (domain.CohortJobStatus, error) {
	s.calls++
	return s.status, s.err
}

type cohortHTTPCatalogue struct{}

func (cohortHTTPCatalogue) Fixture(string) (domain.PreparedCohortJob, error) {
	return domain.PreparedCohortJob{}, domain.NewError(domain.Missing, "cohort_fixture_unavailable")
}
func TestCohortJobRoutesDefaultOffRequireSession(t *testing.T) {
	s := &Server{Config: config.Config{Prefix: "/api/v1", CohortJobMode: "off"}}
	for _, path := range []string{"/api/v1/ml/cohort-jobs", "/api/v1/ml/cohort-jobs/" + strings.Repeat("a", 32)} {
		method := "GET"
		if path == "/api/v1/ml/cohort-jobs" {
			method = "POST"
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(`{}`)))
		if w.Code != 401 {
			t.Fatalf("route not fail-closed: %d %s", w.Code, w.Body.String())
		}
	}
}
func TestCohortHTTPStatusPreservesRunIDAndHidesInternalIdentity(t *testing.T) {
	s := &Server{Config: config.Config{Prefix: "/api/v1"}}
	dto := s.cohortJobDTO(domain.CohortJobStatus{ID: strings.Repeat("a", 32), RunID: 9007199254740993, State: domain.CohortJobSucceeded, CompletedCount: 100})
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out["run_id"] != "9007199254740993" || out["completed_count"] != float64(100) || out["result_url"] != "/api/v1/ml/runs/9007199254740993/snapshot" || out["redrive_available"] != false {
		t.Fatal(string(raw))
	}
	for _, key := range []string{"actor", "actor_account_id", "token", "fence", "lease", "input", "execution", "manifest_sha256"} {
		if _, ok := out[key]; ok {
			t.Fatal("private identity", key)
		}
	}
}
func TestCohortHTTPStrictRequestAndPersistedStatus(t *testing.T) {
	account := int64(1)
	admin := domain.Principal{AccountID: &account, Role: "admin", Authenticated: true, Mechanism: "session"}
	for _, tc := range []struct {
		name, method, body string
		code               int
	}{
		{"get", "GET", "", 200}, {"unknown", "POST", `{"revision":1,"pin":"forged"}`, 400}, {"oversized", "POST", strings.Repeat(" ", 1024) + `{"revision":1}`, 400}, {"extra", "POST", `{"revision":1}{}`, 400}, {"revision", "POST", `{"revision":0}`, 400}, {"cancel", "POST", `{"revision":1}`, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &cohortHTTPStore{status: domain.CohortJobStatus{ID: strings.Repeat("a", 32), State: domain.CohortJobPendingDispatch, Revision: 1, CreatedAt: time.Now()}}
			jobs, _ := service.NewCohortJobs(store, cohortHTTPCatalogue{})
			s := &Server{Config: config.Config{Prefix: "/api/v1", CohortJobMode: "synthetic"}, CohortJobs: jobs}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(tc.method, "/api/v1/ml/cohort-jobs/"+store.status.ID, strings.NewReader(tc.body))
			c.Request = c.Request.WithContext(domain.WithPrincipal(c.Request.Context(), admin))
			c.Params = gin.Params{{Key: "job_id", Value: store.status.ID}}
			var err error
			if tc.method == "GET" {
				err = s.getCohortJob(c)
			} else {
				err = s.cancelCohortJob(c)
			}
			if err != nil {
				writeError(c, err)
			}
			if w.Code != tc.code || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if tc.code == 400 && store.calls != 0 {
				t.Fatal("malformed reached DB")
			}
		})
	}
}

type cohortHTTPReadyCatalogue struct{ value domain.PreparedCohortJob }

func (c cohortHTTPReadyCatalogue) Fixture(string) (domain.PreparedCohortJob, error) {
	return c.value, nil
}
func cohortHTTPPrepared(t *testing.T) domain.PreparedCohortJob {
	t.Helper()
	h := domain.FileSHA256([]byte("fixture"))
	pin := domain.ExecutionPin{Version: "upload_pin_v1", ModelName: "fixture", BundleSHA256: h, ContextVersion: "legacy_v1", DecisionRule: &domain.DecisionRule{ContextVersion: "legacy_v1", PolicyID: "fixture", RuleSHA256: h, Watch: &domain.ThresholdRule{Scale: "raw", Comparison: ">", Threshold: 0}}, FeatureRecipeSHA256: h, FeatureSchemaSHA256: h, SourceManifestSHA256: h, RuntimeManifestSHA256: h}
	f := domain.CohortFixtureManifest{Version: "cohort_fixture_v1", FixtureID: "all100", CohortVersion: "fixture-v1", CohortManifestSHA256: strings.Repeat("a", 64), AsOf: time.Now().UTC(), ExecutionPin: pin, CaptureRecipe: domain.CohortCaptureRecipe{Version: "cohort_fixture_capture_v1", Normalization: "none_supplied_canonical", BackendBuildSHA256: h}}
	reason := "insufficient_telemetry"
	p := domain.PreparedCohortJob{}
	for i := int64(1); i <= 100; i++ {
		f.Members = append(f.Members, domain.CohortFixtureMember{WellID: i, NoComputeReason: &reason})
		p.Members = append(p.Members, domain.CohortFrozenMember{Ordinal: int(i), Well: domain.Well{ID: i, Active: true}, NoComputeReason: &reason})
	}
	raw, err := domain.CanonicalCohortFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	p.Fixture = f
	p.FixtureJSON = raw
	p.FixtureSHA256 = domain.FileSHA256(raw)
	return p
}
func TestCohortHTTPAcceptanceAndSafeErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		code  int
		retry string
	}{
		{"accepted", nil, 202, "2"}, {"busy", domain.NewError(domain.Limited, "cohort_capacity_busy"), 429, "10"}, {"denied", domain.NewError(domain.Missing, "cohort_job_unavailable"), 404, ""}, {"conflict", domain.NewError(domain.Duplicate, "idempotency_conflict"), 409, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &cohortHTTPStore{status: domain.CohortJobStatus{ID: strings.Repeat("a", 32), RunID: 17, State: domain.CohortJobPendingDispatch}, err: tc.err}
			jobs, _ := service.NewCohortJobs(store, cohortHTTPReadyCatalogue{cohortHTTPPrepared(t)})
			s := &Server{Config: config.Config{Prefix: "/api/v1", CohortJobMode: "synthetic"}, CohortJobs: jobs}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/api/v1/ml/cohort-jobs", strings.NewReader(`{"fixture_id":"all100"}`))
			c.Request.Header.Set("Idempotency-Key", strings.Repeat("k", 16))
			actor := int64(1)
			c.Request = c.Request.WithContext(domain.WithPrincipal(c.Request.Context(), domain.Principal{AccountID: &actor, Role: "admin", Authenticated: true, Mechanism: "session"}))
			err := s.submitCohortJob(c)
			if err != nil {
				writeError(c, err)
			}
			if w.Code != tc.code || w.Header().Get("Retry-After") != tc.retry || store.calls != 1 {
				t.Fatalf("%d %s calls%d", w.Code, w.Body.String(), store.calls)
			}
			if tc.code == 202 && w.Header().Get("Location") != "/api/v1/ml/cohort-jobs/"+store.status.ID {
				t.Fatal("missing committed identity")
			}
		})
	}
}

func TestCohortJobPreflightAllowsIdempotencyKey(t *testing.T) {
	s := &Server{Config: config.Config{Prefix: "/api/v1", APIKey: "required", CORS: []string{"https://ui.example"}}}
	for _, origin := range []string{"https://ui.example", "https://denied.example"} {
		t.Run(origin, func(t *testing.T) {
			request := httptest.NewRequest("OPTIONS", "/api/v1/ml/cohort-jobs", nil)
			request.Header.Set("Origin", origin)
			request.Header.Set("Access-Control-Request-Method", "POST")
			request.Header.Set("Access-Control-Request-Headers", "content-type,idempotency-key")
			response := httptest.NewRecorder()
			s.Handler().ServeHTTP(response, request)
			if response.Code != 204 {
				t.Fatalf("preflight blocked by auth: %d", response.Code)
			}
			if origin != "https://ui.example" {
				if response.Header().Get("Access-Control-Allow-Origin") != "" {
					t.Fatal("untrusted origin granted")
				}
				return
			}
			if response.Header().Get("Access-Control-Allow-Origin") != origin || response.Header().Get("Access-Control-Allow-Credentials") != "true" {
				t.Fatal("session origin contract lost")
			}
			allowed := false
			for _, name := range strings.Split(response.Header().Get("Access-Control-Allow-Headers"), ",") {
				if strings.EqualFold(strings.TrimSpace(name), "Idempotency-Key") {
					allowed = true
				}
			}
			if !allowed {
				t.Fatal("browser cannot submit cohort Idempotency-Key")
			}
		})
	}
}
