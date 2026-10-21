package bootstrap

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestFileJobManifestRejectsUnlistedRawAndSpec(t *testing.T) {
	spec := domain.FileParseSpec{Version: "file_input_v1", Format: "csv", Delimiter: ",", HeaderRow: 1, TimeColumn: "time", TimestampLayout: "rfc3339", Timezone: "UTC", Mappings: []domain.Mapping{{Field: "f", Parameter: "F_out", Scale: 1}}}
	raw, e := service.FileJobClientSpecJSON(spec)
	if e != nil {
		t.Fatal(e)
	}
	gate := syntheticFileGate{policy: domain.FileJobPolicy{AllowedInputs: []domain.FileJobAllowedInput{{WellID: 2, RawSHA256: domain.FileSHA256([]byte("fixture")), Format: "csv", ClientSpecJSON: raw, ClientSpecSHA256: domain.FileSHA256(raw)}}}}
	if e = gate.ValidateRaw(context.Background(), 2, domain.FileSHA256([]byte("real")), spec); e == nil {
		t.Fatal("unlisted raw accepted")
	}
	if e = gate.ValidateRaw(context.Background(), 2, domain.FileSHA256([]byte("fixture")), spec); e != nil {
		t.Fatal(e)
	}
	spec.Timezone = "Europe/Moscow"
	if e = gate.ValidateRaw(context.Background(), 2, domain.FileSHA256([]byte("fixture")), spec); e == nil {
		t.Fatal("unlisted timezone accepted")
	}
	var got domain.FileParseSpec
	if e = json.Unmarshal(raw, &got); e != nil || got.SchemaSHA256 != "" {
		t.Fatal(e)
	}
}
