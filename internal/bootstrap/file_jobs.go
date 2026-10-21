package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/fileinput"
	"github.com/effitech-pulse/pulse-backend/internal/adapter/grpcml"
	"github.com/effitech-pulse/pulse-backend/internal/adapter/parameterfile"
	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

// SyntheticFileManifest — immutable trusted fixture configuration, без raw payload.
// Hash относится к точным bytes всего файла; сам hash хранится в отдельном env/guard.
type SyntheticFileManifest struct {
	RunID        string               `json:"run_id"`
	TestSchema   string               `json:"test_schema"`
	AccountIDs   []int64              `json:"account_ids"`
	SchemaPath   string               `json:"schema_path"`
	SchemaSHA256 string               `json:"schema_sha256"`
	Policy       domain.FileJobPolicy `json:"policy"`
}
type syntheticFileGate struct {
	policy   domain.FileJobPolicy
	accounts []int64
}

func (g syntheticFileGate) ValidateRaw(_ context.Context, well int64, sha string, spec domain.FileParseSpec) error {
	raw, e := service.FileJobClientSpecJSON(spec)
	if e != nil {
		return e
	}
	for _, v := range g.policy.AllowedInputs {
		if v.WellID == well && v.RawSHA256 == sha && v.Format == spec.Format && v.ClientSpecSHA256 == domain.FileSHA256(raw) && bytes.Equal(v.ClientSpecJSON, raw) {
			return nil
		}
	}
	return domain.NewFileJobProblem(domain.Invalid, "synthetic_input_required")
}
func (g syntheticFileGate) Eligible(ctx context.Context, well int64) bool {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || p.AccountID == nil {
		return false
	}
	actor := false
	for _, id := range g.accounts {
		if *p.AccountID == id {
			actor = true
		}
	}
	if !actor {
		return false
	}
	for _, v := range g.policy.AllowedInputs {
		if v.WellID == well {
			return true
		}
	}
	return false
}
func (g syntheticFileGate) ValidatePrepared(_ context.Context, p domain.FileJobPrepared) error {
	allowedActor := false
	for _, id := range g.accounts {
		if p.Input.ActorAccountID == id {
			allowedActor = true
		}
	}
	if !allowedActor {
		return domain.NewFileJobProblem(domain.Forbidden, "synthetic_account_required")
	}
	for _, v := range g.policy.AllowedInputs {
		if v.WellID == p.Input.PulseWellID && v.RawSHA256 == p.Input.RawSHA256 && v.Format == p.Input.ParseSpec.Format && reflect.DeepEqual(v.ExecutionPin, p.Input.ExecutionPin) {
			return nil
		}
	}
	return domain.NewFileJobProblem(domain.Invalid, "synthetic_input_required")
}
func loadSyntheticFileManifest(c config.Config) (SyntheticFileManifest, error) {
	var manifest SyntheticFileManifest
	f, e := os.Open(c.FileCheckManifest)
	if e != nil {
		return manifest, errors.New("file_job_manifest_unavailable")
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if e != nil || len(raw) > 1<<20 || domain.FileSHA256(raw) != c.FileCheckManifestSHA256 {
		return manifest, errors.New("file_job_manifest_invalid")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&manifest); e != nil {
		return manifest, errors.New("file_job_manifest_invalid")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return manifest, errors.New("file_job_manifest_invalid")
	}
	if manifest.RunID != c.FileCheckRunID || manifest.Policy.RunID != manifest.RunID || manifest.TestSchema == "" || len(manifest.AccountIDs) == 0 || len(manifest.AccountIDs) > 16 || manifest.Policy.CohortVersion != c.CohortVersion || len(manifest.Policy.AllowedInputs) == 0 {
		return manifest, errors.New("file_job_manifest_invalid")
	}
	// Avoid self-reference: policy manifest hash is supplied by trusted external digest.
	if manifest.Policy.ManifestSHA256 != "" && manifest.Policy.ManifestSHA256 != c.FileCheckManifestSHA256 {
		return manifest, errors.New("file_job_manifest_invalid")
	}
	manifest.Policy.ManifestSHA256 = c.FileCheckManifestSHA256
	manifest.Policy.TestSchema = manifest.TestSchema
	manifest.Policy.AllowedAccountIDs = append([]int64(nil), manifest.AccountIDs...)
	for _, allowed := range manifest.Policy.AllowedInputs {
		var spec domain.FileParseSpec
		if json.Unmarshal(allowed.ClientSpecJSON, &spec) != nil {
			return manifest, errors.New("file_job_manifest_invalid")
		}
		canonical, e := service.FileJobClientSpecJSON(spec)
		if e != nil || !bytes.Equal(canonical, allowed.ClientSpecJSON) || domain.FileSHA256(canonical) != allowed.ClientSpecSHA256 {
			return manifest, errors.New("file_job_manifest_invalid")
		}
	}
	return manifest, nil
}
func buildFileJobs(ctx context.Context, c config.Config, store *postgres.Repository, scope service.FundScope, scorer service.PinnedTelemetryScorer) (*service.FileJobs, *service.FileChecks, *postgres.Repository, error) {
	manifest, e := loadSyntheticFileManifest(c)
	if e != nil {
		return nil, nil, nil, e
	}
	schema, e := (parameterfile.TelemetrySchemaReader{Path: manifest.SchemaPath}).Load(ctx)
	if e != nil || schema.SHA256 != manifest.SchemaSHA256 {
		return nil, nil, nil, errors.New("file_job_schema_invalid")
	}
	trusted, e := store.WithFileJobPolicy(manifest.Policy)
	if e != nil {
		return nil, nil, nil, e
	}
	// Startup validation checks actual PG schema/cohort/guard; never seeds/migrates.
	if e = trusted.ValidateFileJobPolicy(ctx); e != nil {
		return nil, nil, nil, e
	}
	checks, e := service.NewFileChecks(trusted, trusted, scope, fileinput.Reader{}, grpcml.FileJobScorer{Scorer: scorer}, schema, manifest.Policy.AllowedInputs[0].ExecutionPin, domain.DefaultFileInputLimits())
	if e != nil {
		return nil, nil, nil, e
	}
	jobs, e := service.NewFileJobs(trusted, checks, syntheticFileGate{policy: manifest.Policy, accounts: append([]int64(nil), manifest.AccountIDs...)})
	return jobs, checks, trusted, e
}
