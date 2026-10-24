package spreadsheet

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

const (
	passportMaxBytes     = 2 * 1024 * 1024
	passportMaxExpanded  = 16 * 1024 * 1024
	passportMaxCells     = 10000
	passportMaxJSONChars = 16384
)

type passportCell struct {
	value string
	text  bool
}
type passportSheet map[int]map[int]passportCell

type passportContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r passportContextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	n, e := r.reader.Read(p)
	if ce := r.ctx.Err(); ce != nil {
		return n, ce
	}
	return n, e
}

func passportXML(ctx context.Context, raw []byte, visit func(xml.Token) error) error {
	d := xml.NewDecoder(passportContextReader{ctx, bytes.NewReader(raw)})
	for {
		tok, e := d.Token()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return passportFailure("workbook_structure", e)
		}
		if _, ok := tok.(xml.Directive); ok {
			return passportFailure("workbook_structure", nil)
		}
		if e = visit(tok); e != nil {
			return e
		}
	}
}
func passportAttr(s xml.StartElement, key string) string {
	for _, a := range s.Attr {
		if a.Name.Local == key {
			return a.Value
		}
	}
	return ""
}

// passportPreflight bounds decompression and inspects raw cell values before Excelize.
func passportPreflight(ctx context.Context, raw []byte) (map[string]passportSheet, error) {
	z, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil {
		return nil, passportFailure("workbook_structure", e)
	}
	if len(z.File) > 64 {
		return nil, passportFailure("file_limit_exceeded", nil)
	}
	parts := map[string][]byte{}
	total := 0
	for _, f := range z.File {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		n := f.Name
		lower := strings.ToLower(n)
		if n == "" || strings.Contains(n, "\\") || path.Clean(n) != n || strings.HasPrefix(n, "/") || strings.Contains(n, ":") || n == ".." || strings.HasPrefix(n, "../") || f.Flags&1 != 0 || f.Mode()&0111 != 0 {
			return nil, passportFailure("workbook_structure", nil)
		}
		if _, ok := parts[n]; ok {
			return nil, passportFailure("workbook_structure", nil)
		}
		for _, bad := range []string{"vba", "macro", "oleobject", "embeddings/", "externallink", "encrypted", "encryptioninfo", "activex"} {
			if strings.Contains(lower, bad) {
				return nil, passportFailure("workbook_structure", nil)
			}
		}
		if f.UncompressedSize64 > uint64(passportMaxExpanded-total) {
			return nil, passportFailure("file_limit_exceeded", nil)
		}
		r, err := f.Open()
		if err != nil {
			return nil, passportFailure("workbook_structure", err)
		}
		b, err := io.ReadAll(io.LimitReader(passportContextReader{ctx, r}, int64(passportMaxExpanded-total+1)))
		_ = r.Close()
		if err != nil {
			return nil, passportFailure("workbook_structure", err)
		}
		total += len(b)
		if total > passportMaxExpanded {
			return nil, passportFailure("file_limit_exceeded", nil)
		}
		parts[n] = b
	}
	for n, b := range parts {
		if !strings.HasSuffix(n, ".xml") && !strings.HasSuffix(n, ".rels") {
			continue
		}
		e = passportXML(ctx, b, func(tok xml.Token) error {
			if s, ok := tok.(xml.StartElement); ok {
				switch s.Name.Local {
				case "f":
					return passportFailure("formula_forbidden", nil)
				case "mergeCell", "mergeCells", "oleObject", "externalLink", "externalReference", "definedName":
					return passportFailure("workbook_structure", nil)
				}
				if passportAttr(s, "TargetMode") == "External" || passportAttr(s, "hidden") == "1" || passportAttr(s, "hidden") == "true" || s.Name.Local == "sheet" && passportAttr(s, "state") != "" && passportAttr(s, "state") != "visible" {
					return passportFailure("workbook_structure", nil)
				}
				if s.Name.Local == "Relationship" {
					target := passportAttr(s, "Target")
					if strings.Contains(target, ":") || strings.HasPrefix(target, "//") {
						return passportFailure("workbook_structure", nil)
					}
				}
				if s.Name.Local == "Override" {
					ct := strings.ToLower(passportAttr(s, "ContentType"))
					if strings.Contains(ct, "macro") || strings.Contains(ct, "ole") {
						return passportFailure("workbook_structure", nil)
					}
				}
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
	}
	shared := []string{}
	var current strings.Builder
	inSI, inT := false, false
	if b, ok := parts["xl/sharedStrings.xml"]; ok {
		e = passportXML(ctx, b, func(tok xml.Token) error {
			switch v := tok.(type) {
			case xml.StartElement:
				if v.Name.Local == "si" {
					inSI = true
					current.Reset()
				}
				if v.Name.Local == "t" && inSI {
					inT = true
				}
			case xml.CharData:
				if inT {
					current.Write(v)
					if utf8.RuneCountInString(current.String()) > 7*passportMaxJSONChars {
						return passportFailure("file_limit_exceeded", nil)
					}
				}
			case xml.EndElement:
				if v.Name.Local == "t" {
					inT = false
				}
				if v.Name.Local == "si" {
					decoded, err := passportUnescapeText(current.String())
					if err != nil {
						return err
					}
					if utf8.RuneCountInString(decoded) > passportMaxJSONChars {
						return passportFailure("file_limit_exceeded", nil)
					}
					shared = append(shared, decoded)
					inSI = false
				}
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
	}
	rels := map[string]string{}
	e = passportXML(ctx, parts["xl/_rels/workbook.xml.rels"], func(tok xml.Token) error {
		if s, ok := tok.(xml.StartElement); ok && s.Name.Local == "Relationship" {
			target := passportAttr(s, "Target")
			if strings.HasPrefix(target, "/") {
				target = strings.TrimPrefix(target, "/")
			} else {
				target = path.Join("xl", target)
			}
			rels[passportAttr(s, "Id")] = target
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	names := map[string]string{}
	e = passportXML(ctx, parts["xl/workbook.xml"], func(tok xml.Token) error {
		if s, ok := tok.(xml.StartElement); ok && s.Name.Local == "sheet" {
			n := passportAttr(s, "name")
			if _, exists := names[n]; exists {
				return passportFailure("workbook_structure", nil)
			}
			names[n] = rels[passportAttr(s, "id")]
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	if len(names) != 2 || names["meta"] == "" || names["components"] == "" {
		return nil, passportFailure("workbook_structure", nil)
	}
	for n := range parts {
		if strings.HasPrefix(n, "xl/worksheets/") && strings.HasSuffix(n, ".xml") && n != names["meta"] && n != names["components"] {
			return nil, passportStructure("unexpected worksheet")
		}
	}
	sheets := map[string]passportSheet{}
	cells := 0
	for name, part := range names {
		sheet := passportSheet{}
		seenRows := map[int]bool{}
		var cell passportCell
		var value strings.Builder
		var ref, typ string
		inCell, inValue := false, false
		row := 0
		e = passportXML(ctx, parts[part], func(tok xml.Token) error {
			switch v := tok.(type) {
			case xml.StartElement:
				switch v.Name.Local {
				case "row":
					var err error
					row, err = strconv.Atoi(passportAttr(v, "r"))
					if err != nil || row < 1 || row > 10001 {
						return passportFailure("file_limit_exceeded", err)
					}
					if seenRows[row] {
						return passportStructure("duplicate worksheet row")
					}
					seenRows[row] = true
				case "c":
					cells++
					if cells > passportMaxCells {
						return passportFailure("file_limit_exceeded", nil)
					}
					inCell = true
					ref = passportAttr(v, "r")
					typ = passportAttr(v, "t")
					cell = passportCell{text: typ == "s" || typ == "inlineStr"}
					value.Reset()
					if typ != "" && typ != "n" && typ != "s" && typ != "inlineStr" {
						return passportFailure("workbook_structure", nil)
					}
				case "v", "t":
					if inCell {
						inValue = true
					}
				}
			case xml.CharData:
				if inValue {
					value.Write(v)
					if utf8.RuneCountInString(value.String()) > 7*passportMaxJSONChars {
						return passportFailure("file_limit_exceeded", nil)
					}
				}
			case xml.EndElement:
				switch v.Name.Local {
				case "v", "t":
					inValue = false
				case "c":
					inCell = false
					col, r, err := excelize.CellNameToCoordinates(ref)
					if err != nil || r != row || col > 10000 {
						return passportFailure("workbook_structure", err)
					}
					cell.value = value.String()
					if typ == "s" {
						i, err := strconv.Atoi(cell.value)
						if err != nil || i < 0 || i >= len(shared) {
							return passportFailure("workbook_structure", err)
						}
						cell.value = shared[i]
					} else if typ == "inlineStr" {
						decoded, err := passportUnescapeText(cell.value)
						if err != nil {
							return err
						}
						cell.value = decoded
					}
					if sheet[r] == nil {
						sheet[r] = map[int]passportCell{}
					}
					if _, exists := sheet[r][col]; exists {
						return passportFailure("workbook_structure", nil)
					}
					sheet[r][col] = cell
				}
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
		for row, cells := range sheet {
			for col, cell := range cells {
				limit := 512
				if name == "meta" && row > 1 && col == 2 && strings.HasPrefix(cells[1].value, "provenance.") {
					limit = passportMaxJSONChars
				}
				if utf8.RuneCountInString(cell.value) > limit {
					return nil, passportFailure("file_limit_exceeded", nil)
				}
			}
		}
		sheets[name] = sheet
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	return sheets, nil
}

func passportStructure(detail string) error {
	return passportFailure("workbook_structure", fmt.Errorf("%s", detail))
}

// passportEscapeUnit recognizes one ST_Xstring UTF-16 escape. XML entity
// unescaping has already happened; ST_Xstring decoding is a separate pass.
func passportEscapeUnit(s string) (uint16, bool) {
	if len(s) < 7 || s[0] != '_' || s[1] != 'x' || s[6] != '_' {
		return 0, false
	}
	for _, c := range s[2:6] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return 0, false
		}
	}
	n, e := strconv.ParseUint(s[2:6], 16, 16)
	return uint16(n), e == nil
}

func passportUnescapeText(s string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(s); {
		unit, escaped := passportEscapeUnit(s[i:])
		if !escaped {
			b.WriteByte(s[i])
			i++
			continue
		}
		i += 7
		if unit >= 0xD800 && unit <= 0xDBFF {
			low, ok := passportEscapeUnit(s[i:])
			if !ok || low < 0xDC00 || low > 0xDFFF {
				return "", passportStructure("invalid ST_Xstring surrogate")
			}
			b.WriteRune(utf16.DecodeRune(rune(unit), rune(low)))
			i += 7
		} else if unit >= 0xDC00 && unit <= 0xDFFF {
			return "", passportStructure("invalid ST_Xstring surrogate")
		} else {
			b.WriteRune(rune(unit))
		}
	}
	return b.String(), nil
}

// passportEscapeText protects literal escape sequences (including overlapping
// underscore chains) and XML-forbidden controls before Excelize's shared-string
// writer. Its v2.10.1 SetCellStr stores xlsxT.Val before bstrMarshal is applied.
func passportEscapeText(s string) string {
	var b strings.Builder
	for i, r := range s {
		_, literal := passportEscapeUnit(s[i:])
		if r == '_' && literal {
			b.WriteString("_x005F_")
		} else if r < 0x20 || r == 0xFFFE || r == 0xFFFF {
			fmt.Fprintf(&b, "_x%04X_", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
