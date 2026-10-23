package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestCollectionApplyAtomicAndUnknownCommit(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	s, e := r.AcceptCollectionJob(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	c, _ := collectionClaim(t, ctx, r)
	capture := collectionPrepared(t, ctx, r, c.Lease(), 50)
	if e = r.SaveCollectionCapture(ctx, c.Lease(), capture); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `CREATE FUNCTION reject_checkpoint() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected checkpoint failure'; END $$;CREATE TRIGGER reject_checkpoint BEFORE INSERT ON collection_job_checkpoint FOR EACH ROW EXECUTE FUNCTION reject_checkpoint()`); e != nil {
		t.Fatal(e)
	}
	if _, e = r.ApplyCollectionCapture(ctx, c.Lease()); e == nil {
		t.Fatal("SQL failure ignored")
	}
	for _, table := range []string{"telemetry_measurement", "collection_job_checkpoint", "collection_job_coverage"} {
		if collectionCount(t, pool, table) != 0 {
			t.Fatal("partial publication", table)
		}
	}
	if _, e = pool.Exec(ctx, "DROP TRIGGER reject_checkpoint ON collection_job_checkpoint"); e != nil {
		t.Fatal(e)
	}
	// Deliberately discard committed response; repeat must resolve durable checkpoint.
	if _, e = r.ApplyCollectionCapture(ctx, c.Lease()); e != nil {
		t.Fatal(e)
	}
	out, e := r.ApplyCollectionCapture(ctx, c.Lease())
	if e != nil || !out.Status.Write.Checkpoint || out.Status.State != domain.CollectionSucceeded {
		t.Fatal(out, e)
	}
	for _, table := range []string{"collection_job_checkpoint", "collection_job_coverage", "telemetry_collection_log"} {
		if collectionCount(t, pool, table) != 1 {
			t.Fatal("commit replay duplicated", table)
		}
	}
	if collectionCount(t, pool, "telemetry_measurement") != 2 {
		t.Fatal("wrong measurement count")
	}
	status, e := r.CollectionJobStatus(ctx, s.ID)
	if e != nil || status.Write.Inserted != 2 {
		t.Fatal(status, e)
	}
}
func TestCollectionPhysicalLossInsideApplyRollsBack(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	if _, e := r.AcceptCollectionJob(ctx, in); e != nil {
		t.Fatal(e)
	}
	c, _ := collectionClaim(t, ctx, r)
	capture := collectionPrepared(t, ctx, r, c.Lease(), 50)
	if e := r.SaveCollectionCapture(ctx, c.Lease(), capture); e != nil {
		t.Fatal(e)
	}
	// Trigger executes after measurement INSERT and before checkpoint publication, on the owned session.
	if _, e := pool.Exec(ctx, `CREATE FUNCTION collection_kill_owned() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_terminate_backend(pg_backend_pid()); RETURN NEW; END $$;CREATE TRIGGER collection_kill_owned BEFORE INSERT ON collection_job_checkpoint FOR EACH ROW EXECUTE FUNCTION collection_kill_owned()`); e != nil {
		t.Fatal(e)
	}
	if _, e := r.ApplyCollectionCapture(ctx, c.Lease()); e == nil {
		t.Fatal("physical connection loss accepted")
	}
	if e := r.RenewCollectionJob(ctx, c.Lease()); e == nil {
		t.Fatal("pool connection renewed lost owner")
	}
	if _, e := r.LoadCollectionWork(ctx, c.Lease()); e == nil {
		t.Fatal("old claim used pool fallback")
	}
	for _, table := range []string{"telemetry_measurement", "collection_job_checkpoint", "collection_job_coverage"} {
		if collectionCount(t, pool, table) != 0 {
			t.Fatal("physical loss committed", table)
		}
	}
	if _, e := pool.Exec(ctx, "DROP TRIGGER collection_kill_owned ON collection_job_checkpoint"); e != nil {
		t.Fatal(e)
	}
	c.Close()
	time.Sleep(31 * time.Second)
	if _, e := r.ReconcileCollectionJobs(ctx, 8); e != nil {
		t.Fatal(e)
	}
	time.Sleep(6 * time.Second)
	next, _ := collectionClaim(t, ctx, r)
	work, e := r.LoadCollectionWork(ctx, next.Lease())
	if e != nil || work.Capture == nil {
		t.Fatal("durable capture lost", e)
	}
	if _, e = r.ApplyCollectionCapture(ctx, next.Lease()); e != nil {
		t.Fatal(e)
	}
	if collectionCount(t, pool, "telemetry_measurement") != 2 {
		t.Fatal("takeover did not write exactly once")
	}
}
func TestCollectionLeaseTakeoverLateCapture(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	if _, e := r.AcceptCollectionJob(ctx, in); e != nil {
		t.Fatal(e)
	}
	old, _ := collectionClaim(t, ctx, r)
	capture := collectionPrepared(t, ctx, r, old.Lease(), 50)
	// Simulate paused Fetch and renew: real elapsed lease, no UPDATE of lease/state clocks.
	time.Sleep(31 * time.Second)
	if _, e := r.ReconcileCollectionJobs(ctx, 8); e != nil {
		t.Fatal(e)
	}
	status, e := r.CollectionJobStatus(ctx, old.Lease().JobID)
	if e != nil || status.Source.Availability != "unknown" {
		t.Fatal("worker expiry fabricated observed source failure", status, e)
	}
	time.Sleep(6 * time.Second)
	next, _ := collectionClaim(t, ctx, r)
	if e := r.SaveCollectionCapture(ctx, old.Lease(), capture); e == nil {
		t.Fatal("late capture accepted")
	}
	if e := r.RenewCollectionJob(ctx, old.Lease()); e == nil {
		t.Fatal("old fence renewed")
	}
	if _, e := r.ApplyCollectionCapture(ctx, old.Lease()); e == nil {
		t.Fatal("old apply accepted")
	}
	if _, e := r.FinishCollectionFailure(ctx, old.Lease(), domain.CollectionFailure{Code: domain.CollectionCodeInvalidCapture}); e == nil {
		t.Fatal("old failure accepted")
	}
	if collectionCount(t, pool, "collection_job_capture") != 0 {
		t.Fatal("late capture published")
	}
	if e := r.SaveCollectionCapture(ctx, next.Lease(), capture); e != nil {
		t.Fatal(e)
	}
	if _, e := r.ApplyCollectionCapture(ctx, next.Lease()); e != nil {
		t.Fatal(e)
	}
}
func TestCollectionLateIOAfterAuthorityChange(t *testing.T) {
	for _, which := range []string{"source", "account", "binding"} {
		t.Run(which, func(t *testing.T) {
			pool, ctx, r, in := collectionSetup(t)
			s, e := r.AcceptCollectionJob(ctx, in)
			if e != nil {
				t.Fatal(e)
			}
			c, _ := collectionClaim(t, ctx, r)
			capture := collectionPrepared(t, ctx, r, c.Lease(), 50)
			switch which {
			case "source":
				source, e := r.Source(ctx, in.SourceID)
				if e != nil {
					t.Fatal(e)
				}
				source.Name = "edited"
				if _, e = mutateSourceFixture(domain.WithSystemPrincipal(ctx), r, domain.SourceUpdate, source); e != nil {
					t.Fatal(e)
				}
			case "account":
				if _, e = pool.Exec(ctx, "UPDATE auth_user SET revision=revision+1 WHERE id=1"); e != nil {
					t.Fatal(e)
				}
			case "binding":
				if _, e = pool.Exec(ctx, "UPDATE well SET unitek_id=-900001 WHERE id=$1", s.WellID); e != nil {
					t.Fatal(e)
				}
			}
			if e = r.SaveCollectionCapture(ctx, c.Lease(), capture); e == nil {
				t.Fatal("late IO wrote capture")
			}
			if _, e = r.FinishCollectionFailure(ctx, c.Lease(), domain.CollectionFailure{Code: domain.CollectionCodeSourceUnavailable, Retryable: true}); e == nil {
				t.Fatal("stale failure overwrote revoked")
			}
			got, e := r.CollectionJobStatus(ctx, s.ID)
			if e != nil || got.State != domain.CollectionRevoked {
				t.Fatal(got, e)
			}
			if collectionCount(t, pool, "telemetry_measurement") != 0 || collectionCount(t, pool, "collection_job_capture") != 0 {
				t.Fatal("late IO effects")
			}
		})
	}
}
func TestCollectionAuthorityCancelAndRetry(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	s, e := r.AcceptCollectionJob(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	c, _ := collectionClaim(t, ctx, r)
	capture := collectionPrepared(t, ctx, r, c.Lease(), 50)
	if e = r.SaveCollectionCapture(ctx, c.Lease(), capture); e != nil {
		t.Fatal(e)
	}
	current, e := r.CollectionJobStatus(ctx, s.ID)
	if e != nil {
		t.Fatal(e)
	}
	start := make(chan struct{})
	errs := make(chan error, 2)
	go func() { <-start; _, e := r.ApplyCollectionCapture(ctx, c.Lease()); errs <- e }()
	go func() { <-start; _, e := r.CancelCollectionJob(ctx, s.ID, current.Revision); errs <- e }()
	close(start)
	<-errs
	<-errs
	got, e := r.CollectionJobStatus(ctx, s.ID)
	if e != nil {
		t.Fatal(e)
	}
	n := collectionCount(t, pool, "telemetry_measurement")
	if got.State == domain.CollectionSucceeded && n != 2 || got.State == domain.CollectionCancelled && n != 0 || got.State != domain.CollectionSucceeded && got.State != domain.CollectionCancelled {
		t.Fatal(got, n)
	}
	if _, e = r.CancelCollectionJob(ctx, s.ID, 1); e != nil {
		t.Fatal("terminal cancellation not idempotent", e)
	}
	if collectionCount(t, pool, "collection_job_capture") != 1 {
		t.Fatal("cancel removed capture")
	}
	foreign := int64(2)
	other := domain.WithPrincipal(context.Background(), domain.Principal{Authenticated: true, AccountID: &foreign, Mechanism: "session"})
	if _, e = r.CollectionJobStatus(other, s.ID); e == nil || !strings.Contains(e.Error(), "collection_job_missing") {
		t.Fatal("foreign disclosure", e)
	}
}

func TestCollectionAuthorityAcceptanceLock(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	tx, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "UPDATE auth_user SET active=false,revision=revision+1 WHERE id=1"); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := r.AcceptCollectionJob(ctx, in); done <- e }()
	select {
	case e := <-done:
		t.Fatal("acceptance bypassed current account row lock", e)
	case <-time.After(100 * time.Millisecond):
	}
	if collectionCount(t, pool, "collection_job") != 0 {
		t.Fatal("job accepted before current authority serialized")
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-done:
		if e == nil {
			t.Fatal("disabled account accepted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("acceptance blocked after authority commit")
	}
	if collectionCount(t, pool, "collection_job") != 0 || collectionCount(t, pool, "collection_job_source_hold") != 0 {
		t.Fatal("denied acceptance left effects")
	}
}

// Owned serializer wait входит в caller deadline; иначе renew ждёт Apply до10s.
func TestCollectionAuthorityOwnedOperationDeadline(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	if _, e := r.AcceptCollectionJob(ctx, in); e != nil {
		t.Fatal(e)
	}
	c, _ := collectionClaim(t, ctx, r)
	capture := collectionPrepared(t, ctx, r, c.Lease(), 50)
	if e := r.SaveCollectionCapture(ctx, c.Lease(), capture); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `CREATE FUNCTION collection_slow_checkpoint() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.5); RETURN NEW; END $$;CREATE TRIGGER collection_slow_checkpoint BEFORE INSERT ON collection_job_checkpoint FOR EACH ROW EXECUTE FUNCTION collection_slow_checkpoint()`); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := r.ApplyCollectionCapture(ctx, c.Lease()); done <- e }()
	// Observe the owned physical query rather than assuming goroutine scheduling.
	until := time.Now().Add(2 * time.Second)
	for {
		var busy bool
		if e := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event='PgSleep')`, c.Lease().PhysicalBackendPID).Scan(&busy); e != nil {
			t.Fatal(e)
		}
		if busy {
			break
		}
		if time.Now().After(until) {
			t.Fatal("Apply did not reach checkpoint barrier")
		}
		time.Sleep(5 * time.Millisecond)
	}
	bounded, cancel := context.WithTimeout(ctx, 40*time.Millisecond)
	start := time.Now()
	e := r.RenewCollectionJob(bounded, c.Lease())
	cancel()
	elapsed := time.Since(start)
	if e == nil || elapsed > 200*time.Millisecond {
		t.Fatalf("owned operation ignored deadline: error=%v elapsed=%s", e, elapsed)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}

func TestCollectionAuthorityGuardMismatchReconcilesRevoked(t *testing.T) {
	pool, ctx, r, in := collectionSetup(t)
	s, e := r.AcceptCollectionJob(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	// Explicit operator authority change, not a fabricated lease/deadline transition.
	if _, e = pool.Exec(ctx, "UPDATE collection_job_guard SET manifest_sha256=repeat('b',64) WHERE id=true"); e != nil {
		t.Fatal(e)
	}
	if _, e = r.ReconcileCollectionJobs(ctx, 8); e != nil {
		t.Fatal("definite guard denial did not durably revoke", e)
	}
	got, e := r.CollectionJobStatus(ctx, s.ID)
	if e != nil || got.State != domain.CollectionRevoked {
		t.Fatal(got, e)
	}
	if collectionCount(t, pool, "collection_job_source_hold") != 0 {
		t.Fatal("revoked guard retained hold")
	}
}
