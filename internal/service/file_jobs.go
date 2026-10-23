package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type FileJobSyntheticGate interface {
	ValidateRaw(context.Context, int64, string, domain.FileParseSpec) error
	ValidatePrepared(context.Context, domain.FileJobPrepared) error
}
type FileJobs struct {
	store  FileJobStore
	checks *FileChecks
	gate   FileJobSyntheticGate
}

func NewFileJobs(store FileJobStore, checks *FileChecks, gate FileJobSyntheticGate) (*FileJobs, error) {
	if store == nil || checks == nil || gate == nil {
		return nil, fileInvalid("invalid_file_jobs_configuration")
	}
	return &FileJobs{store, checks, gate}, nil
}

// FileJobClientSpecJSON использует точную Q0 канонизацию для trusted manifest.
func FileJobClientSpecJSON(spec domain.FileParseSpec) (json.RawMessage, error) {
	_, raw, e := normalizeFileSpec(spec, domain.DefaultFileInputLimits())
	return raw, e
}
func (s *FileJobs) Submit(ctx context.Context, well int64, key string, spec domain.FileParseSpec, raw io.Reader) (domain.FileJobStatus, error) {
	ctx, _, _, err := s.checks.authorize(ctx, well)
	if err != nil {
		return domain.FileJobStatus{}, err
	}
	if raw == nil || !checkRequestID.MatchString(key) {
		return domain.FileJobStatus{}, fileInvalid("invalid_request_key")
	}
	content, err := io.ReadAll(io.LimitReader(raw, s.checks.limits.RawBytes+1))
	if err != nil {
		return domain.FileJobStatus{}, err
	}
	if int64(len(content)) > s.checks.limits.RawBytes {
		return domain.FileJobStatus{}, domain.NewFileJobProblem(domain.Invalid, "raw_limit")
	}
	if err = s.gate.ValidateRaw(ctx, well, domain.FileSHA256(content), spec); err != nil {
		return domain.FileJobStatus{}, err
	}
	v, err := s.checks.PrepareFileInput(ctx, well, key, spec, bytes.NewReader(content))
	if err != nil {
		return domain.FileJobStatus{}, err
	}
	prepared := domain.FileJobPrepared{Input: v}
	if len(v.Canonical) > 0 {
		prepared.Start, prepared.End, prepared.PointsCount, err = fileJobCoverage(v.Canonical)
		if err != nil {
			return domain.FileJobStatus{}, err
		}
		if prepared.PointsCount != v.Rows {
			return domain.FileJobStatus{}, fileInvalid("input_rows_mismatch")
		}
	}
	if err = s.gate.ValidatePrepared(ctx, prepared); err != nil {
		return domain.FileJobStatus{}, err
	}
	return s.store.AcceptFileJob(ctx, prepared)
}
func fileJobCoverage(raw []byte) (time.Time, time.Time, int, error) {
	r := csv.NewReader(bytes.NewReader(raw))
	header, e := r.Read()
	if e != nil || len(header) == 0 || header[0] != "timestamp" {
		return time.Time{}, time.Time{}, 0, fileInvalid("canonical_timestamp_missing")
	}
	var first, last time.Time
	count := 0
	for {
		row, e := r.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return first, last, count, e
		}
		t, e := time.Parse(time.RFC3339Nano, row[0])
		if e != nil {
			return first, last, count, fileInvalid("canonical_timestamp_invalid")
		}
		if count == 0 || t.Before(first) {
			first = t
		}
		if count == 0 || t.After(last) {
			last = t
		}
		count++
	}
	if count == 0 {
		return first, last, count, fileInvalid("canonical_empty")
	}
	return first.UTC().Truncate(time.Microsecond), last.UTC().Truncate(time.Microsecond).Add(time.Microsecond), count, nil
}
func (s *FileJobs) Get(ctx context.Context, well int64, id string) (domain.FileJobStatus, error) {
	ctx, _, _, e := s.checks.authorize(ctx, well, "filechecks.read")
	if e != nil {
		return domain.FileJobStatus{}, e
	}
	return s.store.FileJob(ctx, well, id)
}
func (s *FileJobs) List(ctx context.Context, well int64, before string) (domain.FileJobPage, error) {
	ctx, _, _, e := s.checks.authorize(ctx, well, "filechecks.read")
	if e != nil {
		return domain.FileJobPage{}, e
	}
	return s.store.FileJobs(ctx, well, before)
}
func (s *FileJobs) Result(ctx context.Context, well int64, id string) (domain.FileSavedCheck, error) {
	ctx, _, _, e := s.checks.authorize(ctx, well, "filechecks.read")
	if e != nil {
		return domain.FileSavedCheck{}, e
	}
	return s.store.FileJobResult(ctx, well, id)
}
func (s *FileJobs) Cancel(ctx context.Context, well int64, id string, revision int64) (domain.FileJobStatus, error) {
	ctx, _, _, e := s.checks.authorize(ctx, well, "filechecks.cancel")
	if e != nil {
		return domain.FileJobStatus{}, e
	}
	if revision < 1 || revision > 9007199254740991 {
		return domain.FileJobStatus{}, fileInvalid("invalid_revision")
	}
	return s.store.CancelFileJob(ctx, well, id, revision)
}

// Eligible проверяет текущую сессию/cohort; не заменяет admission transaction.
func (s *FileJobs) Eligible(ctx context.Context) bool {
	ctx, _, e := s.checks.actor(ctx)
	if e != nil {
		return false
	}
	_, wells, e := s.checks.scope.Members(ctx)
	if e != nil {
		return false
	}
	for _, w := range wells {
		if _, _, _, e = s.checks.authorize(ctx, w.ID); e == nil {
			gate, ok := s.gate.(interface {
				Eligible(context.Context, int64) bool
			})
			if ok && gate.Eligible(ctx, w.ID) {
				return true
			}
		}
	}
	return false
}
