package httpapi

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// Domain scalars already implement the exact closed string-ID/decimal wire format.
type passportChangeSetDTO struct {
	SchemaVersion string                    `json:"schema_version"`
	Rows          []domain.PassportMutation `json:"rows"`
}
type passportCommitDTO struct {
	Digest              string `json:"digest"`
	AcknowledgeWarnings bool   `json:"acknowledge_warnings"`
}
type passportTemplateDTO struct {
	WellIDs []domain.PassportID `json:"well_ids"`
}

func passportDecode(raw []byte, out any) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return passportHTTPError(domain.Invalid, "invalid_json")
	}
	keys := json.NewDecoder(bytes.NewReader(raw))
	if e := passportJSONKeys(keys, 0); e != nil {
		return passportHTTPError(domain.Invalid, "invalid_json")
	}
	if _, e := keys.Token(); e != io.EOF {
		return passportHTTPError(domain.Invalid, "invalid_json")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if e := dec.Decode(out); e != nil {
		return passportHTTPError(domain.Invalid, "invalid_json")
	}
	return nil
}
func passportJSONKeys(dec *json.Decoder, depth int) error {
	if depth > 16 {
		return passportHTTPError(domain.Invalid, "invalid_json")
	}
	token, e := dec.Token()
	if e != nil {
		return e
	}
	d, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, e := dec.Token()
			if e != nil {
				return e
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return passportHTTPError(domain.Invalid, "invalid_json")
			}
			seen[s] = true
			if e = passportJSONKeys(dec, depth+1); e != nil {
				return e
			}
		}
	case '[':
		for dec.More() {
			if e = passportJSONKeys(dec, depth+1); e != nil {
				return e
			}
		}
	default:
		return passportHTTPError(domain.Invalid, "invalid_json")
	}
	_, e = dec.Token()
	return e
}
