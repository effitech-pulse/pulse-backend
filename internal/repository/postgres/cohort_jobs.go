package postgres

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type cohortJobRow struct {
	ID            string                `json:"id"`
	RunID         int64                 `json:"run_id"`
	Actor         int64                 `json:"actor_account_id"`
	ActorRevision int64                 `json:"actor_revision"`
	Key           string                `json:"request_key"`
	Commitment    string                `json:"request_commitment_sha256"`
	Pilot         string                `json:"pilot_run_id"`
	Version       string                `json:"cohort_version"`
	CohortSHA     string                `json:"cohort_manifest_sha256"`
	SnapshotID    string                `json:"snapshot_id"`
	AsOf          time.Time             `json:"as_of"`
	Reserved      int64                 `json:"reserved_bytes"`
	State         domain.CohortJobState `json:"state"`
	Revision      int64                 `json:"revision"`
	Generation    int                   `json:"generation"`
	Fence         int64                 `json:"fence"`
	Attempt       int                   `json:"attempt_count"`
	Starts        int                   `json:"compute_starts"`
	Token         *string               `json:"lease_token_sha256"`
	Until         *time.Time            `json:"lease_until"`
	PID           *int32                `json:"physical_backend_pid"`
	Created       time.Time             `json:"created_at"`
	Updated       time.Time             `json:"updated_at"`
	Deadline      time.Time             `json:"deadline_at"`
	Started       *time.Time            `json:"started_at"`
	Finished      *time.Time            `json:"finished_at"`
	Next          *time.Time            `json:"next_attempt_at"`
	Error         *string               `json:"error_code"`
}

func cohortJobQuery() sq.SelectBuilder { return sqlBuilder.Select("*").From("cohort_job") }
func cohortRow(ctx context.Context, tx db, id string, lock string) (cohortJobRow, error) {
	return one[cohortJobRow](ctx, tx, cohortJobQuery().Where(sq.Eq{"id": id}).Suffix(lock))
}
func cohortMembers(ctx context.Context, tx pgx.Tx, id string) ([]domain.CohortFrozenMember, error) {
	rows, e := tx.Query(ctx, "SELECT member_json FROM cohort_job_member WHERE job_id=$1 ORDER BY ordinal", id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.CohortFrozenMember{}
	for rows.Next() {
		var b []byte
		var m domain.CohortFrozenMember
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &m); e != nil {
			return nil, cohortError(domain.Invalid, "input_corrupt")
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type cohortManifest struct {
	Fixture    domain.CohortFixtureManifest
	Members    []domain.CohortFrozenMember
	FixtureSHA string
	InputSHA   string
	PinSHA     string
	JobSHA     string
}

func loadCohortManifest(ctx context.Context, tx pgx.Tx, id string) (cohortManifest, error) {
	var m cohortManifest
	var fixture, raw []byte
	e := tx.QueryRow(ctx, `SELECT fixture_json,manifest_json,fixture_sha256,input_manifest_sha256,execution_pin_sha256,job_manifest_sha256 FROM cohort_job_manifest WHERE job_id=$1`, id).Scan(&fixture, &raw, &m.FixtureSHA, &m.InputSHA, &m.PinSHA, &m.JobSHA)
	if e != nil {
		return m, e
	}
	if json.Unmarshal(fixture, &m.Fixture) != nil || json.Unmarshal(raw, &m.Members) != nil {
		return m, cohortError(domain.Invalid, "input_corrupt")
	}
	stored, e := cohortMembers(ctx, tx, id)
	if e != nil {
		return m, e
	}
	if len(stored) != 100 || !equalJobJSON(stored, m.Members) {
		return m, cohortError(domain.Invalid, "input_corrupt")
	}
	return m, nil
}
func cohortStatus(ctx context.Context, tx pgx.Tx, j cohortJobRow) (domain.CohortJobStatus, error) {
	s := domain.CohortJobStatus{ID: j.ID, RunID: j.RunID, State: j.State, Revision: j.Revision, CreatedAt: j.Created, UpdatedAt: j.Updated, DeadlineAt: j.Deadline, StartedAt: j.Started, FinishedAt: j.Finished, NextAttemptAt: j.Next, ErrorCode: j.Error, AttemptCount: j.Attempt, SnapshotID: j.SnapshotID, AsOf: j.AsOf, CohortVersion: j.Version, Counts: domain.CohortCoverage{MemberCount: 100, VisibleCount: 100}}
	var fixtureJSON []byte
	if e := tx.QueryRow(ctx, "SELECT fixture_json FROM cohort_job_manifest WHERE job_id=$1", j.ID).Scan(&fixtureJSON); e != nil {
		return s, e
	}
	var fixture domain.CohortFixtureManifest
	if e := json.Unmarshal(fixtureJSON, &fixture); e != nil {
		return s, e
	}
	s.AsOf = fixture.AsOf
	rows, e := tx.Query(ctx, "SELECT checkpoint_json FROM cohort_job_checkpoint WHERE job_id=$1", j.ID)
	if e != nil {
		return s, e
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var c domain.CohortCheckpoint
		if e = rows.Scan(&raw); e != nil {
			return s, e
		}
		if json.Unmarshal(raw, &c) != nil {
			return s, cohortError(domain.Invalid, "input_corrupt")
		}
		s.CompletedCount++
		switch c.Outcome.Status {
		case "scored":
			s.Counts.ScoredCount++
		case "archived":
			s.Counts.ArchivedCount++
		case "inactive":
			s.Counts.InactiveCount++
		case "insufficient":
			s.Counts.InsufficientCount++
		case "error":
			s.Counts.ErrorCount++
		}
	}
	return s, rows.Err()
}
func (r *Repository) AcceptCohortJob(ctx context.Context, v domain.PreparedCohortJob) (domain.CohortJobStatus, error) {
	var zero domain.CohortJobStatus
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	actor, e := cohortSession(ctx)
	if e != nil {
		return zero, e
	}
	if actor != v.ActorAccountID {
		return zero, cohortError(domain.Missing, "cohort_job_not_found")
	}
	tx, e := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	if e = cohortGate(ctx, tx); e != nil {
		return zero, e
	}
	old, lookupErr := one[cohortJobRow](ctx, tx, cohortJobQuery().Where(sq.Eq{"actor_account_id": actor, "request_key": v.RequestKey, "operation_kind": "cohort_score"}))
	if lookupErr != nil && !isMissing(lookupErr) {
		return zero, lookupErr
	}
	if lookupErr != nil {
		var held bool
		if e = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock($1)", runLock).Scan(&held); e != nil {
			return zero, e
		}
		if !held {
			return zero, cohortError(domain.Limited, "cohort_job_capacity")
		}
	}
	a, wells, ok, e := r.cohortAuthority(ctx, tx, actor, nil)
	if e != nil {
		return zero, e
	}
	if !ok {
		return zero, cohortError(domain.Missing, "cohort_job_not_found")
	}

	if lookupErr == nil {
		if old.Commitment != v.RequestCommitmentSHA256 {
			return zero, cohortError(domain.Duplicate, "request_key_conflict")
		}
		if !r.cohortMatches(old) {
			return zero, cohortError(domain.Missing, "cohort_job_not_found")
		}
		out, e := cohortStatus(ctx, tx, old)
		if e != nil {
			return zero, e
		}
		return out, tx.Commit(ctx)
	}
	// Validate all source bytes before applying current lifecycle markers.
	if e = domain.ValidatePreparedCohortJob(v); e != nil {
		return zero, e
	}
	permit := false
	for _, f := range r.cohortJobPolicy.Fixtures {
		if f.FixtureID == v.Fixture.FixtureID && f.FixtureSHA256 == v.FixtureSHA256 {
			permit = true
		}
	}
	if !permit || v.Fixture.CohortVersion != r.cohortJobPolicy.CohortVersion || v.Fixture.CohortManifestSHA256 != r.cohortJobPolicy.CohortManifestSHA256 {
		return zero, cohortError(domain.Forbidden, "synthetic_fixture_required")
	}
	active, e := activeCohort(ctx, tx)
	if e != nil {
		return zero, e
	}
	if active {
		return zero, cohortError(domain.Limited, "cohort_job_capacity")
	}
	var fileActive bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM file_job WHERE state NOT IN ('succeeded','failed','cancelled','expired','revoked')) OR EXISTS(SELECT 1 FROM file_job_runtime WHERE job_id IS NOT NULL)`).Scan(&fileActive); e != nil {
		return zero, e
	}
	if fileActive {
		return zero, cohortError(domain.Limited, "cohort_job_capacity")
	}
	v.Members = append([]domain.CohortFrozenMember(nil), v.Members...)
	sort.Slice(v.Members, func(i, j int) bool { return v.Members[i].Well.ID < v.Members[j].Well.ID })
	var total int64
	for i := range v.Members {
		m := &v.Members[i]
		if m.Well.ID != wells[i].ID {
			return zero, cohortError(domain.Invalid, "cohort_members_mismatch")
		}
		m.Well = wells[i]
		if m.Well.ArchivedAt != nil {
			utc := m.Well.ArchivedAt.UTC()
			m.Well.ArchivedAt = &utc
		}
		metadata, err := json.Marshal(m.Well)
		if err != nil {
			return zero, err
		}
		if len(metadata) > 4096 {
			return zero, cohortError(domain.Limited, "cohort_metadata_capacity")
		}
		m.Ordinal = i + 1
		if m.Well.ArchivedAt != nil {
			reason := "archived"
			m.Input = nil
			m.NoComputeReason = &reason
		} else if !m.Well.Active {
			reason := "inactive"
			m.Input = nil
			m.NoComputeReason = &reason
		}
		if m.Input != nil {
			input := *m.Input
			input.Canonical = append([]byte(nil), input.Canonical...)
			input.Identity.InputSnapshotID, e = fileJobID()
			if e != nil {
				return zero, e
			}
			input.Identity.FingerprintSHA256, e = domain.CohortInputFingerprint(m.Well.ID, input, v.Fixture.ExecutionPin, v.Fixture.CaptureRecipe)
			if e != nil {
				return zero, e
			}
			total += input.Identity.CanonicalBytes
			m.Input = &input
		}
	}
	reserved := total + 36<<20
	var globalCount, accountCount int
	var globalBytes, accountBytes int64
	if e = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE actor_account_id=$1),coalesce(sum(reserved_bytes),0),coalesce(sum(reserved_bytes) FILTER(WHERE actor_account_id=$1),0) FROM cohort_job`, actor).Scan(&globalCount, &accountCount, &globalBytes, &accountBytes); e != nil {
		return zero, e
	}
	if globalCount >= 8 || accountCount >= 4 || globalBytes+reserved > 512<<20 || accountBytes+reserved > 256<<20 {
		return zero, cohortError(domain.Limited, "cohort_job_capacity")
	}
	job, e := fileJobID()
	if e != nil {
		return zero, e
	}
	snapshot, e := fileJobID()
	if e != nil {
		return zero, e
	}
	var run int64
	if e = tx.QueryRow(ctx, `INSERT INTO ml_run(status,trigger,started_at,profile,cohort_version) VALUES('running','manual',clock_timestamp(),'cohort',$1) RETURNING id`, v.Fixture.CohortVersion).Scan(&run); e != nil {
		return zero, e
	}
	if run > math.MaxInt32 {
		return zero, cohortError(domain.Limited, "cohort_run_id_capacity")
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return zero, e
	}
	actorScope, e := json.Marshal(a.Scope)
	if e != nil {
		return zero, e
	}
	_, e = execute(ctx, tx, sqlBuilder.Insert("cohort_job").Columns("id", "run_id", "actor_account_id", "actor_revision", "actor_role", "actor_scope", "request_key", "request_commitment_sha256", "pilot_run_id", "cohort_version", "cohort_manifest_sha256", "snapshot_id", "as_of", "reserved_bytes", "state", "created_at", "updated_at", "deadline_at").Values(job, run, actor, a.Revision, a.Role, actorScope, v.RequestKey, v.RequestCommitmentSHA256, r.cohortJobPolicy.RunID, v.Fixture.CohortVersion, v.Fixture.CohortManifestSHA256, snapshot, v.Fixture.AsOf, reserved, "pending_dispatch", now, now, now.Add(30*time.Minute)))
	if e != nil {
		return zero, e
	}
	inputSHA, e := domain.CohortInputManifestSHA256(v.Fixture, v.Members)
	if e != nil {
		return zero, e
	}
	pinSHA, e := domain.CohortExecutionPinSHA256(v.Fixture.ExecutionPin)
	if e != nil {
		return zero, e
	}
	jobSHA, e := domain.CohortJobManifestSHA256(job, run, snapshot, v.FixtureSHA256, inputSHA, pinSHA)
	if e != nil {
		return zero, e
	}
	raw, e := json.Marshal(v.Members)
	if e != nil {
		return zero, e
	}
	if len(raw) > 512<<10 {
		return zero, cohortError(domain.Limited, "cohort_manifest_capacity")
	}
	_, e = execute(ctx, tx, sqlBuilder.Insert("cohort_job_manifest").Columns("job_id", "fixture_sha256", "input_manifest_sha256", "execution_pin_sha256", "job_manifest_sha256", "fixture_json", "manifest_json").Values(job, v.FixtureSHA256, inputSHA, pinSHA, jobSHA, v.FixtureJSON, raw))
	if e != nil {
		return zero, e
	}
	for _, m := range v.Members {
		raw, e = json.Marshal(m)
		if e != nil {
			return zero, e
		}
		var inputID any
		if m.Input != nil {
			inputID = m.Input.Identity.InputSnapshotID
		}
		_, e = execute(ctx, tx, sqlBuilder.Insert("cohort_job_member").Columns("job_id", "well_id", "ordinal", "member_json", "input_id", "no_compute_reason").Values(job, m.Well.ID, m.Ordinal, raw, inputID, m.NoComputeReason))
		if e != nil {
			return zero, e
		}
		if m.Input != nil {
			in := m.Input
			_, e = execute(ctx, tx, sqlBuilder.Insert("cohort_job_input").Columns("job_id", "well_id", "input_id", "source_kind", "canonical_sha256", "fingerprint_sha256", "canonical_length", "canonical").Values(job, m.Well.ID, in.Identity.InputSnapshotID, in.Identity.SourceKind, in.Identity.CanonicalSHA256, in.Identity.FingerprintSHA256, in.Identity.CanonicalBytes, in.Canonical))
			if e != nil {
				return zero, e
			}
		}
	}
	if e = cohortOutbox(ctx, tx, job, 1, now); e != nil {
		return zero, e
	}
	j, e := cohortRow(ctx, tx, job, "")
	if e != nil {
		return zero, e
	}
	out, e := cohortStatus(ctx, tx, j)
	if e != nil {
		return zero, e
	}
	audit := j
	audit.Revision--
	if e = cohortAction(ctx, tx, audit, "accepted", "accepted", actor, now); e != nil {
		return zero, e
	}
	return out, tx.Commit(ctx)
}
func (r *Repository) cohortMatches(j cohortJobRow) bool {
	p := r.cohortJobPolicy
	return p != nil && j.Pilot == p.RunID && j.Version == p.CohortVersion && j.CohortSHA == p.CohortManifestSHA256
}
func (r *Repository) CohortJobStatus(ctx context.Context, id string) (domain.CohortJobStatus, error) {
	var zero domain.CohortJobStatus
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	actor, e := cohortSession(ctx)
	if e != nil {
		return zero, e
	}
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	_, _, ok, e := r.cohortAuthority(ctx, tx, actor, nil)
	if e != nil {
		return zero, e
	}
	if !ok {
		return zero, cohortError(domain.Missing, "cohort_job_not_found")
	}
	j, e := cohortRow(ctx, tx, id, "")
	if e != nil {
		return zero, e
	}
	if !r.cohortMatches(j) {
		return zero, cohortError(domain.Missing, "cohort_job_not_found")
	}
	out, e := cohortStatus(ctx, tx, j)
	if e != nil {
		return zero, e
	}
	return out, tx.Commit(ctx)
}
