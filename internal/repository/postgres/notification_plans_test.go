package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/migrations"
)

// TestNotificationSQLPlans explains the actual production predicate and anti-join.
func TestNotificationSQLPlans(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer admin.Close()
	schema := fmt.Sprintf("notification_plan_%d", time.Now().UnixNano())
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	p, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	if e = migrations.Apply(ctx, p); e != nil {
		t.Fatal(e)
	}
	_, e = p.Exec(ctx, `INSERT INTO auth_user(login,name,password_hash,role,scope,organization_scope) VALUES('plan','plan','hash','viewer','{"funds":["operational"]}','{"kind":"ngdu","ngdu_id":1,"department_id":null}');
 INSERT INTO auth_role(name) VALUES('plan');INSERT INTO auth_user_role SELECT u.id,r.id FROM auth_user u,auth_role r WHERE u.login='plan' AND r.name='plan';
 INSERT INTO auth_role_permission SELECT id,k FROM auth_role CROSS JOIN unnest(ARRAY['wells.read','risks.read','alerts.read']) k WHERE name='plan';
 INSERT INTO well(id,well_number,ngdu,department,fund_type,active) OVERRIDING SYSTEM VALUE VALUES(1,'visible',1,1,'operational',true),(2,'hidden',2,2,'operational',true);
 UPDATE notification_activation SET activation_id='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',activated_at=clock_timestamp();
 INSERT INTO ml_run(id,status,trigger,profile) OVERRIDING SYSTEM VALUE SELECT g,'done','plan','research' FROM generate_series(1,100000)g;
 INSERT INTO ml_alert(run_id,app_well_id,risk,status) SELECT g,CASE WHEN g<=1000 THEN 1 ELSE 2 END,0,'new' FROM generate_series(1,100000)g;
 INSERT INTO notification_fact(activation_id,kind,alert_id,run_id,well_id,code) SELECT 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','alert.created.v1',id,run_id,app_well_id,'alert_created' FROM ml_alert;
 ANALYZE notification_fact;ANALYZE ml_alert;ANALYZE ml_run;ANALYZE well;`)
	if e != nil {
		t.Fatal(e)
	}
	r := New(p)
	var id int64
	if e = p.QueryRow(ctx, `SELECT id FROM auth_user WHERE login='plan'`).Scan(&id); e != nil {
		t.Fatal(e)
	}
	a, e := r.Authorization(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	scope := domain.NotificationScope{Mode: "research", Principal: domain.Principal{Authenticated: true, AccountID: &id, Authorization: &a}}
	var proof strings.Builder
	for _, scenario := range []string{"99pct-newer-hidden", "all-read"} {
		if scenario == "all-read" {
			if _, e = p.Exec(ctx, `INSERT INTO notification_read(account_id,notification_id) SELECT $1,id FROM notification_fact`, id); e != nil {
				t.Fatal(e)
			}
		}
		f, e := r.notificationReadTx(ctx, scope)
		if e != nil {
			t.Fatal(e)
		}
		for _, kind := range []string{"page", "count"} {
			var query sq.Sqlizer = notificationRowsQuery(f.unread(f.feedQuery()).OrderBy("f.recorded_at DESC", "f.id DESC").Limit(26), id)
			if kind == "count" {
				query = sq.Expr("SELECT count(*) FROM (?) visible", f.unread(f.feedQuery()).Limit(100))
			}
			sql, args, e := build(query)
			if e != nil {
				t.Fatal(e)
			}
			rows, e := f.tx.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+sql, args...)
			if e != nil {
				t.Fatal(e)
			}
			fmt.Fprintf(&proof, "\n%s %s\nSQL: %s\n", scenario, kind, sql)
			for rows.Next() {
				var line string
				if e = rows.Scan(&line); e != nil {
					t.Fatal(e)
				}
				proof.WriteString(line + "\n")
			}
			if e = rows.Err(); e != nil {
				t.Fatal(e)
			}
			rows.Close()
		}
		if e = f.tx.Commit(ctx); e != nil {
			t.Fatal(e)
		}
		result, e := r.ReadNotificationPage(ctx, domain.NotificationQuery{Scope: scope, State: domain.NotificationStateUnread, Limit: 25})
		if e != nil {
			t.Fatal(e)
		}
		want := 26
		if scenario == "all-read" {
			want = 0
		}
		if len(result.Items) != want {
			t.Fatal("authorized page", len(result.Items), want)
		}
	}
	if path := os.Getenv("F4_SQL_PLAN_OUTPUT"); path != "" {
		if e = os.WriteFile(path, []byte(proof.String()), 0600); e != nil {
			t.Fatal(e)
		}
	}
	t.Log(proof.String())
}
