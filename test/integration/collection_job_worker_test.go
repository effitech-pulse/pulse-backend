package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/telemetry"
	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
	"github.com/effitech-pulse/pulse-backend/internal/transport/httpapi"
)

func TestCollectionApplicationHTTPSourceApply(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	var requests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"protocol":"collection-fixture-v1","fixture_id":"dense-v1","well_id":"-900000","start":"2026-10-19T00:00:00Z","end":"2026-10-19T00:02:00Z","data_revision":"generation-a","complete":true,"empty":false,"expected_slots":2,"records":[{"well_id":"-900000","timestamp":"2026-10-19T00:00:00Z","values":{"f":50}},{"well_id":"-900000","timestamp":"2026-10-19T00:01:00Z","values":{"f":0}}]}`)
	}))
	defer fixture.Close()

	source, e := r.Source(ctx, in.SourceID)
	if e != nil {
		t.Fatal(e)
	}
	source.Endpoint = fixture.URL + "/v1/collection-capture"
	source, e = mutateSourceFixture(domain.WithSystemPrincipal(ctx), r, domain.SourceUpdate, source)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE collection_job_guard SET fixture_origin=$1`, fixture.URL); e != nil {
		t.Fatal(e)
	}
	horizon := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	var wellID int64
	if e = pool.QueryRow(ctx, `SELECT id FROM well WHERE unitek_id=-900000`).Scan(&wellID); e != nil {
		t.Fatal(e)
	}
	policy := domain.CollectionPolicy{RunID: "collection-test", TestSchema: pool.Config().ConnConfig.RuntimeParams["search_path"], ManifestSHA256: strings.Repeat("a", 64), FixtureOrigin: fixture.URL, AllowedAccountIDs: []int64{1}, Bindings: []domain.CollectionBinding{{SourceID: source.ID, WellID: wellID, ExternalID: "-900000", IdentityKind: "unitek"}}, Fixtures: []domain.CollectionFixture{{ID: "dense-v1", Protocol: "collection-fixture-v1", Horizon: horizon, RetentionStart: horizon.Add(-31 * 24 * time.Hour), Channels: []domain.CollectionChannel{{Name: "F_out", Unit: "Hz", Minimum: 0, Maximum: 100}}, Mappings: source.Mappings}}}
	r, e = postgres.New(pool).WithCollectionJobPolicy(policy)
	if e != nil {
		t.Fatal(e)
	}
	access, e := service.NewAccess(r, true)
	if e != nil {
		t.Fatal(e)
	}
	token := "synthetic-collection-session"
	sum := sha256.Sum256([]byte(token))
	if e = r.CreateSession(ctx, 1, 1, hex.EncodeToString(sum[:]), time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	jobs, e := service.NewCollectionJobs(r, true)
	if e != nil {
		t.Fatal(e)
	}
	server := &httpapi.Server{Config: config.Config{Prefix: "/api/v1", CollectionJobMode: "synthetic"}, Access: access, CollectionJobs: jobs}
	raw, _ := json.Marshal(in)
	call := func(method, path string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(string(body)))
		req.AddCookie(&http.Cookie{Name: "pulse_session", Value: token})
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, req)
		return w
	}
	accepted := call("POST", "/api/v1/collection-jobs", raw)
	if accepted.Code != 202 {
		t.Fatalf("submit %d %s", accepted.Code, accepted.Body.String())
	}
	var status domain.CollectionJobStatus
	if e = json.Unmarshal(accepted.Body.Bytes(), &status); e != nil {
		t.Fatal(e)
	}
	replay := call("POST", "/api/v1/collection-jobs", raw)
	if replay.Code != 202 || !strings.Contains(replay.Body.String(), status.ID) {
		t.Fatal("lost response replay", replay.Code, replay.Body.String())
	}
	event, e := r.ClaimCollectionOutbox(context.Background(), strings.Repeat("a", 32))
	if e != nil || event == nil {
		t.Fatal(event, e)
	}
	httpSource, e := telemetry.NewCollectionSource(fixture.URL)
	if e != nil {
		t.Fatal(e)
	}
	worker, e := service.NewCollectionJobWorker(r, httpSource, strings.Repeat("b", 32))
	if e != nil {
		t.Fatal(e)
	}
	if e = worker.HandleDelivery(context.Background(), event.Message); e != nil {
		t.Fatal(e)
	}
	fixture.Close()
	if e = worker.HandleDelivery(context.Background(), event.Message); e != nil {
		t.Fatal("terminal duplicate", e)
	}
	if requests.Load() != 1 {
		t.Fatal("terminal replay refetched source", requests.Load())
	}
	read := call("GET", "/api/v1/collection-jobs/"+status.ID, nil)
	if read.Code != 200 {
		t.Fatal(read.Code, read.Body.String())
	}
	if e = json.Unmarshal(read.Body.Bytes(), &status); e != nil {
		t.Fatal(e)
	}
	if status.State != domain.CollectionSucceeded || status.Write.Inserted != 2 || status.Coverage.State != "complete" || status.Capture.UniqueRows != 2 || !status.Write.Checkpoint {
		t.Fatalf("missing atomic publication: %+v", status)
	}
	if collectionCount(t, pool, "telemetry_measurement") != 2 || collectionCount(t, pool, "collection_job_checkpoint") != 1 || collectionCount(t, pool, "collection_job_coverage") != 1 {
		t.Fatal("partial measurement effect")
	}
	var zero float64
	if e = pool.QueryRow(ctx, `SELECT (values->>'F_out')::double precision FROM telemetry_measurement WHERE measured_at=$1`, horizon.Add(-24*time.Hour+time.Minute)).Scan(&zero); e != nil || zero != 0 {
		t.Fatal("valid zero lost", zero, e)
	}
	// Mode off serves accepted metadata without fixture or broker dependencies.
	server.Config.CollectionJobMode = "off"
	server.CollectionJobs, e = service.NewCollectionJobs(postgres.New(pool), false)
	if e != nil {
		t.Fatal(e)
	}
	if w := call("GET", "/api/v1/collection-jobs/"+status.ID, nil); w.Code != 200 {
		t.Fatal("off status", w.Code, w.Body.String())
	}
	if w := call("POST", "/api/v1/collection-jobs", raw); w.Code != 503 {
		t.Fatal("off mutation", w.Code, w.Body.String())
	}
}
