package integration

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

func notificationScope(t *testing.T, p *pgxpool.Pool, r *postgres.Repository, org string, permissions ...string) domain.NotificationScope {
	t.Helper()
	id := rbacReadUser(t, p, "actor", org, permissions...)
	a, e := r.Authorization(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	return domain.NotificationScope{Mode: "research", Principal: domain.Principal{Authenticated: true, AccountID: &id, Authorization: &a}}
}
func notificationSeed(t *testing.T, p *pgxpool.Pool) {
	t.Helper()
	eventsExec(t, p, `UPDATE notification_activation SET activation_id='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',activated_at=clock_timestamp()`)
	eventsWell(t, p, 9007199254740993, 1)
	eventsWell(t, p, 2, 2)
	eventsExec(t, p, `INSERT INTO ml_run(id,status,trigger) OVERRIDING SYSTEM VALUE VALUES(9007199254740995,'done','fixture');
 INSERT INTO ml_alert(id,run_id,app_well_id,risk,status) OVERRIDING SYSTEM VALUE VALUES(9007199254740997,9007199254740995,9007199254740993,0,'confirmed');
 INSERT INTO notification_fact(activation_id,kind,alert_id,run_id,well_id,code) VALUES('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','alert.created.v1',9007199254740997,9007199254740995,9007199254740993,'alert_created')`)
}
func TestNotificationCurrentAuthorityRaceAndScopeBeforeLimit(t *testing.T) {
	p := testDB(t)
	r := postgres.New(p)
	ctx := context.Background()
	s := notificationScope(t, p, r, `{"kind":"ngdu","ngdu_id":1,"department_id":null}`, "alerts.read", "risks.read", "wells.read")
	notificationSeed(t, p)
	eventsExec(t, p, `INSERT INTO ml_run(id,status,trigger) OVERRIDING SYSTEM VALUE SELECT g,'done','hidden' FROM generate_series(1,1100) g;
 INSERT INTO ml_alert(run_id,app_well_id,risk,status) SELECT g,2,0,'new' FROM generate_series(1,1100) g;
 INSERT INTO notification_fact(activation_id,kind,alert_id,run_id,well_id,code) SELECT 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','alert.created.v1',id,run_id,app_well_id,'alert_created' FROM ml_alert WHERE app_well_id=2`)
	page, e := r.ReadNotificationPage(ctx, domain.NotificationQuery{Scope: s, State: domain.NotificationStateUnread, Limit: 25})
	if e != nil || len(page.Items) != 1 {
		t.Fatal(len(page.Items), e)
	}
	count, e := r.CountUnreadNotifications(ctx, s)
	if e != nil || count.Value != 1 || count.Capped {
		t.Fatal(count, e)
	}
	id := page.Items[0].Key.ID
	first, e := r.MarkNotificationRead(ctx, s, id)
	if e != nil {
		t.Fatal(e)
	}
	again, e := r.MarkNotificationRead(ctx, s, id)
	if e != nil || !first.ReadAt.Equal(again.ReadAt) {
		t.Fatal(first, again, e)
	}
	eventsExec(t, p, `INSERT INTO ml_run(id,status,trigger) OVERRIDING SYSTEM VALUE SELECT g,'done','visible' FROM generate_series(2000,2149) g;
 INSERT INTO ml_alert(run_id,app_well_id,risk,status) SELECT g,9007199254740993,0,'new' FROM generate_series(2000,2149)g;
 INSERT INTO notification_fact(activation_id,kind,alert_id,run_id,well_id,code) SELECT 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','alert.created.v1',id,run_id,app_well_id,'alert_created' FROM ml_alert WHERE run_id BETWEEN 2000 AND 2149`)
	count, e = r.CountUnreadNotifications(ctx, s)
	if e != nil || count.Value != 99 || !count.Capped {
		t.Fatal("bounded count", count, e)
	}
	eventsExec(t, p, `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=$1`, *s.Principal.AccountID)
	if _, e = r.MarkNotificationRead(ctx, s, id); e == nil {
		t.Fatal("stale epoch marked")
	}
}

func TestNotificationAuthorityInterleavings(t *testing.T) {
	for _, mutation := range []string{"revoke", "transfer"} {
		t.Run(mutation, func(t *testing.T) {
			p := testDB(t)
			r := postgres.New(p)
			ctx := context.Background()
			s := notificationScope(t, p, r, `{"kind":"ngdu","ngdu_id":1,"department_id":null}`, "alerts.read", "risks.read", "wells.read")
			notificationSeed(t, p)
			page, e := r.ReadNotificationPage(ctx, domain.NotificationQuery{Scope: s, State: domain.NotificationStateAll, Limit: 25})
			if e != nil || len(page.Items) != 1 {
				t.Fatal(page, e)
			}
			id := page.Items[0].Key.ID
			barrier, e := p.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer barrier.Rollback(ctx)
			var pid int
			if mutation == "revoke" {
				e = barrier.QueryRow(ctx, `SELECT pg_backend_pid() FROM auth_user WHERE id=$1 FOR UPDATE`, *s.Principal.AccountID).Scan(&pid)
			} else {
				e = barrier.QueryRow(ctx, `SELECT pg_backend_pid() FROM well WHERE id=9007199254740993 FOR UPDATE`).Scan(&pid)
			}
			if e != nil {
				t.Fatal(e)
			}
			result := make(chan error, 1)
			go func() { _, e := r.MarkNotificationRead(ctx, s, id); result <- e }()
			equipmentWaitBlocked(t, p, pid)
			if mutation == "revoke" {
				_, e = barrier.Exec(ctx, `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=$1`, *s.Principal.AccountID)
			} else {
				_, e = barrier.Exec(ctx, `UPDATE well SET ngdu=2 WHERE id=9007199254740993`)
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = barrier.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			if e = <-result; e == nil {
				t.Fatal("read committed after serialized change")
			}
			var n int
			if e = p.QueryRow(ctx, `SELECT count(*) FROM notification_read`).Scan(&n); e != nil || n != 0 {
				t.Fatal(n, e)
			}
		})
	}
}

func TestNotificationGrantUnionRevokeRestoreAndPersonalRead(t *testing.T) {
	p := testDB(t)
	r := postgres.New(p)
	ctx := context.Background()
	s := notificationScope(t, p, r, `{"kind":"organization","ngdu_id":null,"department_id":null}`, "alerts.read")
	notificationSeed(t, p)
	eventsExec(t, p, `UPDATE auth_user SET role='admin' WHERE id=$1`, *s.Principal.AccountID)
	cap, e := r.NotificationCapabilities(ctx, s)
	if e != nil || len(cap.Families) != 0 {
		t.Fatal("enum or missing prerequisite bypass", cap, e)
	}
	eventsExec(t, p, `INSERT INTO auth_role(name)VALUES('group-prerequisites');INSERT INTO auth_role_permission SELECT id,k FROM auth_role CROSS JOIN unnest(ARRAY['wells.read','risks.read'])k WHERE name='group-prerequisites';
 INSERT INTO auth_group(name)VALUES('group');INSERT INTO auth_group_role SELECT g.id,r.id FROM auth_group g,auth_role r WHERE g.name='group' AND r.name='group-prerequisites';
 `)
	eventsExec(t, p, `INSERT INTO auth_group_member SELECT g.id,$1 FROM auth_group g WHERE g.name='group'`, *s.Principal.AccountID)
	cap, e = r.NotificationCapabilities(ctx, s)
	if e != nil || len(cap.Families) != 1 {
		t.Fatal("current group union missing", cap, e)
	}
	page, e := r.ReadNotificationPage(ctx, domain.NotificationQuery{Scope: s, State: domain.NotificationStateAll, Limit: 25})
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	id := page.Items[0].Key.ID
	first, e := r.MarkNotificationRead(ctx, s, id)
	if e != nil {
		t.Fatal(e)
	}
	b := rbacReadUser(t, p, "other", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "alerts.read", "risks.read", "wells.read")
	ba, e := r.Authorization(ctx, b)
	if e != nil {
		t.Fatal(e)
	}
	bs := s
	bs.Principal = domain.Principal{Authenticated: true, AccountID: &b, Authorization: &ba}
	count, e := r.CountUnreadNotifications(ctx, bs)
	if e != nil || count.Value != 1 {
		t.Fatal("other account inherited read", count, e)
	}
	eventsExec(t, p, `DELETE FROM auth_group_member WHERE user_id=$1`, *s.Principal.AccountID)
	if _, e = r.MarkNotificationRead(ctx, s, id); e == nil {
		t.Fatal("revoked object marked")
	}
	if _, e = r.MarkNotificationRead(ctx, s, 9223372036854775807); e == nil {
		t.Fatal("absent marked")
	}
	eventsExec(t, p, `INSERT INTO auth_group_member SELECT id,$1 FROM auth_group WHERE name='group'`, *s.Principal.AccountID)
	restored, e := r.MarkNotificationRead(ctx, s, id)
	if e != nil || !first.ReadAt.Equal(restored.ReadAt) {
		t.Fatal("first read lost on grant restore", first, restored, e)
	}
	eventsExec(t, p, `DELETE FROM auth_user WHERE id=$1`, *s.Principal.AccountID)
	var n int
	if e = p.QueryRow(ctx, `SELECT count(*) FROM notification_read`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}

func TestNotificationDeadlineReturnsNoPartialResult(t *testing.T) {
	p := testDB(t)
	r := postgres.New(p)
	ctx := context.Background()
	s := notificationScope(t, p, r, `{"kind":"organization","ngdu_id":null,"department_id":null}`, "alerts.read", "risks.read", "wells.read")
	notificationSeed(t, p)
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT id FROM auth_user WHERE id=$1 FOR UPDATE`, *s.Principal.AccountID); e != nil {
		t.Fatal(e)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	count, e := r.CountUnreadNotifications(short, s)
	if e == nil || count.Value != 0 || count.Capped {
		t.Fatal("deadline returned partial count", count, e)
	}
}
