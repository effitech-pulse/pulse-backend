package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/telemetry"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/migrations"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestTelemetrySourceIngestAndDuplicateImport(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal("migration must be repeatable", err)
	}
	app := New(testConfig(), pool, &fakeML{})
	app.http.Integrations = service.NewIntegrations(app.store, telemetry.Connector{}, telemetry.Secrets{}, &fakeML{})
	h := app.Handler()
	created := call(t, h, "POST", "/api/v1/integrations", `{"name":"Test source","kind":"json_push","interval_seconds":5,"well_field":"well_id","time_field":"timestamp","mappings":[{"field":"f","parameter":"F_out","scale":1},{"field":"r","parameter":"R_izol","scale":2}]}`, 200).(map[string]any)
	id := created["id"].(string)
	if created["status"] != "draft" || created["profile_version"] != float64(1) {
		t.Fatal(created)
	}
	call(t, h, "PATCH", "/api/v1/integrations/"+id+"/status", `{"status":"running"}`, 200)
	body := `{"records":[{"well_id":9223372036854775807,"timestamp":"2025-01-01T00:00:00Z","f":0,"r":9999}]}`
	out := call(t, h, "POST", "/api/v1/integrations/"+id+"/ingest", body, 200).(map[string]any)
	if out["accepted"] != float64(1) {
		t.Fatal(out)
	}
	out = call(t, h, "POST", "/api/v1/integrations/"+id+"/ingest", body, 200).(map[string]any)
	if out["duplicates"] != float64(1) {
		t.Fatal(out)
	}
	result := call(t, h, "GET", "/api/v1/telemetry?well_id=9223372036854775807&start=2024-12-31T00:00:00Z&end=2025-01-02T00:00:00Z", "", 200).(map[string]any)
	points := result["points"].([]any)
	if len(points) != 1 {
		t.Fatal(result)
	}
	values := points[0].(map[string]any)["values"].(map[string]any)
	if values["F_out"] != float64(0) || values["R_izol"] != nil {
		t.Fatal(values)
	}
	call(t, h, "PATCH", "/api/v1/integrations/"+id+"/status", `{"status":"paused"}`, 200)
	call(t, h, "POST", "/api/v1/integrations/"+id+"/ingest", body, 422)
	var profiles int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM telemetry_source_profile WHERE source_id=$1", id).Scan(&profiles); err != nil || profiles != 3 {
		t.Fatal(profiles, err)
	}
	call(t, h, "POST", "/api/v1/telemetry", `{"points":[{"well_id":"manual","timestamp":"2025-01-01T00:00:00Z","values":{"F_out":0}}]}`, 200)
	call(t, h, "POST", "/api/v1/telemetry/score", `{"well_id":"manual","start":"2024-12-31T00:00:00Z","end":"2025-01-02T00:00:00Z"}`, 200)
	call(t, h, "GET", "/api/v1/integrations/logs", "", 200)
}

func TestExternalPostgresNumericAndSmallint(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	var schema string
	if err := pool.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "CREATE TABLE source_values (well text, ts timestamptz, f numeric, current smallint); INSERT INTO source_values VALUES ('db-1',now(),0,31)"); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	u.User = url.User(u.User.Username())
	source := domain.TelemetrySource{Kind: "postgres", Endpoint: u.String(), TableName: schema + ".source_values", TimeField: "ts", WellField: "well", Mappings: []domain.Mapping{{Field: "f", Parameter: "F_out", Scale: 1}, {Field: "current", Parameter: "Ia", Scale: 1}}}
	records, err := (postgres.ExternalTelemetry{}).ReadExternal(ctx, source, password)
	if err != nil {
		t.Fatal(err)
	}
	points, rejected := service.MapTelemetry(records, source)
	if len(points) != 1 || rejected != 0 || points[0].Values["F_out"] == nil || *points[0].Values["F_out"] != 0 || points[0].Values["Ia"] == nil || *points[0].Values["Ia"] != 31 {
		t.Fatal(points, rejected)
	}
}
func TestTelemetryPullAndLease(t *testing.T) {
	pool := testDB(t)
	app := New(testConfig(), pool, &fakeML{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("since") == "" {
			t.Error("missing watermark")
		}
		_, _ = fmt.Fprint(w, `[{"well":"pull","ts":"2025-01-01T00:00:00Z","f":50}]`)
	}))
	defer remote.Close()
	app.http.Integrations = service.NewIntegrations(app.store, telemetry.Connector{AllowedHosts: []string{"127.0.0.1"}, Databases: postgres.ExternalTelemetry{}}, telemetry.Secrets{})
	h := app.Handler()
	body := fmt.Sprintf(`{"name":"Pull","kind":"json_pull","endpoint":%q,"interval_seconds":60,"well_field":"well","time_field":"ts","mappings":[{"field":"f","parameter":"F_out","scale":1}]}`, remote.URL)
	created := call(t, h, "POST", "/api/v1/integrations", body, 200).(map[string]any)
	id := created["id"].(string)
	call(t, h, "PATCH", "/api/v1/integrations/"+id+"/status", `{"status":"running"}`, 200)
	claimed, err := app.store.ClaimSource(legacyResearchFixtureContext(), id, false)
	if err != nil || claimed == nil {
		t.Fatal(err)
	}
	call(t, h, "POST", "/api/v1/integrations/"+id+"/collect", "", 409)
	if err = app.store.FailCollection(legacyResearchFixtureContext(), *claimed, "test retry"); err != nil {
		t.Fatal(err)
	}
	out := call(t, h, "POST", "/api/v1/integrations/"+id+"/collect", "", 200).(map[string]any)
	if out["accepted"] != float64(1) {
		t.Fatal(out)
	}
	listed := call(t, h, "GET", "/api/v1/integrations", "", 200).([]any)
	if strings.Contains(fmt.Sprint(listed), "encrypted_secret") {
		t.Fatal("secret exposed")
	}
}

func TestTelemetryBoundsAndArchiveMerge(t *testing.T) {
	pool := testDB(t)
	ctx := legacyResearchFixtureContext()
	app := New(testConfig(), pool, &fakeML{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/bounds" {
			fmt.Fprint(w, `{"start":"2024-12-01T00:00:00Z","end":"2025-01-01T00:00:00Z"}`)
			return
		}
		fmt.Fprint(w, `{"points":[{"well_id":"1","timestamp":"2025-01-01T00:00:00Z","values":{"F_out":50}}],"resolution":"1h"}`)
	}))
	defer server.Close()
	app.http.Integrations = service.NewIntegrations(app.store, telemetry.Connector{}, telemetry.Secrets{}).WithArchive(telemetry.Archive{BaseURL: server.URL, AllowedHosts: []string{"127.0.0.1"}})
	ts, _ := time.Parse(time.RFC3339, "2025-01-01T00:00:00Z")
	zero := 0.0
	_, err := app.store.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: "manual", Points: []domain.Measurement{{WellID: "1", Timestamp: ts, Values: map[string]*float64{"F_out": &zero}}}})
	if err != nil {
		t.Fatal(err)
	}
	h := app.Handler()
	bounds := call(t, h, "GET", "/api/v1/telemetry/bounds?well_id=1", "", 200).(map[string]any)
	if bounds["start"] != "2024-12-01T00:00:00Z" {
		t.Fatal(bounds)
	}
	result := call(t, h, "GET", "/api/v1/telemetry?well_id=1&start=2024-12-31T00:00:00Z&end=2025-01-02T00:00:00Z", "", 200).(map[string]any)
	ps := result["points"].([]any)
	if len(ps) != 1 || ps[0].(map[string]any)["values"].(map[string]any)["F_out"] != float64(0) {
		t.Fatal(result)
	}
}

// Снимок настроек не должен подменять watermark после следующего цикла сбора.
func TestSourceRuntimeTimestampsOverrideSavedProfile(t *testing.T) {
	ctx := legacyResearchFixtureContext()
	repo := postgres.New(testDB(t)).WithExecutionProfile("legacy", "research")
	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	source, err := repo.SaveSource(ctx, domain.TelemetrySource{ID: "watermark-profile", Name: "Watermark", Kind: "json_pull", Status: "running", IntervalSeconds: 5, LastReceived: &old, LastMeasured: &old})
	if err != nil {
		t.Fatal(err)
	}
	value := 50.0
	for n := 1; n <= 2; n++ {
		measured := old.Add(time.Duration(n) * time.Hour)
		lease, claimErr := repo.ClaimSource(ctx, source.ID, false)
		if claimErr != nil || lease == nil {
			t.Fatal(claimErr)
		}
		if _, err = repo.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: source.ID, ProfileVersion: source.ProfileVersion, Lease: lease, Points: []domain.Measurement{{WellID: "watermark-well", Timestamp: measured, Values: map[string]*float64{"F_out": &value}}}, Message: "cycle", Release: true}); err != nil {
			t.Fatal(err)
		}
		got, err := repo.Source(ctx, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		all, err := repo.Sources(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 1 {
			t.Fatal(all)
		}
		for _, s := range []domain.TelemetrySource{got, all[0]} {
			if s.LastMeasured == nil || !s.LastMeasured.Equal(measured) {
				t.Fatalf("cycle %d: stale watermark %v, want %v", n, s.LastMeasured, measured)
			}
			if s.LastReceived == nil || !s.LastReceived.After(old) {
				t.Fatalf("cycle %d: stale collection time %v", n, s.LastReceived)
			}
		}
		source, err = repo.SaveSource(ctx, got)
		if err != nil {
			t.Fatal(err)
		}
	}
}
