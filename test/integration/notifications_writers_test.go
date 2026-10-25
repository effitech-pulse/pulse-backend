package integration

import (
	"context"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

func TestNotificationWriterAtomicityAndTerminalReplay(t *testing.T) {
	p := testDB(t)
	ctx := legacyResearchFixtureContext()
	r := postgres.New(p)
	eventsExec(t, p, `UPDATE notification_activation SET activation_id='0123456789abcdef0123456789abcdef',activated_at=clock_timestamp()`)
	u := int64(7711)
	_, e := r.CreateWell(ctx, domain.Well{UnitekID: &u, NGDU: 1, Department: 1, FundType: "operational", Active: true})
	if e != nil {
		t.Fatal(e)
	}
	l, ok, e := r.Reserve(ctx)
	if e != nil || !ok {
		t.Fatal(ok, e)
	}
	defer l.Close()
	id, e := l.Begin(ctx, "f4")
	if e != nil {
		t.Fatal(e)
	}
	s := domain.Snapshot{Wells: []domain.WellRisk{{UnitekID: &u, Alert: true}}}
	eventsExec(t, p, `ALTER TABLE notification_fact ADD CONSTRAINT injected_failure CHECK(false)`)
	if e = l.Persist(ctx, id, s); e == nil {
		t.Fatal("fact constraint did not roll back publication")
	}
	var n int
	_ = p.QueryRow(ctx, `SELECT count(*) FROM ml_alert`).Scan(&n)
	if n != 0 {
		t.Fatal("partial alerts", n)
	}
	eventsExec(t, p, `ALTER TABLE notification_fact DROP CONSTRAINT injected_failure`)
	if e = l.Persist(ctx, id, s); e != nil {
		t.Fatal(e)
	}
	if e = l.Persist(ctx, id, s); e == nil {
		t.Fatal("terminal replay accepted")
	}
	if e = l.Fail(ctx, id, "late failure"); e != nil {
		t.Fatal(e)
	}
	var status string
	_ = p.QueryRow(ctx, `SELECT status FROM ml_run WHERE id=$1`, id).Scan(&status)
	if status != "done" {
		t.Fatal(status)
	}
	_ = p.QueryRow(ctx, `SELECT count(*) FROM notification_fact`).Scan(&n)
	if n != 1 {
		t.Fatal("facts", n)
	}
	eventsExec(t, p, `UPDATE ml_alert SET status='confirmed'`)
	id, e = l.Begin(ctx, "next")
	if e != nil {
		t.Fatal(e)
	}
	duplicate := s
	duplicate.Wells = append(append([]domain.WellRisk(nil), s.Wells...), s.Wells[0])
	if e = l.Persist(ctx, id, duplicate); e == nil {
		t.Fatal("duplicate mapped wells accepted")
	}
	if e = l.Persist(ctx, id, s); e != nil {
		t.Fatal(e)
	}
	_ = p.QueryRow(ctx, `SELECT count(*) FROM notification_fact`).Scan(&n)
	if n != 2 {
		t.Fatal("new run omitted", n)
	}
}

func TestNotificationCollectionReplayDoesNotTouchNewLease(t *testing.T) {
	p := testDB(t)
	ctx := legacyResearchFixtureContext()
	r := postgres.New(p)
	eventsExec(t, p, `UPDATE notification_activation SET activation_id='0123456789abcdef0123456789abcdef',activated_at=clock_timestamp()`)
	eventsExec(t, p, `INSERT INTO telemetry_source(id,config) VALUES('f4-source','{"id":"f4-source","status":"running","profile_version":1,"interval_seconds":60}')`)
	l, e := r.ClaimSource(ctx, "f4-source", false)
	if e != nil || l == nil {
		t.Fatal(l, e)
	}
	if e = r.FailCollection(ctx, *l, "canary raw source failure"); e != nil {
		t.Fatal(e)
	}
	if e = r.FailCollection(ctx, *l, "retry"); e == nil {
		t.Fatal("terminal retry accepted")
	}
	next, e := r.ClaimSource(ctx, "f4-source", false)
	if e != nil || next == nil {
		t.Fatal(next, e)
	}
	if _, e = r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: l.SourceID, ProfileVersion: l.ProfileVersion, Lease: l, Release: true}); e == nil {
		t.Fatal("old success accepted")
	}
	if e = r.FailCollection(ctx, *l, "old"); e == nil {
		t.Fatal("old failure accepted")
	}
	if e = r.FailCollection(ctx, *next, "second"); e != nil {
		t.Fatal(e)
	}
	var receipts, facts int
	_ = p.QueryRow(context.Background(), `SELECT count(*) FROM telemetry_collection_log WHERE outcome='failed'`).Scan(&receipts)
	_ = p.QueryRow(context.Background(), `SELECT count(*) FROM notification_fact`).Scan(&facts)
	if receipts != 2 || facts != 2 {
		t.Fatal(receipts, facts)
	}
}

func TestNotificationCollectionCompletionAndFailureAtomicity(t *testing.T) {
	p := testDB(t)
	ctx := legacyResearchFixtureContext()
	r := postgres.New(p)
	eventsExec(t, p, `UPDATE notification_activation SET activation_id='0123456789abcdef0123456789abcdef',activated_at=clock_timestamp();INSERT INTO telemetry_source(id,config)VALUES('source','{"status":"running","profile_version":1,"interval_seconds":60}')`)
	l, e := r.ClaimSource(ctx, "source", false)
	if e != nil || l == nil {
		t.Fatal(l, e)
	}
	eventsExec(t, p, `ALTER TABLE notification_fact ADD CONSTRAINT reject_fact CHECK(false)`)
	if e = r.FailCollection(ctx, *l, "failure"); e == nil {
		t.Fatal("injected fact failure ignored")
	}
	var receipts int
	var rawError string
	if e = p.QueryRow(ctx, `SELECT error,(SELECT count(*) FROM telemetry_collection_log)FROM telemetry_source WHERE id='source'`).Scan(&rawError, &receipts); e != nil || rawError != "" || receipts != 0 {
		t.Fatal(rawError, receipts, e)
	}
	eventsExec(t, p, `ALTER TABLE notification_fact DROP CONSTRAINT reject_fact`)
	if _, e = r.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: l.SourceID, ProfileVersion: l.ProfileVersion, Lease: l, Release: true, Rejected: 7}); e != nil {
		t.Fatal(e)
	}
	if e = r.FailCollection(ctx, *l, "late failed"); e == nil {
		t.Fatal("completed attempt changed to failed")
	}
	var outcome string
	var facts int
	if e = p.QueryRow(ctx, `SELECT outcome,(SELECT count(*) FROM notification_fact)FROM telemetry_collection_log`).Scan(&outcome, &facts); e != nil || outcome != "completed" || facts != 0 {
		t.Fatal(outcome, facts, e)
	}
}

func TestNotificationCollectionRevokedAuthorityHasNoFact(t *testing.T) {
	p := testDB(t)
	r := postgres.New(p)
	s := notificationScope(t, p, r, `{"kind":"organization","ngdu_id":null,"department_id":null}`, "integrations.collect", "integrations.read", "telemetry.import", "telemetry.read", "wells.read")
	eventsExec(t, p, `UPDATE auth_user SET scope='{"funds":["operational","presentation"]}' WHERE id=$1`, *s.Principal.AccountID)
	a, e := r.Authorization(context.Background(), *s.Principal.AccountID)
	if e != nil {
		t.Fatal(e)
	}
	s.Principal.Authorization = &a
	ctx := domain.WithRBACMode(domain.WithPrincipal(context.Background(), s.Principal), domain.RBACEnforce)
	eventsExec(t, p, `UPDATE notification_activation SET activation_id='0123456789abcdef0123456789abcdef',activated_at=clock_timestamp();INSERT INTO telemetry_source(id,config)VALUES('source','{"status":"running","profile_version":1,"interval_seconds":60}')`)
	l, e := r.ClaimSource(ctx, "source", false)
	if e != nil || l == nil {
		t.Fatal(l, e)
	}
	eventsExec(t, p, `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=$1`, *s.Principal.AccountID)
	if e = r.FailCollection(ctx, *l, "denied failure"); e == nil {
		t.Fatal("revoked authority emitted failure")
	}
	var facts, receipts int
	if e = p.QueryRow(ctx, `SELECT (SELECT count(*)FROM notification_fact),(SELECT count(*)FROM telemetry_collection_log)`).Scan(&facts, &receipts); e != nil || facts != 0 || receipts != 0 {
		t.Fatal(facts, receipts, e)
	}
}
