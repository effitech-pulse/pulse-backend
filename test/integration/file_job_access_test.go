package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestFileJobClaimLeaseAndSlot(t *testing.T) {
	pool, ctx, store, checks, v := fileJobSetup(t)
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	msg := fileJobMessage(t, pool, v.Input.ID)
	lease, e := store.ClaimFileJob(context.Background(), msg, strings.Repeat("c", 32))
	if e != nil || lease == nil {
		t.Fatal(lease, e)
	}
	duplicate, e := store.ClaimFileJob(context.Background(), msg, strings.Repeat("d", 32))
	if e != nil || duplicate != nil {
		t.Fatal("duplicate claim", duplicate, e)
	}
	next := v
	next.Input = prepareSaved(t, checks, ctx, "q1-slot-request002")
	if _, e = store.AcceptFileJob(ctx, next); e != nil {
		t.Fatal(e)
	}
	busy, e := store.ClaimFileJob(context.Background(), fileJobMessage(t, pool, next.Input.ID), strings.Repeat("d", 32))
	if e != nil || busy != nil {
		t.Fatal(busy, e)
	}
	var generation, attempt int
	if e = pool.QueryRow(ctx, `SELECT generation,attempt_count FROM file_job WHERE id=$1`, next.Input.ID).Scan(&generation, &attempt); e != nil || generation != 2 || attempt != 0 {
		t.Fatal("busy intent", generation, attempt, e)
	}
	execution, e := store.ExecutionFileJob(context.Background(), *lease)
	if e != nil || string(execution.Input.Canonical) != string(v.Input.Canonical) {
		t.Fatal("execution", e)
	}
	if e = store.RenewFileJob(context.Background(), *lease); e != nil {
		t.Fatal(e)
	}
	forged := *lease
	forged.Token = "forged"
	if _, e = store.ExecutionFileJob(context.Background(), forged); e == nil {
		t.Fatal("forged execution")
	}
}
func TestFileJobFinalizeFenceCancelRevoke(t *testing.T) {
	for _, mode := range []string{"success", "cancel", "revoke", "stale", "expired"} {
		t.Run(mode, func(t *testing.T) {
			pool, ctx, store, checks, v := fileJobSetup(t)
			if _, e := store.AcceptFileJob(ctx, v); e != nil {
				t.Fatal(e)
			}
			lease, e := store.ClaimFileJob(context.Background(), fileJobMessage(t, pool, v.Input.ID), strings.Repeat("c", 32))
			if e != nil || lease == nil {
				t.Fatal(lease, e)
			}
			result, e := checks.ComputeStoredInput(ctx, v.Input.ID)
			if e != nil {
				t.Fatal(e)
			}
			switch mode {
			case "cancel":
				got, e := store.CancelFileJob(ctx, 1, v.Input.ID, 0)
				if e != nil || got.State != domain.FileJobCancelRequested {
					t.Fatal(got, e)
				}
			case "revoke":
				if _, e = pool.Exec(ctx, `UPDATE auth_user SET active=false WHERE id=1`); e != nil {
					t.Fatal(e)
				}
			case "stale":
				lease.Token = "wrong"
			case "expired":
				if _, e = pool.Exec(ctx, `UPDATE file_job SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, v.Input.ID); e != nil {
					t.Fatal(e)
				}
			}
			disposition, e := store.FinishFileJob(context.Background(), *lease, &result, nil)
			if mode == "stale" || mode == "expired" {
				if e == nil {
					t.Fatal("late result accepted")
				}
			} else {
				want := domain.FileJobSucceeded
				if mode == "cancel" {
					want = domain.FileJobCancelled
				}
				if mode == "revoke" {
					want = domain.FileJobRevoked
				}
				if e != nil || disposition.Status.State != want {
					t.Fatal(disposition, e)
				}
			}
			var results, checksCount int
			if e = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM file_check_result),(SELECT count(*) FROM manual_check)`).Scan(&results, &checksCount); e != nil || checksCount != 0 {
				t.Fatal(results, checksCount, e)
			}
			wantResults := 0
			if mode == "success" {
				wantResults = 1
			}
			if results != wantResults {
				t.Fatal("result leaked", results)
			}
			if mode == "success" {
				saved, e := store.FileJobResult(ctx, 1, v.Input.ID)
				if e != nil || saved.ID != "q1_"+v.Input.ID || saved.Result.NWindows != 9007199254740993 || saved.Result.Timeline[0].WatchFired == nil || *saved.Result.Timeline[0].WatchFired {
					t.Fatal(saved, e)
				}
				if _, e = store.CancelFileJob(ctx, 1, v.Input.ID, 0); e == nil {
					t.Fatal("success reopened")
				}
				again, e := store.FinishFileJob(context.Background(), *lease, &result, nil)
				if e != nil || again.Kind != "stale" {
					t.Fatal(again, e)
				}
			}
		})
	}
}
func TestFileJobExpiredLeaseRecovery(t *testing.T) {
	pool, ctx, store, _, v := fileJobSetup(t)
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	old, e := store.ClaimFileJob(context.Background(), fileJobMessage(t, pool, v.Input.ID), strings.Repeat("c", 32))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE file_job SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, v.Input.ID); e != nil {
		t.Fatal(e)
	}
	if n, e := store.ReconcileFileJobs(context.Background(), 8); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	state, e := store.FileJob(ctx, 1, v.Input.ID)
	if e != nil || state.State != domain.FileJobRetryWait {
		t.Fatal(state, e)
	}
	if e = store.RenewFileJob(context.Background(), *old); e == nil {
		t.Fatal("expired heartbeat renewed")
	}
	if _, e = pool.Exec(ctx, `UPDATE file_job SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE id=$1`, v.Input.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = store.ReconcileFileJobs(context.Background(), 8); e != nil {
		t.Fatal(e)
	}
	fresh, e := store.ClaimFileJob(context.Background(), fileJobMessage(t, pool, v.Input.ID), strings.Repeat("d", 32))
	if e != nil || fresh == nil || fresh.Fence <= old.Fence || fresh.Generation <= old.Generation {
		t.Fatal(fresh, e)
	}
}
func TestFileJobNoPrincipalCannotReadPrivate(t *testing.T) {
	_, ctx, store, _, v := fileJobSetup(t)
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	if _, e := store.FileJob(context.Background(), 1, v.Input.ID); e == nil {
		t.Fatal("anonymous read")
	}
	if _, e := store.FileJobs(context.Background(), 1, ""); e == nil {
		t.Fatal("anonymous list")
	}
}
func TestFileJobPrivateGetListCursorReview(t *testing.T) {
	pool, ctx, store, checks, v := fileJobSetup(t)
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	lease, e := store.ClaimFileJob(context.Background(), fileJobMessage(t, pool, v.Input.ID), strings.Repeat("c", 32))
	if e != nil {
		t.Fatal(e)
	}
	result, e := checks.ComputeStoredInput(ctx, v.Input.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.FinishFileJob(context.Background(), *lease, &result, nil); e != nil {
		t.Fatal(e)
	}
	reviews := service.NewReviews(store)
	well := int64(1)
	in := domain.ReviewInput{RequestID: "q1-review-request1", Status: "in_progress", Owner: "test", Comment: "synthetic"}
	own, e := reviews.Update(ctx, "file_check", v.Input.ID, &well, in, domain.ReviewActor{ID: "1", Name: "q0", Authenticated: true})
	if e != nil || own.State.Revision != 1 {
		t.Fatal(own, e)
	}
	for _, role := range []string{"technologist", "admin"} {
		t.Run(role, func(t *testing.T) {
			a, e := postgres.New(pool).CreateAccount(ctx, domain.Account{Login: role, Name: role, Role: role, Scope: domain.AccessScope{Funds: []string{"operational"}}, Active: true}, "unused", false)
			if e != nil {
				t.Fatal(e)
			}
			sibling := domain.WithPrincipal(context.Background(), domain.AccountPrincipal(a))
			if _, e = store.FileJob(sibling, 1, v.Input.ID); e == nil {
				t.Fatal("foreign job")
			}
			if _, e = store.FileJobResult(sibling, 1, v.Input.ID); e == nil {
				t.Fatal("foreign result")
			}
			if _, e = reviews.Get(sibling, "file_check", v.Input.ID, &well, 0); e == nil {
				t.Fatal("foreign review")
			}
			if _, e = reviews.Update(sibling, "file_check", v.Input.ID, &well, in, domain.ReviewActor{}); e == nil {
				t.Fatal("foreign review update")
			}
		})
	}
	// Legacy reader remains kind+target scoped and has no fallback to private kind.
	legacy, e := reviews.Get(ctx, "check", v.Input.ID, &well, 0)
	if e == nil || len(legacy.Events) > 0 {
		t.Fatal("legacy reader reached private review")
	}
}
func TestFileJobInsufficientSavedCheck(t *testing.T) {
	pool, ctx, store, checks, v := fileJobSetup(t)
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	lease, e := store.ClaimFileJob(context.Background(), fileJobMessage(t, pool, v.Input.ID), strings.Repeat("c", 32))
	if e != nil {
		t.Fatal(e)
	}
	result, e := checks.ComputeStoredInput(ctx, v.Input.ID)
	if e != nil {
		t.Fatal(e)
	}
	result.Result.Verdict = "insufficient"
	if _, e = store.FinishFileJob(context.Background(), *lease, &result, nil); e != nil {
		t.Fatal(e)
	}
	saved, e := store.FileJobResult(ctx, 1, v.Input.ID)
	if e != nil || saved.Result.Verdict != "insufficient" || saved.PointsCount != 2 {
		t.Fatal(saved, e)
	}
}
func TestFileJobGuardStartupAndUnlistedAccount(t *testing.T) {
	pool, ctx, store, checks, _ := fileJobSetup(t)
	if e := store.ValidateFileJobPolicy(ctx); e != nil {
		t.Fatal(e)
	}
	a, e := postgres.New(pool).CreateAccount(ctx, domain.Account{Login: "unlisted", Name: "unlisted", Role: "technologist", Scope: domain.AccessScope{Funds: []string{"operational"}}, Active: true}, "unused", false)
	if e != nil {
		t.Fatal(e)
	}
	other := domain.WithPrincipal(ctx, domain.AccountPrincipal(a))
	v := prepareSaved(t, checks, other, "q1-unlisted-request")
	if _, e = store.AcceptFileJob(other, domain.FileJobPrepared{Input: v, Start: time.Now(), End: time.Now().Add(time.Hour), PointsCount: 2}); e == nil {
		t.Fatal("unlisted direct store admitted")
	}
	if _, e = pool.Exec(ctx, `UPDATE file_job_pilot_guard SET run_id='other'`); e != nil {
		t.Fatal(e)
	}
	if e = store.ValidateFileJobPolicy(ctx); e == nil {
		t.Fatal("mismatched startup guard")
	}
}

func TestFileJobActorLegacyNamespace(t *testing.T) {
	pool, ctx, store, checks, v := fileJobSetupWithAccounts(t, []int64{1, 2, 3})
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	other, e := postgres.New(pool).CreateAccount(ctx, domain.Account{Login: "sibling", Name: "sibling", Role: "technologist", Scope: domain.AccessScope{Funds: []string{"operational"}}, Active: true}, "unused", false)
	if e != nil {
		t.Fatal(e)
	}
	sibling := domain.WithPrincipal(ctx, domain.AccountPrincipal(other))
	second := v
	second.Input = prepareSaved(t, checks, sibling, v.Input.RequestKey)
	if _, e = store.AcceptFileJob(sibling, second); e != nil {
		t.Fatal(e)
	}
	if second.Input.ID == v.Input.ID {
		t.Fatal("actors shared job")
	}
	if _, e = pool.Exec(ctx, `INSERT INTO manual_check(id,well_id,request_id,start_at,end_at,created_at,input_sha256,points_count,resolution,result) VALUES('legacy-same-key',1,$1,'2026-01-01','2026-02-01',now(),'legacy',1,'raw','{"verdict":"watch"}')`, v.Input.RequestKey); e != nil {
		t.Fatal(e)
	}
	own, e := store.FileJobs(ctx, 1, "")
	if e != nil || len(own.Items) != 1 || own.Items[0].ID != v.Input.ID {
		t.Fatal(own, e)
	}
	others, e := store.FileJobs(sibling, 1, "")
	if e != nil || len(others.Items) != 1 || others.Items[0].ID != second.Input.ID {
		t.Fatal(others, e)
	}
	if _, e = store.FileJobs(sibling, 1, v.Input.ID); e == nil {
		t.Fatal("foreign cursor accepted")
	} else {
		var de *domain.Error
		if !errors.As(e, &de) || de.Kind != domain.Missing {
			t.Fatal(e)
		}
	}
	orphan := prepareSaved(t, checks, ctx, "q1-orphan-request1")
	if _, e = postgres.New(pool).AcceptFileInput(ctx, orphan); e != nil {
		t.Fatal(e)
	}
	if _, e = store.AcceptFileJob(ctx, domain.FileJobPrepared{Input: orphan}); e == nil || e.Error() != "input_not_job" {
		t.Fatal("orphan Q0 proof attached", e)
	}
}
