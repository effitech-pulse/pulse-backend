package integration

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

// waitNotificationLock observes an actual PostgreSQL waiter; no scheduling sleeps.
func waitNotificationLock(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		var n int
		e := p.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%notification_activation%'`).Scan(&n)
		if e != nil {
			t.Fatal(e)
		}
		if n > 0 {
			return
		}
		runtime.Gosched()
	}
	t.Fatal("publication did not wait at activation barrier")
}
func TestNotificationActivationBarrierAcrossWriters(t *testing.T) {
	for _, kind := range []string{"legacy", "collection", "cohort"} {
		t.Run(kind, func(t *testing.T) {
			p := testDB(t)
			r := postgres.New(p)
			ctx := legacyResearchFixtureContext()
			var publish func() error
			var closeLease func()
			if kind == "collection" {
				eventsExec(t, p, `INSERT INTO telemetry_source(id,config) VALUES('source','{"status":"running","profile_version":1,"interval_seconds":60}')`)
				l, e := r.ClaimSource(ctx, "source", false)
				if e != nil || l == nil {
					t.Fatal(l, e)
				}
				publish = func() error { return r.FailCollection(ctx, *l, "failure") }
			} else {
				n := 1
				if kind == "cohort" {
					n = 100
				}
				for i := 0; i < n; i++ {
					u := int64(-900000 - i)
					if _, e := r.CreateWell(ctx, domain.Well{UnitekID: &u, NGDU: -900, Department: -900, FundType: "presentation", Active: true}); e != nil {
						t.Fatal(e)
					}
				}
				if kind == "cohort" {
					if _, e := r.CaptureCohort(ctx, "barrier"); e != nil {
						t.Fatal(e)
					}
				}
				l, ok, e := r.Reserve(ctx)
				if e != nil || !ok {
					t.Fatal(e)
				}
				closeLease = l.Close
				if kind == "legacy" {
					id, e := l.Begin(ctx, "barrier")
					if e != nil {
						t.Fatal(e)
					}
					u := int64(-900000)
					publish = func() error {
						return l.Persist(ctx, id, domain.Snapshot{Wells: []domain.WellRisk{{UnitekID: &u, Alert: true}}})
					}
				} else {
					cl := l.(interface {
						BeginCohort(context.Context, string, string) (int64, error)
						PersistCohort(context.Context, int64, domain.CohortSnapshot) error
					})
					id, e := cl.BeginCohort(ctx, "barrier", "barrier")
					if e != nil {
						t.Fatal(e)
					}
					_, members, e := r.CohortMembers(ctx, "barrier")
					if e != nil {
						t.Fatal(e)
					}
					sid := "barrier"
					now := time.Now().UTC()
					s := domain.CohortSnapshot{ProvenanceStatus: "partial", Profile: "cohort", CohortVersion: "barrier", RunID: &id, SnapshotID: &sid, AsOf: &now, OutcomeCompleteness: "complete"}
					for i, w := range members {
						o := domain.CohortOutcome{AppWellID: w.ID, Metadata: w, Status: "insufficient", ReasonCode: "no_data"}
						if i == 0 {
							o.Status = "scored"
							o.Result = &domain.UploadResult{Verdict: "alert", LatestRisk: .9}
						}
						s.Outcomes = append(s.Outcomes, o)
					}
					publish = func() error { return cl.PersistCohort(ctx, id, s) }
				}
			}
			if closeLease != nil {
				defer closeLease()
			}
			barrier, e := p.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer barrier.Rollback(ctx)
			if _, e = barrier.Exec(ctx, `UPDATE notification_activation SET activation_id='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',activated_at=clock_timestamp() WHERE id`); e != nil {
				t.Fatal(e)
			}
			result := make(chan error, 1)
			go func() { result <- publish() }()
			waitNotificationLock(t, p)
			if e = barrier.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			if e = <-result; e != nil {
				t.Fatal(e)
			}
			var n int
			if e = p.QueryRow(ctx, `SELECT count(*) FROM notification_fact`).Scan(&n); e != nil || n != 1 {
				t.Fatal(n, e)
			}
			a, e := r.ActivateNotifications(ctx, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
			if e != nil || a.ID != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
				t.Fatal(a, e)
			}
		})
	}
	t.Run("RR preexisting snapshot fails safely", func(t *testing.T) {
		p := testDB(t)
		ctx := context.Background()
		rr, e := p.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		if e != nil {
			t.Fatal(e)
		}
		defer rr.Rollback(ctx)
		var inactive bool
		if e = rr.QueryRow(ctx, `SELECT activation_id IS NULL FROM notification_activation`).Scan(&inactive); e != nil || !inactive {
			t.Fatal(e)
		}
		barrier, e := p.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer barrier.Rollback(ctx)
		_, e = barrier.Exec(ctx, `UPDATE notification_activation SET activation_id='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',activated_at=clock_timestamp()`)
		if e != nil {
			t.Fatal(e)
		}
		result := make(chan error, 1)
		go func() {
			_, e := rr.Exec(ctx, `SELECT activation_id FROM notification_activation WHERE id FOR SHARE`)
			result <- e
		}()
		waitNotificationLock(t, p)
		if e = barrier.Commit(ctx); e != nil {
			t.Fatal(e)
		}
		e = <-result
		pe, ok := e.(*pgconn.PgError)
		if !ok || pe.Code != "40001" {
			t.Fatal("RR must fail, never classify stale off", e)
		}
	})
}
