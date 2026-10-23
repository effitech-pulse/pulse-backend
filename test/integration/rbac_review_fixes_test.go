package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
	"github.com/effitech-pulse/pulse-backend/internal/transport/httpapi"
)

func reviewActor(t *testing.T, r *postgres.Repository, id int64) context.Context {
	t.Helper()
	ctx := context.Background()
	a, e := r.Authorization(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	account, e := r.Account(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	p := domain.AccountPrincipal(account)
	p.Authorization = &a
	return domain.WithRBACMode(domain.WithPrincipal(ctx, p), domain.RBACEnforce)
}
func TestRBACReviewDeleteExactPermission(t *testing.T) {
	pool := testDB(t)
	r := postgres.New(pool)
	id := rbacReadUser(t, pool, "delete-only", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "wells.delete", "wells.read")
	w, e := r.CreateWell(legacyResearchFixtureContext(), domain.Well{FundType: "operational", Active: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = r.DeleteWell(reviewActor(t, r, id), w.ID); e != nil {
		t.Fatalf("exact delete permission denied: %v", e)
	}
}
func TestRBACReviewSourceRevocation(t *testing.T) {
	pool := testDB(t)
	r := postgres.New(pool)
	ctx := legacyResearchFixtureContext()
	source, e := r.MutateSource(ctx, domain.SourceMutation{Kind: domain.SourceCreate, Source: domain.TelemetrySource{ID: "review-source", Name: "Original", Kind: "json_push", IntervalSeconds: 60, WellField: "well", TimeField: "time", Mappings: []domain.Mapping{{Field: "f", Parameter: "F_out", Scale: 1}}}})
	if e != nil {
		t.Fatal(e)
	}
	id := rbacReadUser(t, pool, "source-editor", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "integrations.update", "integrations.read")
	if _, e = pool.Exec(ctx, `UPDATE auth_user SET scope='{"funds":["operational","presentation"]}' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	stale := reviewActor(t, r, id)
	if _, e = pool.Exec(ctx, `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	var before, after string
	if e = pool.QueryRow(ctx, `SELECT jsonb_build_object('source',to_jsonb(s),'profiles',(SELECT jsonb_agg(to_jsonb(p)) FROM telemetry_source_profile p WHERE source_id=s.id))::text FROM telemetry_source s WHERE id=$1`, source.ID).Scan(&before); e != nil {
		t.Fatal(e)
	}
	source.Name = "revoked update"
	if _, e = r.MutateSource(stale, domain.SourceMutation{Kind: domain.SourceUpdate, Source: source}); e == nil {
		t.Fatal("revoked source mutation committed")
	}
	if e = pool.QueryRow(ctx, `SELECT jsonb_build_object('source',to_jsonb(s),'profiles',(SELECT jsonb_agg(to_jsonb(p)) FROM telemetry_source_profile p WHERE source_id=s.id))::text FROM telemetry_source s WHERE id=$1`, source.ID).Scan(&after); e != nil || before != after {
		t.Fatal("revoked mutation changed source/profile/fence", e)
	}
}
func TestRBACReviewManualCohortIdentity(t *testing.T) {
	for _, change := range []bool{false, true} {
		t.Run(fmt.Sprint(change), func(t *testing.T) {
			app, _, members := reviewCohort(t)
			ctx := legacyResearchFixtureContext()
			l, ok, e := app.store.Reserve(ctx)
			if e != nil || !ok {
				t.Fatal(ok, e)
			}
			defer l.Close()
			run, e := l.(interface {
				BeginCohort(context.Context, string, string) (int64, error)
			}).BeginCohort(ctx, "review", "senior-v1")
			if e != nil {
				t.Fatal(e)
			}
			sid := "review-snapshot"
			now := time.Now().UTC()
			s := domain.CohortSnapshot{Profile: "cohort", CohortVersion: "senior-v1", RunID: &run, SnapshotID: &sid, AsOf: &now, ProvenanceStatus: "partial", OutcomeCompleteness: "complete"}
			for _, w := range members {
				s.Outcomes = append(s.Outcomes, domain.CohortOutcome{AppWellID: w.ID, Metadata: w, Status: "insufficient", ReasonCode: "no_data"})
			}
			if change {
				if _, e = app.store.PatchWell(ctx, members[0].ID, domain.WellPatch{WellNumber: domain.Field[string]{Present: true, Value: "edited after capture"}}); e != nil {
					t.Fatal(e)
				}
			}
			e = l.(service.CohortRunLease).PersistCohort(ctx, run, s)
			if (e == nil) == change {
				t.Fatalf("changed=%v publication err=%v", change, e)
			}
		})
	}
}

func TestRBACReviewWriterNullableTuple(t *testing.T) {
	pool := testDB(t)
	raw := `{"version":"access_v1","account_id":1,"epoch":1,"roles":[]}`
	if _, e := pool.Exec(context.Background(), `INSERT INTO telemetry_source(id,config,writer_token_sha256,writer_profile_version,writer_execution_actor,writer_authorization)VALUES('valid-account','{}',$1,1,'account',$2)`, strings.Repeat("a", 64), raw); e != nil {
		t.Fatalf("valid control rejected: %v", e)
	}
	if _, e := pool.Exec(context.Background(), `INSERT INTO telemetry_source(id,config,writer_token_sha256,writer_profile_version,writer_authorization)VALUES('bad-null-kind','{}',$1,1,$2)`, strings.Repeat("a", 64), raw); e == nil {
		t.Fatal("NULL discriminator with nonNULL authorization accepted")
	}
}

func mutateSourceFixture(ctx context.Context, r *postgres.Repository, kind domain.SourceMutationKind, s domain.TelemetrySource) (domain.TelemetrySource, error) {
	desired := s.Status
	if s.Name == "" {
		s.Name = "Explicit source fixture"
	}
	if s.IntervalSeconds == 0 {
		s.IntervalSeconds = 60
	}
	if s.WellField == "" {
		s.WellField = "well_id"
	}
	if s.TimeField == "" {
		s.TimeField = "timestamp"
	}
	if len(s.Mappings) == 0 {
		s.Mappings = []domain.Mapping{{Field: "f", Parameter: "F_out", Scale: 1}}
	}
	if s.Kind != "json_push" && s.Endpoint == "" {
		s.Endpoint = "http://fixture:8080/data"
	}
	out, e := r.MutateSource(ctx, domain.SourceMutation{Kind: kind, Source: s, ReplaceSecret: s.EncryptedSecret != ""})
	if e != nil {
		return out, e
	}
	if desired == "running" || desired == "paused" {
		return r.MutateSource(ctx, domain.SourceMutation{Kind: domain.SourceStatus, Source: domain.TelemetrySource{ID: out.ID, Status: desired}})
	}
	return out, nil
}

func TestRBACReviewSourceMutation(t *testing.T) {
	pool := testDB(t)
	r := postgres.New(pool)
	setup := legacyResearchFixtureContext()
	source, e := mutateSourceFixture(setup, r, domain.SourceCreate, domain.TelemetrySource{ID: "typed-source", Name: "current", Kind: "json_push", Status: "running", IntervalSeconds: 60})
	if e != nil {
		t.Fatal(e)
	}
	statusID := rbacReadUser(t, pool, "status-only", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "integrations.status", "integrations.read")
	if _, e = pool.Exec(setup, `UPDATE auth_user SET scope='{"funds":["operational","presentation"]}' WHERE id=$1`, statusID); e != nil {
		t.Fatal(e)
	}
	statusActor := reviewActor(t, r, statusID)
	old := source
	old.Name = "stale name"
	old.EncryptedSecret = "stale secret"
	old.Status = "paused"
	if _, e = pool.Exec(setup, `UPDATE telemetry_source SET encrypted_secret='locked-secret',last_received='2026-01-01'::timestamptz,last_measured='2026-01-02'::timestamptz WHERE id=$1`, source.ID); e != nil {
		t.Fatal(e)
	}
	changed, e := r.MutateSource(statusActor, domain.SourceMutation{Kind: domain.SourceStatus, Source: old})
	if e != nil || changed.Name != "current" || changed.EncryptedSecret != "locked-secret" || changed.LastMeasured == nil || changed.ProfileVersion != source.ProfileVersion+1 {
		t.Fatal(changed, e)
	}
	if _, e = r.MutateSource(statusActor, domain.SourceMutation{Kind: domain.SourceUpdate, Source: old}); e == nil {
		t.Fatal("status key allowed update")
	}
	old.Name = "intentional update"
	old.EncryptedSecret = "ignore stale secret"
	old.LastMeasured = nil
	updated, e := r.MutateSource(setup, domain.SourceMutation{Kind: domain.SourceUpdate, Source: old})
	if e != nil || updated.EncryptedSecret != "locked-secret" || updated.LastMeasured == nil || updated.Status != "draft" || updated.ProfileVersion != changed.ProfileVersion+1 {
		t.Fatal(updated, e)
	}
	old.EncryptedSecret = "new-sealed-secret"
	replaced, e := r.MutateSource(setup, domain.SourceMutation{Kind: domain.SourceUpdate, Source: old, ReplaceSecret: true})
	if e != nil || replaced.EncryptedSecret != "new-sealed-secret" {
		t.Fatal(replaced, e)
	}
	if _, e = r.MutateSource(setup, domain.SourceMutation{Kind: domain.SourceCreate, Source: old}); e == nil {
		t.Fatal("create updated existing source")
	}
	old.ID = "missing-source"
	if _, e = r.MutateSource(setup, domain.SourceMutation{Kind: domain.SourceUpdate, Source: old}); e == nil {
		t.Fatal("update created missing source")
	}
	for _, ctx := range []context.Context{context.Background(), domain.WithPrincipal(context.Background(), domain.Principal{Authenticated: true, Role: "admin", Mechanism: "legacy"})} {
		if _, e = r.MutateSource(ctx, domain.SourceMutation{Kind: domain.SourceUpdate, Source: source}); e == nil {
			t.Fatal("missing/forged authority accepted")
		}
	}
	if _, e = r.MutateSource(setup, domain.SourceMutation{Kind: "unrecognized", Source: source}); e == nil {
		t.Fatal("unknown mutation accepted")
	}
}

func TestRBACReviewSourceLockOrder(t *testing.T) {
	pool := testDB(t)
	r := postgres.New(pool)
	setup := legacyResearchFixtureContext()
	source, e := mutateSourceFixture(setup, r, domain.SourceCreate, domain.TelemetrySource{ID: "source-race", Kind: "json_push", Status: "running"})
	if e != nil {
		t.Fatal(e)
	}
	id := rbacReadUser(t, pool, "race-source-writer", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "integrations.update", "integrations.read")
	if _, e = pool.Exec(setup, `UPDATE auth_user SET scope='{"funds":["operational","presentation"]}' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	actor := reviewActor(t, r, id)
	source.Name = "writer won"
	blocker, e := pool.Begin(setup)
	if e != nil {
		t.Fatal(e)
	}
	defer blocker.Rollback(setup)
	if _, e = blocker.Exec(setup, `SELECT id FROM telemetry_source WHERE id=$1 FOR UPDATE`, source.ID); e != nil {
		t.Fatal(e)
	}
	writer := make(chan error, 1)
	go func() {
		_, e := r.MutateSource(actor, domain.SourceMutation{Kind: domain.SourceUpdate, Source: source})
		writer <- e
	}()
	deadline := time.Now().Add(2 * time.Second)
	locked := false
	for time.Now().Before(deadline) {
		probe, e := pool.Begin(setup)
		if e != nil {
			t.Fatal(e)
		}
		_, e = probe.Exec(setup, `SELECT id FROM auth_user WHERE id=$1 FOR UPDATE NOWAIT`, id)
		probe.Rollback(setup)
		if e != nil {
			locked = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !locked {
		t.Fatal("writer did not lock user before waiting for source")
	}
	revoked := make(chan error, 1)
	go func() {
		tx, e := pool.Begin(setup)
		if e == nil {
			defer tx.Rollback(setup)
			_, e = tx.Exec(setup, `SELECT id FROM auth_rbac_state FOR UPDATE`)
			if e == nil {
				_, e = tx.Exec(setup, `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=$1`, id)
			}
			if e == nil {
				e = tx.Commit(setup)
			}
		}
		revoked <- e
	}()
	select {
	case e := <-revoked:
		t.Fatalf("revoker passed held user lock: %v", e)
	case <-time.After(30 * time.Millisecond):
	}
	if e = blocker.Rollback(setup); e != nil {
		t.Fatal(e)
	}
	for name, ch := range map[string]<-chan error{"writer": writer, "revoker": revoked} {
		select {
		case e := <-ch:
			if e != nil {
				t.Fatal(name, e)
			}
		case <-time.After(6 * time.Second):
			t.Fatal(name, "did not finish")
		}
	}
}

func TestRBACReviewCompatSessionBoundary(t *testing.T) {
	for _, kind := range []string{"file", "cohort", "collection"} {
		t.Run(kind, func(t *testing.T) {
			var ctx context.Context
			var r *postgres.Repository
			var accept func(context.Context) error
			var revoke func()
			switch kind {
			case "file":
				pool, c, store, _, v := fileJobSetup(t)
				ctx = c
				r = store
				accept = func(c context.Context) error { _, e := store.AcceptFileJob(c, v); return e }
				revoke = func() {
					if _, e := pool.Exec(ctx, `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=1`); e != nil {
						t.Fatal(e)
					}
				}
			case "cohort":
				pool, c, store, v := cohortJobSetup(t)
				ctx = c
				r = store
				accept = func(c context.Context) error { _, e := store.AcceptCohortJob(c, v); return e }
				revoke = func() {
					if _, e := pool.Exec(ctx, `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=1`); e != nil {
						t.Fatal(e)
					}
				}
			case "collection":
				pool, c, store, v := collectionSetup(t)
				ctx = c
				r = store
				accept = func(c context.Context) error { _, e := store.AcceptCollectionJob(c, v); return e }
				revoke = func() {
					if _, e := pool.Exec(ctx, `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=1`); e != nil {
						t.Fatal(e)
					}
				}
			}
			p, _ := domain.CurrentPrincipal(ctx)
			p.Authorization = nil
			if e := accept(domain.WithPrincipal(context.Background(), p)); e == nil {
				t.Fatal("missing snapshot accepted")
			}
			revoke()
			if e := accept(ctx); e == nil {
				t.Fatal("stale epoch accepted")
			}
			_ = r
		})
	}
}

func TestRBACReviewManualCohortMatrix(t *testing.T) {
	for _, kind := range []string{"unchanged", "ngdu", "department", "fund", "active", "archive", "unitek", "armits", "aba", "metadata", "duplicate", "missing-metadata"} {
		t.Run(kind, func(t *testing.T) {
			pool := testDB(t)
			app := New(testConfig(), pool, &fakeML{})
			ctx := legacyResearchFixtureContext()
			members := []domain.Well{}
			for i := 0; i < 100; i++ {
				w, e := app.store.CreateWell(ctx, domain.Well{FundType: "presentation", NGDU: -900, Department: -900, Active: true, UnitekID: ptr(int64(-900000 - i))})
				if e != nil {
					t.Fatal(e)
				}
				members = append(members, w)
			}
			if _, e := app.store.CaptureCohort(ctx, "senior-v1"); e != nil {
				t.Fatal(e)
			}
			if _, e := app.store.PatchWell(ctx, members[0].ID, domain.WellPatch{Active: domain.Field[bool]{Present: true, Value: false}}); e != nil {
				t.Fatal(e)
			}
			if _, e := app.store.SetWellArchive(ctx, members[1].ID, nil, true, "mixed frozen lifecycle"); e != nil {
				t.Fatal(e)
			}
			// Capture full live Metadata once before compute, including an unchanged archived member.
			for i := range members {
				w, e := app.store.Well(ctx, members[i].ID)
				if e != nil {
					t.Fatal(e)
				}
				members[i] = w
			}
			l, ok, e := app.store.Reserve(ctx)
			if e != nil || !ok {
				t.Fatal(ok, e)
			}
			defer l.Close()
			run, e := l.(interface {
				BeginCohort(context.Context, string, string) (int64, error)
			}).BeginCohort(ctx, "review-matrix", "senior-v1")
			if e != nil {
				t.Fatal(e)
			}
			sid := "review-" + kind
			now := time.Now().UTC()
			s := domain.CohortSnapshot{Profile: "cohort", CohortVersion: "senior-v1", RunID: &run, SnapshotID: &sid, AsOf: &now, ProvenanceStatus: "partial", OutcomeCompleteness: "complete"}
			for _, w := range members {
				o := domain.CohortOutcome{AppWellID: w.ID, Metadata: w, Status: "insufficient", ReasonCode: "no_data"}
				if w.ArchivedAt != nil {
					o.Status = "archived"
					o.ReasonCode = "archived"
				} else if !w.Active {
					o.Status = "inactive"
					o.ReasonCode = "inactive"
				}
				s.Outcomes = append(s.Outcomes, o)
			}
			// Target edits preserve actor grants/epoch and may leave historical cohort bindings unchanged.
			var sql string
			switch kind {
			case "ngdu":
				sql = "UPDATE well SET ngdu=ngdu-1 WHERE id=3"
			case "department":
				sql = "UPDATE well SET department=department-1 WHERE id=3"
			case "fund":
				sql = "UPDATE well SET fund_type='operational' WHERE id=3"
			case "active":
				sql = "UPDATE well SET active=false WHERE id=3"
			case "archive":
				sql = "UPDATE well SET archived_at=now() WHERE id=3"
			case "unitek":
				sql = "UPDATE well SET unitek_id=-900200 WHERE id=3"
			case "armits":
				sql = "UPDATE well SET armits_id=-900201 WHERE id=3"
			case "aba":
				sql = "UPDATE well SET revision=revision+2 WHERE id=3"
			case "metadata":
				sql = "UPDATE well SET revision=revision+1,well_number='changed' WHERE id=3"
			case "duplicate":
				s.Outcomes[2] = s.Outcomes[0]
			case "missing-metadata":
				s.Outcomes[2].Metadata = domain.Well{}
			}
			if sql != "" { // SQL intentionally exercises finite fields without a revision bump.
				if _, e = pool.Exec(ctx, sql); e != nil {
					t.Fatal(e)
				}
			}
			e = l.(service.CohortRunLease).PersistCohort(ctx, run, s)
			if (e == nil) != (kind == "unchanged") {
				t.Fatalf("kind=%s err=%v", kind, e)
			}
			if kind != "unchanged" {
				rows, e := app.store.CohortAlerts(ctx, run)
				if e != nil || len(rows) != 0 {
					t.Fatal(rows, e)
				}
				if _, e = app.store.CohortSnapshot(ctx, "senior-v1", &run); e == nil {
					t.Fatal("denied publication wrote snapshot")
				}
			}
		})
	}
}

func TestRBACReviewOversizedIntentHTTP(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	ctx = rbacWorkerContext(t, pool, r, "collectionjobs.run")
	if _, e := pool.Exec(ctx, `WITH added AS (INSERT INTO auth_role(name) SELECT 'audit-'||lpad(n::text,4,'0')||repeat('x',100) FROM generate_series(1,650)n RETURNING id) INSERT INTO auth_user_role SELECT 1,id FROM added`); e != nil {
		t.Fatal(e)
	}
	a, e := r.Authorization(ctx, 1)
	if e != nil || len(a.Assignments.Roles) < 650 {
		t.Fatal(len(a.Assignments.Roles), e)
	}
	access, e := service.NewAccess(r, true)
	if e != nil {
		t.Fatal(e)
	}
	access.WithRBACMode(domain.RBACEnforce)
	token := "oversized-intent-session"
	sum := sha256.Sum256([]byte(token))
	if e = r.CreateSession(ctx, 1, a.Epoch, hex.EncodeToString(sum[:]), time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	jobs, e := service.NewCollectionJobs(r, true)
	if e != nil {
		t.Fatal(e)
	}
	server := &httpapi.Server{Config: config.Config{Prefix: "/api/v1", CollectionJobMode: "synthetic", RBACMode: "enforce"}, Access: access, CollectionJobs: jobs}
	raw, _ := json.Marshal(in)
	req := httptest.NewRequest("POST", "/api/v1/collection-jobs", bytes.NewReader(raw))
	req.AddCookie(&http.Cookie{Name: "pulse_session", Value: token})
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, req)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"message":"collection_intent_too_large"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, table := range []string{"collection_job", "collection_job_source_hold", "collection_job_outbox"} {
		if collectionCount(t, pool, table) != 0 {
			t.Fatal("oversized intent effects", table)
		}
	}
}
