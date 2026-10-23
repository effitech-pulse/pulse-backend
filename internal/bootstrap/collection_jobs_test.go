package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestCollectionManifestTrustedRawHash(t *testing.T) {
	h := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	p := domain.CollectionPolicy{RunID: "test-run", TestSchema: "q2c2_test", FixtureOrigin: "http://fixture:8080", AllowedAccountIDs: []int64{1}, Bindings: []domain.CollectionBinding{{SourceID: "source", WellID: 1, ExternalID: "-1", IdentityKind: "unitek"}}, Fixtures: []domain.CollectionFixture{{ID: "fixture", Protocol: "collection-fixture-v1", Horizon: h, RetentionStart: h.Add(-31 * 24 * time.Hour), Channels: []domain.CollectionChannel{{Name: "F_out", Minimum: 0, Maximum: 100}}, Mappings: []domain.Mapping{{Field: "f", Parameter: "F_out", Scale: 1}}}}}
	raw, _ := json.Marshal(p)
	path := filepath.Join(t.TempDir(), "manifest.json")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	c := config.Config{CollectionJobManifest: path, CollectionJobManifestSHA256: strings.TrimPrefix(domain.FileSHA256(raw), "sha256:"), CollectionJobRunID: p.RunID, CollectionJobTestSchema: p.TestSchema}
	got, e := loadCollectionPolicy(c)
	if e != nil || got.ManifestSHA256 != c.CollectionJobManifestSHA256 {
		t.Fatalf("valid manifest: %v", e)
	}
	duplicate := append([]byte(`{"run_id":"test-run",`), raw[1:]...)
	_ = os.WriteFile(path, duplicate, 0600)
	c.CollectionJobManifestSHA256 = strings.TrimPrefix(domain.FileSHA256(duplicate), "sha256:")
	if _, e = loadCollectionPolicy(c); e == nil {
		t.Fatal("duplicate manifest keys accepted")
	}
	_ = os.WriteFile(path, raw, 0600)
	c.CollectionJobManifestSHA256 = strings.Repeat("a", 64)
	if _, e = loadCollectionPolicy(c); e == nil {
		t.Fatal("manifest mismatch accepted")
	}
	p.ManifestSHA256 = strings.TrimPrefix(domain.FileSHA256(raw), "sha256:")
	raw, _ = json.Marshal(p)
	_ = os.WriteFile(path, raw, 0600)
	c.CollectionJobManifestSHA256 = strings.TrimPrefix(domain.FileSHA256(raw), "sha256:")
	if _, e = loadCollectionPolicy(c); e == nil {
		t.Fatal("self-referential manifest accepted")
	}
}
