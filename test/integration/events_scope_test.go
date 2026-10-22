package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestEventsACLBeforeLimitAndCurrentMetadata(t *testing.T) {
	p := testDB(t)
	eventsWell(t, p, 1, 1)
	eventsWell(t, p, 2, 2)
	eventsCheck(t, p, 1, "allowed")
	for i := 0; i < 130; i++ {
		eventsCheck(t, p, 2, "z"+strings.Repeat("x", i))
	}
	s := eventsService(t, p, nil, "research")
	ngdus := []int64{1}
	principal := domain.Principal{AccountID: ptr(int64(41)), Role: "viewer", Authenticated: true, Mechanism: "session", Scope: domain.AccessScope{Funds: []string{"operational"}, NGDUs: &ngdus}}
	for _, role := range []string{"viewer", "technologist", "admin"} {
		principal.Role = role
		ctx := domain.WithPrincipal(context.Background(), principal)
		page, e := s.List(ctx, domain.EventFilter{Limit: 25}, "")
		if e != nil {
			t.Fatal(e)
		}
		if role != "admin" && (len(page.Items) != 1 || page.HasMore) {
			t.Fatalf("ACL after limit %+v", page)
		}
		if role == "admin" && len(page.Items) != 25 {
			t.Fatal("admin scope")
		}
	}
	principal.Role = "viewer"
	ngdus = []int64{}
	ctx := domain.WithPrincipal(context.Background(), principal)
	page, e := s.List(ctx, domain.EventFilter{}, "")
	if e != nil || len(page.Items) != 0 {
		t.Fatalf("empty scope %+v %v", page, e)
	}
	ngdus = []int64{1}
	ctx = domain.WithPrincipal(context.Background(), principal)
	id, _ := service.EncodeEventID(domain.EventLocator{Source: "mc", Target: "allowed"})
	if _, e = s.Get(ctx, id); e != nil {
		t.Fatal(e)
	}
	eventsExec(t, p, `UPDATE well SET ngdu=2 WHERE id=1`)
	if _, e = s.Get(ctx, id); e == nil {
		t.Fatal("current rights revoke ignored")
	}
	well := int64(1)
	if _, e = s.List(ctx, domain.EventFilter{WellID: &well}, ""); e == nil {
		t.Fatal("direct denied well visible")
	}
}
func TestEventsCohortFence(t *testing.T) {
	p := testDB(t)
	for i := int64(1); i <= 101; i++ {
		eventsWell(t, p, i, 1)
	}
	eventsExec(t, p, `INSERT INTO app_cohort(version,manifest_sha256) VALUES('events',repeat('a',64))`)
	eventsExec(t, p, `INSERT INTO app_cohort_member(version,well_id,revision) SELECT 'events',id,1 FROM well WHERE id<=100`)
	eventsCheck(t, p, 1, "inside")
	eventsCheck(t, p, 101, "outside")
	scope, e := service.NewFundScope(context.Background(), postgres.New(p), "events")
	if e != nil {
		t.Fatal(e)
	}
	s := eventsService(t, p, scope, "cohort")
	page, e := s.List(eventsAdmin(), domain.EventFilter{}, "")
	if e != nil || len(page.Items) != 1 {
		t.Fatalf("cohort %+v %v", page, e)
	}
	id, _ := service.EncodeEventID(domain.EventLocator{Source: "mc", Target: "outside"})
	if _, e = s.Get(eventsAdmin(), id); e == nil {
		t.Fatal("admin bypass cohort")
	}
	eventsExec(t, p, `ALTER TABLE app_cohort_member DISABLE TRIGGER app_cohort_member_immutable`)
	eventsExec(t, p, `DELETE FROM app_cohort_member WHERE well_id=100`)
	if _, e = s.List(eventsAdmin(), domain.EventFilter{}, ""); e == nil {
		t.Fatal("incomplete cohort admitted")
	}
}

func TestEventsTimeoutRollsBackReadTransaction(t *testing.T) {
	p := testDB(t)
	eventsWell(t, p, 1, 1)
	eventsCheck(t, p, 1, "timeout")
	s := eventsService(t, p, nil, "research")
	tx, e := p.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(context.Background(), `LOCK TABLE manual_check IN ACCESS EXCLUSIVE MODE`); e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	_, e = s.List(eventsAdmin(), domain.EventFilter{}, "")
	if e == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("statement timeout duration=%v err=%v", time.Since(start), e)
	}
	if e = tx.Rollback(context.Background()); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(eventsAdmin(), time.Second)
	defer cancel()
	page, e := s.List(ctx, domain.EventFilter{}, "")
	if e != nil || len(page.Items) != 1 {
		t.Fatalf("transaction leaked: %+v %v", page, e)
	}
}

func TestEventsReviewBindingUsesOnlyPrimaryTarget(t *testing.T) {
	p := testDB(t)
	eventsWell(t, p, 1, 1)
	eventsWell(t, p, 2, 2)
	for _, id := range []string{"linked", "mismatched"} {
		eventsCheck(t, p, 1, id)
		eventsExec(t, p, `INSERT INTO case_review(kind,target_id,check_id,well_id,state,revision) VALUES('check',$1,$1,CASE WHEN $1='mismatched' THEN 2 END,'{}',1)`, id)
		eventsExec(t, p, `INSERT INTO review_event(kind,target_id,revision,request_id,fingerprint,created_at,actor,comment,before_state,after_state) VALUES('check',$1,1,$1,'fingerprint',$2,'{"id":"user:41","authenticated":"true"}','secret comment','{}','{"status":"future","conclusion":"future"}')`, id, eventsAt)
	}
	s := eventsService(t, p, nil, "research")
	page, e := s.List(eventsAdmin(), domain.EventFilter{Types: []string{"check.review_changed.v1"}}, "")
	if e != nil || len(page.Items) != 1 || page.Items[0].Target.Target != "linked" {
		t.Fatalf("binding %+v %v", page, e)
	}
	v := page.Items[0]
	if v.Actor.Kind != "unknown" || v.Outcome.ReviewStatus == nil || *v.Outcome.ReviewStatus != "unknown" || v.Outcome.Conclusion == nil || *v.Outcome.Conclusion != "unknown" {
		t.Fatalf("normalization %+v", v)
	}
	id, _ := service.EncodeEventID(domain.EventLocator{Source: "review.check", Target: "mismatched", Revision: 1})
	if _, e = s.Get(eventsAdmin(), id); e == nil {
		t.Fatal("mismatched well exposed")
	}
	// A NULL alert app binding remains hidden even to research admin.
	eventsExec(t, p, `INSERT INTO ml_run(id,started_at,status,trigger) OVERRIDING SYSTEM VALUE VALUES(1,now(),'done','manual')`)
	eventsExec(t, p, `INSERT INTO ml_alert(id,run_id,ml_well_id,risk,status) OVERRIDING SYSTEM VALUE VALUES(42,1,1,0,'new')`)
	eventsExec(t, p, `INSERT INTO case_review(kind,target_id,alert_id,state,revision) VALUES('alert','42',42,'{}',1)`)
	eventsExec(t, p, `INSERT INTO review_event(kind,target_id,revision,request_id,fingerprint,created_at,actor,comment,before_state,after_state) VALUES('alert','42',1,'a','f',now(),'{}','','{}','{}')`)
	id, _ = service.EncodeEventID(domain.EventLocator{Source: "review.alert", Target: "42", Revision: 1})
	if _, e = s.Get(eventsAdmin(), id); e == nil {
		t.Fatal("unlinked alert exposed")
	}
}
