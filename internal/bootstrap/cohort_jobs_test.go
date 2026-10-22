package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func cohortCatalogueConfig(t *testing.T) (config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	asof := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	var csv strings.Builder
	csv.WriteString("timestamp,CosF,F_out,Ia,Motor_winding_temperature,Ped_load,Pump_intake_pressure_kgs,qg\n")
	for day := 30; day >= 0; day-- {
		fmt.Fprintf(&csv, "%s,0.8,50,10,60,80,20,100\n", asof.Add(-time.Duration(day)*24*time.Hour).Format(time.RFC3339Nano))
	}
	raw := []byte(csv.String())
	if err := os.WriteFile(filepath.Join(dir, "input.csv"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	h := domain.FileSHA256([]byte("fixture"))
	pin := domain.ExecutionPin{Version: "upload_pin_v1", ModelName: "fixture", BundleSHA256: h, ContextVersion: "legacy_v1", DecisionRule: &domain.DecisionRule{ContextVersion: "legacy_v1", PolicyID: "fixture", RuleSHA256: h, Watch: &domain.ThresholdRule{Scale: "raw", Comparison: ">", Threshold: 0}}, FeatureRecipeSHA256: h, FeatureSchemaSHA256: h, SourceManifestSHA256: h, RuntimeManifestSHA256: h}
	build, e := cohortCaptureBuildSHA256()
	if e != nil {
		t.Fatal(e)
	}
	f := SyntheticCohortFixture{Manifest: domain.CohortFixtureManifest{Version: "cohort_fixture_v1", FixtureID: "all100", CohortVersion: "fixture-v1", CohortManifestSHA256: strings.Repeat("a", 64), AsOf: asof, ExecutionPin: pin, CaptureRecipe: domain.CohortCaptureRecipe{Version: "cohort_fixture_capture_v1", Normalization: "none_supplied_canonical", BackendBuildSHA256: build}}}
	for i := int64(1); i <= 100; i++ {
		f.Manifest.Members = append(f.Manifest.Members, domain.CohortFixtureMember{WellID: i, CanonicalSHA256: domain.FileSHA256(raw), CanonicalBytes: int64(len(raw))})
		f.Inputs = append(f.Inputs, SyntheticCohortInput{WellID: i, Path: "input.csv"})
	}
	manifest := SyntheticCohortManifest{RunID: "test-run", TestSchema: "q2_fixture", AllowedAccountIDs: []int64{1}, CohortVersion: "fixture-v1", CohortManifestSHA256: strings.Repeat("a", 64), Fixtures: []SyntheticCohortFixture{f}}
	data, e := json.Marshal(manifest)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "manifest.json")
	if e = os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	return config.Config{CohortJobManifest: path, CohortJobManifestSHA256: domain.FileSHA256(data), CohortJobRunID: "test-run", CohortJobTestSchema: "q2_fixture", CohortVersion: "fixture-v1"}, dir
}
func TestCohortCatalogueFreezesAllInputsAndClones(t *testing.T) {
	c, dir := cohortCatalogueConfig(t)
	catalogue, _, err := loadCohortCatalogue(c)
	if err != nil {
		t.Fatal(err)
	}
	first, err := catalogue.Fixture("all100")
	if err != nil || len(first.Members) != 100 {
		t.Fatal(err)
	}
	expected := append([]byte(nil), first.Members[0].Input.Canonical...)
	if err = os.WriteFile(filepath.Join(dir, "input.csv"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	first.Members[0].Input.Canonical[0] = 'X'
	first.Fixture.ExecutionPin.DecisionRule.PolicyID = "changed"
	again, err := catalogue.Fixture("all100")
	if err != nil || string(again.Members[0].Input.Canonical) != string(expected) || again.Fixture.ExecutionPin.DecisionRule.PolicyID == "changed" || again.ActorAccountID != 0 || again.RequestKey != "" {
		t.Fatal("catalogue mutable")
	}
	// Worker policy remains available after original bytes disappear.
	if err = os.Remove(filepath.Join(dir, "input.csv")); err != nil {
		t.Fatal(err)
	}
	if _, err = loadCohortPolicy(c); err != nil {
		t.Fatal(err)
	}
}
func TestCohortCatalogueRejectsMissingCorruptAndSymlink(t *testing.T) {
	for _, name := range []string{"missing", "corrupt", "symlink", "manifest_symlink", "hash"} {
		t.Run(name, func(t *testing.T) {
			c, dir := cohortCatalogueConfig(t)
			input := filepath.Join(dir, "input.csv")
			switch name {
			case "missing":
				_ = os.Remove(input)
			case "corrupt":
				_ = os.WriteFile(input, []byte("changed"), 0600)
			case "symlink":
				_ = os.Rename(input, filepath.Join(dir, "actual.csv"))
				_ = os.Symlink("actual.csv", input)
			case "manifest_symlink":
				_ = os.Rename(c.CohortJobManifest, filepath.Join(dir, "actual.json"))
				_ = os.Symlink("actual.json", c.CohortJobManifest)
			case "hash":
				c.CohortJobManifestSHA256 = domain.FileSHA256([]byte("wrong"))
			}
			if _, _, err := loadCohortCatalogue(c); err == nil {
				t.Fatal("unsafe source accepted")
			}
		})
	}
}

func TestCohortCatalogueRejectsHundredthPathAndFutureSample(t *testing.T) {
	for _, name := range []string{"hundredth", "traversal", "future", "capture_build", "oversized"} {
		t.Run(name, func(t *testing.T) {
			c, dir := cohortCatalogueConfig(t)
			data, err := os.ReadFile(c.CohortJobManifest)
			if err != nil {
				t.Fatal(err)
			}
			var descriptor SyntheticCohortManifest
			if err = json.Unmarshal(data, &descriptor); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "hundredth":
				descriptor.Fixtures[0].Inputs[99].Path = "missing.csv"
			case "traversal":
				descriptor.Fixtures[0].Inputs[99].Path = "../input.csv"
			case "capture_build":
				descriptor.Fixtures[0].Manifest.CaptureRecipe.BackendBuildSHA256 = domain.FileSHA256([]byte("different API"))
			case "oversized":
				if err = os.WriteFile(filepath.Join(dir, "input.csv"), []byte(strings.Repeat("x", (512<<10)+1)), 0600); err != nil {
					t.Fatal(err)
				}
			case "future":
				raw, err := os.ReadFile(filepath.Join(dir, "input.csv"))
				if err != nil {
					t.Fatal(err)
				}
				raw = []byte(strings.ReplaceAll(string(raw), "2026-10-01T00:00:00Z", "2026-10-02T00:00:00Z"))
				if err = os.WriteFile(filepath.Join(dir, "input.csv"), raw, 0600); err != nil {
					t.Fatal(err)
				}
				for i := range descriptor.Fixtures[0].Manifest.Members {
					descriptor.Fixtures[0].Manifest.Members[i].CanonicalSHA256 = domain.FileSHA256(raw)
					descriptor.Fixtures[0].Manifest.Members[i].CanonicalBytes = int64(len(raw))
				}
			}
			data, err = json.Marshal(descriptor)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(c.CohortJobManifest, data, 0600); err != nil {
				t.Fatal(err)
			}
			c.CohortJobManifestSHA256 = domain.FileSHA256(data)
			if _, _, err = loadCohortCatalogue(c); err == nil {
				t.Fatal("unsafe catalogue accepted")
			}
		})
	}
}

func TestCohortCatalogueRejectsFIFOWithoutBlocking(t *testing.T) {
	// Subprocess isolates a potentially blocking open and lets the parent bound the proof.
	if mode := os.Getenv("PULSE_Q2_FIFO_TEST_MODE"); mode != "" {
		c, dir := cohortCatalogueConfig(t)
		path := filepath.Join(dir, "input.csv")
		if mode == "descriptor" {
			path = c.CohortJobManifest
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := unix.Mkfifo(path, 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := loadCohortCatalogue(c); err == nil {
			t.Fatal("FIFO accepted as trusted regular file")
		}
		return
	}
	for _, mode := range []string{"input", "descriptor"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCohortCatalogueRejectsFIFOWithoutBlocking$")
			cmd.Env = append(os.Environ(), "PULSE_Q2_FIFO_TEST_MODE="+mode)
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatal("catalogue FIFO open blocked past bounded startup check")
			}
			if err != nil {
				t.Fatalf("FIFO rejection failed: %v %s", err, output)
			}
		})
	}
}
