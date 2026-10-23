package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestMaximumSyntheticManifestMatchesFrozenPolicy(t *testing.T) {
	raw, err := os.ReadFile("testdata/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var policy domain.CollectionPolicy
	if err = json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	sha := sha256.Sum256(raw)
	policy.ManifestSHA256 = hex.EncodeToString(sha[:])
	if err = domain.ValidateCollectionPolicy(policy); err != nil {
		t.Fatal("maximum valid control rejected", err)
	}
	policy.Fixtures[0].Channels = append(policy.Fixtures[0].Channels, domain.CollectionChannel{Name: "unexpected", Minimum: 0, Maximum: 1})
	if err = domain.ValidateCollectionPolicy(policy); err == nil {
		t.Fatal("cap+1 descriptor accepted")
	}
}
