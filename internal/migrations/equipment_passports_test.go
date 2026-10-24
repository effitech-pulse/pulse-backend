package migrations

import (
	"context"
	"testing"
)

func TestEquipmentPassportMigration18(t *testing.T) {
	p := rbacMigrationDB(t)
	ctx := context.Background()
	for _, schema := range []string{rbacSchema, employeeProfilesSchema} {
		if _, e := p.Exec(ctx, schema); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := p.Exec(ctx, `INSERT INTO well(ngdu,department)VALUES(0,0); INSERT INTO auth_user(login,name,password_hash,role,scope)VALUES('passport','Сотрудник','hash','viewer','{"funds":["operational"]}'); INSERT INTO auth_user_role SELECT u.id,r.id FROM auth_user u CROSS JOIN auth_role r WHERE r.builtin_key='viewer'`); e != nil {
		t.Fatal(e)
	}
	snapshot := func() string {
		t.Helper()
		var raw string
		e := p.QueryRow(ctx, `SELECT jsonb_build_object('wells',(SELECT jsonb_agg(to_jsonb(w)) FROM well w),'users',(SELECT jsonb_agg(to_jsonb(u)) FROM auth_user u),'roles',(SELECT jsonb_agg(to_jsonb(r)) FROM auth_role r),'permissions',(SELECT jsonb_agg(to_jsonb(k) ORDER BY role_id,permission_key) FROM auth_role_permission k),'assignments',(SELECT jsonb_agg(to_jsonb(a)) FROM auth_user_role a),'graph',(SELECT to_jsonb(g) FROM auth_rbac_state g))::text`).Scan(&raw)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	before := snapshot()
	if e := Apply(ctx, p); e != nil {
		t.Fatal(e)
	}
	var present bool
	if e := p.QueryRow(ctx, `SELECT to_regclass('equipment_passport') IS NOT NULL AND to_regclass('equipment_passport_enrollment') IS NOT NULL AND EXISTS(SELECT 1 FROM backend_schema_migration WHERE version=18)`).Scan(&present); e != nil || !present {
		t.Fatal("migration18 missing", e)
	}
	if before != snapshot() {
		t.Fatal("migration modified existing well/RBAC")
	}
	if e := Apply(ctx, p); e != nil {
		t.Fatal(e)
	}
	if before != snapshot() {
		t.Fatal("reapply modified existing well/RBAC")
	}
}
