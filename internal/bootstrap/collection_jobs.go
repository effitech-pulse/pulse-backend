package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func loadCollectionPolicy(c config.Config) (domain.CollectionPolicy, error) {
	fail := func() (domain.CollectionPolicy, error) {
		return domain.CollectionPolicy{}, errors.New("collection_manifest_invalid")
	}
	raw, e := cohortReadRegular(filepath.Dir(c.CollectionJobManifest), filepath.Base(c.CollectionJobManifest), 65536)
	if e != nil || collectionManifestSHA256(raw) != c.CollectionJobManifestSHA256 {
		return fail()
	}
	if collectionManifestKeys(json.NewDecoder(bytes.NewReader(raw)), 0) != nil {
		return fail()
	}
	var p domain.CollectionPolicy
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil {
		return fail()
	}
	var extra any
	if d.Decode(&extra) != io.EOF || p.ManifestSHA256 != "" || p.RunID != c.CollectionJobRunID || p.TestSchema != c.CollectionJobTestSchema {
		return fail()
	}
	p.ManifestSHA256 = collectionManifestSHA256(raw)
	if domain.ValidateCollectionPolicy(p) != nil {
		return fail()
	}
	return p, nil
}
func buildCollectionJobs(ctx context.Context, c config.Config, store *postgres.Repository) (*service.CollectionJobs, error) {
	trusted := store
	if c.CollectionJobMode == "synthetic" {
		p, e := loadCollectionPolicy(c)
		if e != nil {
			return nil, e
		}
		trusted, e = store.WithCollectionJobPolicy(p)
		if e != nil {
			return nil, e
		}
	}
	if e := trusted.ValidateCollectionJobPolicy(ctx); e != nil {
		return nil, e
	}
	return service.NewCollectionJobs(trusted, c.CollectionJobMode == "synthetic")
}

// collectionManifestKeys не даёт duplicate-key JSON менять trusted metadata по порядку полей.
func collectionManifestKeys(d *json.Decoder, depth int) error {
	if depth > 12 {
		return errors.New("collection_manifest_invalid")
	}
	tok, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	if delim == '{' {
		seen := map[string]bool{}
		for d.More() {
			tok, e = d.Token()
			if e != nil {
				return e
			}
			key, ok := tok.(string)
			if !ok || seen[key] {
				return errors.New("collection_manifest_invalid")
			}
			seen[key] = true
			if e = collectionManifestKeys(d, depth+1); e != nil {
				return e
			}
		}
	} else if delim == '[' {
		for d.More() {
			if e = collectionManifestKeys(d, depth+1); e != nil {
				return e
			}
		}
	} else {
		return errors.New("collection_manifest_invalid")
	}
	_, e = d.Token()
	return e
}

func collectionManifestSHA256(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
