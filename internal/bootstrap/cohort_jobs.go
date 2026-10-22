package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

// SyntheticCohortManifest связывает trusted descriptor с typed fixture без путей в domain.
type SyntheticCohortManifest struct {
	RunID                string                   `json:"run_id"`
	TestSchema           string                   `json:"test_schema"`
	AllowedAccountIDs    []int64                  `json:"allowed_account_ids"`
	CohortVersion        string                   `json:"cohort_version"`
	CohortManifestSHA256 string                   `json:"cohort_manifest_sha256"`
	Fixtures             []SyntheticCohortFixture `json:"fixtures"`
}
type SyntheticCohortFixture struct {
	Manifest domain.CohortFixtureManifest `json:"manifest"`
	Inputs   []SyntheticCohortInput       `json:"inputs"`
}
type SyntheticCohortInput struct {
	WellID int64  `json:"well_id"`
	Path   string `json:"path"`
}
type cohortCatalogue struct {
	fixtures map[string]domain.PreparedCohortJob
}

func (c *cohortCatalogue) Fixture(id string) (domain.PreparedCohortJob, error) {
	v, ok := c.fixtures[id]
	if !ok {
		return domain.PreparedCohortJob{}, domain.NewError(domain.Missing, "cohort_fixture_unavailable")
	}
	// Canonical is explicitly omitted from JSON, therefore clone its bytes separately.
	raw, err := json.Marshal(v)
	if err != nil {
		return domain.PreparedCohortJob{}, err
	}
	var copy domain.PreparedCohortJob
	if err = json.Unmarshal(raw, &copy); err != nil {
		return copy, err
	}
	for i := range copy.Members {
		if v.Members[i].Input != nil {
			copy.Members[i].Input.Canonical = append([]byte(nil), v.Members[i].Input.Canonical...)
		}
	}
	return copy, nil
}
func cohortDescriptorError() error { return errors.New("cohort_job_manifest_invalid") }

// cohortReadRegular открывает каждый root-relative компонент с O_NOFOLLOW.
func cohortReadRegular(root, relative string, limit int64) ([]byte, error) {
	if relative == "" || filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == "." || strings.Contains(relative, "\\") {
		return nil, cohortDescriptorError()
	}
	parts := strings.Split(relative, string(filepath.Separator))
	for _, part := range parts {
		if part == ".." || part == "." || part == "" {
			return nil, cohortDescriptorError()
		}
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, cohortDescriptorError()
	}
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, part, flags, 0)
		_ = unix.Close(fd)
		if e != nil {
			return nil, cohortDescriptorError()
		}
		fd = next
	}
	file := os.NewFile(uintptr(fd), "cohort-source")
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() < 1 || stat.Size() > limit {
		return nil, cohortDescriptorError()
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) > limit || int64(len(raw)) != stat.Size() {
		return nil, cohortDescriptorError()
	}
	return raw, nil
}
func cohortCaptureBuildSHA256() (string, error) {
	name, err := os.Executable()
	if err != nil {
		return "", cohortDescriptorError()
	}
	file, err := os.Open(name)
	if err != nil {
		return "", cohortDescriptorError()
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(file, (128<<20)+1))
	if err != nil || n > 128<<20 {
		return "", cohortDescriptorError()
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
func loadCohortDescriptor(c config.Config) (SyntheticCohortManifest, domain.CohortJobPolicy, error) {
	var manifest SyntheticCohortManifest
	raw, err := cohortReadRegular(filepath.Dir(c.CohortJobManifest), filepath.Base(c.CohortJobManifest), 512<<10)
	if err != nil || domain.FileSHA256(raw) != c.CohortJobManifestSHA256 {
		return manifest, domain.CohortJobPolicy{}, cohortDescriptorError()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil {
		return manifest, domain.CohortJobPolicy{}, cohortDescriptorError()
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return manifest, domain.CohortJobPolicy{}, cohortDescriptorError()
	}
	if manifest.RunID != c.CohortJobRunID || manifest.TestSchema != c.CohortJobTestSchema || manifest.CohortVersion != c.CohortVersion || manifest.RunID == "" || manifest.TestSchema == "" || manifest.TestSchema == "public" || len(manifest.Fixtures) < 1 || len(manifest.Fixtures) > 2 || len(manifest.AllowedAccountIDs) < 1 || len(manifest.AllowedAccountIDs) > 16 {
		return manifest, domain.CohortJobPolicy{}, cohortDescriptorError()
	}
	seenAccounts := map[int64]bool{}
	for _, id := range manifest.AllowedAccountIDs {
		if id <= 0 || seenAccounts[id] {
			return manifest, domain.CohortJobPolicy{}, cohortDescriptorError()
		}
		seenAccounts[id] = true
	}
	policy := domain.CohortJobPolicy{RunID: manifest.RunID, TestSchema: manifest.TestSchema, ManifestSHA256: c.CohortJobManifestSHA256, CohortVersion: manifest.CohortVersion, CohortManifestSHA256: manifest.CohortManifestSHA256, AllowedAccountIDs: append([]int64(nil), manifest.AllowedAccountIDs...)}
	seen := map[string]bool{}
	for _, fixture := range manifest.Fixtures {
		if fixture.Manifest.CohortVersion != manifest.CohortVersion || fixture.Manifest.CohortManifestSHA256 != manifest.CohortManifestSHA256 || seen[fixture.Manifest.FixtureID] {
			return manifest, policy, cohortDescriptorError()
		}
		seen[fixture.Manifest.FixtureID] = true
		sha, e := domain.CohortFixtureSHA256(fixture.Manifest)
		if e != nil {
			return manifest, policy, cohortDescriptorError()
		}
		policy.Fixtures = append(policy.Fixtures, domain.CohortFixturePermit{FixtureID: fixture.Manifest.FixtureID, FixtureSHA256: sha})
	}
	return manifest, policy, nil
}

// loadCohortPolicy не читает source inputs и не сравнивает capture с worker executable.
func loadCohortPolicy(c config.Config) (domain.CohortJobPolicy, error) {
	_, policy, err := loadCohortDescriptor(c)
	return policy, err
}
func cohortInputMetadata(raw []byte) (domain.TelemetryBounds, *time.Time, int, int, error) {
	reader := csv.NewReader(bytes.NewReader(raw))
	header, err := reader.Read()
	if err != nil || len(header) < 2 {
		return domain.TelemetryBounds{}, nil, 0, 0, cohortDescriptorError()
	}
	var first, last time.Time
	rows := 0
	for {
		record, e := reader.Read()
		if e == io.EOF {
			break
		}
		if e != nil || len(record) == 0 {
			return domain.TelemetryBounds{}, nil, 0, 0, cohortDescriptorError()
		}
		stamp, e := time.Parse(time.RFC3339Nano, record[0])
		if e != nil {
			return domain.TelemetryBounds{}, nil, 0, 0, cohortDescriptorError()
		}
		stamp = stamp.UTC()
		if rows == 0 {
			first = stamp
		}
		last = stamp
		rows++
	}
	end := last.Add(time.Nanosecond)
	return domain.TelemetryBounds{Start: &first, End: &end}, &last, rows, len(header) - 1, nil
}
func loadCohortCatalogue(c config.Config) (*cohortCatalogue, domain.CohortJobPolicy, error) {
	manifest, policy, err := loadCohortDescriptor(c)
	if err != nil {
		return nil, policy, err
	}
	build, err := cohortCaptureBuildSHA256()
	if err != nil {
		return nil, policy, err
	}
	catalogue := &cohortCatalogue{fixtures: map[string]domain.PreparedCohortJob{}}
	var total int64
	for _, fixture := range manifest.Fixtures {
		if fixture.Manifest.CaptureRecipe.BackendBuildSHA256 != build {
			return nil, policy, cohortDescriptorError()
		}
		canonical, err := domain.CanonicalCohortFixture(fixture.Manifest)
		if err != nil {
			return nil, policy, err
		}
		// Sorting is owned by the shared canonical fixture helper.
		var normalized domain.CohortFixtureManifest
		if err = json.Unmarshal(canonical, &normalized); err != nil {
			return nil, policy, err
		}
		prepared := domain.PreparedCohortJob{Fixture: normalized, FixtureSHA256: domain.FileSHA256(canonical), FixtureJSON: canonical}
		paths := map[int64]string{}
		for _, input := range fixture.Inputs {
			if paths[input.WellID] != "" || input.Path == "" {
				return nil, policy, cohortDescriptorError()
			}
			paths[input.WellID] = input.Path
		}
		for i, member := range normalized.Members {
			frozen := domain.CohortFrozenMember{Ordinal: i + 1, Well: domain.Well{ID: member.WellID, Active: true}, NoComputeReason: member.NoComputeReason}
			if member.NoComputeReason == nil {
				raw, e := cohortReadRegular(filepath.Dir(c.CohortJobManifest), paths[member.WellID], 512<<10)
				if e != nil {
					return nil, policy, e
				}
				delete(paths, member.WellID)
				bounds, watermark, rows, columns, e := cohortInputMetadata(raw)
				if e != nil {
					return nil, policy, e
				}
				frozen.Input = &domain.CohortFrozenInput{Identity: domain.PinnedInputIdentity{SourceKind: "cohort_snapshot", CanonicalSHA256: domain.FileSHA256(raw), CanonicalBytes: int64(len(raw))}, Canonical: raw, Bounds: bounds, Watermark: watermark, Resolution: "supplied_canonical", Rows: rows, Columns: columns}
				total += int64(len(raw))
				if total > 48<<20 {
					return nil, policy, cohortDescriptorError()
				}
			}
			prepared.Members = append(prepared.Members, frozen)
		}
		if len(paths) != 0 {
			return nil, policy, cohortDescriptorError()
		}
		validation := prepared
		validation.ActorAccountID = manifest.AllowedAccountIDs[0]
		validation.RequestKey = "catalogue_validation"
		validation.RequestCommitmentSHA256, err = domain.CohortRequestCommitment(validation.ActorAccountID, normalized.FixtureID, prepared.FixtureSHA256)
		if err != nil {
			return nil, policy, err
		}
		if err = domain.ValidatePreparedCohortJob(validation); err != nil {
			return nil, policy, err
		}
		catalogue.fixtures[normalized.FixtureID] = prepared
	}
	return catalogue, policy, nil
}
func buildCohortJobs(ctx context.Context, c config.Config, store *postgres.Repository) (*service.CohortJobs, *postgres.Repository, error) {
	catalogue, policy, err := loadCohortCatalogue(c)
	if err != nil {
		return nil, nil, err
	}
	trusted, err := store.WithCohortJobPolicy(policy)
	if err != nil {
		return nil, nil, err
	}
	if err = trusted.ValidateCohortJobPolicy(ctx); err != nil {
		return nil, nil, err
	}
	jobs, err := service.NewCohortJobs(trusted, catalogue)
	return jobs, trusted, err
}
