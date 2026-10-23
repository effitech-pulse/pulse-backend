package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

// Registry хранит только locator dedicated connection; права и lease проверяются в PG.
type cohortClaimRegistry struct {
	mu     sync.Mutex
	claims map[string]*cohortClaim
}
type cohortClaim struct {
	gate     chan struct{}
	closed   chan struct{}
	once     sync.Once
	conn     *pgxpool.Conn
	socket   net.Conn
	lease    domain.CohortJobLease
	registry *cohortClaimRegistry
}

func (c *cohortClaim) Lease() domain.CohortJobLease { return c.lease }
func (c *cohortClaim) take(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return cohortError(domain.Duplicate, "stale_lease")
	case c.gate <- struct{}{}:
	}
	select {
	case <-c.closed:
		c.release()
		return cohortError(domain.Duplicate, "stale_lease")
	default:
	}
	if c.conn == nil || c.conn.Conn().IsClosed() {
		c.release()
		return cohortError(domain.Duplicate, "stale_lease")
	}
	return nil
}
func (c *cohortClaim) cleanup() {
	if c.conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		raw := c.conn.Hijack()
		_ = raw.Close(ctx)
		c.conn = nil
	}
}
func (c *cohortClaim) release() {
	select {
	case <-c.closed:
		c.cleanup()
	default:
	}
	<-c.gate
}
func (c *cohortClaim) Alive(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if e := c.take(ctx); e != nil {
		return e
	}
	defer c.release()
	var held bool
	e := c.conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=pg_backend_pid() AND locktype='advisory' AND classid=0 AND objid=$1 AND objsubid=1 AND granted)`, runLock).Scan(&held)
	if e != nil {
		return e
	}
	if !held {
		return cohortError(domain.Duplicate, "stale_lease")
	}
	return nil
}
func (c *cohortClaim) Close() {
	c.once.Do(func() {
		c.registry.mu.Lock()
		delete(c.registry.claims, c.lease.Token)
		c.registry.mu.Unlock()
		close(c.closed)
		_ = c.socket.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case c.gate <- struct{}{}:
		c.cleanup()
		<-c.gate
	case <-ctx.Done(): /* Active bounded operation owns cleanup after socket cancellation. */
	}
}
func (r *Repository) beginCohortClaimTx(ctx context.Context, l domain.CohortJobLease) (pgx.Tx, func(), error) {
	if r.cohortClaims == nil {
		return nil, nil, cohortError(domain.Duplicate, "stale_lease")
	}
	r.cohortClaims.mu.Lock()
	c := r.cohortClaims.claims[l.Token]
	r.cohortClaims.mu.Unlock()
	if c == nil || c.lease.JobID != l.JobID || c.lease.Fence != l.Fence || c.lease.PhysicalBackendPID != l.PhysicalBackendPID {
		return nil, nil, cohortError(domain.Duplicate, "stale_lease")
	}
	if e := c.take(ctx); e != nil {
		return nil, nil, e
	}
	tx, e := c.conn.Begin(ctx)
	if e != nil {
		c.release()
		return nil, nil, e
	}
	return tx, c.release, nil
}
func (r *Repository) staleCohortDisposition(ctx context.Context, l domain.CohortJobLease) (domain.CohortJobDisposition, error) {
	var zero domain.CohortJobDisposition
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	j, e := cohortRow(ctx, tx, l.JobID, "")
	if e != nil {
		return zero, e
	}
	s, e := cohortStatus(ctx, tx, j)
	if e != nil {
		return zero, e
	}
	return domain.CohortJobDisposition{Kind: "stale", Status: s}, tx.Commit(ctx)
}
func cohortUpdate(ctx context.Context, tx pgx.Tx, j cohortJobRow, values sq.Eq, now time.Time) error {
	values["revision"] = sq.Expr("revision+1")
	values["updated_at"] = now
	_, e := execute(ctx, tx, sqlBuilder.Update("cohort_job").SetMap(values).Where(sq.Eq{"id": j.ID}))
	return e
}
func cohortSupersede(ctx context.Context, tx pgx.Tx, id string) error {
	_, e := execute(ctx, tx, sqlBuilder.Update("cohort_job_outbox").SetMap(sq.Eq{"status": "superseded", "claim_token_sha256": nil, "claim_until": nil}).Where(sq.Eq{"job_id": id}).Where(sq.NotEq{"status": "superseded"}))
	return e
}
func cohortAction(ctx context.Context, tx pgx.Tx, j cohortJobRow, kind, code string, actor any, now time.Time) error {
	_, e := execute(ctx, tx, sqlBuilder.Insert("cohort_job_action").Columns("job_id", "revision", "kind", "reason_code", "actor_account_id", "created_at").Values(j.ID, j.Revision+1, kind, code, actor, now))
	return e
}
func cohortTerminal(ctx context.Context, tx pgx.Tx, j cohortJobRow, state domain.CohortJobState, code string, now time.Time) error {
	var safe any
	if code != "" {
		safe = code
	}
	if e := cohortUpdate(ctx, tx, j, sq.Eq{"state": state, "finished_at": now, "error_code": safe, "lease_token_sha256": nil, "lease_until": nil, "physical_backend_pid": nil, "next_attempt_at": nil, "fence": sq.Expr("fence+1")}, now); e != nil {
		return e
	}
	if state != domain.CohortJobSucceeded {
		if _, e := execute(ctx, tx, sqlBuilder.Update("ml_run").SetMap(sq.Eq{"status": "error", "finished_at": now, "error": safe}).Where(sq.Eq{"id": j.RunID, "status": "running"})); e != nil {
			return e
		}
	}
	if _, e := execute(ctx, tx, sqlBuilder.Update("cohort_job_inbox").SetMap(sq.Eq{"state": "completed", "outcome": state, "updated_at": now}).Where(sq.Eq{"job_id": j.ID, "state": "processing"})); e != nil {
		return e
	}
	if e := cohortSupersede(ctx, tx, j.ID); e != nil {
		return e
	}
	return cohortAction(ctx, tx, j, string(state), code, nil, now)
}
func cohortReceipt(ctx context.Context, tx pgx.Tx, j cohortJobRow, event string, generation int, state string, now time.Time) error {
	_, e := execute(ctx, tx, sqlBuilder.Insert("cohort_job_inbox").Columns("event_id", "job_id", "generation", "fence", "state", "updated_at").Values(event, j.ID, generation, j.Fence, state, now).Suffix("ON CONFLICT(event_id) DO NOTHING"))
	return e
}
func cohortQuarantine(ctx context.Context, tx pgx.Tx, j cohortJobRow, code string, now time.Time) error {
	if e := cohortUpdate(ctx, tx, j, sq.Eq{"state": "quarantined", "error_code": code, "lease_token_sha256": nil, "lease_until": nil, "physical_backend_pid": nil, "next_attempt_at": nil, "fence": sq.Expr("fence+1")}, now); e != nil {
		return e
	}
	if e := cohortSupersede(ctx, tx, j.ID); e != nil {
		return e
	}
	return cohortAction(ctx, tx, j, "quarantine", code, nil, now)
}
func cohortGeneration(ctx context.Context, tx pgx.Tx, j cohortJobRow, now time.Time) error {
	if j.Generation >= 8 {
		return cohortQuarantine(ctx, tx, j, "dispatch_exhausted", now)
	}
	if e := cohortSupersede(ctx, tx, j.ID); e != nil {
		return e
	}
	if e := cohortUpdate(ctx, tx, j, sq.Eq{"generation": j.Generation + 1, "state": "pending_dispatch", "next_attempt_at": nil, "error_code": nil}, now); e != nil {
		return e
	}
	return cohortOutbox(ctx, tx, j.ID, j.Generation+1, now)
}
func cohortRetry(ctx context.Context, tx pgx.Tx, j cohortJobRow, code string, now time.Time) error {
	delay := 10 * time.Second
	if j.Attempt >= 2 {
		delay = 30 * time.Second
	}
	if j.Attempt >= 3 || !j.Deadline.After(now.Add(delay+10*time.Second)) {
		return cohortTerminal(ctx, tx, j, domain.CohortJobFailed, "attempts_exhausted", now)
	}
	if e := cohortUpdate(ctx, tx, j, sq.Eq{"state": "retry_wait", "next_attempt_at": now.Add(delay), "lease_token_sha256": nil, "lease_until": nil, "physical_backend_pid": nil, "error_code": code, "fence": sq.Expr("fence+1")}, now); e != nil {
		return e
	}
	if _, e := execute(ctx, tx, sqlBuilder.Update("cohort_job_inbox").SetMap(sq.Eq{"state": "completed", "outcome": "retry", "updated_at": now}).Where(sq.Eq{"job_id": j.ID, "state": "processing"})); e != nil {
		return e
	}
	if e := cohortSupersede(ctx, tx, j.ID); e != nil {
		return e
	}
	return cohortAction(ctx, tx, j, "retry", code, nil, now)
}
func (r *Repository) lockCohort(ctx context.Context, tx pgx.Tx, id string) (cohortJobRow, cohortManifest, bool, error) {
	j, e := cohortRow(ctx, tx, id, "")
	if e != nil {
		return j, cohortManifest{}, false, e
	}
	m, e := loadCohortManifest(ctx, tx, id)
	if e != nil {
		return j, m, false, e
	}
	_, _, ok, e := r.cohortAuthority(ctx, tx, j.Actor, m.Members, j.Authorization, "cohortjobs.run", true)
	if e != nil {
		return j, m, false, e
	}
	j, e = cohortRow(ctx, tx, id, "FOR UPDATE")
	return j, m, ok && r.cohortMatches(j), e
}
func cohortPhysical(ctx context.Context, tx pgx.Tx, pid int32) (bool, error) {
	var held bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory' AND classid=0 AND objid=$2 AND objsubid=1 AND granted AND database=(SELECT oid FROM pg_database WHERE datname=current_database()))`, pid, runLock).Scan(&held)
	return held, e
}
func cohortLeaseMatches(j cohortJobRow, l domain.CohortJobLease, now time.Time) bool {
	return j.ID == l.JobID && j.Generation == l.Generation && j.Fence == l.Fence && j.Attempt == l.Attempt && j.Token != nil && *j.Token == domain.FileSHA256([]byte(l.Token)) && j.Until != nil && j.Until.After(now) && j.PID != nil && *j.PID == l.PhysicalBackendPID && j.State == domain.CohortJobRunning
}
func cohortDenied(j cohortJobRow, ok bool, now time.Time) (domain.CohortJobState, string) {
	if !ok {
		return domain.CohortJobRevoked, "access_revoked"
	}
	if !j.Deadline.After(now) {
		return domain.CohortJobExpired, "job_deadline"
	}
	if j.State == domain.CohortJobCancelRequested {
		return domain.CohortJobCancelled, "cancelled"
	}
	return "", ""
}
func (r *Repository) ClaimCohortJob(ctx context.Context, m domain.CohortJobMessage, worker string) (service.CohortJobClaim, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !fileJobOpaque(worker) || !fileJobOpaque(m.EventID) {
		return nil, cohortError(domain.Invalid, "invalid_worker_identity")
	}
	conn, e := r.pool.Acquire(ctx)
	if e != nil {
		return nil, e
	}
	keep := false
	locked := false
	defer func() {
		if !keep {
			if locked {
				(&lease{conn: conn}).Close()
			} else {
				conn.Release()
			}
		}
	}()
	tx, e := conn.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if e = cohortGate(ctx, tx); e != nil {
		return nil, e
	}
	event, e := one[cohortOutboxRow](ctx, tx, cohortOutboxQuery().Where(sq.Eq{"event_id": m.EventID}))
	if isMissing(e) {
		return nil, cohortError(domain.Missing, "cohort_event_unknown")
	}
	if e != nil {
		return nil, e
	}
	// Physical reservation precedes authority/job locks; busy is persisted without consuming an attempt.
	if e = tx.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", runLock).Scan(&locked); e != nil {
		return nil, e
	}
	j, manifest, allowed, e := r.lockCohort(ctx, tx, event.JobID)
	if e != nil {
		return nil, e
	}
	_ = manifest
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return nil, e
	}
	if m.Version != 1 || m.Kind != "cohort.ready.v1" || m.JobID != event.JobID || m.Generation != event.Generation {
		if !j.State.Terminal() {
			if e = cohortQuarantine(ctx, tx, j, "invalid_envelope", now); e != nil {
				return nil, e
			}
		}
		if e = cohortReceipt(ctx, tx, j, event.EventID, event.Generation, "superseded", now); e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	if j.State.Terminal() || j.Generation != m.Generation || j.State == domain.CohortJobQuarantined || j.State == domain.CohortJobRetryWait {
		if e = cohortReceipt(ctx, tx, j, event.EventID, event.Generation, "superseded", now); e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	if j.State == domain.CohortJobRunning || j.State == domain.CohortJobCancelRequested {
		if e = cohortReceipt(ctx, tx, j, event.EventID, event.Generation, "duplicate", now); e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	if state, code := cohortDenied(j, allowed, now); state != "" {
		if e = cohortReceipt(ctx, tx, j, event.EventID, event.Generation, "processing", now); e != nil {
			return nil, e
		}
		if e = cohortTerminal(ctx, tx, j, state, code, now); e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	var q1 bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM file_job WHERE state NOT IN ('succeeded','failed','cancelled','expired','revoked')) OR EXISTS(SELECT 1 FROM file_job_runtime WHERE job_id IS NOT NULL)`).Scan(&q1); e != nil {
		return nil, e
	}
	if !locked || q1 {
		if e = cohortGeneration(ctx, tx, j, now); e != nil {
			return nil, e
		}
		if e = cohortReceipt(ctx, tx, j, event.EventID, event.Generation, "superseded", now); e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	if j.Attempt >= 3 || j.Deadline.Sub(now) <= 10*time.Second {
		if e = cohortTerminal(ctx, tx, j, domain.CohortJobFailed, "attempts_exhausted", now); e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	token, e := fileJobID()
	if e != nil {
		return nil, e
	}
	l := domain.CohortJobLease{JobID: j.ID, EventID: m.EventID, Generation: j.Generation, Fence: j.Fence + 1, Token: token, DeadlineAt: j.Deadline, Attempt: j.Attempt + 1, PhysicalBackendPID: int32(conn.Conn().PgConn().PID())}
	if e = cohortUpdate(ctx, tx, j, sq.Eq{"state": "running", "attempt_count": l.Attempt, "fence": l.Fence, "lease_token_sha256": domain.FileSHA256([]byte(token)), "lease_until": now.Add(45 * time.Second), "physical_backend_pid": l.PhysicalBackendPID, "heartbeat_at": now, "started_at": sq.Expr("COALESCE(started_at,?)", now), "error_code": nil}, now); e != nil {
		return nil, e
	}
	j.Fence = l.Fence
	if e = cohortAction(ctx, tx, j, "claim", "claim_started", nil, now); e != nil {
		return nil, e
	}
	if e = cohortReceipt(ctx, tx, j, m.EventID, m.Generation, "processing", now); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	keep = true
	claim := &cohortClaim{gate: make(chan struct{}, 1), closed: make(chan struct{}), conn: conn, socket: conn.Conn().PgConn().Conn(), lease: l, registry: r.cohortClaims}
	r.cohortClaims.mu.Lock()
	r.cohortClaims.claims[l.Token] = claim
	r.cohortClaims.mu.Unlock()
	return claim, nil
}
func (r *Repository) checkedCohortLease(ctx context.Context, tx pgx.Tx, l domain.CohortJobLease) (cohortJobRow, cohortManifest, error) {
	j, m, ok, e := r.lockCohort(ctx, tx, l.JobID)
	if e != nil {
		return j, m, e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return j, m, e
	}
	if !j.State.Terminal() {
		if state, code := cohortDenied(j, ok, now); state != "" {
			if e = cohortTerminal(ctx, tx, j, state, code, now); e != nil {
				return j, m, e
			}
			if e = tx.Commit(ctx); e != nil {
				return j, m, e
			}
			return j, m, cohortError(domain.Duplicate, code)
		}
	}
	if !cohortLeaseMatches(j, l, now) {
		return j, m, cohortError(domain.Duplicate, "stale_lease")
	}
	held, e := cohortPhysical(ctx, tx, l.PhysicalBackendPID)
	if e != nil {
		return j, m, e
	}
	if !held {
		return j, m, cohortError(domain.Duplicate, "stale_lease")
	}
	if e = validateCohortManifest(j, m); e != nil {
		return j, m, e
	}
	return j, m, nil
}
func validateCohortManifest(j cohortJobRow, m cohortManifest) error {
	fixture, e := domain.CohortFixtureSHA256(m.Fixture)
	if e != nil || fixture != m.FixtureSHA {
		return cohortError(domain.Invalid, "input_corrupt")
	}
	input, e := domain.CohortInputManifestSHA256(m.Fixture, m.Members)
	if e != nil || input != m.InputSHA {
		return cohortError(domain.Invalid, "input_corrupt")
	}
	pin, e := domain.CohortExecutionPinSHA256(m.Fixture.ExecutionPin)
	if e != nil || pin != m.PinSHA {
		return cohortError(domain.Invalid, "input_corrupt")
	}
	hash, e := domain.CohortJobManifestSHA256(j.ID, j.RunID, j.SnapshotID, fixture, input, pin)
	if e != nil || hash != m.JobSHA || j.Version != m.Fixture.CohortVersion || j.CohortSHA != m.Fixture.CohortManifestSHA256 || !j.AsOf.Equal(m.Fixture.AsOf.Truncate(time.Microsecond)) {
		return cohortError(domain.Invalid, "input_corrupt")
	}
	return nil
}
func cohortExecution(ctx context.Context, tx pgx.Tx, j cohortJobRow, m cohortManifest, member domain.CohortFrozenMember) (domain.CohortMemberExecution, error) {
	x := domain.CohortMemberExecution{JobID: j.ID, Member: member, ExecutionPin: m.Fixture.ExecutionPin, ExecutionPinSHA256: m.PinSHA, InputManifestSHA256: m.InputSHA}
	if member.Input != nil {
		var raw []byte
		e := tx.QueryRow(ctx, "SELECT canonical FROM cohort_job_input WHERE job_id=$1 AND well_id=$2", j.ID, member.Well.ID).Scan(&raw)
		if errors.Is(e, pgx.ErrNoRows) {
			return x, cohortError(domain.Invalid, "input_unavailable")
		}
		if e != nil {
			return x, e
		}
		cp := *member.Input
		cp.Canonical = raw
		x.Member.Input = &cp
		if domain.FileSHA256(raw) != cp.Identity.CanonicalSHA256 || int64(len(raw)) != cp.Identity.CanonicalBytes {
			return x, cohortError(domain.Invalid, "input_corrupt")
		}
		fingerprint, e := domain.CohortInputFingerprint(member.Well.ID, cp, m.Fixture.ExecutionPin, m.Fixture.CaptureRecipe)
		if e != nil || fingerprint != cp.Identity.FingerprintSHA256 {
			return x, cohortError(domain.Invalid, "input_corrupt")
		}
	}
	return x, nil
}
func (r *Repository) NextCohortMember(ctx context.Context, l domain.CohortJobLease) (*domain.CohortMemberExecution, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, release, e := r.beginCohortClaimTx(ctx, l)
	if e != nil {
		return nil, e
	}
	defer release()
	defer tx.Rollback(ctx)
	if e = cohortOwnedRuntimeGate(ctx, tx); e != nil {
		return nil, e
	}
	j, m, e := r.checkedCohortLease(ctx, tx, l)
	if e != nil {
		return nil, e
	}
	accepted, e := list[struct {
		ID int64 `json:"well_id"`
	}](ctx, tx, sqlBuilder.Select("well_id").From("cohort_job_checkpoint").Where(sq.Eq{"job_id": j.ID}))
	if e != nil {
		return nil, e
	}
	seen := map[int64]bool{}
	for _, c := range accepted {
		seen[c.ID] = true
	}
	for _, member := range m.Members {
		if seen[member.Well.ID] {
			continue
		}
		x, e := cohortExecution(ctx, tx, j, m, member)
		if e != nil {
			return nil, e
		}
		if member.Input != nil {
			var n int
			if e = tx.QueryRow(ctx, "SELECT count(*) FROM cohort_job_member_attempt WHERE job_id=$1 AND well_id=$2", j.ID, member.Well.ID).Scan(&n); e != nil {
				return nil, e
			}
			if n >= 3 || j.Starts >= 102 {
				now, e := dbJobNow(ctx, tx)
				if e != nil {
					return nil, e
				}
				if e = cohortTerminal(ctx, tx, j, domain.CohortJobFailed, "attempts_exhausted", now); e != nil {
					return nil, e
				}
				if e = tx.Commit(ctx); e != nil {
					return nil, e
				}
				return nil, cohortError(domain.Duplicate, "attempts_exhausted")
			}
			x.Attempt = n + 1
			if _, e = execute(ctx, tx, sqlBuilder.Insert("cohort_job_member_attempt").Columns("job_id", "well_id", "attempt", "fence", "started_at").Values(j.ID, member.Well.ID, n+1, j.Fence, sq.Expr("clock_timestamp()"))); e != nil {
				return nil, e
			}
			if _, e = execute(ctx, tx, sqlBuilder.Update("cohort_job").Set("compute_starts", j.Starts+1).Where(sq.Eq{"id": j.ID})); e != nil {
				return nil, e
			}
		}
		return &x, tx.Commit(ctx)
	}
	if len(seen) != 100 {
		return nil, cohortError(domain.Invalid, "input_corrupt")
	}
	return nil, tx.Commit(ctx)
}
func (r *Repository) RenewCohortJob(ctx context.Context, l domain.CohortJobLease) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, release, e := r.beginCohortClaimTx(ctx, l)
	if e != nil {
		return e
	}
	defer release()
	defer tx.Rollback(ctx)
	if e = cohortOwnedRuntimeGate(ctx, tx); e != nil {
		return e
	}
	j, _, e := r.checkedCohortLease(ctx, tx, l)
	if e != nil {
		return e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Update("cohort_job").SetMap(sq.Eq{"lease_until": now.Add(45 * time.Second), "heartbeat_at": now}).Where(sq.Eq{"id": j.ID})); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (r *Repository) CheckpointCohortMember(ctx context.Context, l domain.CohortJobLease, c domain.CohortCheckpoint) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, release, e := r.beginCohortClaimTx(ctx, l)
	if e != nil {
		return e
	}
	defer release()
	defer tx.Rollback(ctx)
	if e = cohortOwnedRuntimeGate(ctx, tx); e != nil {
		return e
	}
	j, m, e := r.checkedCohortLease(ctx, tx, l)
	if e != nil {
		return e
	}
	var member *domain.CohortFrozenMember
	for i := range m.Members {
		if m.Members[i].Well.ID == c.WellID {
			member = &m.Members[i]
			break
		}
	}
	if member == nil {
		return cohortError(domain.Invalid, "result_identity_mismatch")
	}
	x, e := cohortExecution(ctx, tx, j, m, *member)
	if e != nil {
		return e
	}
	if e = domain.ValidateCohortCheckpoint(x, c); e != nil {
		return e
	}
	var old string
	e = tx.QueryRow(ctx, "SELECT digest_sha256 FROM cohort_job_checkpoint WHERE job_id=$1 AND well_id=$2", j.ID, c.WellID).Scan(&old)
	if e == nil {
		if old != c.DigestSHA256 {
			return cohortError(domain.Duplicate, "checkpoint_conflict")
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if member.Input != nil {
		var n int
		if e = tx.QueryRow(ctx, "SELECT count(*) FROM cohort_job_member_attempt WHERE job_id=$1 AND well_id=$2 AND fence=$3", j.ID, c.WellID, j.Fence).Scan(&n); e != nil {
			return e
		}
		if n == 0 {
			return cohortError(domain.Invalid, "result_identity_mismatch")
		}
	}
	raw, e := json.Marshal(c)
	if e != nil {
		return e
	}
	if len(raw) > 256<<10 {
		return cohortError(domain.Limited, "cohort_result_capacity")
	}
	var total int
	if e = tx.QueryRow(ctx, "SELECT coalesce(sum(octet_length(checkpoint_json)),0) FROM cohort_job_checkpoint WHERE job_id=$1", j.ID).Scan(&total); e != nil {
		return e
	}
	if total+len(raw) > 16<<20 {
		return cohortError(domain.Limited, "cohort_result_capacity")
	}
	if _, e = execute(ctx, tx, sqlBuilder.Insert("cohort_job_checkpoint").Columns("job_id", "well_id", "fence", "digest_sha256", "checkpoint_json", "created_at").Values(j.ID, c.WellID, j.Fence, c.DigestSHA256, raw, sq.Expr("clock_timestamp()"))); e != nil {
		return e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return e
	}
	if e = cohortUpdate(ctx, tx, j, sq.Eq{}, now); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func cohortFailureCode(f *domain.CohortJobFailure) string {
	if f == nil {
		return "execution_failed"
	}
	switch f.Code {
	case "ml_unavailable", "ml_deadline", "execution_busy", "input_corrupt", "input_unavailable", "bundle_unavailable", "result_identity_mismatch", "unsupported_verdict", "rpc_incompatible", "execution_interrupted", "execution_failed", "cohort_result_capacity":
		return f.Code
	}
	return "execution_failed"
}
func (r *Repository) FinishCohortJob(ctx context.Context, l domain.CohortJobLease, failure *domain.CohortJobFailure) (domain.CohortJobDisposition, error) {
	var zero domain.CohortJobDisposition
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tx, release, e := r.beginCohortClaimTx(ctx, l)
	if e != nil {
		var de *domain.Error
		if errors.As(e, &de) && de.Message == "stale_lease" {
			return r.staleCohortDisposition(ctx, l)
		}
		return zero, e
	}
	defer release()
	defer tx.Rollback(ctx)
	if e = cohortOwnedRuntimeGate(ctx, tx); e != nil {
		return zero, e
	}
	j, m, allowed, e := r.lockCohort(ctx, tx, l.JobID)
	if e != nil {
		return zero, e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return zero, e
	}
	finish := func(kind string) (domain.CohortJobDisposition, error) {
		current, e := cohortRow(ctx, tx, j.ID, "")
		if e != nil {
			return zero, e
		}
		status, e := cohortStatus(ctx, tx, current)
		if e != nil {
			return zero, e
		}
		return domain.CohortJobDisposition{Kind: kind, Status: status}, tx.Commit(ctx)
	}
	if j.State.Terminal() {
		return finish("stale")
	}
	if state, code := cohortDenied(j, allowed, now); state != "" {
		if e = cohortTerminal(ctx, tx, j, state, code, now); e != nil {
			return zero, e
		}
		return finish("ack")
	}
	if !cohortLeaseMatches(j, l, now) {
		return finish("stale")
	}
	held, e := cohortPhysical(ctx, tx, l.PhysicalBackendPID)
	if e != nil {
		return zero, e
	}
	if !held {
		return finish("stale")
	}
	if e = validateCohortManifest(j, m); e != nil {
		return zero, e
	}
	if failure != nil {
		code := cohortFailureCode(failure)
		retry := failure.Retryable && (code == "ml_unavailable" || code == "ml_deadline" || code == "execution_busy")
		if retry {
			e = cohortRetry(ctx, tx, j, code, now)
		} else {
			e = cohortTerminal(ctx, tx, j, domain.CohortJobFailed, code, now)
		}
		if e != nil {
			return zero, e
		}
		return finish("ack")
	}
	var profile string
	var version *string
	if e = tx.QueryRow(ctx, "SELECT profile,cohort_version FROM ml_run WHERE id=$1 FOR UPDATE", j.RunID).Scan(&profile, &version); e != nil {
		return zero, e
	}
	if profile != "cohort" || version == nil || *version != j.Version {
		return zero, cohortError(domain.Invalid, "result_identity_mismatch")
	}
	rows, e := tx.Query(ctx, "SELECT checkpoint_json FROM cohort_job_checkpoint WHERE job_id=$1 ORDER BY well_id", j.ID)
	if e != nil {
		return zero, e
	}
	checks := []domain.CohortCheckpoint{}
	for rows.Next() {
		var raw []byte
		var c domain.CohortCheckpoint
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return zero, e
		}
		if json.Unmarshal(raw, &c) != nil {
			rows.Close()
			return zero, cohortError(domain.Invalid, "input_corrupt")
		}
		checks = append(checks, c)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return zero, e
	}
	if len(checks) != 100 || len(m.Members) != 100 {
		return zero, cohortError(domain.Duplicate, "cohort_incomplete")
	}
	s := domain.CohortSnapshot{Profile: "cohort", CohortVersion: j.Version, SnapshotID: &j.SnapshotID, RunID: &j.RunID, AsOf: &m.Fixture.AsOf, ProvenanceStatus: "partial", OutcomeCompleteness: "complete", ManifestSHA256: j.CohortSHA, Execution: &domain.CohortExecutionIdentity{Version: "cohort_execution_v1", SourceKind: "cohort_snapshot", CohortManifestSHA256: j.CohortSHA, InputManifestSHA256: m.InputSHA, ExecutionPinSHA256: m.PinSHA, ExecutionPin: m.Fixture.ExecutionPin, ReplayStatus: "immutable_inputs_pinned"}}
	for i, c := range checks {
		x, e := cohortExecution(ctx, tx, j, m, m.Members[i])
		if e != nil {
			return zero, e
		}
		if e = domain.ValidateCohortCheckpoint(x, c); e != nil {
			return zero, e
		}
		s.Outcomes = append(s.Outcomes, c.Outcome)
	}
	s.Recount()
	raw, e := json.Marshal(s)
	if e != nil {
		return zero, e
	}
	if len(raw) > 20<<20 {
		return zero, cohortError(domain.Limited, "cohort_snapshot_capacity")
	}
	if e = persistCohortTx(ctx, tx, j.RunID, s, 2); e != nil {
		return zero, e
	}
	if e = cohortTerminal(ctx, tx, j, domain.CohortJobSucceeded, "", now); e != nil {
		return zero, e
	}
	return finish("ack")
}
func (r *Repository) CancelCohortJob(ctx context.Context, id string, revision int64) (domain.CohortJobStatus, error) {
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
	if e = cohortGate(ctx, tx); e != nil {
		return zero, e
	}
	_, _, allowed, e := r.cohortAuthority(ctx, tx, actor, nil, nil, "cohortjobs.cancel", false)
	if e != nil {
		return zero, e
	}
	if !allowed {
		return zero, cohortError(domain.Missing, "cohort_job_not_found")
	}
	j, e := cohortRow(ctx, tx, id, "FOR UPDATE")
	if e != nil {
		return zero, e
	}
	if j.Actor != actor || !r.cohortMatches(j) {
		return zero, cohortError(domain.Missing, "cohort_job_not_found")
	}
	if j.State == domain.CohortJobCancelled {
		out, e := cohortStatus(ctx, tx, j)
		if e != nil {
			return zero, e
		}
		return out, tx.Commit(ctx)
	}
	if j.State.Terminal() {
		actual, err := cohortStatus(ctx, tx, j)
		if err != nil {
			return zero, err
		}
		if err = tx.Commit(ctx); err != nil {
			return zero, err
		}
		return actual, cohortError(domain.Duplicate, "job_terminal")
	}
	if revision != j.Revision {
		return zero, cohortError(domain.Duplicate, "revision_conflict")
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return zero, e
	}
	if j.State == domain.CohortJobRunning || j.State == domain.CohortJobCancelRequested {
		e = cohortUpdate(ctx, tx, j, sq.Eq{"state": "cancel_requested", "fence": sq.Expr("fence+1"), "error_code": "cancelled"}, now)
		if e == nil {
			e = cohortSupersede(ctx, tx, j.ID)
		}
	} else {
		e = cohortTerminal(ctx, tx, j, domain.CohortJobCancelled, "cancelled", now)
	}
	if e != nil {
		return zero, e
	}
	if e = cohortAction(ctx, tx, j, "cancel", "cancelled", actor, now); e != nil {
		return zero, e
	}
	j, e = cohortRow(ctx, tx, id, "")
	if e != nil {
		return zero, e
	}
	out, e := cohortStatus(ctx, tx, j)
	if e != nil {
		return zero, e
	}
	return out, tx.Commit(ctx)
}
func (r *Repository) ReconcileCohortJobs(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 8 {
		limit = 8
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, e := list[struct {
		ID string `json:"id"`
	}](ctx, r.pool, sqlBuilder.Select("id").From("cohort_job").Where(sq.NotEq{"state": []string{"succeeded", "failed", "cancelled", "expired", "revoked"}}).OrderBy("updated_at", "id").Limit(uint64(limit)))
	if e != nil {
		return 0, e
	}
	n := 0
	for _, row := range rows {
		if e = r.reconcileCohort(ctx, row.ID); e != nil {
			return n, e
		}
		n++
	}
	return n, nil
}
func (r *Repository) reconcileCohort(ctx context.Context, id string) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = cohortGate(ctx, tx); e != nil {
		return e
	}
	j, _, allowed, e := r.lockCohort(ctx, tx, id)
	if e != nil {
		return e
	}
	if j.State.Terminal() {
		return tx.Commit(ctx)
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return e
	}
	if state, code := cohortDenied(j, allowed, now); state != "" {
		e = cohortTerminal(ctx, tx, j, state, code, now)
	} else {
		switch j.State {
		case domain.CohortJobRunning:
			if j.Until == nil || !j.Until.After(now) {
				e = cohortRetry(ctx, tx, j, "lease_expired", now)
			}
		case domain.CohortJobRetryWait:
			if j.Next != nil && !j.Next.After(now) {
				e = cohortGeneration(ctx, tx, j, now)
			}
		case domain.CohortJobQueued:
			if now.Sub(j.Updated) >= 60*time.Second {
				e = cohortGeneration(ctx, tx, j, now)
			}
		case domain.CohortJobPendingDispatch:
			var exhausted bool
			e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM cohort_job_outbox WHERE job_id=$1 AND generation=$2 AND status='exhausted')`, j.ID, j.Generation).Scan(&exhausted)
			if e == nil && exhausted {
				e = cohortQuarantine(ctx, tx, j, "publish_exhausted", now)
			}
		}
	}
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}

var _ service.CohortJobStore = (*Repository)(nil)
var _ service.CohortJobClaim = (*cohortClaim)(nil)
