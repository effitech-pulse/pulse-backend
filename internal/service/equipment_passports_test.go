package service

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type passportTestCodec struct {
	sourceRows []domain.PassportRowSource
	rows       domain.PassportChangeSet
	enter      chan context.Context
}

func (c passportTestCodec) Decode(ctx context.Context, _ io.Reader) (domain.PassportWorkbook, error) {
	if c.enter != nil {
		c.enter <- ctx
		<-ctx.Done()
		return domain.PassportWorkbook{}, ctx.Err()
	}
	return domain.PassportWorkbook{ChangeSet: c.rows, FileSHA256: strings.Repeat("a", 64), Rows: c.sourceRows}, nil
}
func (c passportTestCodec) Encode(context.Context, io.Writer, domain.PassportExport) error {
	return nil
}

type passportTestStore struct {
	EquipmentPassportStore
	sources    []domain.PassportSource
	provenance []domain.PassportProvenance
}

func (s *passportTestStore) PreparePassport(_ context.Context, _ domain.PassportPolicy, in domain.PassportChangeSet, source domain.PassportProvenance, today domain.PassportDate) (domain.PassportPreview, error) {
	s.sources = append(s.sources, source.Source)
	s.provenance = append(s.provenance, source)
	p, e := domain.PreparePassportChanges(in, []domain.PassportBasis{{Well: domain.Well{ID: 1, Revision: 1}}}, today)
	return domain.PassportPreview{Commands: p.ChangeSet, Diff: p.Diff, Issues: p.Issues, ChangedCount: p.ChangedCount, UnchangedCount: p.UnchangedCount}, e
}
func passportServiceActor(id int64) context.Context {
	a := domain.AuthorizationSnapshot{AccountID: id, Active: true, Epoch: 1, Permissions: []domain.Permission{"wells.read", "equipment_passports.read", "equipment_passports.update", "equipment_passports.import", "equipment_passports.export", "equipment_passports.history.read"}}
	return domain.WithRBACMode(domain.WithPrincipal(context.Background(), domain.Principal{Authenticated: true, Mechanism: "session", AccountID: &id, Authorization: &a}), domain.RBACEnforce)
}
func TestEquipmentPassportServiceParity(t *testing.T) {
	m := domain.PassportMutation{RowKey: "r", WellID: 1, ExpectedWellRevision: 1, Slot: "pump", Action: "register"}
	m.Model = domain.Field[string]{Present: true, Value: "Насос"}
	rows := domain.PassportChangeSet{SchemaVersion: domain.PassportSchemaVersion, Rows: []domain.PassportMutation{m}}
	store := &passportTestStore{}
	s, e := NewEquipmentPassports(store, passportTestCodec{rows: rows, sourceRows: []domain.PassportRowSource{{RowKey: "r", Sheet: "components", Index: 7}}}, PassportOptions{AppScope: "research", Now: func() time.Time { return time.Date(2026, 10, 24, 1, 0, 0, 0, time.UTC) }})
	if e != nil {
		t.Fatal(e)
	}
	manual, e := s.Prepare(passportServiceActor(1), rows, domain.PassportProvenance{Source: "manual"})
	if e != nil {
		t.Fatal(e)
	}
	excel, e := s.Import(passportServiceActor(1), strings.NewReader("fixture"))
	if e != nil {
		t.Fatal(e)
	}
	if len(store.provenance[1].Rows) != 1 || store.provenance[1].Rows[0].Index != 7 {
		t.Fatal("physical row was inferred from command order")
	}
	if !reflect.DeepEqual(manual.Diff, excel.Diff) || manual.ChangedCount != 1 || excel.ChangedCount != 1 || !reflect.DeepEqual(store.sources, []domain.PassportSource{"manual", "excel"}) {
		t.Fatal(manual, excel, store.sources)
	}
}
func TestEquipmentPassportLimits(t *testing.T) {
	entered := make(chan context.Context, 2)
	s, e := NewEquipmentPassports(&passportTestStore{}, passportTestCodec{enter: entered}, PassportOptions{AppScope: "research"})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(passportServiceActor(7001))
	done := make(chan error, 1)
	go func() { _, e := s.Import(ctx, strings.NewReader("x")); done <- e }()
	active := <-entered
	deadline, ok := active.Deadline()
	if !ok || time.Until(deadline) > 10*time.Second || time.Until(deadline) < 9*time.Second {
		t.Fatal("missing 10s budget")
	}
	if _, e = s.Import(passportServiceActor(7001), strings.NewReader("x")); e == nil {
		t.Fatal("same actor admitted twice")
	}
	ctx2, cancel2 := context.WithCancel(passportServiceActor(7002))
	done2 := make(chan error, 1)
	go func() { _, e := s.Import(ctx2, strings.NewReader("x")); done2 <- e }()
	<-entered
	if _, e = s.Import(passportServiceActor(7003), strings.NewReader("x")); e == nil {
		t.Fatal("third process parse admitted")
	}
	cancel()
	if e = <-done; e == nil {
		t.Fatal("cancel succeeded")
	}
	cancel2()
	if e = <-done2; e == nil {
		t.Fatal("cancel succeeded")
	}
	ctx3, cancel3 := context.WithCancel(passportServiceActor(7001))
	done3 := make(chan error, 1)
	go func() { _, e := s.Import(ctx3, strings.NewReader("x")); done3 <- e }()
	<-entered
	cancel3()
	<-done3
}

func TestEquipmentPassportHistoryClosedEnums(t *testing.T) {
	for _, field := range []string{"action", "slot", "source"} {
		raw := `{"version":1,"filters":[{"field":"` + field + `","op":"in","values":["unknown-value"]}],"sort":[]}`
		if _, e := parseTableRequest(passportServiceActor(1), "equipmenthistory", 1, raw, 25, ""); e == nil {
			t.Fatalf("accepted unknown %s enum", field)
		}
	}
}

func TestEquipmentPassportHistoryEnumsRequireExactValue(t *testing.T) {
	for _, tt := range []struct{ field, valid, invalid string }{{"action", "register", "register update"}, {"slot", "pump", "pump motor"}, {"source", "manual", "manual excel"}} {
		raw := `{"version":1,"filters":[{"field":"` + tt.field + `","op":"in","values":["` + tt.invalid + `"]}],"sort":[]}`
		if _, e := parseTableRequest(passportServiceActor(1), "equipmenthistory", 1, raw, 25, ""); e == nil {
			t.Fatalf("accepted compound %s enum %q", tt.field, tt.invalid)
		}
		raw = strings.Replace(raw, tt.invalid, tt.valid, 1)
		if _, e := parseTableRequest(passportServiceActor(1), "equipmenthistory", 1, raw, 25, ""); e != nil {
			t.Fatalf("exact %s enum rejected: %v", tt.field, e)
		}
	}
}
