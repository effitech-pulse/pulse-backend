package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func collectionFinish(ctx context.Context, tx pgx.Tx, j *collectionJobRow, state, reason, outcome string) error {
	var code any
	if reason != "" {
		code = reason
	}
	_, e := execute(ctx, tx, sqlBuilder.Update("collection_job").Set("state", state).Set("revision", sq.Expr("revision+1")).Set("fence", sq.Expr("fence+1")).Set("error_code", code).Set("lease_token_sha256", nil).Set("lease_until", nil).Set("physical_backend_pid", nil).Set("attempt_deadline", nil).Set("next_attempt_at", nil).Set("finished_at", sq.Expr("clock_timestamp()")).Set("updated_at", sq.Expr("clock_timestamp()")).Where(sq.Eq{"id": j.ID}))
	if e != nil {
		return e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Delete("collection_job_source_hold").Where(sq.Eq{"job_id": j.ID, "source_id": j.Intent.Binding.SourceID})); e != nil {
		return e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Update("collection_job_inbox").Set("state", "completed").Set("outcome", outcome).Set("updated_at", sq.Expr("clock_timestamp()")).Where(sq.Eq{"job_id": j.ID, "state": "processing"})); e != nil {
		return e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Update("collection_job_outbox").Set("state", "superseded").Set("claim_token_sha256", nil).Set("claim_until", nil).Where(sq.Eq{"job_id": j.ID})); e != nil {
		return e
	}
	*j, e = collectionRow(ctx, tx, j.ID, "")
	if e != nil {
		return e
	}
	return collectionAction(ctx, tx, *j, state, code)
}
func (r *Repository) CancelCollectionJob(ctx context.Context, id string, revision int64) (domain.CollectionJobStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var zero domain.CollectionJobStatus
	tx, e := collectionTX(ctx, r.pool)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	locator, e := collectionRow(ctx, tx, id, "")
	if isMissing(e) {
		return zero, collectionError(domain.Missing, "collection_job_missing")
	}
	if e != nil {
		return zero, e
	}
	if _, _, e = r.collectionGuard(ctx, tx, false); e != nil {
		return zero, e
	}
	if e = collectionReadAuthority(ctx, tx, locator); e != nil {
		return zero, e
	}
	if _, _, e = collectionSource(ctx, tx, locator.Intent.Binding.SourceID); e != nil {
		return zero, e
	}
	j, e := collectionRow(ctx, tx, id, "FOR UPDATE")
	if e != nil {
		return zero, e
	}
	if !collectionTerminal(j.State) {
		if j.Revision != revision {
			return zero, collectionError(domain.Duplicate, "revision_conflict")
		}
		if e = collectionFinish(ctx, tx, &j, "cancelled", "cancelled", "terminal"); e != nil {
			return zero, e
		}
	}
	s, e := collectionStatus(ctx, tx, j)
	if e != nil {
		return zero, e
	}
	return s, tx.Commit(ctx)
}
func collectionCapture(ctx context.Context, tx pgx.Tx, id string) (*domain.CollectionPreparedCapture, error) {
	c := &domain.CollectionPreparedCapture{}
	var provenance []byte
	e := tx.QueryRow(ctx, `SELECT raw,raw_sha256,canonical,canonical_sha256,provenance,received_at,raw_rows,unique_rows,repeated_rows,marker_values,null_values,excluded_setpoints,empty FROM collection_job_capture WHERE job_id=$1`, id).Scan(&c.Raw, &c.RawSHA256, &c.Canonical, &c.CanonicalSHA256, &provenance, &c.ReceivedAt, &c.RawRows, &c.UniqueRows, &c.RepeatedRows, &c.MarkerValues, &c.NullValues, &c.ExcludedSetpoints, &c.Empty)
	if e == pgx.ErrNoRows {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	if json.Unmarshal(provenance, &c.Provenance) != nil || telemetryTokenSHA(string(c.Raw)) != c.RawSHA256 || telemetryTokenSHA(string(c.Canonical)) != c.CanonicalSHA256 {
		return nil, collectionError(domain.Invalid, "capture_corrupt")
	}
	return c, nil
}
func (r *Repository) LoadCollectionWork(ctx context.Context, l domain.CollectionLease) (out domain.CollectionWork, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = r.collectionOwned(ctx, l, false, func(tx pgx.Tx, j *collectionJobRow) error {
		out.Intent = j.Intent
		var e error
		out.Capture, e = collectionCapture(ctx, tx, j.ID)
		return e
	})
	return
}
func (r *Repository) SaveCollectionCapture(ctx context.Context, l domain.CollectionLease, c domain.CollectionPreparedCapture) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.collectionOwned(ctx, l, false, func(tx pgx.Tx, j *collectionJobRow) error {
		verified, e := domain.PrepareCollectionCapture(j.Intent, domain.CollectionRawCapture{Body: c.Raw, ReceivedAt: c.ReceivedAt})
		if e != nil {
			return e
		}
		if !bytes.Equal(verified.Canonical, c.Canonical) || verified.CanonicalSHA256 != c.CanonicalSHA256 || verified.RawSHA256 != c.RawSHA256 || !equalJobJSON(verified, c) {
			return collectionError(domain.Invalid, "capture_corrupt")
		}
		old, e := collectionCapture(ctx, tx, j.ID)
		if e != nil {
			return e
		}
		if old != nil {
			if bytes.Equal(old.Raw, c.Raw) && bytes.Equal(old.Canonical, c.Canonical) {
				return nil
			}
			return collectionError(domain.Duplicate, "input_conflict")
		}
		provenance, e := json.Marshal(c.Provenance)
		if e != nil {
			return e
		}
		_, e = execute(ctx, tx, sqlBuilder.Insert("collection_job_capture").Columns("job_id", "raw", "raw_sha256", "canonical", "canonical_sha256", "provenance", "received_at", "raw_rows", "unique_rows", "repeated_rows", "marker_values", "null_values", "excluded_setpoints", "empty").Values(j.ID, c.Raw, c.RawSHA256, c.Canonical, c.CanonicalSHA256, provenance, c.ReceivedAt, c.RawRows, c.UniqueRows, c.RepeatedRows, c.MarkerValues, c.NullValues, c.ExcludedSetpoints, c.Empty))
		if e != nil {
			return e
		}
		availability := "dense"
		if c.Empty {
			availability = "explicit_empty"
		}
		if _, e = execute(ctx, tx, sqlBuilder.Update("collection_job").Set("revision", sq.Expr("revision+1")).Set("availability", availability).Set("updated_at", sq.Expr("clock_timestamp()")).Where(sq.Eq{"id": j.ID})); e != nil {
			return e
		}
		*j, e = collectionRow(ctx, tx, j.ID, "")
		if e != nil {
			return e
		}
		return collectionAction(ctx, tx, *j, "captured", nil)
	})
}
func (r *Repository) RenewCollectionJob(ctx context.Context, l domain.CollectionLease) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.collectionOwned(ctx, l, false, func(tx pgx.Tx, j *collectionJobRow) error {
		_, e := execute(ctx, tx, sqlBuilder.Update("collection_job").Set("lease_until", sq.Expr("LEAST(clock_timestamp()+interval '30 seconds',attempt_deadline,deadline)")).Set("updated_at", sq.Expr("clock_timestamp()")).Where(sq.Eq{"id": j.ID}))
		return e
	})
}
func (r *Repository) ApplyCollectionCapture(ctx context.Context, l domain.CollectionLease) (out domain.CollectionDisposition, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err = r.collectionOwned(ctx, l, true, func(tx pgx.Tx, j *collectionJobRow) error {
		if collectionTerminal(j.State) {
			out.Kind = "terminal"
			var e error
			out.Status, e = collectionStatus(ctx, tx, *j)
			return e
		}
		c, e := collectionCapture(ctx, tx, j.ID)
		if e != nil {
			return e
		}
		if c == nil {
			return collectionError(domain.Invalid, "invalid_capture")
		}
		var canonical struct {
			Version    string                      `json:"version"`
			Provenance domain.CollectionProvenance `json:"provenance"`
			Points     []domain.Measurement        `json:"points"`
		}
		if json.Unmarshal(c.Canonical, &canonical) != nil || canonical.Version != "collection-canonical-v1" || canonical.Provenance != c.Provenance || len(canonical.Points) != c.UniqueRows {
			return collectionError(domain.Invalid, "capture_corrupt")
		}
		// Savepoint allows an incompatible row to record a failed job without publishing any rows.
		nested, e := tx.Begin(ctx)
		if e != nil {
			return e
		}
		inserted, identical, e := insertTelemetryRows(ctx, nested, j.Intent.Binding.SourceID, canonical.Points, &c.Provenance)
		if e != nil {
			_ = nested.Rollback(ctx)
			var de *domain.Error
			if !errors.As(e, &de) || de.Kind != domain.Duplicate {
				return e
			}
			if _, e = execute(ctx, tx, sqlBuilder.Update("collection_job").Set("rejected", c.UniqueRows).Set("availability", "conflict").Where(sq.Eq{"id": j.ID})); e != nil {
				return e
			}
			if e = collectionFinish(ctx, tx, j, "failed", de.Message, "terminal"); e != nil {
				return e
			}
			out.Kind = "terminal"
			out.Status, e = collectionStatus(ctx, tx, *j)
			return e
		}
		if e = nested.Commit(ctx); e != nil {
			return e
		}
		now, e := dbJobNow(ctx, tx)
		if e != nil {
			return e
		}
		if j.Until == nil || !j.Until.After(now) || !j.AttemptDeadline.After(now) || !j.Deadline.After(now) {
			return collectionError(domain.Duplicate, "stale_lease")
		}
		if _, e = execute(ctx, tx, sqlBuilder.Insert("collection_job_checkpoint").Columns("job_id", "capture_sha256", "inserted", "identical").Values(j.ID, c.CanonicalSHA256, inserted, identical)); e != nil {
			return e
		}
		var maximum *time.Time
		for _, p := range canonical.Points {
			if maximum == nil || p.Timestamp.After(*maximum) {
				v := p.Timestamp
				maximum = &v
			}
		}
		state := "complete"
		if c.Empty {
			state = "complete_empty"
		}
		if _, e = execute(ctx, tx, sqlBuilder.Insert("collection_job_coverage").Columns("job_id", "source_id", "binding_sha256", "data_revision", "start_at", "end_at", "state", "maximum_measured_at").Values(j.ID, j.Intent.Binding.SourceID, j.Intent.BindingSHA256, c.Provenance.DataRevision, j.Intent.Start, j.Intent.End, state, maximum)); e != nil {
			return e
		}
		if _, e = execute(ctx, tx, sqlBuilder.Insert("telemetry_collection_log").Columns("source_id", "accepted", "duplicates", "rejected", "message").Values(j.Intent.Binding.SourceID, inserted, identical, 0, "collection_committed")); e != nil {
			return e
		}
		if _, e = execute(ctx, tx, sqlBuilder.Update("telemetry_source").Set("last_received", now).Set("error", "").Where(sq.Eq{"id": j.Intent.Binding.SourceID})); e != nil {
			return e
		}
		if e = collectionFinish(ctx, tx, j, "succeeded", "", "applied"); e != nil {
			return e
		}
		out.Kind = "applied"
		out.Status, e = collectionStatus(ctx, tx, *j)
		return e
	})
	return
}
func collectionRetry(ctx context.Context, tx pgx.Tx, j *collectionJobRow, code string) error {
	delay := 5 * time.Second
	if j.Attempt >= 2 {
		delay = 15 * time.Second
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return e
	}
	if j.Attempt >= 3 {
		return collectionFinish(ctx, tx, j, "failed", "attempts_exhausted", "terminal")
	}
	if j.Generation >= 8 {
		return collectionFinish(ctx, tx, j, "quarantined", "dispatch_exhausted", "terminal")
	}
	if !j.Deadline.After(now.Add(delay)) {
		return collectionFinish(ctx, tx, j, "expired", "job_deadline", "terminal")
	}
	_, e = execute(ctx, tx, sqlBuilder.Update("collection_job").Set("state", "retry_wait").Set("revision", sq.Expr("revision+1")).Set("fence", sq.Expr("fence+1")).Set("generation", j.Generation+1).Set("error_code", code).Set("lease_token_sha256", nil).Set("lease_until", nil).Set("physical_backend_pid", nil).Set("attempt_deadline", nil).Set("next_attempt_at", now.Add(delay)).Set("updated_at", now).Where(sq.Eq{"id": j.ID}))
	if e != nil {
		return e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Update("collection_job_inbox").Set("state", "completed").Set("outcome", "retry").Set("updated_at", now).Where(sq.Eq{"job_id": j.ID, "state": "processing"})); e != nil {
		return e
	}
	if _, e = execute(ctx, tx, sqlBuilder.Update("collection_job_outbox").Set("state", "superseded").Where(sq.Eq{"job_id": j.ID})); e != nil {
		return e
	}
	*j, e = collectionRow(ctx, tx, j.ID, "")
	if e != nil {
		return e
	}
	if e = collectionOutbox(ctx, tx, j.ID, j.Generation, now.Add(delay)); e != nil {
		return e
	}
	return collectionAction(ctx, tx, *j, "retry", code)
}
func (r *Repository) FinishCollectionFailure(ctx context.Context, l domain.CollectionLease, f domain.CollectionFailure) (out domain.CollectionDisposition, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = r.collectionOwned(ctx, l, false, func(tx pgx.Tx, j *collectionJobRow) error {
		var e error
		if f.Retryable && (f.Code == domain.CollectionCodeSourceUnavailable || f.Code == domain.CollectionCodeSourceTimeout) {
			// Только полученный результат source I/O подтверждает unavailable; expiry worker этого не доказывает.
			if _, e = execute(ctx, tx, sqlBuilder.Update("collection_job").Set("availability", "unavailable").Where(sq.Eq{"id": j.ID})); e != nil {
				return e
			}
			e = collectionRetry(ctx, tx, j, string(f.Code))
			out.Kind = "retry"
		} else {
			availability := "invalid"
			if f.Code == domain.CollectionCodeSourceUnavailable || f.Code == domain.CollectionCodeSourceTimeout {
				availability = "unavailable"
			}
			if _, e = execute(ctx, tx, sqlBuilder.Update("collection_job").Set("availability", availability).Where(sq.Eq{"id": j.ID})); e != nil {
				return e
			}
			e = collectionFinish(ctx, tx, j, "failed", string(f.Code), "terminal")
			out.Kind = "terminal"
		}
		if e != nil {
			return e
		}
		if collectionTerminal(j.State) {
			out.Kind = "terminal"
		}
		out.Status, e = collectionStatus(ctx, tx, *j)
		return e
	})
	return
}
func (r *Repository) ReconcileCollectionJobs(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 8 {
		return 0, collectionError(domain.Invalid, "invalid_collection_request")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	candidates, e := list[collectionJobRow](ctx, r.pool, sqlBuilder.Select("*").From("collection_job").Where("state IN ('pending_dispatch','running','retry_wait')").OrderBy("accepted_at").Limit(uint64(limit)))
	if e != nil {
		return 0, e
	}
	n := 0
	for _, candidate := range candidates {
		tx, e := collectionTX(ctx, r.pool)
		if e != nil {
			return n, e
		}
		e = func() error {
			_, _, guardErr := r.collectionGuard(ctx, tx, false)
			if guardErr != nil {
				var de *domain.Error
				if !errors.As(guardErr, &de) || de.Kind != domain.Forbidden {
					return guardErr
				}
			}
			reason, e := r.collectionAuthority(ctx, tx, candidate.Intent)
			if guardErr != nil {
				reason = "access_revoked"
			}
			if e != nil {
				return e
			}
			j, e := collectionRow(ctx, tx, candidate.ID, "FOR UPDATE")
			if e != nil {
				return e
			}
			if collectionTerminal(j.State) {
				return nil
			}
			now, e := dbJobNow(ctx, tx)
			if e != nil {
				return e
			}
			if reason != "" {
				return collectionFinish(ctx, tx, &j, "revoked", reason, "terminal")
			}
			if !j.Deadline.After(now) {
				return collectionFinish(ctx, tx, &j, "expired", "job_deadline", "terminal")
			}
			if j.State == domain.CollectionRunning {
				if j.Until != nil && j.Until.After(now) && j.AttemptDeadline != nil && j.AttemptDeadline.After(now) {
					return nil
				}
				return collectionRetry(ctx, tx, &j, "source_timeout")
			}
			// Confirmed wakeups may be lost after broker restart; a bounded new generation redrives them.
			var confirmed bool
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM collection_job_outbox WHERE job_id=$1 AND generation=$2 AND state='confirmed' AND updated_at<clock_timestamp()-interval '5 seconds')`, j.ID, j.Generation).Scan(&confirmed); e != nil {
				return e
			}
			if confirmed {
				if j.Generation >= 8 {
					return collectionFinish(ctx, tx, &j, "quarantined", "dispatch_exhausted", "terminal")
				}
				if _, e = execute(ctx, tx, sqlBuilder.Update("collection_job_outbox").Set("state", "superseded").Where(sq.Eq{"job_id": j.ID})); e != nil {
					return e
				}
				if _, e = execute(ctx, tx, sqlBuilder.Update("collection_job").Set("generation", j.Generation+1).Set("revision", sq.Expr("revision+1")).Where(sq.Eq{"id": j.ID})); e != nil {
					return e
				}
				j.Generation++
			}
			due := now
			if j.Next != nil {
				due = *j.Next
			}
			return collectionOutbox(ctx, tx, j.ID, j.Generation, due)
		}()
		if e == nil {
			e = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(ctx)
		}
		if e != nil {
			return n, e
		}
		n++
	}
	return n, nil
}
