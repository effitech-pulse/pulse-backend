package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var collectionMeasuredNames = map[string]bool{
	"F_out": true, "Ia": true, "Ib": true, "Ic": true, "Ped_load": true, "CosF": true, "R_izol": true, "Motor_winding_temperature": true, "Pump_intake_pressure_kgs": true, "Annulus_pressure": true, "Vibration_X": true, "Vibration_Y": true, "Vibration_Z": true,
	"Flowrate for current day": true, "Flowrate for previously day": true, "energy_Input_energy_reactive_total_MW": true, "energy_Input_energy_reactive_total_kW": true,
}
var collectionSafeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var collectionHashID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var collectionPrefix = regexp.MustCompile(`^[34]x[0-9A-Fa-f]{4} `)

func collectionErr(code CollectionCode) error { return NewError(Invalid, string(code)) }
func collectionRawSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func collectionDigest(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", collectionErr(CollectionCodeInvalidCapture)
	}
	return collectionRawSHA256(b), nil
}
func collectionName(s string) string  { return collectionPrefix.ReplaceAllString(s, "") }
func collectionFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// ValidateCollectionPolicy проверяет ограниченные synthetic metadata без ввода-вывода.
func ValidateCollectionPolicy(p CollectionPolicy) error {
	bad := func() error { return collectionErr(CollectionCodeInvalidCollectionRequest) }
	if !collectionSafeID.MatchString(p.RunID) || !regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`).MatchString(p.TestSchema) || (p.TestSchema == "public" || p.TestSchema == "information_schema" || strings.HasPrefix(p.TestSchema, "pg_")) || !collectionHashID.MatchString(p.ManifestSHA256) {
		return bad()
	}
	u, e := url.Parse(p.FixtureOrigin)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Port() == "" {
		return bad()
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "fixture" && (ip == nil || !ip.IsLoopback()) {
		return bad()
	}
	if len(p.AllowedAccountIDs) < 1 || len(p.AllowedAccountIDs) > 2 || len(p.Bindings) < 1 || len(p.Bindings) > 2 || len(p.Fixtures) < 1 || len(p.Fixtures) > 4 {
		return bad()
	}
	seenAccounts := map[int64]bool{}
	for _, id := range p.AllowedAccountIDs {
		if id <= 0 || seenAccounts[id] {
			return bad()
		}
		seenAccounts[id] = true
	}
	sources := map[string]bool{}
	wells := map[int64]bool{}
	for _, b := range p.Bindings {
		n, e := strconv.ParseInt(b.ExternalID, 10, 64)
		if !collectionSafeID.MatchString(b.SourceID) || b.WellID <= 0 || e != nil || n >= 0 || strconv.FormatInt(n, 10) != b.ExternalID || (b.IdentityKind != "unitek" && b.IdentityKind != "armits") || sources[b.SourceID] || wells[b.WellID] {
			return bad()
		}
		sources[b.SourceID] = true
		wells[b.WellID] = true
	}
	fixtures := map[string]bool{}
	for _, f := range p.Fixtures {
		if !collectionSafeID.MatchString(f.ID) || fixtures[f.ID] || f.Protocol != "collection-fixture-v1" || f.Horizon.IsZero() || f.Horizon.Second() != 0 || f.Horizon.Nanosecond() != 0 || !f.RetentionStart.Equal(f.Horizon.Add(-31*24*time.Hour)) {
			return bad()
		}
		fixtures[f.ID] = true
		if _, e := collectionMappings(f); e != nil {
			return bad()
		}
	}
	b, e := json.Marshal(p)
	if e != nil || len(b) > 65536 {
		return bad()
	}
	return nil
}

// NormalizeCollectionPeriod сохраняет точную минутную границу и нормализует aware RFC3339.
func NormalizeCollectionPeriod(start, end string, f CollectionFixture) (time.Time, time.Time, error) {
	a, e := time.Parse(time.RFC3339Nano, start)
	if e != nil {
		return time.Time{}, time.Time{}, collectionErr(CollectionCodeInvalidPeriod)
	}
	b, e := time.Parse(time.RFC3339Nano, end)
	if e != nil || len(start) > 200 || len(end) > 200 || a.Second() != 0 || a.Nanosecond() != 0 || b.Second() != 0 || b.Nanosecond() != 0 || !b.After(a) || b.Sub(a) > 24*time.Hour || a.Before(f.RetentionStart) || b.After(f.Horizon) {
		return time.Time{}, time.Time{}, collectionErr(CollectionCodeInvalidPeriod)
	}
	return a.UTC(), b.UTC(), nil
}

// CollectionRequestSHA256 исключает replay namespace и исходное представление timezone.
func CollectionRequestSHA256(s CollectionSubmit, a, b time.Time) (string, error) {
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`).MatchString(s.ReplayKey) || !collectionSafeID.MatchString(s.FixtureID) || !collectionSafeID.MatchString(s.SourceID) || !b.After(a) {
		return "", collectionErr(CollectionCodeInvalidCollectionRequest)
	}
	return collectionDigest(struct {
		Version string    `json:"version"`
		Fixture string    `json:"fixture_id"`
		Source  string    `json:"source_id"`
		Start   time.Time `json:"start"`
		End     time.Time `json:"end"`
	}{"collection-request-v1", s.FixtureID, s.SourceID, a.UTC(), b.UTC()})
}

// CollectionPayloadSHA256 связывает wide row с полной captured provenance.
func CollectionPayloadSHA256(m Measurement, p CollectionProvenance) (string, error) {
	values := make(map[string]*float64, len(m.Values))
	for k, v := range m.Values {
		if v != nil {
			if !collectionFinite(*v) {
				return "", collectionErr(CollectionCodeInvalidCapture)
			}
			n := *v
			if n == 0 {
				n = 0
			}
			values[k] = &n
		} else {
			values[k] = nil
		}
	}
	m.Values = values
	m.Timestamp = m.Timestamp.UTC()
	return collectionDigest(struct {
		Point      Measurement          `json:"point"`
		Provenance CollectionProvenance `json:"provenance"`
	}{m, p})
}

type collectionMapped struct {
	mapping Mapping
	channel CollectionChannel
	set     bool
}

func collectionMappings(f CollectionFixture) (map[string]collectionMapped, error) {
	bad := func() (map[string]collectionMapped, error) { return nil, collectionErr(CollectionCodeInvalidCapture) }
	if len(f.Channels) < 1 || len(f.Channels) > 20 || len(f.Mappings) < 1 || len(f.Mappings) > 20 {
		return bad()
	}
	aliases := map[string]CollectionChannel{}
	measured, sets, extras := 0, 0, 0
	for _, c := range f.Channels {
		if c.Name == "" || len(c.Name) > 200 || collectionName(c.Name) != c.Name || !collectionFinite(c.Minimum) || !collectionFinite(c.Maximum) || c.Minimum > c.Maximum || c.Minimum < -1e12 || c.Maximum > 1e12 || len(c.Unit) > 200 {
			return bad()
		}
		if c.IsSet || strings.HasPrefix(c.Name, "set_") {
			sets++
		} else {
			if !collectionMeasuredNames[c.Name] {
				return bad()
			}
			if strings.HasPrefix(c.Name, "Flowrate ") || strings.HasPrefix(c.Name, "energy_") {
				extras++
			}
			measured++
		}
		names := append([]string{c.Name}, c.Aliases...)
		for _, name := range names {
			name = collectionName(name)
			if name == "" || len(name) > 200 {
				return bad()
			}
			if prev, ok := aliases[name]; ok && prev.Name != c.Name {
				return bad()
			}
			aliases[name] = c
		}
		for _, v := range c.MissingMarkers {
			if !collectionFinite(v) {
				return bad()
			}
		}
		lower := strings.ToLower(c.Name)
		if (c.Name == "F_out" || c.Name == "Ia" || c.Name == "Ib" || c.Name == "Ic" || strings.Contains(lower, "flowrate")) && (c.Minimum > 0 || c.Maximum < 0) {
			return bad()
		}
	}
	if measured > 16 || sets > 4 || extras > 3 {
		return bad()
	}
	out := map[string]collectionMapped{}
	for _, m := range f.Mappings {
		c, ok := aliases[collectionName(m.Parameter)]
		if !ok || m.Field == "" || len(m.Field) > 200 || len(m.Parameter) > 200 || len(m.Unit) > 200 || len(m.Missing) > 200 || !collectionFinite(m.Scale) || !collectionFinite(m.Offset) {
			return bad()
		}
		if _, ok := out[m.Field]; ok {
			return bad()
		}
		if m.Unit != "" && m.Unit != c.Unit {
			return bad()
		}
		if m.Missing != "" {
			if v, e := strconv.ParseFloat(m.Missing, 64); e != nil || !collectionFinite(v) {
				return bad()
			}
		}
		out[m.Field] = collectionMapped{m, c, m.IsSet || c.IsSet || strings.HasPrefix(c.Name, "set_")}
	}
	return out, nil
}
