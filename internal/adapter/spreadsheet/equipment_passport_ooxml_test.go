package spreadsheet

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func passportRewriteZIP(t testing.TB, raw []byte, edit func(map[string][]byte)) []byte {
	t.Helper()
	z, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		t.Fatal(e)
	}
	parts := map[string][]byte{}
	for _, f := range z.File {
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		parts[f.Name], e = io.ReadAll(r)
		_ = r.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	edit(parts)
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for n, v := range parts {
		p, e := w.Create(n)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = p.Write(v)
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}

func TestPassportOOXMLSafety(t *testing.T) {
	raw := passportFixture(t, nil)
	for name, edit := range map[string]func(map[string][]byte){
		"cached formula": func(p map[string][]byte) {
			p["xl/worksheets/sheet2.xml"] = bytes.Replace(p["xl/worksheets/sheet2.xml"], []byte("</sheetData>"), []byte(`<row r="3"><c r="A3"><f>1+1</f><v>2</v></c></row></sheetData>`), 1)
		},
		"hidden column": func(p map[string][]byte) {
			p["xl/worksheets/sheet2.xml"] = bytes.Replace(p["xl/worksheets/sheet2.xml"], []byte("<sheetData>"), []byte(`<cols><col min="1" max="1" hidden="1"/></cols><sheetData>`), 1)
		},
		"duplicate row": func(p map[string][]byte) {
			p["xl/worksheets/sheet2.xml"] = bytes.Replace(p["xl/worksheets/sheet2.xml"], []byte("</sheetData>"), []byte(`<row r="2"><c r="AB2" t="inlineStr"><is><t></t></is></c></row></sheetData>`), 1)
		},
		"orphan sheet": func(p map[string][]byte) { p["xl/worksheets/sheet3.xml"] = []byte(`<worksheet/>`) },
		"hidden row": func(p map[string][]byte) {
			p["xl/worksheets/sheet2.xml"] = bytes.Replace(p["xl/worksheets/sheet2.xml"], []byte(`<row r="2"`), []byte(`<row hidden="1" r="2"`), 1)
		},
		"merged": func(p map[string][]byte) {
			p["xl/worksheets/sheet2.xml"] = bytes.Replace(p["xl/worksheets/sheet2.xml"], []byte("</worksheet>"), []byte(`<mergeCells><mergeCell ref="A2:B2"/></mergeCells></worksheet>`), 1)
		},
		"external": func(p map[string][]byte) {
			p["xl/_rels/evil.rels"] = []byte(`<Relationships><Relationship TargetMode="External" Target="https://example.com"/></Relationships>`)
		},
		"macro":     func(p map[string][]byte) { p["xl/vbaProject.bin"] = []byte("macro") },
		"ole":       func(p map[string][]byte) { p["xl/embeddings/oleObject.bin"] = []byte("ole") },
		"encrypted": func(p map[string][]byte) { p["EncryptedPackage"] = []byte("encrypted") },
		"traversal": func(p map[string][]byte) { p["../evil.xml"] = []byte("x") },
		"expanded":  func(p map[string][]byte) { p["oversized.xml"] = bytes.Repeat([]byte(" "), 16*1024*1024) },
		"entries": func(p map[string][]byte) {
			for i := 0; i < 65; i++ {
				p[strings.Repeat("x", i+1)+".xml"] = []byte("x")
			}
		},
		"cell chars": func(p map[string][]byte) {
			p["xl/sharedStrings.xml"] = bytes.Replace(p["xl/sharedStrings.xml"], []byte("pump"), []byte(strings.Repeat("x", 513)), 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(passportRewriteZIP(t, raw, edit)))
			if e == nil {
				t.Fatal("unsafe OOXML accepted")
			}
		})
	}
	t.Run("compressed bytes", func(t *testing.T) {
		_, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(make([]byte, 2*1024*1024+1)))
		if e == nil {
			t.Fatal("oversized bytes accepted")
		}
	})
	t.Run("hidden sheet", func(t *testing.T) {
		raw := passportFixture(t, func(f *excelize.File) { _ = f.SetSheetVisible("components", false) })
		_, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(raw))
		if e == nil {
			t.Fatal("hidden sheet accepted")
		}
	})
}

func TestPassportOOXMLCellBudget(t *testing.T) {
	raw := passportRewriteZIP(t, passportFixture(t, nil), func(p map[string][]byte) {
		var cells strings.Builder
		cells.WriteString(`<row r="3">`)
		for i := 1; i <= 10001; i++ {
			cell, _ := excelize.CoordinatesToCellName(i, 3)
			fmt.Fprintf(&cells, `<c r="%s"/>`, cell)
		}
		cells.WriteString(`</row></sheetData>`)
		p["xl/worksheets/sheet2.xml"] = bytes.Replace(p["xl/worksheets/sheet2.xml"], []byte("</sheetData>"), []byte(cells.String()), 1)
	})
	_, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(raw))
	var feature *domain.PassportError
	if !errors.As(e, &feature) || feature.Code != "file_limit_exceeded" {
		t.Fatalf("cell budget: %v", e)
	}
}

func TestPassportOOXMLPhoneticIdentity(t *testing.T) {
	for _, inline := range []bool{false, true} {
		name := "shared"
		if inline {
			name = "inline"
		}
		t.Run(name, func(t *testing.T) {
			raw := passportRewriteZIP(t, passportFixture(t, nil), func(parts map[string][]byte) {
				if inline {
					part := parts["xl/worksheets/sheet2.xml"]
					start := bytes.Index(part, []byte(`<c r="B2"`))
					if start < 0 {
						t.Fatal("missing fixture ID cell")
					}
					end := bytes.Index(part[start:], []byte(`</c>`))
					if end < 0 {
						t.Fatal("missing fixture ID cell end")
					}
					end += start + len(`</c>`)
					replacement := []byte(`<c r="B2" t="inlineStr"><is><t>9007199254740993</t><rPh sb="0" eb="1"><t>4</t></rPh></is></c>`)
					parts["xl/worksheets/sheet2.xml"] = append(append(append([]byte{}, part[:start]...), replacement...), part[end:]...)
				} else {
					parts["xl/sharedStrings.xml"] = bytes.Replace(parts["xl/sharedStrings.xml"], []byte(`<t>9007199254740993</t>`), []byte(`<t>9007199254740993</t><rPh sb="0" eb="1"><t>4</t></rPh>`), 1)
				}
			})
			f, e := excelize.OpenReader(bytes.NewReader(raw))
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			visible, e := f.GetCellValue("components", "B2")
			if e != nil || visible != "9007199254740993" {
				t.Fatalf("visible ID changed: %q %v", visible, e)
			}
			got, e := (PassportCodec{}).Decode(context.Background(), bytes.NewReader(raw))
			var feature *domain.PassportError
			if !errors.As(e, &feature) || feature.Code != "workbook_structure" || len(got.ChangeSet.Rows) != 0 {
				t.Fatalf("unsupported phonetic annotation must fail closed without commands: %+v %v", got, e)
			}
		})
	}
}
