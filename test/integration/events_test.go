package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
	"github.com/effitech-pulse/pulse-backend/internal/transport/httpapi"
)

var eventsAt = time.Date(2026, 10, 22, 12, 0, 1, 123456000, time.UTC)

func eventsExec(t *testing.T, p *pgxpool.Pool, q string, args ...any) {
	t.Helper()
	if _, e := p.Exec(context.Background(), q, args...); e != nil {
		t.Fatal(e)
	}
}
func eventsWell(t *testing.T, p *pgxpool.Pool, id, ngdu int64) {
	t.Helper()
	eventsExec(t, p, `INSERT INTO well(id,unitek_id,armits_id,well_number,ngdu,department,active,fund_type) OVERRIDING SYSTEM VALUE VALUES($1,$1,$1,'101',$2,1,false,'operational')`, id, ngdu)
}
func eventsCheck(t *testing.T, p *pgxpool.Pool, well int64, id string) {
	t.Helper()
	_, e := postgres.New(p).SaveManualCheck(context.Background(), domain.ManualCheck{ID: id, WellID: well, RequestID: id, Start: eventsAt.Add(-time.Hour), End: eventsAt, CreatedAt: eventsAt, InputSHA256: strings.Repeat("0", 64), PointsCount: 1, Resolution: "1h", Result: domain.UploadResult{Verdict: "insufficient", Filename: "must-not-leak.csv", Timeline: []domain.TimelinePoint{{Risk: 0, WatchFired: ptr(false)}}}})
	if e != nil {
		t.Fatal(e)
	}
}
func eventsService(t *testing.T, p *pgxpool.Pool, scope service.FundScope, mode string) *service.Events {
	t.Helper()
	s, e := service.NewEvents(postgres.New(p), scope, service.EventsOptions{Mode: mode, Prefix: "/api/v1"})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func eventsAdmin() context.Context {
	return domain.WithPrincipal(context.Background(), domain.Principal{Role: "admin", Mechanism: "legacy"})
}
func TestEventsTraversalExactAndSourceDeletion(t *testing.T) {
	p := testDB(t)
	eventsWell(t, p, 9223372036854775807, 1)
	eventsCheck(t, p, 9223372036854775807, "42")
	eventsCheck(t, p, 9223372036854775807, "q1_исторический/Период 01?x=0 +%#")
	eventsExec(t, p, `INSERT INTO ml_run(id,started_at,status,trigger) OVERRIDING SYSTEM VALUE VALUES(1,now(),'done','manual')`)
	eventsExec(t, p, `INSERT INTO ml_alert(id,run_id,ml_well_id,app_well_id,risk,status) OVERRIDING SYSTEM VALUE VALUES(42,1,1,9223372036854775807,0,'new')`)
	for _, kind := range []string{"check", "alert"} {
		eventsExec(t, p, `INSERT INTO case_review(kind,target_id,check_id,alert_id,well_id,state,revision) VALUES($1,'42',CASE WHEN $1='check' THEN '42' END,CASE WHEN $1='alert' THEN 42 END,NULL,'{}',20)`, kind)
		for i := 1; i <= 20; i++ {
			eventsExec(t, p, `INSERT INTO review_event(kind,target_id,revision,request_id,fingerprint,created_at,actor,comment,before_state,after_state) VALUES($1,'42',$2::bigint,($2::bigint)::text,'fingerprint',$3,'{"id":"user:41","name":"saved name","authenticated":true}','saved comment',jsonb_build_object('revision',$2::bigint-1,'status','new'),jsonb_build_object('revision',$2::bigint,'status','closed','conclusion','inconclusive'))`, kind, i, eventsAt)
		}
	}
	for i := 1; i <= 20; i++ {
		eventsCheck(t, p, 9223372036854775807, fmt.Sprintf("mc-%02d", i))
		eventsExec(t, p, `INSERT INTO well_event(well_id,revision,created_at,kind,actor,reason,before_state,after_state) VALUES(9223372036854775807,$1,$2,CASE WHEN $1=1 THEN 'archive' WHEN $1=2 THEN 'restore' ELSE 'metadata' END,'{}','saved reason','{}',jsonb_build_object('id',9223372036854775807::bigint,'revision',$1::bigint))`, i, eventsAt)
	}
	s := eventsService(t, p, nil, "research")
	seen := map[string]bool{}
	cursor := ""
	var first domain.EventSummary
	for pages := 0; ; pages++ {
		page, e := s.List(eventsAdmin(), domain.EventFilter{Limit: 25}, cursor)
		if e != nil {
			t.Fatal(e)
		}
		if pages == 0 {
			first = page.Items[0]
			if first.EventType != "well.metadata_changed.v1" || first.Target.Revision != 20 {
				t.Fatalf("ordering %+v", first)
			}
		}
		for _, v := range page.Items {
			if seen[v.EventID] {
				t.Fatal("duplicate", v.EventID)
			}
			seen[v.EventID] = true
		}
		if !page.HasMore {
			break
		}
		cursor = *page.NextCursor
		if pages > 10 {
			t.Fatal("endless pagination")
		}
	}
	if len(seen) != 82 {
		t.Fatalf("got %d events", len(seen))
	}
	id, _ := service.EncodeEventID(domain.EventLocator{Source: "review.check", Target: "42", Revision: 2})
	detail, e := s.Get(eventsAdmin(), id)
	if e != nil || detail.Record.Review.Revision != 2 || detail.Record.Review.After.Revision != 2 {
		t.Fatalf("exact %+v %v", detail, e)
	}
	eventsExec(t, p, `DELETE FROM well_event WHERE well_id=9223372036854775807 AND revision=20`)
	if _, e = s.Get(eventsAdmin(), first.EventID); e == nil {
		t.Fatal("deleted source visible")
	}
	// Continuation compares key values and does not need its anchor row.
	page, e := s.List(eventsAdmin(), domain.EventFilter{Limit: 1}, "")
	if e != nil {
		t.Fatal(e)
	}
	eventsExec(t, p, `DELETE FROM well_event WHERE well_id=9223372036854775807 AND revision=19`)
	if _, e = s.List(eventsAdmin(), domain.EventFilter{Limit: 1}, *page.NextCursor); e != nil {
		t.Fatal(e)
	}
	server := &httpapi.Server{Config: testConfig(), Events: s}
	w := httptest.NewRecorder()
	server.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/events/"+id, nil))
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var wire struct {
		Detail struct {
			Revision string `json:"revision"`
		}
	}
	if json.Unmarshal(w.Body.Bytes(), &wire) != nil || wire.Detail.Revision != "2" {
		t.Fatal(w.Body.String())
	}
}
func TestEventsCommittedFactsOnly(t *testing.T) {
	p := testDB(t)
	eventsWell(t, p, 1, 1)
	s := eventsService(t, p, nil, "research")
	ctx := eventsAdmin()
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	_, e = tx.Exec(ctx, `INSERT INTO well_event(well_id,revision,kind,actor,reason,before_state,after_state) VALUES(1,1,'archive','{}','','{}','{}')`)
	if e != nil {
		t.Fatal(e)
	}
	page, e := s.List(ctx, domain.EventFilter{}, "")
	if e != nil || len(page.Items) != 0 {
		t.Fatalf("uncommitted %+v %v", page, e)
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	page, e = s.List(ctx, domain.EventFilter{}, "")
	if e != nil || len(page.Items) != 0 {
		t.Fatal("rollback fact")
	}
	eventsCheck(t, p, 1, "committed")
	eventsCheck(t, p, 1, "committed")
	page, e = s.List(ctx, domain.EventFilter{}, "")
	if e != nil || len(page.Items) != 1 {
		t.Fatalf("committed replay %+v %v", page, e)
	}
}

func TestEventsLiveFiltersRedactionAndUnknownSnapshots(t *testing.T) {
	p := testDB(t)
	eventsWell(t, p, 1, 1)
	for _, id := range []string{"middle", "tail", "zero"} {
		eventsCheck(t, p, 1, id)
	}
	s := eventsService(t, p, nil, "research")
	first, e := s.List(eventsAdmin(), domain.EventFilter{Limit: 1}, "")
	if e != nil {
		t.Fatal(e)
	}
	eventsCheck(t, p, 1, "zz-new-before-anchor")
	eventsCheck(t, p, 1, "aa-new-after-anchor")
	next, e := s.List(eventsAdmin(), domain.EventFilter{Limit: 1}, *first.NextCursor)
	if e != nil {
		t.Fatal(e)
	}
	if len(next.Items) != 1 || next.Items[0].Target.Target != "tail" {
		t.Fatalf("live K continuation %+v", next)
	}
	eventsExec(t, p, `UPDATE manual_check SET result='{"verdict":"future","filename":"secret.csv","unknown_secret":"must-not-leak","timeline":[{"risk":0,"watch_fired":false,"raw_score":null,"high_fired":null}]}' WHERE id='middle'`)
	id, _ := service.EncodeEventID(domain.EventLocator{Source: "mc", Target: "middle"})
	v, e := s.Get(eventsAdmin(), id)
	if e != nil || v.Event.Outcome.Verdict == nil || *v.Event.Outcome.Verdict != "unknown" || v.Record.Check.Result.Verdict != "future" {
		t.Fatalf("saved unknown %+v %v", v, e)
	}
	// Header/archive filtering uses current metadata, while inactive=false is not an ACL.
	eventsExec(t, p, `UPDATE well SET archived_at=now() WHERE id=1`)
	page, e := s.List(eventsAdmin(), domain.EventFilter{Archive: "current"}, "")
	if e != nil || len(page.Items) != 0 {
		t.Fatal("current archive filter")
	}
	page, e = s.List(eventsAdmin(), domain.EventFilter{Archive: "archived"}, "")
	if e != nil || len(page.Items) != 5 {
		t.Fatalf("archived inactive %+v %v", page, e)
	}
	server := &httpapi.Server{Config: testConfig(), Events: s}
	for _, path := range []string{"/api/v1/events", "/api/v1/events/" + id} {
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || strings.Contains(w.Body.String(), "secret.csv") || strings.Contains(w.Body.String(), "must-not-leak") {
			t.Fatalf("redaction %d %s", w.Code, w.Body.String())
		}
		if path == "/api/v1/events" && strings.Contains(w.Body.String(), "future") {
			t.Fatal("raw unknown verdict in summary")
		}
	}
}
