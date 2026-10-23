package domain

import (
	"bytes"
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

type collectionEnvelope struct {
	Protocol      string             `json:"protocol"`
	FixtureID     string             `json:"fixture_id"`
	WellID        string             `json:"well_id"`
	Start         string             `json:"start"`
	End           string             `json:"end"`
	DataRevision  string             `json:"data_revision"`
	Complete      bool               `json:"complete"`
	Empty         bool               `json:"empty"`
	ExpectedSlots int                `json:"expected_slots"`
	Records       []collectionRecord `json:"records"`
}
type collectionRecord struct {
	WellID    string                     `json:"well_id"`
	Timestamp string                     `json:"timestamp"`
	Values    map[string]json.RawMessage `json:"values"`
}

// collectionJSONKeys отвергает повторные ключи на любой глубине до typed decode.
func collectionJSONKeys(d *json.Decoder, depth int) error {
	if depth > 8 {
		return collectionErr(CollectionCodeInvalidCapture)
	}
	tok, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		if s, ok := tok.(string); ok && len(s) > 200 {
			return collectionErr(CollectionCodeInvalidCapture)
		}
		return nil
	}
	if delim == '{' {
		seen := map[string]bool{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := key.(string)
			if !ok || len(s) > 200 || seen[s] {
				return collectionErr(CollectionCodeInvalidCapture)
			}
			seen[s] = true
			if e = collectionJSONKeys(d, depth+1); e != nil {
				return e
			}
		}
	} else if delim == '[' {
		for d.More() {
			if e = collectionJSONKeys(d, depth+1); e != nil {
				return e
			}
		}
	} else {
		return collectionErr(CollectionCodeInvalidCapture)
	}
	_, e = d.Token()
	return e
}

// PrepareCollectionCapture проверяет полноту captured периода до любых measurement writes.
func PrepareCollectionCapture(i CollectionIntent, r CollectionRawCapture) (CollectionPreparedCapture, error) {
	bad := func(code CollectionCode) (CollectionPreparedCapture, error) {
		return CollectionPreparedCapture{}, collectionErr(code)
	}
	if len(r.Body) == 0 {
		return bad(CollectionCodeInvalidCapture)
	}
	if len(r.Body) > 2097152 {
		return bad(CollectionCodeInputLimit)
	}
	d := json.NewDecoder(bytes.NewReader(r.Body))
	d.UseNumber()
	if e := collectionJSONKeys(d, 0); e != nil {
		return bad(CollectionCodeInvalidCapture)
	}
	if _, e := d.Token(); e != io.EOF {
		return bad(CollectionCodeInvalidCapture)
	}
	var required map[string]json.RawMessage
	if json.Unmarshal(r.Body, &required) != nil || len(required) != 10 {
		return bad(CollectionCodeInvalidCapture)
	}
	var env collectionEnvelope
	d = json.NewDecoder(bytes.NewReader(r.Body))
	d.DisallowUnknownFields()
	if e := d.Decode(&env); e != nil {
		return bad(CollectionCodeInvalidCapture)
	}
	a, b, e := NormalizeCollectionPeriod(env.Start, env.End, i.Fixture)
	if e != nil || !a.Equal(i.Start) || !b.Equal(i.End) || env.Protocol != "collection-fixture-v1" || env.Protocol != i.Fixture.Protocol || env.FixtureID != i.Fixture.ID || env.WellID != i.Binding.ExternalID || !collectionSafeID.MatchString(env.DataRevision) || !env.Complete || env.Empty != i.Fixture.Empty || env.Records == nil {
		return bad(CollectionCodeInvalidCapture)
	}
	slots := int(b.Sub(a) / time.Minute)
	if env.Empty {
		if env.ExpectedSlots != 0 || len(env.Records) != 0 {
			return bad(CollectionCodeInvalidCapture)
		}
	} else if env.ExpectedSlots != slots {
		return bad(CollectionCodeInvalidCapture)
	}
	if len(env.Records) > 2880 {
		return bad(CollectionCodeInputLimit)
	}
	mappings, e := collectionMappings(i.Fixture)
	if e != nil {
		return bad(CollectionCodeInvalidCapture)
	}
	p := CollectionProvenance{SourceID: i.Binding.SourceID, SourceProfileVersion: i.SourceProfileVersion, SourceConfigSHA256: i.SourceConfigSHA256, FixtureID: i.Fixture.ID, DataRevision: env.DataRevision, BindingSHA256: i.BindingSHA256, MappingSHA256: i.MappingSHA256, CanonicalVersion: i.CanonicalVersion, SchemaVersion: i.SchemaVersion}
	if i.CanonicalVersion != "collection-canonical-v1" || i.SchemaVersion != "collection-measurement-v1" {
		return bad(CollectionCodeInvalidCapture)
	}
	out := CollectionPreparedCapture{Raw: append([]byte(nil), r.Body...), ReceivedAt: r.ReceivedAt.UTC(), Provenance: p, RawRows: len(env.Records), Empty: env.Empty}
	points := map[int64]Measurement{}
	digests := map[int64]string{}
	for _, row := range env.Records {
		ts, e := time.Parse(time.RFC3339Nano, row.Timestamp)
		if e != nil || ts.Second() != 0 || ts.Nanosecond() != 0 || ts.Before(a) || !ts.Before(b) || row.WellID != i.Binding.ExternalID || len(row.Values) != len(mappings) {
			return bad(CollectionCodeInvalidCapture)
		}
		if len(row.Values) > 20 {
			return bad(CollectionCodeInputLimit)
		}
		values := map[string]*float64{}
		seen := map[string]bool{}
		for field, raw := range row.Values {
			m, ok := mappings[field]
			if !ok {
				return bad(CollectionCodeInvalidCapture)
			}
			var value *float64
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				out.NullValues++
			} else {
				var number json.Number
				dec := json.NewDecoder(bytes.NewReader(raw))
				dec.UseNumber()
				v, e := dec.Token()
				if e != nil {
					return bad(CollectionCodeInvalidCapture)
				}
				number, ok = v.(json.Number)
				if !ok {
					return bad(CollectionCodeInvalidCapture)
				}
				n, e := strconv.ParseFloat(number.String(), 64)
				if e != nil || !collectionFinite(n) {
					return bad(CollectionCodeInvalidCapture)
				}
				marker := (m.channel.Name == "R_izol" && n == 9999) || (strings.HasPrefix(m.channel.Name, "energy_Input_energy_reactive_total_") && (n == 9999 || n == 999.9))
				for _, missing := range m.channel.MissingMarkers {
					marker = marker || n == missing
				}
				if m.mapping.Missing != "" {
					v, _ := strconv.ParseFloat(m.mapping.Missing, 64)
					marker = marker || n == v
				}
				if marker {
					out.MarkerValues++
				} else {
					n = n*m.mapping.Scale + m.mapping.Offset
					if !collectionFinite(n) || n < -1e12 || n > 1e12 || n < m.channel.Minimum || n > m.channel.Maximum {
						return bad(CollectionCodeInvalidCapture)
					}
					if n == 0 {
						n = 0
					}
					value = &n
				}
			}
			name := m.channel.Name
			if seen[name] {
				prev := values[name]
				if (prev == nil) != (value == nil) || (prev != nil && *prev != *value) {
					return bad(CollectionCodeInputConflict)
				}
			}
			// Alias consistency applies to setpoints too; remove them only after validation.
			values[name] = value
			seen[name] = true
		}
		for _, m := range mappings {
			if m.set {
				delete(values, m.channel.Name)
				out.ExcludedSetpoints++
			}
		}
		if len(values) > 16 {
			return bad(CollectionCodeInputLimit)
		}
		point := Measurement{WellID: "pulse:" + strconv.FormatInt(i.Binding.WellID, 10), Timestamp: ts.UTC(), Values: values}
		hash, e := CollectionPayloadSHA256(point, p)
		if e != nil {
			return bad(CollectionCodeInvalidCapture)
		}
		key := ts.Unix()
		if prev, ok := digests[key]; ok {
			if prev != hash {
				return bad(CollectionCodeInputConflict)
			}
			out.RepeatedRows++
		} else {
			points[key] = point
			digests[key] = hash
		}
	}
	if !env.Empty && len(points) != slots {
		return bad(CollectionCodeInvalidCapture)
	}
	ordered := make([]Measurement, 0, len(points))
	for _, point := range points {
		ordered = append(ordered, point)
	}
	sort.Slice(ordered, func(a, b int) bool { return ordered[a].Timestamp.Before(ordered[b].Timestamp) })
	canonical := struct {
		Version    string               `json:"version"`
		Provenance CollectionProvenance `json:"provenance"`
		Points     []Measurement        `json:"points"`
	}{i.CanonicalVersion, p, ordered}
	out.Canonical, e = json.Marshal(canonical)
	if e != nil {
		return bad(CollectionCodeInvalidCapture)
	}
	if len(out.Canonical) > 2097152 {
		return bad(CollectionCodeInputLimit)
	}
	out.RawSHA256 = collectionRawSHA256(out.Raw)
	out.CanonicalSHA256 = collectionRawSHA256(out.Canonical)
	out.UniqueRows = len(ordered)
	return out, nil
}
