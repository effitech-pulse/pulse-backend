package parameterfile

import (
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"math"
	"os"
	"regexp"
	"strings"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// TelemetrySchemaReader снимает immutable snapshot model params large.json.
type TelemetrySchemaReader struct{ Path string }

func (r TelemetrySchemaReader) Load(ctx context.Context) (domain.TelemetrySchemaSnapshot, error) {
	f, err := os.Open(r.Path)
	if err != nil {
		return domain.TelemetrySchemaSnapshot{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 4<<20+1))
	if err != nil {
		return domain.TelemetrySchemaSnapshot{}, err
	}
	if err = ctx.Err(); err != nil {
		return domain.TelemetrySchemaSnapshot{}, err
	}
	if len(raw) > 4<<20 {
		return domain.TelemetrySchemaSnapshot{}, domain.NewError(domain.Invalid, "schema_limit")
	}
	return ParseTelemetrySchema(raw)
}

var devicePrefix = regexp.MustCompile(`^[34]x[0-9a-fA-F]{4}\s+`)

// ParseTelemetrySchema разбирает только authoritative tags/server_parameter shape.
func ParseTelemetrySchema(raw []byte) (domain.TelemetrySchemaSnapshot, error) {
	var doc struct {
		Tags []struct {
			Alias   string `json:"alias"`
			Display string `json:"displayname"`
			Unit    string `json:"eu"`
			Server  *struct {
				IsSet  *bool    `json:"is_set"`
				Scale  *float64 `json:"coef_k"`
				Offset *float64 `json:"coef_b"`
				Unit   string   `json:"eu"`
			} `json:"server_parameter"`
		} `json:"tags"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return domain.TelemetrySchemaSnapshot{}, err
	}
	out := domain.TelemetrySchemaSnapshot{Bytes: append([]byte(nil), raw...), SHA256: domain.FileSHA256(raw), DecoderSources: TelemetrySchemaSources()}
	names := map[string]string{}
	for _, tag := range doc.Tags {
		if tag.Alias == "" || tag.Server == nil || tag.Server.IsSet == nil || tag.Server.Scale == nil || tag.Server.Offset == nil || math.IsNaN(*tag.Server.Scale) || math.IsInf(*tag.Server.Scale, 0) || *tag.Server.Scale == 0 || math.IsNaN(*tag.Server.Offset) || math.IsInf(*tag.Server.Offset, 0) {
			return out, domain.NewError(domain.Invalid, "invalid_telemetry_schema")
		}
		name := devicePrefix.ReplaceAllString(tag.Alias, "")
		c := domain.TelemetryChannel{Canonical: name, Aliases: []string{tag.Alias}, Unit: tag.Server.Unit, Scale: *tag.Server.Scale, Offset: *tag.Server.Offset, IsSet: *tag.Server.IsSet || strings.HasPrefix(name, "set_")}
		if c.Unit == "" {
			c.Unit = tag.Unit
		}
		if tag.Display != "" {
			c.Aliases = append(c.Aliases, tag.Display)
		}
		if name == "R_izol" {
			c.Missing = "9999"
		}
		if strings.HasPrefix(name, "energy_Input_energy_reactive_total_") {
			c.Missing = "9999;999.9"
		}
		for _, alias := range append([]string{name}, c.Aliases...) {
			if old, ok := names[alias]; ok && old != name {
				return out, domain.NewError(domain.Invalid, "ambiguous_schema_alias")
			}
			names[alias] = name
		}
		out.Channels = append(out.Channels, c)
	}
	if len(out.Channels) == 0 {
		return out, domain.NewError(domain.Invalid, "empty_telemetry_schema")
	}
	return out, nil
}

//go:embed telemetry_schema.go
var schemaDecoderSource []byte
var schemaDecoderSHA = domain.FileSHA256(schemaDecoderSource)

func TelemetrySchemaSources() []domain.FileParserPart {
	return []domain.FileParserPart{{Name: "adapter/parameterfile/telemetry_schema.go", SHA256: schemaDecoderSHA}}
}
