package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func collectionSetup(t *testing.T) (*pgxpool.Pool, context.Context, *postgres.Repository, domain.CollectionSubmit) {
	t.Helper()
	pool := testDB(t)
	ctx := domain.WithSystemPrincipal(context.Background())
	r := postgres.New(pool)
	if _, e := pool.Exec(ctx, `INSERT INTO auth_user(login,name,password_hash,role,scope)VALUES('collection-admin','Synthetic','unused','admin','{"funds":["presentation"],"ngdus":null,"departments":null}')`); e != nil {
		t.Fatal(e)
	}
	external := int64(-900000)
	w, e := r.CreateWell(ctx, domain.Well{FundType: "presentation", Active: true, UnitekID: &external})
	if e != nil {
		t.Fatal(e)
	}
	mapping := []domain.Mapping{{Field: "f", Parameter: "F_out", Scale: 1, Unit: "Hz"}}
	source, e := r.SaveSource(ctx, domain.TelemetrySource{ID: "collection-source", Name: "Synthetic", Kind: "json_pull", Status: "running", Endpoint: "http://fixture:8080/v1/collection-capture", IntervalSeconds: 86400, WellIdentity: "unitek", WellField: "well_id", TimeField: "timestamp", Mappings: mapping})
	if e != nil {
		t.Fatal(e)
	}
	horizon := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	p := domain.CollectionPolicy{RunID: "collection-test", TestSchema: pool.Config().ConnConfig.RuntimeParams["search_path"], ManifestSHA256: strings.Repeat("a", 64), FixtureOrigin: "http://fixture:8080", AllowedAccountIDs: []int64{1}, Bindings: []domain.CollectionBinding{{SourceID: source.ID, WellID: w.ID, ExternalID: "-900000", IdentityKind: "unitek"}}, Fixtures: []domain.CollectionFixture{{ID: "dense-v1", Protocol: "collection-fixture-v1", Horizon: horizon, RetentionStart: horizon.Add(-31 * 24 * time.Hour), Channels: []domain.CollectionChannel{{Name: "F_out", Aliases: []string{"f", "F_out"}, Unit: "Hz", Minimum: 0, Maximum: 100, MissingMarkers: []float64{}}}, Mappings: mapping}}}
	if _, e = pool.Exec(ctx, `INSERT INTO collection_job_guard(id,run_id,manifest_sha256,test_schema,fixture_origin,synthetic_only,accepting,expires_at)VALUES(true,$1,$2,$3,$4,true,true,clock_timestamp()+interval '1 hour')`, p.RunID, p.ManifestSHA256, p.TestSchema, p.FixtureOrigin); e != nil {
		t.Fatal(e)
	}
	r, e = r.WithCollectionJobPolicy(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.ValidateCollectionJobPolicy(ctx); e != nil {
		t.Fatal(e)
	}
	account, e := r.Account(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	auth, e := r.Authorization(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	principal := domain.AccountPrincipal(account)
	principal.Authorization = &auth
	ctx = domain.WithPrincipal(ctx, domain.TrustedCompatibilityPrincipal(principal))
	return pool, ctx, r, domain.CollectionSubmit{ReplayKey: "collection-replay-0001", FixtureID: "dense-v1", SourceID: source.ID, Start: "2026-10-19T00:00:00Z", End: "2026-10-19T00:02:00Z"}
}
func collectionClaim(t *testing.T, ctx context.Context, r *postgres.Repository) (service.CollectionJobClaim, domain.CollectionMessage) {
	t.Helper()
	event, e := r.ClaimCollectionOutbox(ctx, "dispatcher")
	if e != nil || event == nil {
		t.Fatal("outbox", event, e)
	}
	c, e := r.ClaimCollectionJob(ctx, event.Message, "worker")
	if e != nil || c == nil {
		t.Fatal("claim", c, e)
	}
	t.Cleanup(c.Close)
	return c, event.Message
}
func collectionPrepared(t *testing.T, ctx context.Context, r *postgres.Repository, l domain.CollectionLease, value int) domain.CollectionPreparedCapture {
	t.Helper()
	work, e := r.LoadCollectionWork(ctx, l)
	if e != nil {
		t.Fatal(e)
	}
	body := []byte(fmt.Sprintf(`{"protocol":"collection-fixture-v1","fixture_id":"dense-v1","well_id":"-900000","start":"2026-10-19T00:00:00Z","end":"2026-10-19T00:02:00Z","data_revision":"generation-a","complete":true,"empty":false,"expected_slots":2,"records":[{"well_id":"-900000","timestamp":"2026-10-19T00:00:00Z","values":{"f":%d}},{"well_id":"-900000","timestamp":"2026-10-19T00:01:00Z","values":{"f":0}}]}`, value))
	c, e := domain.PrepareCollectionCapture(work.Intent, domain.CollectionRawCapture{Body: body, ReceivedAt: time.Now().UTC()})
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func collectionCount(t *testing.T, p *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if e := p.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestCollectionAcceptanceDurableReplay(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); s, e := r.AcceptCollectionJob(ctx, in); ids <- s.ID; errs <- e }()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	id := ""
	for got := range ids {
		if id == "" {
			id = got
		}
		if got != id {
			t.Fatal("replay created another job")
		}
	}
	for _, table := range []string{"collection_job", "collection_job_source_hold", "collection_job_outbox"} {
		if n := collectionCount(t, pool, table); n != 1 {
			t.Fatal(table, n)
		}
	}
	s, e := r.AcceptCollectionJob(ctx, in)
	if e != nil || s.ID != id {
		t.Fatal("lost response replay", s, e)
	}
	in.End = "2026-10-19T00:03:00Z"
	if _, e = r.AcceptCollectionJob(ctx, in); e == nil || e.Error() != "replay_conflict" {
		t.Fatal("changed content", e)
	}
}
func TestCollectionIntentAndCaptureImmutable(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	s, e := r.AcceptCollectionJob(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	offset := in
	offset.Start = "2026-10-19T03:00:00+03:00"
	offset.End = "2026-10-19T03:02:00+03:00"
	same, e := r.AcceptCollectionJob(ctx, offset)
	if e != nil || same.ID != s.ID || same.OriginalStart != in.Start {
		t.Fatal(same, e)
	}
	c, _ := collectionClaim(t, ctx, r)
	capture := collectionPrepared(t, ctx, r, c.Lease(), 50)
	if collectionCount(t, pool, "telemetry_measurement") != 0 {
		t.Fatal("measurement before capture")
	}
	if e = r.SaveCollectionCapture(ctx, c.Lease(), capture); e != nil {
		t.Fatal(e)
	}
	if e = r.SaveCollectionCapture(ctx, c.Lease(), capture); e != nil {
		t.Fatal("same capture", e)
	}
	changed := collectionPrepared(t, ctx, r, c.Lease(), 51)
	if e = r.SaveCollectionCapture(ctx, c.Lease(), changed); e == nil {
		t.Fatal("capture replaced")
	}
	if _, e = pool.Exec(ctx, "UPDATE collection_job SET intent='{}' WHERE id=$1", s.ID); e == nil {
		t.Fatal("intent mutable")
	}
	if _, e = pool.Exec(ctx, "UPDATE collection_job_capture SET raw='x' WHERE job_id=$1", s.ID); e == nil {
		t.Fatal("capture mutable")
	}
	source, e := r.Source(ctx, in.SourceID)
	if e != nil {
		t.Fatal(e)
	}
	source.Name = "changed"
	if _, e = r.SaveSource(ctx, source); e != nil {
		t.Fatal(e)
	}
	if _, e = r.LoadCollectionWork(ctx, c.Lease()); e == nil {
		t.Fatal("source change did not revoke")
	}
	got, e := r.CollectionJobStatus(ctx, s.ID)
	if e != nil || got.State != domain.CollectionRevoked || got.Capture.State != "ready" {
		t.Fatal(got, e)
	}
	if collectionCount(t, pool, "collection_job_source_hold") != 0 {
		t.Fatal("revoked hold retained")
	}
	var intent domain.CollectionIntent
	var raw []byte
	if e = pool.QueryRow(ctx, "SELECT intent FROM collection_job WHERE id=$1", s.ID).Scan(&raw); e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &intent); e != nil || intent.SourceProfileVersion != 1 {
		t.Fatal(intent, e)
	}
}
