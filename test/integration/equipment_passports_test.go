package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/spreadsheet"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

var equipmentPolicy = domain.PassportPolicy{AppScope: "research"}

func equipmentFixture(t *testing.T) (*pgxpool.Pool, *postgres.Repository, context.Context) {
	t.Helper()
	p := testDB(t)
	ctx := context.Background()
	_, e := p.Exec(ctx, `INSERT INTO well(ngdu,department)VALUES(0,0),(0,0); INSERT INTO auth_user(login,name,password_hash,role,scope,organization_scope)VALUES('equipment','Иван','hash','viewer','{"funds":["operational"]}','{"kind":"organization","ngdu_id":null,"department_id":null}'); INSERT INTO auth_role(name)VALUES('equipment-test'); INSERT INTO auth_role_permission SELECT id,k FROM auth_role CROSS JOIN unnest(ARRAY['wells.read','equipment_passports.read','equipment_passports.update','equipment_passports.import','equipment_passports.export','equipment_passports.history.read'])k WHERE name='equipment-test'; INSERT INTO auth_user_role SELECT u.id,r.id FROM auth_user u CROSS JOIN auth_role r WHERE u.login='equipment' AND r.name='equipment-test'`)
	if e != nil {
		t.Fatal(e)
	}
	r := postgres.New(p)
	a, e := r.Authorization(ctx, 1)
	if e != nil {
		t.Fatal(e)
	}
	id := int64(1)
	ctx = domain.WithRBACMode(domain.WithPrincipal(ctx, domain.Principal{Authenticated: true, Mechanism: "session", AccountID: &id, Name: "Иван", Authorization: &a}), domain.RBACEnforce)
	return p, r, ctx
}
func equipmentRows(m ...domain.PassportMutation) domain.PassportChangeSet {
	return domain.PassportChangeSet{SchemaVersion: domain.PassportSchemaVersion, Rows: m}
}
func equipmentRegister(well domain.PassportID) domain.PassportMutation {
	m := domain.PassportMutation{RowKey: fmt.Sprint("r", well), WellID: well, ExpectedWellRevision: 1, Slot: "pump", Action: "register"}
	m.Model = domain.Field[string]{Present: true, Value: "Насос"}
	m.PumpNominalHeadM = domain.Field[domain.PassportDecimal]{Present: true, Value: "1000000000"}
	return m
}
func equipmentPreview(t *testing.T, r *postgres.Repository, ctx context.Context, rows domain.PassportChangeSet) domain.PassportPreview {
	t.Helper()
	v, e := r.PreparePassport(ctx, equipmentPolicy, rows, domain.PassportProvenance{Source: "manual"}, "2026-10-24")
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func equipmentCommit(t *testing.T, r *postgres.Repository, ctx context.Context, v domain.PassportPreview) domain.PassportReceipt {
	t.Helper()
	receipt, e := r.CommitPassport(ctx, equipmentPolicy, domain.PassportCommitInput{PreviewID: v.ID, Digest: v.Digest, AcknowledgeWarnings: true, IdempotencyKey: uuid.NewString()}, "2026-10-24")
	if e != nil {
		t.Fatal(e)
	}
	return receipt
}
func equipmentCount(t *testing.T, p *pgxpool.Pool, table string) int {
	t.Helper()
	var n int // table names in tests are fixed literals.
	if e := p.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func equipmentKind(t *testing.T, e error, want domain.Kind) {
	t.Helper()
	var de *domain.Error
	if !errors.As(e, &de) || de.Kind != want {
		t.Fatalf("want %s got %v", want, e)
	}
}

func TestEquipmentPassportAtomicCommit(t *testing.T) {
	p, r, ctx := equipmentFixture(t)
	v := equipmentPreview(t, r, ctx, equipmentRows(equipmentRegister(1), equipmentRegister(2)))
	_, e := r.CommitPassport(ctx, equipmentPolicy, domain.PassportCommitInput{PreviewID: v.ID, Digest: strings.Repeat("f", 64), AcknowledgeWarnings: true, IdempotencyKey: uuid.NewString()}, "2026-10-24")
	equipmentKind(t, e, domain.Duplicate)
	if equipmentCount(t, p, "equipment_passport_event") != 0 {
		t.Fatal("forged digest wrote")
	}
	if _, e = p.Exec(ctx, `UPDATE well SET revision=revision+1 WHERE id=2`); e != nil {
		t.Fatal(e)
	}
	_, e = r.CommitPassport(ctx, equipmentPolicy, domain.PassportCommitInput{PreviewID: v.ID, Digest: v.Digest, AcknowledgeWarnings: true, IdempotencyKey: uuid.NewString()}, "2026-10-24")
	equipmentKind(t, e, domain.Duplicate)
	if equipmentCount(t, p, "equipment_passport") != 0 || equipmentCount(t, p, "equipment_passport_receipt") != 0 {
		t.Fatal("mixed conflict partially wrote")
	}
	// Mixed allowed/denied input is rejected before preview, with no head creation.
	if _, e = p.Exec(ctx, `UPDATE auth_user SET organization_scope='{"kind":"none","ngdu_id":null,"department_id":null}' WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	_, e = r.PreparePassport(ctx, equipmentPolicy, equipmentRows(equipmentRegister(1), equipmentRegister(2)), domain.PassportProvenance{Source: "manual"}, "2026-10-24")
	equipmentKind(t, e, domain.Missing)
}
func TestEquipmentPassportConcurrentCreate(t *testing.T) {
	p, r, ctx := equipmentFixture(t)
	a := equipmentPreview(t, r, ctx, equipmentRows(equipmentRegister(1)))
	b := equipmentPreview(t, r, ctx, equipmentRows(equipmentRegister(1)))
	start := make(chan struct{})
	done := make(chan error, 2)
	var wg sync.WaitGroup
	for _, v := range []domain.PassportPreview{a, b} {
		wg.Add(1)
		go func(v domain.PassportPreview) {
			defer wg.Done()
			<-start
			_, e := r.CommitPassport(ctx, equipmentPolicy, domain.PassportCommitInput{PreviewID: v.ID, Digest: v.Digest, AcknowledgeWarnings: true, IdempotencyKey: uuid.NewString()}, "2026-10-24")
			done <- e
		}(v)
	}
	close(start)
	wg.Wait()
	close(done)
	success := 0
	for e := range done {
		if e == nil {
			success++
		} else {
			equipmentKind(t, e, domain.Duplicate)
		}
	}
	if success != 1 || equipmentCount(t, p, "equipment_passport") != 1 || equipmentCount(t, p, "equipment_passport_event") != 1 {
		t.Fatal("duplicate concurrent effects", success)
	}
}
func TestEquipmentPassportHistoryDates(t *testing.T) {
	p, r, ctx := equipmentFixture(t)
	v := equipmentPreview(t, r, ctx, equipmentRows(equipmentRegister(1)))
	first := equipmentCommit(t, r, ctx, v)
	view, e := r.ReadPassport(ctx, equipmentPolicy, 1)
	if e != nil || len(view.Slots) != 1 || view.Slots[0].InstalledOn != nil || view.Slots[0].Status != "current" || len(view.Provenance) != 1 {
		t.Fatal(view, e)
	}
	old := view.Slots[0]
	m := equipmentRegister(1)
	m.Action = "replace"
	m.ExpectedPassportRevision = view.Revision
	m.ComponentID = old.ComponentID
	m.InstallationID = old.ID
	m.ChangeReason = "Физическая замена"
	m.ReplacementOn = domain.Field[domain.PassportDate]{Present: true, Null: true}
	v = equipmentPreview(t, r, ctx, equipmentRows(m))
	second := equipmentCommit(t, r, ctx, v)
	view, e = r.ReadPassport(ctx, equipmentPolicy, 1)
	if e != nil || *view.Slots[0].ID == *old.ID || view.Slots[0].InstalledOn != nil {
		t.Fatal(view, e)
	}
	current := view.Slots[0]
	// An unrelated correction of the predecessor must not overwrite current LastChange.
	m.Action = "correct"
	m.ExpectedPassportRevision = view.Revision
	m.ReplacementOn = domain.Field[domain.PassportDate]{}
	m.Model = domain.Field[string]{Present: true, Value: "Исправлено старое имя"}
	v = equipmentPreview(t, r, ctx, equipmentRows(m))
	equipmentCommit(t, r, ctx, v)
	view, e = r.ReadPassport(ctx, equipmentPolicy, 1)
	if e != nil || view.Provenance[0].LastChange.ReceiptID != second.ID {
		t.Fatal("predecessor correction rewrote current provenance", view, e)
	}
	// Linked correction touches both predecessor and current successor, and updates provenance.
	m.ExpectedPassportRevision = view.Revision
	m.Model = domain.Field[string]{}
	m.LinkedInstallationID = current.ID
	m.ReplacementOn = domain.Field[domain.PassportDate]{Present: true, Value: "2026-05-01"}
	v = equipmentPreview(t, r, ctx, equipmentRows(m))
	linked := equipmentCommit(t, r, ctx, v)
	view, e = r.ReadPassport(ctx, equipmentPolicy, 1)
	if e != nil || *view.Slots[0].InstalledOn != "2026-05-01" || view.Provenance[0].LastChange.ReceiptID != linked.ID {
		t.Fatal(view, e)
	}
	history, e := r.PassportHistoryTable(ctx, equipmentPolicy, 1, domain.TableRequest{Query: domain.TableQuery{Version: 1}, PageSize: 50})
	if e != nil || len(history.Items) != 4 {
		t.Fatal(history, e)
	}
	if history.Items[3].ReceiptID != first.ID || history.Items[3].Diff.After[0].InstalledOn != nil || history.Items[0].Diff.After[0].RemovedOn == nil || len(history.Items[0].Diff.After) != 2 {
		t.Fatal("original event/linked pair changed", history)
	}
	// Void current frees slot and never restores predecessor.
	m = equipmentRegister(1)
	m.Action = "void"
	m.PassportAttributesPatch = domain.PassportAttributesPatch{}
	m.ComponentID = current.ComponentID
	m.InstallationID = current.ID
	m.ExpectedPassportRevision = view.Revision
	m.ChangeReason = "Ошибочная установка"
	equipmentCommit(t, r, ctx, equipmentPreview(t, r, ctx, equipmentRows(m)))
	view, e = r.ReadPassport(ctx, equipmentPolicy, 1)
	if e != nil || len(view.Slots) != 0 || len(view.Provenance) != 0 {
		t.Fatal(view, e)
	}
	if _, e = p.Exec(ctx, `UPDATE equipment_passport_event SET reason='rewrite'`); e == nil {
		t.Fatal("history mutable")
	}
}
func TestEquipmentPassportAuthorityAndReplay(t *testing.T) {
	p, r, ctx := equipmentFixture(t)
	v := equipmentPreview(t, r, ctx, equipmentRows(equipmentRegister(1)))
	key := uuid.NewString()
	in := domain.PassportCommitInput{PreviewID: v.ID, Digest: v.Digest, AcknowledgeWarnings: true, IdempotencyKey: key}
	receipt, e := r.CommitPassport(ctx, equipmentPolicy, in, "2026-10-24")
	if e != nil {
		t.Fatal(e)
	}
	// Expired preview payload is gone; durable key replay still returns the same receipt.
	if _, e = p.Exec(ctx, `UPDATE equipment_passport_preview SET expires_at=now()-interval '25 hours'`); e != nil {
		t.Fatal(e)
	}
	if e = r.PurgeExpiredPassportPreviews(ctx); e != nil {
		t.Fatal(e)
	}
	again, e := r.CommitPassport(ctx, equipmentPolicy, in, "2026-10-24")
	if e != nil || receipt.ID != again.ID || equipmentCount(t, p, "equipment_passport_event") != 1 {
		t.Fatal(again, e)
	}
	in.Digest = strings.Repeat("f", 64)
	_, e = r.CommitPassport(ctx, equipmentPolicy, in, "2026-10-24")
	equipmentKind(t, e, domain.Duplicate)
	if _, e = p.Exec(ctx, `UPDATE well SET archived_at=now() WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	// Receipt remains readable when archived; replay does not write or validate stale revisions.
	if _, e = r.ReadPassportReceipt(ctx, equipmentPolicy, receipt.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `UPDATE auth_user SET organization_scope='{"kind":"none","ngdu_id":null,"department_id":null}' WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	_, e = r.ReadPassportReceipt(ctx, equipmentPolicy, receipt.ID)
	equipmentKind(t, e, domain.Missing)
	if _, e = p.Exec(ctx, `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	_, e = r.ReadPassport(ctx, equipmentPolicy, 1)
	equipmentKind(t, e, domain.Unauthorized)
}
func TestEquipmentPassportPreviewRetention(t *testing.T) {
	p, r, ctx := equipmentFixture(t)
	a := equipmentPreview(t, r, ctx, equipmentRows(equipmentRegister(1)))
	equipmentPreview(t, r, ctx, equipmentRows(equipmentRegister(1)))
	_, e := r.PreparePassport(ctx, equipmentPolicy, equipmentRows(equipmentRegister(1)), domain.PassportProvenance{Source: "manual"}, "2026-10-24")
	equipmentKind(t, e, domain.Limited)
	var ttl bool
	if e = p.QueryRow(ctx, `SELECT expires_at BETWEEN now()+interval '29 minutes' AND now()+interval '31 minutes' FROM equipment_passport_preview WHERE id=$1`, int64(a.ID)).Scan(&ttl); e != nil || !ttl {
		t.Fatal("TTL", e)
	}
	if _, e = p.Exec(ctx, `UPDATE equipment_passport_preview SET expires_at=now()-interval '1 second' WHERE id=$1`, int64(a.ID)); e != nil {
		t.Fatal(e)
	}
	_, e = r.ReadPassportPreview(ctx, equipmentPolicy, a.ID)
	var pe *domain.PassportError
	if !errors.As(e, &pe) || pe.Code != "preview_expired" {
		t.Fatal(e)
	}
	_, e = r.CommitPassport(ctx, equipmentPolicy, domain.PassportCommitInput{PreviewID: a.ID, Digest: a.Digest, AcknowledgeWarnings: true, IdempotencyKey: uuid.NewString()}, "2026-10-24")
	if !errors.As(e, &pe) || pe.Code != "preview_expired" {
		t.Fatal(e)
	}
	if e = r.PurgeExpiredPassportPreviews(ctx); e != nil {
		t.Fatal(e)
	}
	var payload []byte
	if e = p.QueryRow(ctx, `SELECT payload FROM equipment_passport_preview WHERE id=$1`, int64(a.ID)).Scan(&payload); e != nil || payload != nil {
		t.Fatal("expired payload retained", e)
	}
	// Malformed oversized normalized payload has a separate bounded budget even before DB CHECK.
	huge := equipmentRegister(1)
	huge.Model = domain.Field[string]{Present: true, Value: strings.Repeat("x", 1100000)}
	_, e = r.PreparePassport(ctx, equipmentPolicy, equipmentRows(huge), domain.PassportProvenance{Source: "manual"}, "2026-10-24")
	if !errors.As(e, &pe) || pe.Code != "file_limit_exceeded" {
		t.Fatal("oversize", e)
	}
}
func TestEquipmentPassportExcelProvenance(t *testing.T) {
	_, r, ctx := equipmentFixture(t)
	hash := strings.Repeat("a", 64)
	version := domain.PassportSchemaVersion
	v, e := r.PreparePassport(ctx, equipmentPolicy, equipmentRows(equipmentRegister(1)), domain.PassportProvenance{Source: "excel", SourceFileSHA256: &hash, TemplateVersion: &version, Rows: []domain.PassportRowSource{{RowKey: "r1", Sheet: "components", Index: 7}}}, "2026-10-24")
	if e != nil {
		t.Fatal(e)
	}
	receipt := equipmentCommit(t, r, ctx, v)
	history, e := r.PassportHistoryTable(ctx, equipmentPolicy, 1, domain.TableRequest{Query: domain.TableQuery{Version: 1}, PageSize: 50})
	if e != nil || len(history.Items) != 1 {
		t.Fatal(history, e)
	}
	event := history.Items[0]
	if event.SourceRow == nil || event.SourceRow.Index != 7 || event.SourceFileSHA256 == nil || *event.SourceFileSHA256 != hash || event.ReceiptID != receipt.ID {
		t.Fatal(event)
	}
	// No history permission is required to read current provenance.
	p, _ := domain.CurrentPrincipal(ctx)
	a := *p.Authorization
	a.Permissions = []domain.Permission{"wells.read", "equipment_passports.read"}
	p.Authorization = &a
	read := domain.WithPrincipal(ctx, p)
	view, e := r.ReadPassport(read, equipmentPolicy, 1)
	if e != nil || len(view.Provenance) != 1 || view.Provenance[0].LastChange.SourceRow.Index != 7 {
		t.Fatal(view, e)
	}
	raw, _ := json.Marshal(event)
	if !strings.Contains(string(raw), `"receipt_id":"`) {
		t.Fatal(string(raw))
	}
}

func TestEquipmentPassportConsumedPreviewReleasesBudget(t *testing.T) {
	_, r, ctx := equipmentFixture(t)
	a := equipmentPreview(t, r, ctx, equipmentRows(equipmentRegister(1)))
	receipt := equipmentCommit(t, r, ctx, a)
	b := equipmentPreview(t, r, ctx, equipmentRows(equipmentRegister(2)))
	equipmentCommit(t, r, ctx, b)
	// A third preview is legal immediately after two completed intents.
	view, e := r.ReadPassport(ctx, equipmentPolicy, 1)
	if e != nil {
		t.Fatal(e)
	}
	m := equipmentRegister(1)
	m.Action = "update"
	m.ComponentID = view.Slots[0].ComponentID
	m.InstallationID = view.Slots[0].ID
	m.ExpectedPassportRevision = view.Revision
	m.PassportAttributesPatch = domain.PassportAttributesPatch{}
	c := equipmentPreview(t, r, ctx, equipmentRows(m))
	if c.UnchangedCount != 1 {
		t.Fatal(c)
	}
	_, e = r.ReadPassportPreview(ctx, equipmentPolicy, a.ID)
	var pe *domain.PassportError
	if !errors.As(e, &pe) || pe.Code != "preview_consumed" {
		t.Fatal("consumed getter must fail closed", e)
	}
	again, e := r.CommitPassport(ctx, equipmentPolicy, domain.PassportCommitInput{PreviewID: a.ID, Digest: a.Digest, IdempotencyKey: receipt.RequestID, AcknowledgeWarnings: true}, "2026-10-24")
	if e != nil || again.ID != receipt.ID {
		t.Fatal("durable replay lost", again, e)
	}
}

// Observe a real database lock wait, instead of depending on scheduling/sleeps.
func equipmentWaitBlocked(t *testing.T, p *pgxpool.Pool, pid int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked bool
		if e := p.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); e != nil {
			t.Fatal(e)
		}
		if blocked {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("commit did not reach actor lock barrier")
		case <-ticker.C:
		}
	}
}
func TestEquipmentPassportAuthorityBarriers(t *testing.T) {
	for _, tt := range []struct {
		name, sql string
		want      domain.Kind
	}{
		{"revoke", `DELETE FROM auth_role_permission WHERE permission_key='equipment_passports.update'`, domain.Forbidden},
		{"epoch", `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=1`, domain.Unauthorized},
		{"scope", `UPDATE auth_user SET scope='{"funds":[]}' WHERE id=1`, domain.Missing},
		{"archive", `UPDATE well SET archived_at=now() WHERE id=1`, domain.Duplicate},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, r, ctx := equipmentFixture(t)
			v := equipmentPreview(t, r, ctx, equipmentRows(equipmentRegister(1)))
			lock, e := p.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer lock.Rollback(ctx)
			var pid int
			if e = lock.QueryRow(ctx, `SELECT pg_backend_pid() FROM auth_user WHERE id=1 FOR UPDATE`).Scan(&pid); e != nil {
				t.Fatal(e)
			}
			work, cancel := context.WithCancel(ctx)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, e := r.CommitPassport(work, equipmentPolicy, domain.PassportCommitInput{PreviewID: v.ID, Digest: v.Digest, AcknowledgeWarnings: true, IdempotencyKey: uuid.NewString()}, "2026-10-24")
				done <- e
			}()
			equipmentWaitBlocked(t, p, pid)
			if _, e = lock.Exec(ctx, tt.sql); e != nil {
				t.Fatal(e)
			}
			if e = lock.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			equipmentKind(t, <-done, tt.want)
			if equipmentCount(t, p, "equipment_passport_receipt") != 0 || equipmentCount(t, p, "equipment_passport") != 0 {
				t.Fatal("authority race wrote")
			}
		})
	}
}
func equipmentActor(t *testing.T, p *pgxpool.Pool, r *postgres.Repository, login string) context.Context {
	t.Helper()
	var id int64
	ctx := context.Background()
	e := p.QueryRow(ctx, `INSERT INTO auth_user(login,name,password_hash,role,scope,organization_scope)VALUES($1,$1,'hash','viewer','{"funds":["operational"]}','{"kind":"organization","ngdu_id":null,"department_id":null}') RETURNING id`, login).Scan(&id)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, `INSERT INTO auth_user_role SELECT $1,id FROM auth_role WHERE name='equipment-test'`, id); e != nil {
		t.Fatal(e)
	}
	a, e := r.Authorization(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	return domain.WithRBACMode(domain.WithPrincipal(ctx, domain.Principal{Authenticated: true, Mechanism: "session", AccountID: &id, Name: login, Authorization: &a}), domain.RBACEnforce)
}
func TestEquipmentPassportGlobalPreviewQuota(t *testing.T) {
	p, r, first := equipmentFixture(t)
	firstPreview := equipmentPreview(t, r, first, equipmentRows(equipmentRegister(1)))
	equipmentPreview(t, r, first, equipmentRows(equipmentRegister(1)))
	for i := 0; i < 7; i++ {
		ctx := equipmentActor(t, p, r, fmt.Sprint("quota", i))
		equipmentPreview(t, r, ctx, equipmentRows(equipmentRegister(1)))
		equipmentPreview(t, r, ctx, equipmentRows(equipmentRegister(1)))
	}
	ninth := equipmentActor(t, p, r, "quota-ninth")
	_, e := r.PreparePassport(ninth, equipmentPolicy, equipmentRows(equipmentRegister(1)), domain.PassportProvenance{Source: "manual"}, "2026-10-24")
	equipmentKind(t, e, domain.Limited)
	var count, bytes int
	if e = p.QueryRow(first, `SELECT count(*),sum(octet_length(payload::text)) FROM equipment_passport_preview WHERE payload IS NOT NULL`).Scan(&count, &bytes); e != nil || count != 16 || bytes > 16777216 {
		t.Fatal(count, bytes, e)
	}
	_, e = r.ReadPassportPreview(ninth, equipmentPolicy, firstPreview.ID)
	equipmentKind(t, e, domain.Missing)
	if e = r.CancelPassportPreview(first, equipmentPolicy, firstPreview.ID); e != nil {
		t.Fatal(e)
	}
	equipmentPreview(t, r, ninth, equipmentRows(equipmentRegister(1)))
	if _, e = p.Exec(first, `UPDATE equipment_passport_preview SET payload=jsonb_build_object('too_large',repeat('a',1048576)) WHERE id=(SELECT max(id) FROM equipment_passport_preview)`); e == nil {
		t.Fatal("DB accepted >1MiB payload")
	}
}
func TestEquipmentPassportStaleClearAndSharedRevision(t *testing.T) {
	p, r, ctx := equipmentFixture(t)
	pump := equipmentRegister(1)
	motor := equipmentRegister(1)
	motor.RowKey = "motor"
	motor.Slot = "motor"
	motor.PassportAttributesPatch = domain.PassportAttributesPatch{Model: domain.Field[string]{Present: true, Value: "ПЭД"}}
	initial := equipmentCommit(t, r, ctx, equipmentPreview(t, r, ctx, equipmentRows(pump, motor)))
	if len(initial.Wells) != 1 || initial.Wells[0].AfterRevision != 1 || initial.ChangedCount != 2 {
		t.Fatal(initial)
	}
	var shared bool
	if e := p.QueryRow(ctx, `SELECT count(*)=2 AND min(revision)=1 AND max(revision)=1 FROM equipment_passport_event`).Scan(&shared); e != nil || !shared {
		t.Fatal("per-well revision/event count", e)
	}
	view, e := r.ReadPassport(ctx, equipmentPolicy, 1)
	if e != nil {
		t.Fatal(e)
	}
	var current domain.PassportInstallation
	for _, v := range view.Slots {
		if v.Slot == "pump" {
			current = v
		}
	}
	m := equipmentRegister(1)
	m.Action = "update"
	m.ComponentID = current.ComponentID
	m.InstallationID = current.ID
	m.ExpectedPassportRevision = view.Revision
	m.ChangeReason = "Уточнение"
	m.PassportAttributesPatch = domain.PassportAttributesPatch{Model: domain.Field[string]{Present: true, Null: true}}
	clear := equipmentPreview(t, r, ctx, equipmentRows(m))
	m.Model = domain.Field[string]{Present: true, Value: "Новое имя"}
	edit := equipmentPreview(t, r, ctx, equipmentRows(m))
	equipmentCommit(t, r, ctx, edit)
	_, e = r.CommitPassport(ctx, equipmentPolicy, domain.PassportCommitInput{PreviewID: clear.ID, Digest: clear.Digest, AcknowledgeWarnings: true, IdempotencyKey: uuid.NewString()}, "2026-10-24")
	equipmentKind(t, e, domain.Duplicate)
	view, e = r.ReadPassport(ctx, equipmentPolicy, 1)
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range view.Slots {
		if v.Slot == "pump" && *v.Attributes.Model != "Новое имя" {
			t.Fatal("stale clear overwrote", view)
		}
	}
	var wellRevision int64
	if e = p.QueryRow(ctx, "SELECT revision FROM well WHERE id=1").Scan(&wellRevision); e != nil || wellRevision != 1 {
		t.Fatal("passport wrote well revision", e)
	}
}
func TestEquipmentPassportNoopReceiptTargetsAndProvenance(t *testing.T) {
	p, r, ctx := equipmentFixture(t)
	initial := equipmentCommit(t, r, ctx, equipmentPreview(t, r, ctx, equipmentRows(equipmentRegister(1), equipmentRegister(2))))
	rows := []domain.PassportMutation{}
	for _, id := range []domain.PassportID{1, 2} {
		view, e := r.ReadPassport(ctx, equipmentPolicy, id)
		if e != nil {
			t.Fatal(e)
		}
		m := equipmentRegister(id)
		m.Action = "update"
		m.ComponentID = view.Slots[0].ComponentID
		m.InstallationID = view.Slots[0].ID
		m.ExpectedPassportRevision = view.Revision
		m.PassportAttributesPatch = domain.PassportAttributesPatch{}
		rows = append(rows, m)
	}
	noOp := equipmentCommit(t, r, ctx, equipmentPreview(t, r, ctx, equipmentRows(rows...)))
	if noOp.ChangedCount != 0 || noOp.UnchangedCount != 2 || equipmentCount(t, p, "equipment_passport_event") != 2 {
		t.Fatal(noOp)
	}
	view, e := r.ReadPassport(ctx, equipmentPolicy, 1)
	if e != nil || view.Revision != 1 || view.Provenance[0].LastChange.ReceiptID != initial.ID {
		t.Fatal("noop altered provenance/revision", view, e)
	}
	if _, e = p.Exec(ctx, `UPDATE well SET fund_type='presentation' WHERE id=2`); e != nil {
		t.Fatal(e)
	}
	_, e = r.ReadPassportReceipt(ctx, equipmentPolicy, noOp.ID)
	equipmentKind(t, e, domain.Missing)
	_, e = r.PreparePassport(ctx, equipmentPolicy, equipmentRows(rows...), domain.PassportProvenance{Source: "manual"}, "2026-10-24")
	equipmentKind(t, e, domain.Missing)
	other := equipmentActor(t, p, r, "other-owner")
	_, e = r.ReadPassportReceipt(other, equipmentPolicy, initial.ID)
	equipmentKind(t, e, domain.Missing)
}
func TestEquipmentPassportCohortFence(t *testing.T) {
	p, r, ctx := equipmentFixture(t)
	if _, e := p.Exec(ctx, `INSERT INTO app_cohort(version,manifest_sha256)VALUES('equipment-cohort',repeat('b',64)); INSERT INTO app_cohort_member(version,well_id,revision)VALUES('equipment-cohort',1,1)`); e != nil {
		t.Fatal(e)
	}
	policy := domain.PassportPolicy{AppScope: "cohort", CohortVersion: "equipment-cohort"}
	_, e := r.PreparePassport(ctx, policy, equipmentRows(equipmentRegister(1), equipmentRegister(2)), domain.PassportProvenance{Source: "manual"}, "2026-10-24")
	equipmentKind(t, e, domain.Missing)
	if equipmentCount(t, p, "equipment_passport_preview") != 0 {
		t.Fatal("mixed cohort created preview")
	}
	if _, e = r.ReadPassport(ctx, policy, 2); e == nil {
		t.Fatal("cohort read escaped")
	}
}
func TestEquipmentPassportHistoryQueryAndExportBoundary(t *testing.T) {
	p, r, ctx := equipmentFixture(t)
	first := equipmentCommit(t, r, ctx, equipmentPreview(t, r, ctx, equipmentRows(equipmentRegister(1))))
	view, e := r.ReadPassport(ctx, equipmentPolicy, 1)
	if e != nil {
		t.Fatal(e)
	}
	m := equipmentRegister(1)
	m.Action = "update"
	m.ComponentID = view.Slots[0].ComponentID
	m.InstallationID = view.Slots[0].ID
	m.ExpectedPassportRevision = view.Revision
	m.Model = domain.Field[string]{Present: true, Value: "Новое имя"}
	m.ChangeReason = "Уточнение"
	second := equipmentCommit(t, r, ctx, equipmentPreview(t, r, ctx, equipmentRows(m)))
	req := domain.TableRequest{Query: domain.TableQuery{Version: 1}, PageSize: 1}
	page, e := r.PassportHistoryTable(ctx, equipmentPolicy, 1, req)
	if e != nil || len(page.Items) != 1 || !page.HasMore || page.Items[0].ReceiptID != second.ID {
		t.Fatal(page, e)
	}
	req.After = page.Keys
	page, e = r.PassportHistoryTable(ctx, equipmentPolicy, 1, req)
	if e != nil || len(page.Items) != 1 || page.HasMore || page.Items[0].ReceiptID != first.ID {
		t.Fatal(page, e)
	}
	req = domain.TableRequest{Query: domain.TableQuery{Version: 1, Filters: []domain.TableFilter{{Field: "action", Op: "in", Values: []json.RawMessage{json.RawMessage(`"register"`)}}}}, PageSize: 1}
	page, e = r.PassportHistoryTable(ctx, equipmentPolicy, 1, req)
	if e != nil || len(page.Items) != 1 || page.HasMore || page.Items[0].Action != "register" {
		t.Fatal("filtered after LIMIT", page, e)
	}
	// Seed immutable historical assertions, then verify export rejects1001 without truncation.
	if _, e = p.Exec(ctx, `INSERT INTO equipment_passport_event(passport_id,well_id,revision,receipt_id,row_key,action,slot,recorded_at,actor_id,actor_name,source,reason,diff,payload) SELECT e.passport_id,e.well_id,e.revision,e.receipt_id,'boundary-'||g,e.action,e.slot,e.recorded_at,e.actor_id,e.actor_name,e.source,e.reason,e.diff,e.payload FROM equipment_passport_event e CROSS JOIN generate_series(1,999)g WHERE e.id=(SELECT min(id) FROM equipment_passport_event)`); e != nil {
		t.Fatal(e)
	}
	_, e = r.LoadPassportExport(ctx, equipmentPolicy, domain.PassportExportRequest{WellIDs: []domain.PassportID{1}, Mode: "history", Query: domain.TableQuery{Version: 1}}, domain.PassportPurposeExportHistory)
	var pe *domain.PassportError
	if !errors.As(e, &pe) || pe.Code != "file_limit_exceeded" {
		t.Fatal("history silently truncated", e)
	}
}

func TestEquipmentPassportNormalizedDecimalChecks(t *testing.T) {
	p, r, ctx := equipmentFixture(t)
	equipmentCommit(t, r, ctx, equipmentPreview(t, r, ctx, equipmentRows(equipmentRegister(1))))
	for _, raw := range []string{"1000000000", "999999999.123456", "0.000001"} {
		if _, e := p.Exec(ctx, `UPDATE equipment_passport_component SET pump_nominal_head_m=$1::numeric`, raw); e != nil {
			t.Fatal("valid exact decimal rejected", raw, e)
		}
	}
	for _, raw := range []string{"1000000000.000001", "0.0000001", "NaN", "Infinity", "0", "-1"} {
		if _, e := p.Exec(ctx, `UPDATE equipment_passport_component SET pump_nominal_head_m=$1::numeric`, raw); e == nil {
			t.Fatal("invalid decimal accepted", raw)
		}
	}
	if _, e := p.Exec(ctx, `UPDATE equipment_passport_component SET motor_rated_power_kw=1`); e == nil {
		t.Fatal("foreign-slot numeric accepted")
	}
	if _, e := p.Exec(ctx, `UPDATE equipment_passport_component SET pump_stage_count=0`); e == nil {
		t.Fatal("zero stages accepted")
	}
}

func TestEquipmentPassportExportProvenanceRowKeys(t *testing.T) {
	pool, repo, ctx := equipmentFixture(t)
	const large domain.PassportID = 9007199254740993
	if _, err := pool.Exec(ctx, `INSERT INTO well(id,ngdu,department) OVERRIDING SYSTEM VALUE VALUES($1,0,0)`, int64(large)); err != nil {
		t.Fatal(err)
	}
	rows := equipmentRows(equipmentRegister(1), equipmentRegister(large))
	reference := "Паспорт завода №42"
	for i := range rows.Rows {
		rows.Rows[i].SourceReference = &reference
	}
	receipt := equipmentCommit(t, repo, ctx, equipmentPreview(t, repo, ctx, rows))
	snapshot, err := repo.LoadPassportExport(ctx, equipmentPolicy, domain.PassportExportRequest{WellIDs: []domain.PassportID{1, large}, Mode: "current_components"}, domain.PassportPurposeExportCurrent)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = (spreadsheet.PassportCodec{}).Encode(ctx, &out, snapshot); err != nil {
		t.Fatal(err)
	}
	book, err := excelize.OpenReader(bytes.NewReader(out.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	defer book.Close()
	components, err := book.GetRows("components")
	if err != nil {
		t.Fatal(err)
	}
	meta, err := book.GetRows("meta")
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{}
	for _, row := range meta {
		if len(row) == 2 {
			metadata[row[0]] = row[1]
		}
	}
	if len(components) != 3 {
		t.Fatal("missing actual component rows", components)
	}
	for _, row := range components[1:] {
		key, well := row[0], row[1]
		if key != well+":pump" || well != "1" && well != "9007199254740993" {
			t.Fatal("row identity rounded or changed", row)
		}
		raw, ok := metadata["provenance."+key]
		if !ok {
			t.Fatalf("no provenance for actual components row %q; meta=%v", key, metadata)
		}
		var source struct {
			Source          domain.PassportSource `json:"source"`
			ActorID         domain.PassportID     `json:"actor_id"`
			ReceiptID       domain.PassportID     `json:"receipt_id"`
			SourceReference *string               `json:"source_reference"`
			RecordedAt      time.Time             `json:"recorded_at"`
		}
		if err = json.Unmarshal([]byte(raw), &source); err != nil {
			t.Fatal(err)
		}
		if source.Source != "manual" || source.ActorID != receipt.Actor.AccountID || source.ReceiptID != receipt.ID || source.SourceReference == nil || *source.SourceReference != reference || !source.RecordedAt.Equal(receipt.RecordedAt) {
			t.Fatal("row metadata does not identify committed source", source, receipt)
		}
	}
}

func TestEquipmentPassportConcurrentPreviewConsumption(t *testing.T) {
	for _, cancelRace := range []bool{false, true} {
		name := "different-commit-keys"
		if cancelRace {
			name = "cancel-versus-commit"
		}
		t.Run(name, func(t *testing.T) {
			pool, repo, ctx := equipmentFixture(t)
			equipmentCommit(t, repo, ctx, equipmentPreview(t, repo, ctx, equipmentRows(equipmentRegister(1))))
			view, err := repo.ReadPassport(ctx, equipmentPolicy, 1)
			if err != nil {
				t.Fatal(err)
			}
			row := equipmentRegister(1)
			row.Action = "update"
			row.ComponentID = view.Slots[0].ComponentID
			row.InstallationID = view.Slots[0].ID
			row.ExpectedPassportRevision = view.Revision
			row.PassportAttributesPatch = domain.PassportAttributesPatch{}
			preview := equipmentPreview(t, repo, ctx, equipmentRows(row))
			if preview.UnchangedCount != 1 {
				t.Fatal(preview)
			}
			blocker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback(ctx)
			var blockerPID int
			if err = blocker.QueryRow(ctx, "SELECT pg_backend_pid() FROM well WHERE id=1 FOR UPDATE").Scan(&blockerPID); err != nil {
				t.Fatal(err)
			}
			// Follow the actual lock wait graph: old code has two well waiters; fixed
			// code has one well waiter and one private preview row waiter behind it.
			waitFor := func(n int, early <-chan error) (error, bool) {
				t.Helper()
				wait, stop := context.WithTimeout(ctx, 800*time.Millisecond)
				defer stop()
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for {
					var count int
					if _, err := blocker.Exec(wait, "SELECT pg_stat_clear_snapshot()"); err != nil {
						t.Fatal(err)
					}
					if err := blocker.QueryRow(wait, `WITH RECURSIVE blocked(pid) AS (
      SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))
      UNION SELECT a.pid FROM pg_stat_activity a JOIN blocked b ON b.pid=ANY(pg_blocking_pids(a.pid))
     ) SELECT count(*) FROM blocked`, blockerPID).Scan(&count); err != nil {
						t.Fatal(err)
					}
					if count >= n {
						return nil, false
					}
					select {
					case err := <-early:
						return err, true
					case <-wait.Done():
						t.Fatalf("did not reach %d actual lock waiters", n)
					case <-ticker.C:
					}
				}
			}
			type outcome struct {
				key     string
				receipt domain.PassportReceipt
				err     error
			}
			done := make(chan outcome, 2)
			commit := func(key string) {
				receipt, err := repo.CommitPassport(ctx, equipmentPolicy, domain.PassportCommitInput{PreviewID: preview.ID, Digest: preview.Digest, AcknowledgeWarnings: true, IdempotencyKey: key}, "2026-10-25")
				done <- outcome{key, receipt, err}
			}
			go commit(uuid.NewString())
			if cancelRace {
				waitFor(1, nil)
				cancelled := make(chan error, 1)
				go func() { cancelled <- repo.CancelPassportPreview(ctx, equipmentPolicy, preview.ID) }()
				cancelErr, early := waitFor(2, cancelled)
				if err = blocker.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				committed := <-done
				if !early {
					cancelErr = <-cancelled
				}
				var pe *domain.PassportError
				if committed.err != nil || !errors.As(cancelErr, &pe) || pe.Code != "preview_consumed" {
					t.Fatalf("cancel and commit not serialized: commit=%v cancel=%v", committed.err, cancelErr)
				}
			} else {
				go commit(uuid.NewString())
				waitFor(2, nil)
				if err = blocker.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				successes := 0
				for i := 0; i < 2; i++ {
					result := <-done
					if result.err == nil {
						successes++
						replay, err := repo.CommitPassport(ctx, equipmentPolicy, domain.PassportCommitInput{PreviewID: preview.ID, Digest: preview.Digest, AcknowledgeWarnings: true, IdempotencyKey: result.key}, "2026-10-25")
						if err != nil || replay.ID != result.receipt.ID {
							t.Fatal("same-key receipt-first replay changed", replay, err)
						}
					} else {
						var pe *domain.PassportError
						if !errors.As(result.err, &pe) || pe.Code != "preview_consumed" {
							t.Fatal("unexpected loser result", result.err)
						}
					}
				}
				if successes != 1 {
					t.Fatalf("private no-op preview consumed %d times", successes)
				}
			}
			var receipts int
			if err = pool.QueryRow(ctx, "SELECT count(*) FROM equipment_passport_receipt WHERE preview_id=$1", int64(preview.ID)).Scan(&receipts); err != nil {
				t.Fatal(err)
			}
			if receipts != 1 {
				t.Fatalf("one preview produced %d durable receipts", receipts)
			}
		})
	}
}
