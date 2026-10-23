package migrations

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func rbacMigrationDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("rbac_upgrade_%d", time.Now().UnixNano())
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { pool.Close(); _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	for _, s := range []string{initialSchema, `CREATE TABLE backend_schema_migration(version integer PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now());INSERT INTO backend_schema_migration(version)VALUES(1)`, integrationSchema, wellIdentitySchema, manualChecksSchema, reviewsSchema, fundTypeSchema, accessSchema, wellLifecycleSchema, cohortSchema, snapshotSchema, runScopeSchema, fileInputsSchema, fileJobsSchema, cohortJobsSchema, collectionJobsSchema} {
		if _, e = pool.Exec(ctx, s); e != nil {
			t.Fatal(e)
		}
	}
	return pool
}

func TestRBACMigrationScopeTruthTable(t *testing.T) {
	p := rbacMigrationDB(t)
	ctx := context.Background()
	cases := []struct{ role, scope, kind string }{
		{"admin", `{"funds":["presentation"],"ngdus":[],"departments":[]}`, "organization"},
		{"viewer", `{"funds":["operational"],"ngdus":null,"departments":null}`, "organization"},
		{"technologist", `{"funds":[],"ngdus":[],"departments":null}`, "none"},
		{"viewer", `{"funds":["presentation"],"ngdus":[0],"departments":[-1]}`, "department"},
		{"technologist", `{"funds":["operational"],"ngdus":[-1,0],"departments":[0,-2]}`, "legacy"},
		{"viewer", `{"funds":["operational"],"ngdus":null,"departments":[3]}`, "legacy"},
		{"viewer", `{"funds":["operational"],"ngdus":[-1],"departments":null}`, "ngdu"},
	}
	before := make([]string, len(cases))
	for i, c := range cases {
		var id int64
		e := p.QueryRow(ctx, `INSERT INTO auth_user(login,name,password_hash,role,scope)VALUES($1,$1,'hash',$2,$3) RETURNING id`, fmt.Sprint(i), c.role, c.scope).Scan(&id)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = p.Exec(ctx, `INSERT INTO auth_identity VALUES('test',$1,$2);`, fmt.Sprint(i), id); e != nil {
			t.Fatal(e)
		}
		if e = p.QueryRow(ctx, `SELECT row_to_json(u)::text FROM auth_user u WHERE id=$1`, id).Scan(&before[i]); e != nil {
			t.Fatal(e)
		}
	}

	if _, e := p.Exec(ctx, `INSERT INTO auth_session(token_hash,user_id,expires_at) SELECT 'old',id,now()+interval '1 hour' FROM auth_user WHERE login='0'`); e != nil {
		t.Fatal(e)
	}
	if e := Apply(ctx, p); e != nil {
		t.Fatal(e)
	}

	var oldEpoch int64
	if e := p.QueryRow(ctx, `SELECT authz_epoch FROM auth_session WHERE token_hash='old'`).Scan(&oldEpoch); e != nil || oldEpoch != 0 {
		t.Fatal("old session changed", oldEpoch, e)
	}
	for i, c := range cases {
		var raw []byte
		var org []byte
		var id int64
		var hash, scope string
		e := p.QueryRow(ctx, `SELECT id,password_hash,scope::text,organization_scope FROM auth_user WHERE login=$1`, fmt.Sprint(i)).Scan(&id, &hash, &scope, &org)
		if e != nil {
			t.Fatal(e)
		}

		var unchanged bool
		if e = p.QueryRow(ctx, `SELECT (to_jsonb(u)-'organization_scope'-'authz_epoch')=$1::jsonb FROM auth_user u WHERE id=$2`, before[i], id).Scan(&unchanged); e != nil || !unchanged {
			t.Fatal("original account fields changed", i, e)
		}
		var old struct {
			ID    int64           `json:"id"`
			Scope json.RawMessage `json:"scope"`
		}
		if e = json.Unmarshal([]byte(before[i]), &old); e != nil {
			t.Fatal(e)
		}
		raw, _ = json.Marshal(json.RawMessage(scope))
		var oldScope, newScope domain.AccessScope
		_ = json.Unmarshal(old.Scope, &oldScope)
		_ = json.Unmarshal(raw, &newScope)
		var normalizedOld, normalizedNew any
		_ = json.Unmarshal(old.Scope, &normalizedOld)
		_ = json.Unmarshal(raw, &normalizedNew)
		x, _ := json.Marshal(normalizedOld)
		y, _ := json.Marshal(normalizedNew)
		if string(x) != string(y) || id != old.ID || hash != "hash" {
			t.Fatal("legacy account changed", i)
		}
		var bindings int
		if e = p.QueryRow(ctx, `SELECT count(*) FROM auth_identity WHERE user_id=$1`, id).Scan(&bindings); e != nil || bindings != 1 {
			t.Fatal("identity changed", e)
		}
		if c.kind == "legacy" {
			if org != nil {
				t.Fatal("legacy scope flattened", i)
			}
			a := domain.AuthorizationSnapshot{Active: true, LegacyScope: newScope}
			oldp := domain.Principal{Role: c.role, Scope: oldScope}
			for _, n := range []int64{-2, -1, 0, 1, 3} {
				for _, d := range []int64{-2, -1, 0, 1, 3} {
					for _, f := range []string{"operational", "presentation"} {
						w := domain.Well{NGDU: n, Department: d, FundType: f}
						if a.AllowsWell(w) != oldp.Allows(w) {
							t.Fatal("predicate changed", i, n, d, f)
						}
					}
				}
			}
		} else {
			var got domain.OrganizationScope
			if e = json.Unmarshal(org, &got); e != nil || got.Kind != c.kind {
				t.Fatal(i, string(org), e)
			}
			if c.kind == "department" && (*got.NGDUID != 0 || *got.DepartmentID != -1) {
				t.Fatal(got)
			}
		}
	}
	var n int
	if e := p.QueryRow(ctx, `SELECT count(*) FROM auth_user_role`).Scan(&n); e != nil || n != len(cases) {
		t.Fatal(n, e)
	}
}

func TestRBACMigrationIdempotent(t *testing.T) {
	p := rbacMigrationDB(t)
	ctx := context.Background()
	if _, e := p.Exec(ctx, `INSERT INTO auth_user(login,name,password_hash,role,scope) VALUES('seed','seed','hash','viewer','{"funds":[],"ngdus":null,"departments":null}')`); e != nil {
		t.Fatal(e)
	}

	if e := Apply(ctx, p); e != nil {
		t.Fatal(e)
	}
	var n int
	if e := p.QueryRow(ctx, `SELECT count(*) FROM auth_role_permission p JOIN auth_role r ON r.id=p.role_id WHERE builtin_key='admin'`).Scan(&n); e != nil || n != len(domain.PermissionCatalog()) {
		t.Fatal(n, e)
	}

	viewer := strings.Fields("alerts.read checks.read cohort.runs.read incidents.read parameters.read references.read risks.explain risks.export risks.read telemetry.read wells.export wells.history.read wells.read")
	technologist := append(slices.Clone(viewer), strings.Fields("checks.run checks.review alerts.review incidents.create filechecks.read filechecks.run filechecks.cancel filechecks.review")...)
	admin := make([]string, 0)
	for _, key := range domain.PermissionCatalog() {
		admin = append(admin, string(key.Key))
	}
	for key, want := range map[string][]string{"admin": admin, "viewer": viewer, "technologist": technologist} {
		var got []string
		if e := p.QueryRow(ctx, `SELECT array_agg(p.permission_key ORDER BY p.permission_key) FROM auth_role_permission p JOIN auth_role r ON r.id=p.role_id WHERE r.builtin_key=$1`, key).Scan(&got); e != nil {
			t.Fatal(e)
		}
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatal("incorrect seed", key, got, want)
		}
	}
	for key, want := range map[string]int{"viewer": 13, "technologist": 21} {
		if e := p.QueryRow(ctx, `SELECT count(*) FROM auth_role_permission p JOIN auth_role r ON r.id=p.role_id WHERE builtin_key=$1`, key).Scan(&n); e != nil || n != want {
			t.Fatal(key, n, e)
		}
	}
	if _, e := p.Exec(ctx, `DELETE FROM auth_user_role; DELETE FROM auth_role_permission WHERE role_id=(SELECT id FROM auth_role WHERE builtin_key='admin'); DELETE FROM auth_role WHERE builtin_key='viewer'`); e != nil {
		t.Fatal(e)
	}
	if e := Apply(ctx, p); e != nil {
		t.Fatal(e)
	}
	if e := p.QueryRow(ctx, `SELECT count(*) FROM auth_role_permission p JOIN auth_role r ON r.id=p.role_id WHERE builtin_key='admin' OR builtin_key='viewer'`).Scan(&n); e != nil || n != 0 {
		t.Fatal("seed restored", n, e)
	}
	if e := p.QueryRow(ctx, `SELECT count(*) FROM auth_user_role`).Scan(&n); e != nil || n != 0 {
		t.Fatal("membership restored", n, e)
	}

}

func TestRBACMigrationConstraints(t *testing.T) {
	p := rbacMigrationDB(t)
	ctx := context.Background()
	if e := Apply(ctx, p); e != nil {
		t.Fatal(e)
	}
	for _, org := range []string{
		`null`,
		`{"kind":"organization"}`,
		`{"kind":"unknown","ngdu_id":null,"department_id":null}`,
		`{"kind":"organization","ngdu_id":0,"department_id":null}`,
		`{"kind":"department","ngdu_id":0,"department_id":null}`,
		`{"kind":"ngdu","ngdu_id":0.5,"department_id":null}`,
		`{"kind":"ngdu","ngdu_id":9223372036854775808,"department_id":null}`,
		`{"kind":"none","ngdu_id":null,"department_id":null,"extra":true}`,
	} {
		if _, e := p.Exec(ctx, `INSERT INTO auth_user(login,name,password_hash,role,scope,organization_scope)VALUES('bad','bad','hash','viewer','{}',$1)`, org); e == nil {
			t.Fatal("invalid organization persisted", org)
		}
	}
	var unactivated bool
	if e := p.QueryRow(ctx, `SELECT activated_at IS NULL AND revision=1 FROM auth_rbac_state WHERE id`).Scan(&unactivated); e != nil || !unactivated {
		t.Fatal("migration activated runtime", e)
	}
}
