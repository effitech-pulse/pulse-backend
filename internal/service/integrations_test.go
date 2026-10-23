package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestMapTelemetryPreservesZeroAndExactID(t *testing.T) {
	s := domain.TelemetrySource{WellField: "equipment.id", TimeField: "timestamp", Mappings: []domain.Mapping{{Field: "values.f", Parameter: "F_out", Scale: 1}, {Field: "values.r", Parameter: "R_izol", Scale: 2, Offset: 5}, {Field: "set", Parameter: "Annulus_pressure", IsSet: true}}}
	rows := []map[string]any{{"equipment": map[string]any{"id": json.Number("9223372036854775807")}, "timestamp": "2025-01-01T00:00:00Z", "values": map[string]any{"f": json.Number("0"), "r": json.Number("9999")}, "set": 100}}
	points, rejected := MapTelemetry(rows, s)
	if rejected != 0 || len(points) != 1 {
		t.Fatal(points, rejected)
	}
	p := points[0]
	if p.WellID != "9223372036854775807" || p.Values["F_out"] == nil || *p.Values["F_out"] != 0 || p.Values["R_izol"] != nil {
		t.Fatal(p)
	}
	if _, ok := p.Values["Annulus_pressure"]; ok {
		t.Fatal("setpoint leaked")
	}
}
func TestMapTelemetryRejectsInvalidTimestamp(t *testing.T) {
	s := domain.TelemetrySource{WellField: "well", TimeField: "ts", Mappings: []domain.Mapping{{Field: "f", Parameter: "F_out", Scale: 1}}}
	points, rejected := MapTelemetry([]map[string]any{{"well": "x", "ts": "invalid", "f": 0}, {"well": "x", "ts": "2025-01-01T00:00:00Z", "f": " "}}, s)
	if rejected != 1 || len(points) != 1 || points[0].Values["F_out"] != nil {
		t.Fatal(points, rejected)
	}
}
func TestSourceRejectsPasswordInAddressAndDuplicateMappings(t *testing.T) {
	s := domain.TelemetrySource{Name: "test", Kind: "json_pull", IntervalSeconds: 5, Endpoint: "https://user:password@example.test", WellField: "well", TimeField: "ts", Mappings: []domain.Mapping{{Field: "f", Parameter: "F_out", Scale: 1}}}
	if validateSource(s) == nil {
		t.Fatal("credential URL accepted")
	}
	s.Endpoint = "https://example.test"
	s.Mappings = append(s.Mappings, s.Mappings[0])
	if validateSource(s) == nil {
		t.Fatal("duplicate mapping accepted")
	}
}

type pulseOnlyStore struct{ TelemetryStore }

func (pulseOnlyStore) Telemetry(ctx context.Context, id string, start, end time.Time) (domain.TelemetryResult, error) {
	zero := 0.0
	if id != "pulse:2" {
		return domain.TelemetryResult{}, errors.New("wrong storage identity")
	}
	return domain.TelemetryResult{Points: []domain.Measurement{{WellID: id, Timestamp: start, Values: map[string]*float64{"F_out": &zero}}}}, nil
}
func (pulseOnlyStore) TelemetryBounds(ctx context.Context, id string) (domain.TelemetryBounds, error) {
	at := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	return domain.TelemetryBounds{Start: &at, End: &at}, nil
}

type forbiddenArchive struct{}

func (forbiddenArchive) Read(context.Context, string, time.Time, time.Time) (domain.TelemetryResult, error) {
	return domain.TelemetryResult{}, errors.New("archive must not be used")
}
func (forbiddenArchive) Bounds(context.Context, string) (domain.TelemetryBounds, error) {
	return domain.TelemetryBounds{}, errors.New("archive must not be used")
}
func TestUnlinkedPulseWellDoesNotRequireExternalArchive(t *testing.T) {
	service := NewIntegrations(pulseOnlyStore{}, nil, nil).WithArchive(forbiddenArchive{})
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	data, err := service.ReadApp(internalFixtureContext(), domain.Well{ID: 2}, start, start.Add(time.Hour))
	if err != nil || len(data.Points) != 1 || data.Points[0].WellID != "2" || *data.Points[0].Values["F_out"] != 0 {
		t.Fatal(data, err)
	}
	bounds, err := service.BoundsApp(internalFixtureContext(), domain.Well{ID: 2})
	if err != nil || bounds.Start == nil {
		t.Fatal(bounds, err)
	}
}

func TestCleanValueScalarSemantics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   any
		mapping domain.Mapping
		want    float64
		present bool
	}{
		{"zero", "0", domain.Mapping{Parameter: "F_out", Scale: 1}, 0, true},
		{"negative_zero", "-0", domain.Mapping{Parameter: "F_out", Scale: 1}, 0, true},
		{"decimal_comma", " 1,5 ", domain.Mapping{Scale: 2, Offset: 1}, 4, true},
		{"scientific", json.Number("5e1"), domain.Mapping{Scale: .5, Offset: -2}, 23, true},
		{"isolation_marker_before_scale", 9999.0, domain.Mapping{Parameter: "R_izol", Scale: 2, Offset: 5}, 0, false},
		{"reactive_marker", 999.9, domain.Mapping{Parameter: "energy_Input_energy_reactive_total_kW", Scale: 2}, 0, false},
		{"custom_marker", "-999", domain.Mapping{Scale: 2, Offset: 1, Missing: " -888; -999, -777 "}, 0, false},
		{"marker_tolerance", 1e-7, domain.Mapping{Scale: 1, Missing: "0"}, 0, false},
		{"valid_beyond_marker_tolerance", 2e-6, domain.Mapping{Scale: 1, Missing: "0"}, 2e-6, true},
		{"blank", " ", domain.Mapping{Scale: 1}, 0, false},
		{"null", nil, domain.Mapping{Scale: 1}, 0, false},
		{"nan", math.NaN(), domain.Mapping{Scale: 1}, 0, false},
		{"inf", math.Inf(1), domain.Mapping{Scale: 1}, 0, false},
		{"scale_overflow", 1e308, domain.Mapping{Scale: 2}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := cleanValue(tc.input, tc.mapping)
			if (got != nil) != tc.present || (got != nil && (*got != tc.want || math.Signbit(*got) != math.Signbit(tc.want))) {
				t.Fatal("scalar cleaning changed", got, tc.want, tc.present)
			}
		})
	}
}
