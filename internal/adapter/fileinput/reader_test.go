package fileinput

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestCSVBoundsAndExactIDs(t *testing.T) {
	limits := domain.DefaultFileInputLimits()
	limits.Rows = 1
	spec := domain.FileParseSpec{Format: "csv", Delimiter: ",", HeaderRow: 1}
	raw := "timestamp,id,F_out\n2026-01-01T00:00:00Z,9007199254740993,0\n"
	got, err := (Reader{}).Read(context.Background(), strings.NewReader(raw), spec, limits)
	if err != nil || got.Rows[0][1] != "9007199254740993" {
		t.Fatalf("exact ID: %#v %v", got, err)
	}
	if _, err = (Reader{}).Read(context.Background(), strings.NewReader(raw+"2026-01-02T00:00:00Z,1,0\n"), spec, limits); err == nil {
		t.Fatal("row cap bypass")
	}
	for _, bad := range []string{"x,x\n1,2\n", "x,y\n1\n"} {
		if _, err = (Reader{}).Read(context.Background(), strings.NewReader(bad), spec, limits); err == nil {
			t.Fatal("bad CSV accepted")
		}
	}
}

func TestCSVExactCaps(t *testing.T) {
	raw := "abc,d\n123,4\n"
	base := domain.DefaultFileInputLimits()
	base.RawBytes = int64(len(raw))
	base.Columns = 2
	base.FieldBytes = 3
	base.Rows = 1
	spec := domain.FileParseSpec{Format: "csv", Delimiter: ",", HeaderRow: 1}
	if _, e := (Reader{}).Read(context.Background(), strings.NewReader(raw), spec, base); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"raw", "columns", "field", "header"} {
		t.Run(name, func(t *testing.T) {
			l := base
			s := spec
			switch name {
			case "raw":
				l.RawBytes--
			case "columns":
				l.Columns--
			case "field":
				l.FieldBytes--
			case "header":
				s.HeaderRow = 101
			}
			if _, e := (Reader{}).Read(context.Background(), strings.NewReader(raw), s, l); e == nil {
				t.Fatal("cap bypass")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := (Reader{}).Read(ctx, strings.NewReader(raw), spec, base); e == nil {
		t.Fatal("cancel ignored")
	}
}

func xlsxFixture(t *testing.T, formula bool) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	_ = f.SetSheetRow("Sheet1", "A1", &[]any{"timestamp", "id", "F_out"})
	_ = f.SetSheetRow("Sheet1", "A2", &[]any{"2026-01-01T00:00:00Z", "9007199254740993", 0})
	if formula {
		_ = f.SetCellFormula("Sheet1", "C2", "1+1")
	}
	b, e := f.WriteToBuffer()
	if e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestXLSXBoundsFormulaAndDateSystem(t *testing.T) {
	raw := xlsxFixture(t, false)
	l := domain.DefaultFileInputLimits()
	well := "id"
	spec := domain.FileParseSpec{Format: "xlsx", Sheet: "Sheet1", HeaderRow: 1, TimeColumn: "timestamp", WellColumn: &well, Mappings: []domain.Mapping{{Field: "F_out", Parameter: "F_out"}}}
	out, e := (Reader{}).Read(context.Background(), bytes.NewReader(raw), spec, l)
	if e != nil || out.Rows[0][1] != "9007199254740993" || out.Rows[0][2] != "0" || out.Date1904 {
		t.Fatalf("xlsx exact: %#v %v", out, e)
	}
	if _, e = (Reader{}).Read(context.Background(), bytes.NewReader(xlsxFixture(t, true)), spec, l); e == nil {
		t.Fatal("formula accepted")
	}
	z, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		t.Fatal(e)
	}
	var total, maxEntry int64
	for _, entry := range z.File {
		r, _ := entry.Open()
		n, _ := io.Copy(io.Discard, r)
		_ = r.Close()
		total += n
		if n > maxEntry {
			maxEntry = n
		}
	}
	l.ExpandedBytes = total
	l.EntryBytes = maxEntry
	l.Entries = len(z.File)
	if _, e = (Reader{}).Read(context.Background(), bytes.NewReader(raw), spec, l); e != nil {
		t.Fatalf("exact expansion: %v", e)
	}
	for _, name := range []string{"expanded", "entry", "entries", "ratio"} {
		t.Run(name, func(t *testing.T) {
			small := l
			switch name {
			case "expanded":
				small.ExpandedBytes--
			case "entry":
				small.EntryBytes--
			case "entries":
				small.Entries--
			case "ratio":
				small.ExpansionRatio = 1
			}
			if _, e := (Reader{}).Read(context.Background(), bytes.NewReader(raw), spec, small); e == nil {
				t.Fatal("zip cap bypass")
			}
		})
	}
	f := excelize.NewFile()
	date1904 := true
	_ = f.SetWorkbookProps(&excelize.WorkbookPropsOptions{Date1904: &date1904})
	_ = f.SetSheetRow("Sheet1", "A1", &[]any{"timestamp", "F_out"})
	_ = f.SetSheetRow("Sheet1", "A2", &[]any{44560, 0})
	b, _ := f.WriteToBuffer()
	_ = f.Close()
	spec.WellColumn = nil
	out, e = (Reader{}).Read(context.Background(), b, spec, domain.DefaultFileInputLimits())
	if e != nil || !out.Date1904 {
		t.Fatalf("date1904 lost: %v", e)
	}
}

func TestCSVExactHeaderRowCap(t *testing.T) {
	l := domain.DefaultFileInputLimits()
	spec := domain.FileParseSpec{Format: "csv", Delimiter: ",", HeaderRow: 100}
	raw := strings.Repeat("preamble\n", 99) + "value\n0\n"
	out, e := (Reader{}).Read(context.Background(), strings.NewReader(raw), spec, l)
	if e != nil || out.Rows[0][0] != "0" {
		t.Fatal("exact header cap failed", e)
	}
	spec.HeaderRow++
	if _, e = (Reader{}).Read(context.Background(), strings.NewReader(raw), spec, l); e == nil {
		t.Fatal("header +1 accepted")
	}
}
