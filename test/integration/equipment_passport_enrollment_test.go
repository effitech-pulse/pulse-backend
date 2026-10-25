package integration

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

func enrollmentFixture(t *testing.T) (*pgxpool.Pool, *postgres.Repository, domain.PassportEnrollmentInput) {
	t.Helper()
	p := testDB(t)
	ctx := context.Background()
	// Custom name and legacy viewer prove that authority comes from effective keys.
	_, e := p.Exec(ctx, `INSERT INTO auth_user(login,name,password_hash,role,scope,organization_scope) VALUES('manager','Renamed manager','hash','viewer','{"funds":["operational"],"ngdus":[7],"departments":[8]}','{"kind":"department","ngdu_id":7,"department_id":8}'); INSERT INTO auth_role(name)VALUES('Custom management'); INSERT INTO auth_user_role SELECT 1,id FROM auth_role WHERE name='Custom management'; INSERT INTO auth_role_permission SELECT r.id,p.permission_key FROM auth_role r CROSS JOIN auth_role_permission p JOIN auth_role b ON b.id=p.role_id WHERE r.name='Custom management' AND b.builtin_key='admin'`)
	if e != nil {
		t.Fatal(e)
	}
	r := postgres.New(p)
	a, e := r.Authorization(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE auth_user SET job_title='Инженер',workplace_division='Цех',profile_revision=4 WHERE id=1; INSERT INTO auth_group(name)VALUES('Existing group'); INSERT INTO auth_group_member SELECT id,1 FROM auth_group WHERE name='Existing group'; INSERT INTO auth_user(login,name,password_hash,role,scope,organization_scope)VALUES('other','Other','hash','viewer','{"funds":["operational"]}','{"kind":"organization","ngdu_id":null,"department_id":null}'); INSERT INTO auth_user_role SELECT 2,id FROM auth_role WHERE name='Custom management'`); e != nil {
		t.Fatal(e)
	}
	return p, r, domain.PassportEnrollmentInput{RequestID: uuid.NewString(), TargetAccountID: 1, ExpectedAccountRevision: 1, ExpectedEpoch: domain.PassportRevision(a.Epoch), ExpectedGraphRevision: domain.PassportRevision(a.GraphRevision), PermissionKeys: []domain.Permission{"wells.read", "equipment_passports.read", "equipment_passports.update", "equipment_passports.import", "equipment_passports.export", "equipment_passports.history.read"}}
}
func enrollmentReceiptEqual(a, b domain.PassportEnrollmentReceipt) bool {
	sameTime := a.RecordedAt.Equal(b.RecordedAt)
	a.RecordedAt = time.Time{}
	b.RecordedAt = time.Time{}
	return sameTime && reflect.DeepEqual(a, b)
}
func TestPassportEnrollment(t *testing.T) {
	for _, sql := range []string{`UPDATE auth_user SET active=false WHERE id=1`, `DELETE FROM auth_role_permission WHERE permission_key='users.scope.assign'`} {
		t.Run("replay requires current manager", func(t *testing.T) {
			p, r, in := enrollmentFixture(t)
			ctx := context.Background()
			if _, e := r.EnrollEquipmentPassportPermissions(ctx, in); e != nil {
				t.Fatal(e)
			}
			if _, e := p.Exec(ctx, sql); e != nil {
				t.Fatal(e)
			}
			_, e := r.EnrollEquipmentPassportPermissions(ctx, in)
			equipmentKind(t, e, domain.Forbidden)
			if equipmentCount(t, p, "equipment_passport_enrollment") != 1 {
				t.Fatal("replay mutated audit")
			}
		})
	}
	t.Run("renamed manager replay audit preservation sessions", func(t *testing.T) {
		p, r, in := enrollmentFixture(t)
		ctx := context.Background()
		otherBefore, e := r.Authorization(ctx, 2)
		if e != nil {
			t.Fatal(e)
		}
		var profileBefore []byte
		if e = p.QueryRow(ctx, `SELECT jsonb_build_array(job_title,workplace_division,profile_revision,role,scope,organization_scope) FROM auth_user WHERE id=1`).Scan(&profileBefore); e != nil {
			t.Fatal(e)
		}
		before, e := r.Authorization(ctx, 1)
		if e != nil {
			t.Fatal(e)
		}
		// Session bytes are synthetic; enrollment must revoke every target session.
		if _, e = p.Exec(ctx, `INSERT INTO auth_session(user_id,token_hash,expires_at)VALUES(1,'synthetic-enrollment-session',clock_timestamp()+interval '1 hour')`); e != nil {
			t.Fatal(e)
		}
		receipt, e := r.EnrollEquipmentPassportPermissions(ctx, in)
		if e != nil {
			t.Fatal(e)
		}
		if receipt.ActorKind != "maintenance" || receipt.TargetAccountID != 1 || receipt.RequestID != in.RequestID || receipt.Digest == "" || receipt.RecordedAt.IsZero() {
			t.Fatal(receipt)
		}
		if !reflect.DeepEqual(receipt.Before, before) || receipt.After.Epoch != before.Epoch+1 || receipt.After.GraphRevision != before.GraphRevision+1 || receipt.After.Require(in.PermissionKeys...) != nil {
			t.Fatal(receipt)
		}
		if !reflect.DeepEqual(before.Organization, receipt.After.Organization) || !reflect.DeepEqual(before.LegacyScope, receipt.After.LegacyScope) || len(receipt.After.Assignments.DirectRoleIDs) != len(before.Assignments.DirectRoleIDs)+1 {
			t.Fatal("scope or old roles changed")
		}
		for _, id := range before.Assignments.DirectRoleIDs {
			if !slices.Contains(receipt.After.Assignments.DirectRoleIDs, id) {
				t.Fatal("old role removed")
			}
		}
		var revision int64
		if e = p.QueryRow(ctx, `SELECT revision FROM auth_user WHERE id=1`).Scan(&revision); e != nil || revision != 2 {
			t.Fatal(revision, e)
		}
		if equipmentCount(t, p, "auth_session") != 0 || equipmentCount(t, p, "equipment_passport_enrollment") != 1 {
			t.Fatal("session/audit mismatch")
		}
		otherAfter, e := r.Authorization(ctx, 2)
		if e != nil {
			t.Fatal(e)
		}
		// Graph revision is global, every other per-account field must remain identical.
		otherAfter.GraphRevision = otherBefore.GraphRevision
		if !reflect.DeepEqual(otherBefore, otherAfter) {
			t.Fatal("other account modified")
		}
		var profileAfter []byte
		if e = p.QueryRow(ctx, `SELECT jsonb_build_array(job_title,workplace_division,profile_revision,role,scope,organization_scope) FROM auth_user WHERE id=1`).Scan(&profileAfter); e != nil || string(profileBefore) != string(profileAfter) {
			t.Fatal("profile/legacy state changed", e)
		}
		if !reflect.DeepEqual(before.Assignments.Groups, receipt.After.Assignments.Groups) {
			t.Fatal("groups changed")
		}
		slices.Reverse(in.PermissionKeys)
		replay, e := r.EnrollEquipmentPassportPermissions(ctx, in)
		if e != nil || !enrollmentReceiptEqual(receipt, replay) {
			t.Fatal("replay changed receipt", e)
		}
		after, e := r.Authorization(ctx, 1)
		if e != nil || !reflect.DeepEqual(after, receipt.After) {
			t.Fatal("replay mutated access", e)
		}
		collision := in
		collision.TargetAccountID = 2
		_, e = r.EnrollEquipmentPassportPermissions(ctx, collision)
		equipmentKind(t, e, domain.Duplicate)
		collision = in
		collision.ExpectedEpoch++
		_, e = r.EnrollEquipmentPassportPermissions(ctx, collision)
		equipmentKind(t, e, domain.Duplicate)
		in.PermissionKeys = []domain.Permission{"wells.read", "equipment_passports.read"}
		_, e = r.EnrollEquipmentPassportPermissions(ctx, in)
		equipmentKind(t, e, domain.Duplicate)
		var payload []byte
		if e = p.QueryRow(ctx, `SELECT payload FROM equipment_passport_enrollment`).Scan(&payload); e != nil {
			t.Fatal(e)
		}
		var persisted domain.PassportEnrollmentReceipt
		if e = json.Unmarshal(payload, &persisted); e != nil || !enrollmentReceiptEqual(persisted, receipt) {
			t.Fatal("typed audit mismatch", e)
		}
		if _, e = p.Exec(ctx, `UPDATE equipment_passport_enrollment SET actor_kind='maintenance'`); e == nil {
			t.Fatal("audit mutable")
		}
	})
	for _, tc := range []struct {
		name   string
		change func(*domain.PassportEnrollmentInput)
		sql    string
		kind   domain.Kind
	}{
		{name: "inactive", sql: `UPDATE auth_user SET active=false WHERE id=1`, kind: domain.Forbidden},
		{name: "named admin without effective manager", sql: `UPDATE auth_user SET role='admin',name='admin' WHERE id=1; DELETE FROM auth_role_permission WHERE permission_key='users.scope.assign'`, kind: domain.Forbidden},
		{name: "stale account", change: func(in *domain.PassportEnrollmentInput) { in.ExpectedAccountRevision++ }, kind: domain.Duplicate},
		{name: "stale epoch", change: func(in *domain.PassportEnrollmentInput) { in.ExpectedEpoch++ }, kind: domain.Duplicate},
		{name: "stale graph", change: func(in *domain.PassportEnrollmentInput) { in.ExpectedGraphRevision++ }, kind: domain.Duplicate},
		{name: "unknown target", change: func(in *domain.PassportEnrollmentInput) { in.TargetAccountID = 999 }, kind: domain.Missing},
		{name: "unknown key", change: func(in *domain.PassportEnrollmentInput) { in.PermissionKeys = append(in.PermissionKeys, "unknown") }, kind: domain.Invalid},
		{name: "unrelated key", change: func(in *domain.PassportEnrollmentInput) { in.PermissionKeys = append(in.PermissionKeys, "users.read") }, kind: domain.Invalid},
		{name: "missing closure", change: func(in *domain.PassportEnrollmentInput) {
			in.PermissionKeys = []domain.Permission{"equipment_passports.import", "equipment_passports.read", "wells.read"}
		}, kind: domain.Invalid},
		{name: "empty", change: func(in *domain.PassportEnrollmentInput) { in.PermissionKeys = nil }, kind: domain.Invalid},
		{name: "duplicate key", change: func(in *domain.PassportEnrollmentInput) { in.PermissionKeys = append(in.PermissionKeys, "wells.read") }, kind: domain.Invalid},
		{name: "invalid uuid", change: func(in *domain.PassportEnrollmentInput) { in.RequestID = "bad" }, kind: domain.Invalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, r, in := enrollmentFixture(t)
			ctx := context.Background()
			if tc.change != nil {
				tc.change(&in)
			}
			if tc.sql != "" {
				if _, e := p.Exec(ctx, tc.sql); e != nil {
					t.Fatal(e)
				}
			}
			_, e := r.EnrollEquipmentPassportPermissions(ctx, in)
			equipmentKind(t, e, tc.kind)
			if equipmentCount(t, p, "equipment_passport_enrollment") != 0 {
				t.Fatal("rejection wrote audit")
			}
		})
	}
}
func TestEquipmentPassportBootstrapRestoreNoAutoGrant(t *testing.T) {
	p := testDB(t)
	ctx := context.Background()
	if _, e := p.Exec(ctx, `DELETE FROM auth_role WHERE builtin_key='admin'`); e != nil {
		t.Fatal(e)
	}
	r := postgres.New(p)
	a, e := r.CreateAccount(ctx, domain.Account{Login: "restored", Name: "Restored", Scope: domain.AccessScope{Funds: []string{"operational"}}}, "hash", true)
	if e != nil {
		t.Fatal(e)
	}
	snapshot, e := r.Authorization(ctx, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(snapshot.Permissions, domain.BuiltinAccessPermissions("admin")) {
		t.Fatal("restore expanded frozen permissions", snapshot.Permissions)
	}
	if equipmentCount(t, p, "equipment_passport_enrollment") != 0 {
		t.Fatal("bootstrap enrolled")
	}
}
