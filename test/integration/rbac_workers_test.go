package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/migrations"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func testRBACFileClaimRevocation(t *testing.T) {
	p, ctx, r, checks, v := fileJobSetup(t)
	// A real persisted grant through a group; the old role is history only.
	_, e := p.Exec(ctx, `UPDATE auth_user SET organization_scope='{"kind":"organization","ngdu_id":null,"department_id":null}'; INSERT INTO auth_group(name) VALUES('operators'); INSERT INTO auth_group_role SELECT g.id,r.id FROM auth_group g,auth_role r WHERE r.builtin_key='technologist'; INSERT INTO auth_group_member SELECT id,1 FROM auth_group`)
	if e != nil {
		t.Fatal(e)
	}
	a, e := r.Authorization(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	account, e := r.Account(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	principal := domain.AccountPrincipal(account)
	principal.Authorization = &a
	ctx = domain.WithRBACMode(domain.WithPrincipal(ctx, principal), domain.RBACEnforce)
	accepted, e := r.AcceptFileJob(ctx, v)
	if e != nil {
		t.Fatal(e)
	}
	// Revoking an assignment increments the epoch before the worker reads input.
	if _, e = p.Exec(ctx, `DELETE FROM auth_group_member; UPDATE auth_user SET authz_epoch=authz_epoch+1`); e != nil {
		t.Fatal(e)
	}
	worker, e := service.NewFileJobWorker(r, checks, "11111111111111111111111111111111")
	if e != nil {
		t.Fatal(e)
	}
	work := domain.WithRBACMode(context.Background(), domain.RBACEnforce)
	if e = worker.HandleDelivery(work, fileJobMessage(t, p, accepted.ID)); e != nil {
		t.Fatal(e)
	}
	var state string
	var results int
	if e = p.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM file_check_result) FROM file_job WHERE id=$1`, accepted.ID).Scan(&state, &results); e != nil {
		t.Fatal(e)
	}
	if state != "revoked" || results != 0 {
		t.Fatalf("revoked job published: %s results=%d", state, results)
	}
}

func TestRBACLegacyScheduledActors(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	id := rbacReadUser(t, p, "runner", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "runs.start", "runs.read", "risks.read", "wells.read")
	if _, e := p.Exec(ctx, `UPDATE auth_user SET scope='{"funds":["operational","presentation"]}' WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	a, e := r.Authorization(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	account, e := r.Account(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	principal := domain.AccountPrincipal(account)
	principal.Authorization = &a
	ctx = domain.WithRBACMode(domain.WithPrincipal(ctx, principal), domain.RBACEnforce)
	lease, ok, e := r.Reserve(ctx)
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	defer lease.Close()
	run, e := lease.Begin(ctx, "manual")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if e = lease.Persist(ctx, run, domain.Snapshot{}); e == nil {
		t.Fatal("revoked initiator published run")
	}
	var done int
	if e = p.QueryRow(ctx, `SELECT count(*) FROM ml_run WHERE status='done'`).Scan(&done); e != nil || done != 0 {
		t.Fatal(done, e)
	}
}

func TestRBACActivation(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	id := rbacManager(t, p)
	if e := r.CreateSession(ctx, id, 1, "before-cutover", time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	activator, ok := any(r).(interface{ ActivateRBAC(context.Context) error })
	if !ok {
		t.Fatal("activation is not implemented")
	}
	if e := activator.ActivateRBAC(ctx); e != nil {
		t.Fatal(e)
	}
	if e := activator.ActivateRBAC(ctx); e != nil {
		t.Fatal(e)
	}
	var epoch int64
	var sessions int
	var activated bool
	if e := p.QueryRow(ctx, `SELECT authz_epoch,(SELECT count(*) FROM auth_session),(SELECT activated_at IS NOT NULL FROM auth_rbac_state) FROM auth_user WHERE id=$1`, id).Scan(&epoch, &sessions, &activated); e != nil {
		t.Fatal(e)
	}
	if epoch != 2 || sessions != 0 || !activated {
		t.Fatal(epoch, sessions, activated)
	}
}

func TestRBACWorkerSchema(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	var valid bool
	for _, raw := range []string{`null`, `{}`, `{"version":"access_v1","account_id":1,"epoch":1,"roles":[],"extra":true}`, `{"version":"access_v1","account_id":1.5,"epoch":1,"roles":[]}`, `{"version":"access_v1","account_id":1,"epoch":9223372036854775808,"roles":[]}`, `{"version":"access_v1","account_id":1,"epoch":1,"roles":[{"id":1,"name":"a","revision":1,"builtin_key":null},{"id":1,"name":"b","revision":1,"builtin_key":null}]}`} {
		if e := p.QueryRow(ctx, `SELECT valid_job_authorization($1::jsonb)`, raw).Scan(&valid); e != nil {
			t.Fatal(e)
		}
		if valid {
			t.Fatal("invalid frame accepted", raw)
		}
	}
	if e := p.QueryRow(ctx, `SELECT valid_job_authorization('{"version":"access_v1","account_id":9007199254740993,"epoch":1,"roles":[]}'::jsonb)`).Scan(&valid); e != nil || !valid {
		t.Fatal(valid, e)
	}
}

// rbacWorkerContext выдаёт реальные права через группу, projection остаётся viewer.
func rbacWorkerContext(t *testing.T, p *pgxpool.Pool, r *postgres.Repository, permission string) context.Context {
	t.Helper()
	ctx := context.Background()
	_, err := p.Exec(ctx, `UPDATE auth_user SET role='viewer',scope='{"funds":["operational","presentation"]}',organization_scope='{"kind":"department","ngdu_id":0,"department_id":0}' WHERE id=1; INSERT INTO auth_group(name)VALUES('rbac-worker'); INSERT INTO auth_group_role SELECT g.id,r.id FROM auth_group g,auth_role r WHERE g.name='rbac-worker' AND r.builtin_key='admin'; INSERT INTO auth_group_member SELECT id,1 FROM auth_group WHERE name='rbac-worker'`)
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.Authorization(ctx, 1)
	if err != nil || a.Require(domain.Permission(permission)) != nil {
		t.Fatal(a, err)
	}
	account, err := r.Account(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	principal := domain.AccountPrincipal(account)
	principal.Authorization = &a
	return domain.WithRBACMode(domain.WithPrincipal(ctx, principal), domain.RBACEnforce)
}
func rbacWorkerMutation(t *testing.T, p *pgxpool.Pool, ctx context.Context, kind, permission string) {
	t.Helper()
	var sql string
	switch kind {
	case "membership":
		sql = `DELETE FROM auth_group_member WHERE user_id=1;UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=1`
	case "role":
		sql = `DELETE FROM auth_role_permission WHERE permission_key=$1`
	case "epoch":
		sql = `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=1`
	case "scope":
		sql = `UPDATE auth_user SET organization_scope='{"kind":"none","ngdu_id":null,"department_id":null}' WHERE id=1`
	case "funds":
		sql = `UPDATE auth_user SET scope='{"funds":[]}' WHERE id=1`
	case "inactive":
		sql = `UPDATE auth_user SET active=false WHERE id=1`
	case "well":
		sql = `UPDATE well SET ngdu=99`
	}
	var err error
	if kind == "role" {
		_, err = p.Exec(ctx, sql, permission)
	} else {
		_, err = p.Exec(ctx, sql)
	}
	if err != nil {
		t.Fatal(err)
	}
}
func TestRBACWorkerRevocation(t *testing.T) {
	t.Run("file-before-claim", testRBACFileClaimRevocation)
	for _, kind := range []string{"membership", "role", "epoch", "scope", "funds", "inactive", "well"} {
		t.Run("file-final-"+kind, func(t *testing.T) {
			p, _, r, checks, v := fileJobSetup(t)
			ctx := rbacWorkerContext(t, p, r, "filechecks.run")
			work := domain.WithRBACMode(context.Background(), domain.RBACEnforce)
			j, err := r.AcceptFileJob(ctx, v)
			if err != nil {
				t.Fatal(err)
			}
			lease, err := r.ClaimFileJob(work, fileJobMessage(t, p, j.ID), strings.Repeat("1", 32))
			if err != nil || lease == nil {
				t.Fatal(lease, err)
			}
			result, err := checks.ComputeStoredInput(ctx, v.Input.ID)
			if err != nil {
				t.Fatal(err)
			}
			rbacWorkerMutation(t, p, ctx, kind, "filechecks.run")
			if err = r.RenewFileJob(work, *lease); err == nil {
				t.Fatal("revoked heartbeat accepted")
			}
			if _, err = r.ExecutionFileJob(work, *lease); err == nil {
				t.Fatal("revoked input read accepted")
			}
			if _, err = r.FinishFileJob(work, *lease, &result, nil); err != nil {
				t.Fatal(err)
			}
			var state string
			if err = p.QueryRow(ctx, `SELECT state FROM file_job WHERE id=$1`, j.ID).Scan(&state); err != nil || state != "revoked" {
				t.Fatal(state, err)
			}
			for _, table := range []string{"file_check_result", "manual_check"} {
				if collectionCount(t, p, table) != 0 {
					t.Fatal("publication after revoke", table)
				}
			}
		})
		t.Run("cohort-checkpoint-"+kind, func(t *testing.T) {
			p, _, r, v := cohortJobSetup(t)
			ctx := rbacWorkerContext(t, p, r, "cohortjobs.run")
			work := domain.WithRBACMode(context.Background(), domain.RBACEnforce)
			j, err := r.AcceptCohortJob(ctx, v)
			if err != nil {
				t.Fatal(err)
			}
			claim, err := r.ClaimCohortJob(work, cohortMessage(t, p, j.ID), strings.Repeat("1", 32))
			if err != nil || claim == nil {
				t.Fatal(err)
			}
			defer claim.Close()
			x, err := r.NextCohortMember(work, claim.Lease())
			if err != nil || x == nil {
				t.Fatal(err)
			}
			checkpoint := cohortMarkerCheckpoint(t, *x)
			rbacWorkerMutation(t, p, ctx, kind, "cohortjobs.run")
			if err = r.CheckpointCohortMember(work, claim.Lease(), checkpoint); err == nil {
				t.Fatal("revoked checkpoint accepted")
			}
			if _, err = r.FinishCohortJob(work, claim.Lease(), nil); err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"cohort_job_checkpoint", "ml_run_snapshot", "ml_alert"} {
				if collectionCount(t, p, table) != 0 {
					t.Fatal("publication after revoke", table)
				}
			}
		})
		t.Run("collection-final-"+kind, func(t *testing.T) {
			p, _, r, in := collectionSetup(t)
			ctx := rbacWorkerContext(t, p, r, "collectionjobs.run")
			if _, err := r.AcceptCollectionJob(ctx, in); err != nil {
				t.Fatal(err)
			}
			work := domain.WithRBACMode(context.Background(), domain.RBACEnforce)
			claim, _ := collectionClaim(t, work, r)
			defer claim.Close()
			capture := collectionPrepared(t, work, r, claim.Lease(), 50)
			if err := r.SaveCollectionCapture(work, claim.Lease(), capture); err != nil {
				t.Fatal(err)
			}
			rbacWorkerMutation(t, p, ctx, kind, "collectionjobs.run")
			// Existing protocol may return revoked disposition with no Go error.
			_, _ = r.ApplyCollectionCapture(work, claim.Lease())
			for _, table := range []string{"telemetry_measurement", "collection_job_checkpoint", "collection_job_coverage"} {
				if collectionCount(t, p, table) != 0 {
					t.Fatal("publication after revoke", table)
				}
			}
		})
	}
}

func TestRBACMissingStorageContext(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	// SQL builds the fixture without pretending to be an application caller.
	if _, err := p.Exec(ctx, `INSERT INTO well(ngdu,department,active)VALUES(0,0,true)`); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []domain.RBACMode{domain.RBACCompat, domain.RBACEnforce} {
		t.Run(string(mode), func(t *testing.T) {
			raw := domain.WithRBACMode(ctx, mode)
			wells, err := r.Wells(raw, false)
			if err == nil && len(wells) > 0 {
				t.Fatal("missing context exposed well rows")
			}
			if _, err = r.CreateWell(raw, domain.Well{NGDU: 0, Department: 0, Active: true}); err == nil {
				t.Fatal("missing context created well")
			}
		})
	}
}

func TestRBACLegacyCollectEpoch(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	r := postgres.New(p)
	id := rbacReadUser(t, p, "collector", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "integrations.collect", "integrations.read", "telemetry.import", "telemetry.read", "wells.read")
	if _, err := p.Exec(ctx, `UPDATE auth_user SET scope='{"funds":["operational","presentation"]}' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	a, err := r.Authorization(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	account, err := r.Account(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	principal := domain.AccountPrincipal(account)
	principal.Authorization = &a
	ctx = domain.WithRBACMode(domain.WithPrincipal(ctx, principal), domain.RBACEnforce)
	source, err := r.SaveSource(ctx, domain.TelemetrySource{ID: "rbac-collect", Status: "running", Kind: "json_pull", IntervalSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := r.ClaimSource(ctx, source.ID, false)
	if err != nil || lease == nil {
		t.Fatal(lease, err)
	}
	if _, err = p.Exec(ctx, `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: source.ID, ProfileVersion: source.ProfileVersion, Lease: lease, Release: true}); err == nil {
		t.Fatal("revoked collection published receipt")
	}
	if collectionCount(t, p, "telemetry_collection_log") != 0 {
		t.Fatal("successful receipt after revoke")
	}
	if err = r.FailCollection(ctx, *lease, "access_revoked"); err != nil {
		t.Fatal(err)
	}
	var released bool
	if err = p.QueryRow(ctx, `SELECT writer_token_sha256 IS NULL AND writer_execution_actor IS NULL AND writer_authorization IS NULL FROM telemetry_source WHERE id=$1`, source.ID).Scan(&released); err != nil || !released {
		t.Fatal(released, err)
	}
}

func TestRBACActivationDrainAndReadiness(t *testing.T) {
	for _, kind := range []string{"running-ml", "writer", "incomplete-schema", "unknown-key"} {
		t.Run(kind, func(t *testing.T) {
			p := testDB(t)
			ctx := context.Background()
			r := postgres.New(p)
			id := rbacManager(t, p)
			if err := r.CreateSession(ctx, id, 1, "keep-session", time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "running-ml":
				if _, err := p.Exec(ctx, `INSERT INTO ml_run(status,trigger,started_at)VALUES('running','manual',clock_timestamp())`); err != nil {
					t.Fatal(err)
				}
			case "writer":
				if _, err := p.Exec(ctx, `INSERT INTO telemetry_source(id,config,lease_until)VALUES('held','{}',clock_timestamp()+interval '1 hour')`); err != nil {
					t.Fatal(err)
				}
			case "incomplete-schema":
				if _, err := p.Exec(ctx, `DROP TRIGGER ml_run_rbac_authority_immutable ON ml_run`); err != nil {
					t.Fatal(err)
				}
			case "unknown-key":
				if _, err := p.Exec(ctx, `INSERT INTO auth_role_permission SELECT id,'future.unknown' FROM auth_role WHERE builtin_key='viewer'`); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.ActivateRBAC(ctx); err == nil {
				t.Fatal("unsafe activation accepted", kind)
			}
			var epoch int64
			var sessions int
			var active bool
			if err := p.QueryRow(ctx, `SELECT authz_epoch,(SELECT count(*) FROM auth_session),(SELECT activated_at IS NOT NULL FROM auth_rbac_state) FROM auth_user WHERE id=$1`, id).Scan(&epoch, &sessions, &active); err != nil {
				t.Fatal(err)
			}
			if epoch != 1 || sessions != 1 || active {
				t.Fatal("failed activation mutated authority", epoch, sessions, active)
			}
		})
	}
	p := testDB(t)
	r := postgres.New(p)
	ctx := context.Background()
	rbacManager(t, p)
	if err := r.ValidateRBAC(ctx, domain.RBACEnforce); err == nil {
		t.Fatal("enforce before activation accepted")
	}
	if err := r.ValidateRBAC(ctx, domain.RBACCompat); err != nil {
		t.Fatal(err)
	}
	if err := r.ActivateRBAC(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.ValidateRBAC(ctx, domain.RBACCompat); err == nil {
		t.Fatal("compat downgrade accepted")
	}
	if err := r.ValidateRBAC(ctx, domain.RBACEnforce); err != nil {
		t.Fatal(err)
	}
}

func TestRBACWorkerSchemaImmutableRepeat(t *testing.T) {
	p, ctx, r, _, v := fileJobSetup(t)
	j, err := r.AcceptFileJob(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `INSERT INTO ml_run(status,trigger,started_at)VALUES('done','historical',clock_timestamp())`); err != nil {
		t.Fatal(err)
	}
	for _, repeat := range []bool{false, true} {
		if repeat {
			if err = migrations.Apply(ctx, p); err != nil {
				t.Fatal(err)
			}
		}
		for _, q := range []string{
			`UPDATE file_job SET actor_authorization=NULL`,
			`UPDATE file_job SET actor_authorization=jsonb_set(actor_authorization,'{epoch}','999')`,
			`UPDATE ml_run SET execution_actor='system_ml'`,
		} {
			if _, err = p.Exec(ctx, q); err == nil {
				t.Fatal("immutable authority changed", repeat, q)
			}
		}
		var present bool
		if err = p.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='cohort_job'::regclass AND conname='cohort_job_actor_role_access')`).Scan(&present); err != nil || !present {
			t.Fatal(present, err)
		}
	}
	var raw string
	if err = p.QueryRow(ctx, `SELECT actor_authorization->>'version' FROM file_job WHERE id=$1`, j.ID).Scan(&raw); err != nil || raw != "access_v1" {
		t.Fatal(raw, err)
	}
}

func TestRBACWorkerOwnerConstraint(t *testing.T) {
	bad := `{"version":"access_v1","account_id":999,"epoch":1,"roles":[]}`
	t.Run("file", func(t *testing.T) {
		p, ctx, r, _, v := fileJobSetup(t)
		if _, err := p.Exec(ctx, `CREATE FUNCTION corrupt_job_auth()RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.actor_authorization='`+bad+`'::jsonb;RETURN NEW;END$$;CREATE TRIGGER corrupt_job_auth BEFORE INSERT ON file_job FOR EACH ROW EXECUTE FUNCTION corrupt_job_auth()`); err != nil {
			t.Fatal(err)
		}
		if _, err := r.AcceptFileJob(ctx, v); err == nil {
			t.Fatal("owner mismatch accepted")
		}
		for _, table := range []string{"file_job", "file_input", "file_job_outbox"} {
			if collectionCount(t, p, table) != 0 {
				t.Fatal("partial acceptance", table)
			}
		}
	})
	t.Run("cohort", func(t *testing.T) {
		p, ctx, r, v := cohortJobSetup(t)
		if _, err := p.Exec(ctx, `CREATE FUNCTION corrupt_job_auth()RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.actor_authorization='`+bad+`'::jsonb;RETURN NEW;END$$;CREATE TRIGGER corrupt_job_auth BEFORE INSERT ON cohort_job FOR EACH ROW EXECUTE FUNCTION corrupt_job_auth()`); err != nil {
			t.Fatal(err)
		}
		if _, err := r.AcceptCohortJob(ctx, v); err == nil {
			t.Fatal("owner mismatch accepted")
		}
		for _, table := range []string{"cohort_job", "ml_run", "cohort_job_outbox"} {
			if collectionCount(t, p, table) != 0 {
				t.Fatal("partial acceptance", table)
			}
		}
	})
	t.Run("collection", func(t *testing.T) {
		p, ctx, r, in := collectionSetup(t)
		if _, err := p.Exec(ctx, `CREATE FUNCTION corrupt_job_auth()RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.intent=jsonb_set(NEW.intent,'{authorization}','`+bad+`'::jsonb);RETURN NEW;END$$;CREATE TRIGGER corrupt_job_auth BEFORE INSERT ON collection_job FOR EACH ROW EXECUTE FUNCTION corrupt_job_auth()`); err != nil {
			t.Fatal(err)
		}
		if _, err := r.AcceptCollectionJob(ctx, in); err == nil {
			t.Fatal("owner mismatch accepted")
		}
		for _, table := range []string{"collection_job", "collection_job_source_hold", "collection_job_outbox"} {
			if collectionCount(t, p, table) != 0 {
				t.Fatal("partial acceptance", table)
			}
		}
	})
}

func TestRBACWorkerSQLFailureRetry(t *testing.T) {
	p, _, r, checks, v := fileJobSetup(t)
	ctx := rbacWorkerContext(t, p, r, "filechecks.run")
	work := domain.WithRBACMode(context.Background(), domain.RBACEnforce)
	j, err := r.AcceptFileJob(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := r.ClaimFileJob(work, fileJobMessage(t, p, j.ID), strings.Repeat("a", 32))
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	result, err := checks.ComputeStoredInput(ctx, v.Input.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Exec(ctx, `CREATE FUNCTION reject_result()RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'injected SQL failure';END$$;CREATE TRIGGER reject_result BEFORE INSERT ON file_check_result FOR EACH ROW EXECUTE FUNCTION reject_result()`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.FinishFileJob(work, *lease, &result, nil); err == nil {
		t.Fatal("SQL error ignored")
	}
	var state string
	if err = p.QueryRow(ctx, `SELECT state FROM file_job WHERE id=$1`, j.ID).Scan(&state); err != nil || state != "running" {
		t.Fatal("SQL failure marked revoked", state, err)
	}
	if collectionCount(t, p, "file_check_result") != 0 {
		t.Fatal("partial result")
	}
	if _, err = p.Exec(ctx, `DROP TRIGGER reject_result ON file_check_result`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.FinishFileJob(work, *lease, &result, nil); err != nil {
		t.Fatal(err)
	}
	if collectionCount(t, p, "file_check_result") != 1 {
		t.Fatal("same fenced attempt could not publish after SQL recovery")
	}
}

func TestRBACWorkerGraphPublicationOrder(t *testing.T) {
	p, _, r, checks, v := fileJobSetup(t)
	ctx := rbacWorkerContext(t, p, r, "filechecks.run")
	work := domain.WithRBACMode(context.Background(), domain.RBACEnforce)
	root := rbacManager(t, p)
	j, err := r.AcceptFileJob(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := r.ClaimFileJob(work, fileJobMessage(t, p, j.ID), strings.Repeat("b", 32))
	if err != nil || lease == nil {
		t.Fatal(err)
	}
	result, err := checks.ComputeStoredInput(ctx, v.Input.ID)
	if err != nil {
		t.Fatal(err)
	}
	change := rbacChange(t, r, 1)
	change.Name = "changed during compute"
	change.Login = "worker"
	payload := domain.AccessChange{User: &change}
	preview, err := r.PreviewAccessChange(ctx, root, payload, domain.AccessPageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(ctx)
	if _, err = blocker.Exec(ctx, `SELECT id FROM auth_user WHERE id=1 FOR SHARE`); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(work, 3*time.Second)
	defer cancel()
	edits := make(chan error, 1)
	publication := make(chan error, 1)
	go func() {
		_, e := r.ApplyAccessChange(bounded, root, domain.AccessApply{Change: payload, GraphRevision: preview.GraphRevision, ActorEpoch: preview.ActorEpoch, Fingerprint: preview.Fingerprint}, nil)
		edits <- e
	}()
	go func() { _, e := r.FinishFileJob(bounded, *lease, &result, nil); publication <- e }()
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, done := range []chan error{edits, publication} {
		select {
		case err = <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-bounded.Done():
			t.Fatal("graph/user publication lock order stalled")
		}
	}
	var state string
	if err = p.QueryRow(ctx, `SELECT state FROM file_job WHERE id=$1`, j.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	count := collectionCount(t, p, "file_check_result")
	if state == "succeeded" && count != 1 || state == "revoked" && count != 0 || (state != "succeeded" && state != "revoked") {
		t.Fatal("non-linearized output", state, count)
	}
}

func rbacOrganizationWorker(t *testing.T, p *pgxpool.Pool, r *postgres.Repository, permission string) context.Context {
	t.Helper()
	ctx := rbacWorkerContext(t, p, r, permission)
	if _, err := p.Exec(ctx, `UPDATE auth_user SET organization_scope='{"kind":"organization","ngdu_id":null,"department_id":null}' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	a, err := r.Authorization(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	principal, _ := domain.CurrentPrincipal(ctx)
	principal.Authorization = &a
	return domain.WithPrincipal(ctx, principal)
}
func mutateAcceptedWell(t *testing.T, p *pgxpool.Pool, kind string) {
	t.Helper()
	var q string
	switch kind {
	case "unchanged":
		return
	case "ngdu":
		q = `UPDATE well SET ngdu=99 WHERE id=1`
	case "department":
		q = `UPDATE well SET department=99 WHERE id=1`
	case "fund":
		q = `UPDATE well SET fund_type=CASE WHEN fund_type='operational' THEN 'presentation' ELSE 'operational' END WHERE id=1`
	case "active":
		q = `UPDATE well SET active=false WHERE id=1`
	case "archived":
		q = `UPDATE well SET archived_at=clock_timestamp() WHERE id=1`
	case "unitek":
		q = `UPDATE well SET unitek_id=123456789 WHERE id=1`
	case "armits":
		q = `UPDATE well SET armits_id=123456789 WHERE id=1`
	case "aba":
		q = `UPDATE well SET ngdu=99,revision=revision+1 WHERE id=1;UPDATE well SET ngdu=0,revision=revision+1 WHERE id=1`
	case "metadata":
		q = `UPDATE well SET responsible='changed',revision=revision+1 WHERE id=1`
	}
	if _, err := p.Exec(context.Background(), q); err != nil {
		t.Fatal(err)
	}
}
func TestRBACWorkerWellIdentity(t *testing.T) {
	for _, kind := range []string{"unchanged", "ngdu", "department", "fund", "active", "archived", "unitek", "armits", "aba", "metadata"} {
		t.Run("Q1/"+kind, func(t *testing.T) {
			p, _, r, checks, v := fileJobSetup(t)
			ctx := rbacOrganizationWorker(t, p, r, "filechecks.run")
			work := domain.WithRBACMode(context.Background(), domain.RBACEnforce)
			j, err := r.AcceptFileJob(ctx, v)
			if err != nil {
				t.Fatal(err)
			}
			lease, err := r.ClaimFileJob(work, fileJobMessage(t, p, j.ID), strings.Repeat("c", 32))
			if err != nil || lease == nil {
				t.Fatal(err)
			}
			result, err := checks.ComputeStoredInput(ctx, v.Input.ID)
			if err != nil {
				t.Fatal(err)
			}
			mutateAcceptedWell(t, p, kind)
			out, err := r.FinishFileJob(work, *lease, &result, nil)
			if err != nil {
				t.Fatal(err)
			}
			expected := domain.FileJobRevoked
			count := 0
			if kind == "unchanged" {
				expected = domain.FileJobSucceeded
				count = 1
			}
			if out.Status.State != expected || collectionCount(t, p, "file_check_result") != count {
				t.Fatal("changed accepted well published", out.Status.State, kind)
			}
		})
		t.Run("Q2/"+kind, func(t *testing.T) {
			p, _, r, v := cohortJobSetup(t)
			ctx := rbacOrganizationWorker(t, p, r, "cohortjobs.run")
			work := domain.WithRBACMode(context.Background(), domain.RBACEnforce)
			j, err := r.AcceptCohortJob(ctx, v)
			if err != nil {
				t.Fatal(err)
			}
			claim, err := r.ClaimCohortJob(work, cohortMessage(t, p, j.ID), strings.Repeat("d", 32))
			if err != nil || claim == nil {
				t.Fatal(err)
			}
			defer claim.Close()
			steps := 1
			if kind == "unchanged" || kind == "ngdu" {
				steps = 100
			}
			var last domain.CohortCheckpoint
			for i := 0; i < steps; i++ {
				if i%10 == 0 {
					if e := r.RenewCohortJob(work, claim.Lease()); e != nil {
						t.Fatal(e)
					}
				}
				x, e := r.NextCohortMember(work, claim.Lease())
				if e != nil || x == nil {
					t.Fatal(e)
				}
				last = cohortMarkerCheckpoint(t, *x)
				if e = r.CheckpointCohortMember(work, claim.Lease(), last); e != nil {
					t.Fatal(e)
				}
			}
			mutateAcceptedWell(t, p, kind)
			if steps == 1 {
				if _, e := r.NextCohortMember(work, claim.Lease()); e == nil {
					t.Fatal("changed accepted well allowed Next")
				}
				if e := r.CheckpointCohortMember(work, claim.Lease(), last); e == nil {
					t.Fatal("changed accepted well allowed checkpoint")
				}
			}
			if _, err = r.FinishCohortJob(work, claim.Lease(), nil); err != nil {
				t.Fatal(err)
			}
			expected := "revoked"
			snapshots := 0
			if kind == "unchanged" {
				expected = "succeeded"
				snapshots = 1
			}
			var state string
			var done bool
			if err = p.QueryRow(ctx, `SELECT state,(SELECT status='done' FROM ml_run WHERE id=$2) FROM cohort_job WHERE id=$1`, j.ID, j.RunID).Scan(&state, &done); err != nil {
				t.Fatal(err)
			}
			if state != expected || collectionCount(t, p, "ml_run_snapshot") != snapshots || done != (kind == "unchanged") {
				t.Fatal("changed accepted well published", state, done, kind)
			}
		})
	}
}

func TestRBACWorkerWellFrameSchema(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	valid := `{"version":"file_job_well_v1","well_id":1,"revision":2,"fund_type":"operational","ngdu":-1,"department":0,"active":true,"archived_at":null,"unitek_id":-9007199254740993,"armits_id":0}`
	var ok bool
	if err := p.QueryRow(ctx, `SELECT valid_file_job_well_authority($1::jsonb)`, valid).Scan(&ok); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for _, bad := range []string{`null`, `{}`, strings.Replace(valid, `"revision":2`, `"revision":0`, 1), strings.Replace(valid, `"department":0`, `"department":1.5`, 1), strings.Replace(valid, `"fund_type":"operational"`, `"fund_type":null`, 1), strings.Replace(valid, `"archived_at":null`, `"archived_at":"2026-02-30T00:00:00Z"`, 1), strings.Replace(valid, `"archived_at":null`, `"archived_at":"2026-01-01T00:00:00"`, 1), strings.Replace(valid, `"armits_id":0`, `"armits_id":9223372036854775808`, 1)} {
		if err := p.QueryRow(ctx, `SELECT valid_file_job_well_authority($1::jsonb)`, bad).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatal("bad accepted well frame", bad)
		}
	}
	p2, session, r, _, v := fileJobSetup(t)
	if _, err := r.AcceptFileJob(session, v); err != nil {
		t.Fatal(err)
	}
	for _, repeat := range []bool{false, true} {
		if repeat {
			if err := migrations.Apply(ctx, p2); err != nil {
				t.Fatal(err)
			}
		}
		for _, q := range []string{`UPDATE file_job SET well_authority=NULL`, `UPDATE file_job SET well_id=well_id+1`, `UPDATE file_job SET well_authority=jsonb_set(well_authority,'{revision}','9')`} {
			if _, err := p2.Exec(ctx, q); err == nil {
				t.Fatal("well identity mutable", repeat, q)
			}
		}
	}
}
