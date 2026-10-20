package parameterfile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestTelemetrySchemaSnapshot(t *testing.T) {
	raw := []byte(`{"tags":[{"alias":"4x0101 R_izol","displayname":"Изоляция","server_parameter":{"is_set":false,"coef_k":1,"coef_b":0}},{"alias":"3x0629 set_Annular_pressure","server_parameter":{"is_set":true,"coef_k":0.001,"coef_b":0}}]}`)
	path := filepath.Join(t.TempDir(), "large.json")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	schema, e := (TelemetrySchemaReader{Path: path}).Load(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if schema.SHA256 != domain.FileSHA256(raw) || schema.Channels[0].Canonical != "R_izol" || schema.Channels[0].Missing != "9999" || !schema.Channels[1].IsSet {
		t.Fatal("authoritative semantics lost")
	}
	for _, bad := range []string{`{"R_izol":["Изоляция"]}`, `{"tags":[{"alias":"R_izol","server_parameter":{"coef_k":1,"coef_b":0}}]}`} {
		if _, e = ParseTelemetrySchema([]byte(bad)); e == nil {
			t.Fatal("non-authoritative schema accepted")
		}
	}
}
