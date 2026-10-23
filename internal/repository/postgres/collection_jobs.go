package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"sync"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

type collectionJobRow struct {
	ID              string                    `json:"id"`
	Actor           int64                     `json:"actor_account_id"`
	Intent          domain.CollectionIntent   `json:"intent"`
	Request         string                    `json:"request_sha256"`
	State           domain.CollectionJobState `json:"state"`
	Revision        int64                     `json:"revision"`
	Generation      int                       `json:"generation"`
	Attempt         int                       `json:"attempt"`
	Fence           int64                     `json:"fence"`
	Token           *string                   `json:"lease_token_sha256"`
	Until           *time.Time                `json:"lease_until"`
	PID             *int32                    `json:"physical_backend_pid"`
	AttemptDeadline *time.Time                `json:"attempt_deadline"`
	Next            *time.Time                `json:"next_attempt_at"`
	Deadline        time.Time                 `json:"deadline"`
	Error           *domain.CollectionCode    `json:"error_code"`
	Availability    string                    `json:"availability"`
	Rejected        int                       `json:"rejected"`
}
type collectionClaimRegistry struct {
	mu     sync.Mutex
	claims map[string]*collectionClaim
}
type collectionClaim struct {
	gate     chan struct{}
	conn     *pgxpool.Conn
	registry *collectionClaimRegistry
	lease    domain.CollectionLease
	closed   bool
}

func (c *collectionClaim) Lease() domain.CollectionLease { return c.lease }
func (c *collectionClaim) Close() {
	c.gate <- struct{}{}
	defer func() { <-c.gate }()
	if c.closed {
		return
	}
	c.closed = true
	c.registry.mu.Lock()
	delete(c.registry.claims, c.lease.Token)
	c.registry.mu.Unlock()
	c.conn.Release()
}
func collectionRow(ctx context.Context, tx db, id, lock string) (collectionJobRow, error) {
	return one[collectionJobRow](ctx, tx, sqlBuilder.Select("*").From("collection_job").Where(sq.Eq{"id": id}).Suffix(lock))
}
func collectionTerminal(s domain.CollectionJobState) bool {
	return s != domain.CollectionPendingDispatch && s != domain.CollectionRunning && s != domain.CollectionRetryWait
}
func collectionTX(ctx context.Context, conn interface {
	Begin(context.Context) (pgx.Tx, error)
}) (pgx.Tx, error) {
	tx, e := conn.Begin(ctx)
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, "SET LOCAL lock_timeout='2s'"); e != nil {
		_ = tx.Rollback(ctx)
		return nil, e
	}
	return tx, nil
}
func collectionAction(ctx context.Context, tx pgx.Tx, j collectionJobRow, kind string, reason any) error {
	_, e := execute(ctx, tx, sqlBuilder.Insert("collection_job_action").Columns("job_id", "revision", "kind", "reason_code").Values(j.ID, j.Revision, kind, reason).Suffix("ON CONFLICT DO NOTHING"))
	return e
}
func collectionStatus(ctx context.Context, tx pgx.Tx, j collectionJobRow) (domain.CollectionJobStatus, error) {
	i := j.Intent
	s := domain.CollectionJobStatus{ID: j.ID, State: j.State, Revision: j.Revision, FixtureID: i.Fixture.ID, SourceID: i.Binding.SourceID, WellID: i.Binding.WellID, Start: i.Start, End: i.End, OriginalStart: i.OriginalStart, OriginalEnd: i.OriginalEnd, AcceptedAt: i.AcceptedAt, Deadline: j.Deadline, Attempt: j.Attempt, MaximumAttempts: 3, NextAttemptAt: j.Next, ErrorCode: j.Error, Capture: domain.CollectionCaptureStatus{State: "absent"}, Write: domain.CollectionWriteStatus{Rejected: j.Rejected}, Coverage: domain.CollectionCoverageStatus{State: "none"}, Source: domain.CollectionSourceStatus{AcceptedProfileVersion: i.SourceProfileVersion, Availability: j.Availability}}
	var rawBytes int64
	var captured time.Time
	e := tx.QueryRow(ctx, `SELECT octet_length(raw),raw_rows,unique_rows,repeated_rows,marker_values,null_values,excluded_setpoints,captured_at FROM collection_job_capture WHERE job_id=$1`, j.ID).Scan(&rawBytes, &s.Capture.RawRows, &s.Capture.UniqueRows, &s.Capture.RepeatedRows, &s.Capture.MarkerValues, &s.Capture.NullValues, &s.Capture.ExcludedSetpoints, &captured)
	if e != nil && e != pgx.ErrNoRows {
		return s, e
	}
	if e == nil {
		s.Capture.State = "ready"
		s.Capture.RawBytes = rawBytes
		s.Capture.CapturedAt = &captured
	}
	var committed time.Time
	e = tx.QueryRow(ctx, `SELECT inserted,identical,committed_at FROM collection_job_checkpoint WHERE job_id=$1`, j.ID).Scan(&s.Write.Inserted, &s.Write.Identical, &committed)
	if e != nil && e != pgx.ErrNoRows {
		return s, e
	}
	if e == nil {
		s.Write.Checkpoint = true
		s.Write.CommittedAt = &committed
	}
	e = tx.QueryRow(ctx, `SELECT state,start_at,end_at,data_revision,maximum_measured_at FROM collection_job_coverage WHERE job_id=$1`, j.ID).Scan(&s.Coverage.State, &s.Coverage.Start, &s.Coverage.End, &s.Coverage.DataRevision, &s.Coverage.MaximumMeasuredAt)
	if e != nil && e != pgx.ErrNoRows {
		return s, e
	}
	var raw []byte
	var secret string
	e = tx.QueryRow(ctx, `SELECT config,encrypted_secret FROM telemetry_source WHERE id=$1`, i.Binding.SourceID).Scan(&raw, &secret)
	if e != nil && e != pgx.ErrNoRows {
		return s, e
	}
	if e == nil {
		var source domain.TelemetrySource
		if e = json.Unmarshal(raw, &source); e != nil {
			return s, e
		}
		s.Source.CurrentUsable = source.Status == "running" && source.ProfileVersion == i.SourceProfileVersion && collectionSourceHash(source) == i.SourceConfigSHA256 && secret == ""
	}
	return s, nil
}
func collectionReadAuthority(ctx context.Context, tx pgx.Tx, j collectionJobRow, permission domain.Permission) error {
	actor, e := cohortSession(ctx)
	if e != nil {
		return e
	}
	if actor != j.Actor {
		return collectionError(domain.Missing, "collection_job_missing")
	}
	auth, e := jobAuthority(ctx, tx, actor, nil, permission, false)
	if e != nil {
		return e
	}

	w, e := collectionWell(ctx, tx, j.Intent.Binding.WellID)
	if e != nil {
		return e
	}
	if !collectionAllows(auth, w) {
		return collectionError(domain.Forbidden, "collection_access_denied")
	}
	return nil
}
func (r *Repository) CollectionJobStatus(ctx context.Context, id string) (domain.CollectionJobStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var zero domain.CollectionJobStatus
	tx, e := collectionTX(ctx, r.pool)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	j, e := collectionRow(ctx, tx, id, "")
	if isMissing(e) {
		return zero, collectionError(domain.Missing, "collection_job_missing")
	}
	if e != nil {
		return zero, e
	}
	if e = collectionReadAuthority(ctx, tx, j, "collectionjobs.read"); e != nil {
		return zero, e
	}
	j, e = collectionRow(ctx, tx, id, "FOR SHARE")
	if e != nil {
		return zero, e
	}
	s, e := collectionStatus(ctx, tx, j)
	if e != nil {
		return zero, e
	}
	return s, tx.Commit(ctx)
}
func (r *Repository) AcceptCollectionJob(ctx context.Context, in domain.CollectionSubmit) (domain.CollectionJobStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var zero domain.CollectionJobStatus
	actor, e := cohortSession(ctx)
	if e != nil {
		return zero, e
	}
	if r.collectionJobPolicy == nil {
		return zero, collectionError(domain.Unavailable, "collection_jobs_disabled")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`).MatchString(in.ReplayKey) {
		return zero, collectionError(domain.Invalid, "invalid_collection_request")
	}
	tx, e := collectionTX(ctx, r.pool)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	count, _, e := r.collectionGuard(ctx, tx, false)
	if e != nil {
		return zero, e
	}
	// Replay is resolved from the original accepted descriptor, before current source validation.
	old, e := one[collectionJobRow](ctx, tx, sqlBuilder.Select("*").From("collection_job").Where(sq.Eq{"actor_account_id": actor, "replay_key_sha256": telemetryTokenSHA(in.ReplayKey)}))
	if e == nil {
		if e = collectionReadAuthority(ctx, tx, old, "collectionjobs.run"); e != nil {
			return zero, e
		}
		start, end, err := domain.NormalizeCollectionPeriod(in.Start, in.End, old.Intent.Fixture)
		if err != nil {
			return zero, err
		}
		sha, err := domain.CollectionRequestSHA256(in, start, end)
		if err != nil {
			return zero, err
		}
		if sha != old.Request {
			return zero, collectionError(domain.Duplicate, "replay_conflict")
		}
		s, err := collectionStatus(ctx, tx, old)
		if err != nil {
			return zero, err
		}
		return s, tx.Commit(ctx)
	}
	if !isMissing(e) {
		return zero, e
	}
	if _, _, e = r.collectionGuard(ctx, tx, true); e != nil {
		return zero, e
	}
	p := r.collectionJobPolicy
	var fixture domain.CollectionFixture
	var binding domain.CollectionBinding
	for _, f := range p.Fixtures {
		if f.ID == in.FixtureID {
			fixture = f
		}
	}
	for _, b := range p.Bindings {
		if b.SourceID == in.SourceID {
			binding = b
		}
	}
	if fixture.ID == "" {
		return zero, collectionError(domain.Invalid, "fixture_unavailable")
	}
	if binding.SourceID == "" {
		return zero, collectionError(domain.Invalid, "source_not_allowed")
	}
	start, end, e := domain.NormalizeCollectionPeriod(in.Start, in.End, fixture)
	if e != nil {
		return zero, e
	}
	auth, e := jobAuthority(ctx, tx, actor, nil, "collectionjobs.run", false)
	if e != nil {
		return zero, e
	}
	a, e := collectionAccount(ctx, tx, actor)
	if e != nil {
		return zero, e
	}
	w, e := collectionWell(ctx, tx, binding.WellID)
	if e != nil {
		return zero, e
	}
	source, until, e := collectionSource(ctx, tx, in.SourceID)
	if e != nil {
		return zero, e
	}
	if !slices.Contains(p.AllowedAccountIDs, actor) || !collectionAllows(auth, w) {
		return zero, collectionError(domain.Forbidden, "collection_access_denied")
	}
	view, ok := collectionWellView(w, binding)
	if !ok || view.ExternalID != binding.ExternalID || source.WellIdentity != binding.IdentityKind || source.Kind != "json_pull" || source.Status != "running" || source.EncryptedSecret != "" || source.Endpoint != p.FixtureOrigin+"/v1/collection-capture" || !equalJobJSON(source.Mappings, fixture.Mappings) {
		return zero, collectionError(domain.Invalid, "source_not_allowed")
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return zero, e
	}
	var held bool
	if e = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM collection_job_source_hold WHERE source_id=$1)", in.SourceID).Scan(&held); e != nil {
		return zero, e
	}
	if held || until.After(now) {
		return zero, collectionError(domain.Duplicate, "source_busy")
	}
	var active int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM collection_job WHERE state IN ('pending_dispatch','running','retry_wait')`).Scan(&active); e != nil {
		return zero, e
	}
	if count >= 32 || active >= 2 {
		return zero, collectionError(domain.Limited, "collection_capacity")
	}
	id, e := fileJobID()
	if e != nil {
		return zero, e
	}
	request, e := domain.CollectionRequestSHA256(in, start, end)
	if e != nil {
		return zero, e
	}
	snapshotAuth, e := admissionAuthorization(ctx, tx, actor, "collectionjobs.run")
	if e != nil {
		return zero, e
	}
	intent := domain.CollectionIntent{Authorization: snapshotAuth, JobID: id, ActorAccountID: actor, ActorRevision: a.Revision, ScopeSHA256: collectionHash(a.Scope), RunID: p.RunID, ManifestSHA256: p.ManifestSHA256, Fixture: fixture, Binding: binding, Well: view, SourceProfileVersion: source.ProfileVersion, SourceConfigSHA256: collectionSourceHash(source), MappingSHA256: collectionHash(source.Mappings), BindingSHA256: collectionHash(binding), CanonicalVersion: "collection-canonical-v1", SchemaVersion: "collection-measurement-v1", FixtureOrigin: p.FixtureOrigin, OriginalStart: in.Start, OriginalEnd: in.End, Start: start, End: end, AcceptedAt: now, Deadline: now.Add(300 * time.Second), RequestSHA256: request}
	raw, e := json.Marshal(intent)
	if len(raw) > 65536 {
		return zero, collectionError(domain.Invalid, "collection_intent_too_large")
	}
	if e != nil {
		return zero, e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Insert("collection_job").Columns("id", "actor_account_id", "replay_key_sha256", "request_sha256", "intent", "state", "deadline", "accepted_at").Values(id, actor, telemetryTokenSHA(in.ReplayKey), request, raw, "pending_dispatch", intent.Deadline, now)); e != nil {
		return zero, e
	}
	var generation int64
	if e = tx.QueryRow(ctx, `UPDATE telemetry_source SET writer_generation=writer_generation+1,writer_token_sha256=NULL,writer_profile_version=NULL,writer_execution_actor=NULL,writer_authorization=NULL WHERE id=$1 RETURNING writer_generation`, in.SourceID).Scan(&generation); e != nil {
		return zero, e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Insert("collection_job_source_hold").Columns("source_id", "job_id", "source_generation").Values(in.SourceID, id, generation)); e != nil {
		return zero, e
	}
	if _, e = tx.Exec(ctx, "UPDATE collection_job_guard SET accepted_jobs=accepted_jobs+1 WHERE id=true"); e != nil {
		return zero, e
	}
	if e = collectionOutbox(ctx, tx, id, 1, now); e != nil {
		return zero, e
	}
	j, e := collectionRow(ctx, tx, id, "")
	if e != nil {
		return zero, e
	}
	if e = collectionAction(ctx, tx, j, "accepted", nil); e != nil {
		return zero, e
	}
	s, e := collectionStatus(ctx, tx, j)
	if e != nil {
		return zero, e
	}
	return s, tx.Commit(ctx)
}
func (r *Repository) ClaimCollectionJob(ctx context.Context, m domain.CollectionMessage, worker string) (service.CollectionJobClaim, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if worker == "" || m.Version != 1 || m.Kind != "collection.ready.v1" || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(m.JobID) || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(m.EventID) || m.Generation < 1 || m.Generation > 8 {
		return nil, collectionError(domain.Invalid, "invalid_collection_request")
	}
	conn, e := r.pool.Acquire(ctx)
	if e != nil {
		return nil, e
	}
	keep := false
	defer func() {
		if !keep {
			conn.Release()
		}
	}()
	tx, e := collectionTX(ctx, conn)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	locator, e := collectionRow(ctx, tx, m.JobID, "")
	if e != nil {
		return nil, e
	}
	_, _, guardErr := r.collectionGuard(ctx, tx, false)
	if guardErr != nil {
		var de *domain.Error
		if !errors.As(guardErr, &de) || de.Kind != domain.Forbidden {
			return nil, guardErr
		}
	}
	reason, e := r.collectionAuthority(ctx, tx, locator.Intent)
	if guardErr != nil {
		reason = "access_revoked"
	}
	if e != nil {
		return nil, e
	}
	j, e := collectionRow(ctx, tx, m.JobID, "FOR UPDATE")
	if e != nil {
		return nil, e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return nil, e
	}
	var eventExists bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM collection_job_outbox WHERE event_id=$1 AND job_id=$2 AND generation=$3)`, m.EventID, m.JobID, m.Generation).Scan(&eventExists); e != nil {
		return nil, e
	}
	if !eventExists {
		return nil, collectionError(domain.Invalid, "invalid_collection_request")
	}
	if collectionTerminal(j.State) || m.Generation != j.Generation {
		if e = collectionInbox(ctx, tx, m, j.Fence, "ignored"); e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	if reason != "" {
		if e = collectionFinish(ctx, tx, &j, "revoked", reason, "terminal"); e != nil {
			return nil, e
		}
		if e = collectionInbox(ctx, tx, m, j.Fence, "terminal"); e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	if j.State == domain.CollectionRunning || j.Next != nil && j.Next.After(now) {
		return nil, collectionError(domain.Duplicate, "stale_lease")
	}
	if !j.Deadline.After(now) || j.Attempt >= 3 {
		code := "job_deadline"
		if j.Attempt >= 3 {
			code = "attempts_exhausted"
		}
		if e = collectionFinish(ctx, tx, &j, "expired", code, "terminal"); e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	token, e := fileJobID()
	if e != nil {
		return nil, e
	}
	until := now.Add(30 * time.Second)
	attemptDeadline := now.Add(60 * time.Second)
	if attemptDeadline.After(j.Deadline) {
		attemptDeadline = j.Deadline
	}
	pid := conn.Conn().PgConn().PID()
	_, e = execute(ctx, tx, sqlBuilder.Update("collection_job").Set("state", "running").Set("revision", sq.Expr("revision+1")).Set("attempt", j.Attempt+1).Set("fence", j.Fence+1).Set("lease_token_sha256", telemetryTokenSHA(token)).Set("lease_until", until).Set("physical_backend_pid", pid).Set("attempt_deadline", attemptDeadline).Set("next_attempt_at", nil).Set("updated_at", now).Where(sq.Eq{"id": j.ID}))
	if e != nil {
		return nil, e
	}
	j, e = collectionRow(ctx, tx, j.ID, "")
	if e != nil {
		return nil, e
	}
	if e = collectionAction(ctx, tx, j, "claimed", nil); e != nil {
		return nil, e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Insert("collection_job_inbox").Columns("event_id", "job_id", "generation", "fence", "state").Values(m.EventID, m.JobID, m.Generation, j.Fence, "processing").Suffix("ON CONFLICT (event_id) DO UPDATE SET fence=EXCLUDED.fence,state='processing',outcome=NULL,updated_at=clock_timestamp()")); e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	c := &collectionClaim{gate: make(chan struct{}, 1), conn: conn, registry: r.collectionClaims, lease: domain.CollectionLease{JobID: j.ID, Token: token, Fence: j.Fence, Generation: j.Generation, Attempt: j.Attempt, PhysicalBackendPID: int32(pid), Until: until, AttemptDeadline: attemptDeadline}}
	r.collectionClaims.mu.Lock()
	r.collectionClaims.claims[token] = c
	r.collectionClaims.mu.Unlock()
	keep = true
	return c, nil
}
func (r *Repository) collectionOwned(ctx context.Context, l domain.CollectionLease, allowTerminal bool, fn func(pgx.Tx, *collectionJobRow) error) error {
	if r.collectionClaims == nil {
		return collectionError(domain.Duplicate, "stale_lease")
	}
	r.collectionClaims.mu.Lock()
	c := r.collectionClaims.claims[l.Token]
	r.collectionClaims.mu.Unlock()
	if c == nil {
		return collectionError(domain.Duplicate, "stale_lease")
	}
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.gate }()
	if c.closed || c.conn.Conn().IsClosed() || c.lease.JobID != l.JobID || c.lease.Fence != l.Fence || c.conn.Conn().PgConn().PID() != uint32(l.PhysicalBackendPID) {
		return collectionError(domain.Duplicate, "stale_lease")
	}
	tx, e := collectionTX(ctx, c.conn)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	locator, e := collectionRow(ctx, tx, l.JobID, "")
	if e != nil {
		return e
	}
	_, _, guardErr := r.collectionGuard(ctx, tx, false)
	var reason string
	if guardErr != nil {
		var de *domain.Error
		if !errors.As(guardErr, &de) || de.Kind != domain.Forbidden {
			return guardErr
		}
		reason = "access_revoked"
	}
	authReason, e := r.collectionAuthority(ctx, tx, locator.Intent)
	if e != nil {
		return e
	}
	if reason == "" {
		reason = authReason
	}
	j, e := collectionRow(ctx, tx, l.JobID, "FOR UPDATE")
	if e != nil {
		return e
	}
	if allowTerminal && collectionTerminal(j.State) {
		return fn(tx, &j)
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return e
	}
	if j.State != domain.CollectionRunning || j.Token == nil || *j.Token != telemetryTokenSHA(l.Token) || j.Fence != l.Fence || j.Generation != l.Generation || j.PID == nil || *j.PID != l.PhysicalBackendPID || j.Until == nil || !j.Until.After(now) || j.AttemptDeadline == nil || !j.AttemptDeadline.After(now) || !j.Deadline.After(now) {
		return collectionError(domain.Duplicate, "stale_lease")
	}
	if reason != "" {
		if e = collectionFinish(ctx, tx, &j, "revoked", reason, "terminal"); e != nil {
			return e
		}
		if e = tx.Commit(ctx); e != nil {
			return e
		}
		return collectionError(domain.Forbidden, reason)
	}
	ownedUntil, ownedAttemptDeadline, ownedDeadline := j.Until, j.AttemptDeadline, j.Deadline
	if e = fn(tx, &j); e != nil {
		return e
	}
	// Re-read the database clock after all SQL, never trust the diagnostic caller Until.
	var stillValid bool
	e = tx.QueryRow(ctx, `SELECT CASE WHEN state='running' THEN lease_until>clock_timestamp() AND attempt_deadline>clock_timestamp() AND deadline>clock_timestamp() ELSE $2::timestamptz>clock_timestamp() AND $3::timestamptz>clock_timestamp() AND $4::timestamptz>clock_timestamp() END FROM collection_job WHERE id=$1`, j.ID, ownedUntil, ownedAttemptDeadline, ownedDeadline).Scan(&stillValid)
	if e != nil {
		return e
	}
	if !stillValid {
		return collectionError(domain.Duplicate, "stale_lease")
	}
	return tx.Commit(ctx)
}
