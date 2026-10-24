package migrations

import (
	"context"
	"testing"
)

func TestEmployeeProfilesMigration16To17AndRepeat(t *testing.T) {
	p := rbacMigrationDB(t)
	ctx := context.Background()
	if _, e := p.Exec(ctx, rbacSchema); e != nil {
		t.Fatal(e)
	}
	var id int64
	if e := p.QueryRow(ctx, `INSERT INTO auth_user(login,name,password_hash,role,scope)VALUES('profile','Имя','hash','viewer','{"funds":["operational"],"ngdus":null,"departments":null}') RETURNING id`).Scan(&id); e != nil {
		t.Fatal(e)
	}

	if _, e := p.Exec(ctx, `INSERT INTO auth_role(name,description) VALUES('custom role','preserve'); INSERT INTO auth_group(name) VALUES('custom group'); INSERT INTO auth_role_permission SELECT id,'wells.read' FROM auth_role WHERE name='custom role'`); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `INSERT INTO auth_user_role SELECT $1,id FROM auth_role WHERE name='custom role';`, id); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `INSERT INTO auth_group_member SELECT id,$1 FROM auth_group WHERE name='custom group'`, id); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `INSERT INTO auth_session(token_hash,user_id,expires_at,authz_epoch) VALUES('profile-session',$1,now()+interval '1 hour',1)`, id); e != nil {
		t.Fatal(e)
	}
	snapshot := func() string {
		t.Helper()
		var raw string
		if e := p.QueryRow(ctx, `SELECT jsonb_build_object('roles',(SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM auth_role r),'groups',(SELECT jsonb_agg(to_jsonb(g) ORDER BY id) FROM auth_group g),'members',(SELECT jsonb_agg(to_jsonb(m)) FROM auth_group_member m),'assignments',(SELECT jsonb_agg(to_jsonb(a)) FROM auth_user_role a),'sessions',(SELECT jsonb_agg(to_jsonb(s)) FROM auth_session s),'graph',(SELECT to_jsonb(s) FROM auth_rbac_state s))::text`).Scan(&raw); e != nil {
			t.Fatal(e)
		}
		return raw
	}
	before := snapshot()
	var accountBefore string
	if e := p.QueryRow(ctx, `SELECT to_jsonb(u)::text FROM auth_user u WHERE id=$1`, id).Scan(&accountBefore); e != nil {
		t.Fatal(e)
	}
	if e := Apply(ctx, p); e != nil {
		t.Fatal(e)
	}
	if after := snapshot(); after != before {
		t.Fatal("migration changed RBAC or sessions", before, after)
	}
	var same bool
	if e := p.QueryRow(ctx, `SELECT (to_jsonb(u)-'job_title'-'workplace_organization'-'workplace_division'-'workplace_department'-'manager_account_id'-'profile_revision')=$1::jsonb FROM auth_user u WHERE id=$2`, accountBefore, id).Scan(&same); e != nil || !same {
		t.Fatal("account changed", e)
	}
	var revision int64
	var title *string
	if e := p.QueryRow(ctx, `SELECT profile_revision,job_title FROM auth_user WHERE id=$1`, id).Scan(&revision, &title); e != nil {
		t.Fatal(e)
	}
	if revision != 1 || title != nil {
		t.Fatal(revision, title)
	}
	if _, e := p.Exec(ctx, `UPDATE auth_user SET job_title='Инженер',profile_revision=2 WHERE id=$1`, id); e != nil {
		t.Fatal(e)
	}
	if e := Apply(ctx, p); e != nil {
		t.Fatal(e)
	}
	if e := p.QueryRow(ctx, `SELECT profile_revision,job_title FROM auth_user WHERE id=$1`, id).Scan(&revision, &title); e != nil || revision != 2 || title == nil || *title != "Инженер" {
		t.Fatal(revision, title, e)
	}
}

func TestEmployeeProfilesDDLAtomicity(t *testing.T) {
	p := rbacMigrationDB(t)
	ctx := context.Background()
	if _, e := p.Exec(ctx, rbacSchema); e != nil {
		t.Fatal(e)
	}
	if _, e := p.Exec(ctx, `CREATE TABLE auth_user_manager_account_idx(block boolean)`); e != nil {
		t.Fatal(e)
	}
	if e := Apply(ctx, p); e == nil {
		t.Fatal("DDL conflict accepted")
	}
	var present bool
	if e := p.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='auth_user' AND column_name='job_title') OR EXISTS(SELECT 1 FROM backend_schema_migration WHERE version=17)`).Scan(&present); e != nil || present {
		t.Fatal("partial migration", present, e)
	}
}
func TestEmployeeProfilesConstraints(t *testing.T) {
	p := rbacMigrationDB(t)
	ctx := context.Background()
	if e := Apply(ctx, p); e != nil {
		t.Fatal(e)
	}
	var id int64
	if e := p.QueryRow(ctx, `INSERT INTO auth_user(login,name,password_hash,role,scope)VALUES('profile','Имя','hash','viewer','{"funds":["operational"],"ngdus":null,"departments":null}') RETURNING id`).Scan(&id); e != nil {
		t.Fatal(e)
	}
	for _, q := range []string{`UPDATE auth_user SET manager_account_id=id`, `UPDATE auth_user SET manager_account_id=999999`, `UPDATE auth_user SET profile_revision=0`, `UPDATE auth_user SET job_title=''`, `UPDATE auth_user SET job_title=' trim '`, `UPDATE auth_user SET workplace_division=repeat('я',201)`} {
		if _, e := p.Exec(ctx, q); e == nil {
			t.Fatal("invalid profile accepted", q)
		}
	}
}
