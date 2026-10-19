package postgres

import (
	"context"
	"encoding/json"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/observability"
)

func scanSource(row pgx.Row) (domain.TelemetrySource, error) {
	var s domain.TelemetrySource
	var raw []byte
	var secret, message string
	var received, measured *time.Time
	err := row.Scan(&raw, &secret, &received, &measured, &message)
	if err == pgx.ErrNoRows {
		return s, domain.NewError(domain.Missing, "Источник не найден")
	}
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	// JSON-декодер переиспользует ненулевые указатели. Динамические времена
	// сканируются отдельно, чтобы старый снимок профиля не изменил watermark.
	s.EncryptedSecret = secret
	s.LastReceived = received
	s.LastMeasured = measured
	s.Error = message
	if s.Error != "" && s.Status == "running" {
		s.Status = "error"
	}
	return s, nil
}
func (r *Repository) Source(ctx context.Context, id string) (domain.TelemetrySource, error) {
	q, args, err := build(sqlBuilder.Select("config", "encrypted_secret", "last_received", "last_measured", "error").From("telemetry_source").Where(sq.Eq{"id": id}))
	if err != nil {
		return domain.TelemetrySource{}, err
	}
	return scanSource(r.pool.QueryRow(ctx, q, args...))
}
func (r *Repository) Sources(ctx context.Context) ([]domain.TelemetrySource, error) {
	q, args, err := build(sqlBuilder.Select("config", "encrypted_secret", "last_received", "last_measured", "error").From("telemetry_source").OrderBy("id"))
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.TelemetrySource{}
	for rows.Next() {
		s, e := scanSource(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
func (r *Repository) SaveSource(ctx context.Context, s domain.TelemetrySource) (domain.TelemetrySource, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return s, err
	}
	defer tx.Rollback(ctx)
	q, args, err := build(sqlBuilder.Select("config").From("telemetry_source").Where(sq.Eq{"id": s.ID}).Suffix("FOR UPDATE"))
	if err != nil {
		return s, err
	}
	var oldData []byte
	err = tx.QueryRow(ctx, q, args...).Scan(&oldData)
	if err != nil && err != pgx.ErrNoRows {
		return s, err
	}
	var old domain.TelemetrySource
	if len(oldData) > 0 {
		if err = json.Unmarshal(oldData, &old); err != nil {
			return s, err
		}
	}
	s.ProfileVersion = old.ProfileVersion + 1
	data, err := json.Marshal(s)
	if err != nil {
		return s, err
	}
	_, err = execute(ctx, tx, sqlBuilder.Insert("telemetry_source").Columns("id", "config", "encrypted_secret").Values(s.ID, data, s.EncryptedSecret).Suffix("ON CONFLICT (id) DO UPDATE SET config=EXCLUDED.config, encrypted_secret=EXCLUDED.encrypted_secret, error='', next_poll=now()"))
	if err != nil {
		return s, err
	}
	if _, err = execute(ctx, tx, sqlBuilder.Insert("telemetry_source_profile").Columns("source_id", "version", "config").Values(s.ID, s.ProfileVersion, data)); err != nil {
		return s, err
	}
	if err = tx.Commit(ctx); err != nil {
		return s, err
	}
	return r.Source(ctx, s.ID)
}
func (r *Repository) ClaimSource(ctx context.Context, id string, due bool) (bool, error) {
	q := sqlBuilder.Update("telemetry_source").Set("lease_until", sq.Expr("now()+interval '60 seconds'")).Where(sq.Eq{"id": id}).Where("config->>'status' = 'running'").Where("lease_until < now()")
	if due {
		q = q.Where("next_poll <= now()")
	}
	tag, err := execute(ctx, r.pool, q)
	return tag.RowsAffected() == 1, err
}
func (r *Repository) CommitTelemetry(ctx context.Context, id string, points []domain.Measurement, rejected int, message string, release bool) (log domain.CollectionLog, err error) {
	ctx, finish := observability.Database(ctx, "telemetry.save")
	defer func() { finish(err) }()
	log = domain.CollectionLog{SourceID: id, Timestamp: time.Now().UTC(), Rejected: rejected, Message: message}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return log, err
	}
	defer tx.Rollback(ctx)
	batch := &pgx.Batch{}
	var latest *time.Time
	for _, p := range points {
		raw, e := json.Marshal(p.Values)
		if e != nil {
			return log, e
		}
		q, args, e := build(sqlBuilder.Insert("telemetry_measurement").Columns("well_id", "measured_at", "values", "quality", "source_id").Values(p.WellID, p.Timestamp, raw, p.Quality, id).Suffix("ON CONFLICT (well_id,measured_at) DO NOTHING"))
		if e != nil {
			return log, e
		}
		batch.Queue(q, args...)
		if latest == nil || p.Timestamp.After(*latest) {
			t := p.Timestamp
			latest = &t
		}
	}
	if len(points) > 0 {
		result := tx.SendBatch(ctx, batch)
		for range points {
			tag, e := result.Exec()
			if e != nil {
				_ = result.Close()
				return log, e
			}
			if tag.RowsAffected() == 1 {
				log.Accepted++
			} else {
				log.Duplicates++
			}
		}
		if err = result.Close(); err != nil {
			return log, err
		}
	}
	if _, err = execute(ctx, tx, sqlBuilder.Insert("telemetry_collection_log").Columns("timestamp", "source_id", "accepted", "duplicates", "rejected", "message").Values(log.Timestamp, id, log.Accepted, log.Duplicates, rejected, message)); err != nil {
		return log, err
	}
	if id != "manual" {
		q := sqlBuilder.Update("telemetry_source").Set("last_received", log.Timestamp).Set("error", "").Set("last_measured", sq.Expr("GREATEST(last_measured, ?::timestamptz)", latest))
		if release {
			q = q.Set("lease_until", sq.Expr("'-infinity'::timestamptz")).Set("next_poll", sq.Expr("now() + (config->>'interval_seconds')::int * interval '1 second'"))
		}
		if _, err = execute(ctx, tx, q.Where(sq.Eq{"id": id})); err != nil {
			return log, err
		}
	}
	return log, tx.Commit(ctx)
}
func (r *Repository) FailCollection(ctx context.Context, id, message string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	_, err = execute(ctx, tx, sqlBuilder.Update("telemetry_source").Set("error", message).Set("lease_until", sq.Expr("'-infinity'::timestamptz")).Set("next_poll", sq.Expr("now()+interval '60 seconds'")).Where(sq.Eq{"id": id}))
	if err != nil {
		return err
	}
	_, err = execute(ctx, tx, sqlBuilder.Insert("telemetry_collection_log").Columns("source_id", "accepted", "duplicates", "rejected", "message").Values(id, 0, 0, 0, message))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *Repository) Telemetry(ctx context.Context, well string, start, end time.Time) (out domain.TelemetryResult, err error) {
	ctx, finish := observability.Database(ctx, "telemetry.read")
	defer func() { finish(err) }()
	out = domain.TelemetryResult{Points: []domain.Measurement{}}
	q, args, err := build(sqlBuilder.Select("well_id", "measured_at", "values", "quality").From("telemetry_measurement").Where(sq.Eq{"well_id": well}).Where(sq.GtOrEq{"measured_at": start}).Where(sq.LtOrEq{"measured_at": end}).OrderBy("measured_at").Limit(10001))
	if err != nil {
		return out, err
	}
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p domain.Measurement
		var raw []byte
		if err = rows.Scan(&p.WellID, &p.Timestamp, &raw, &p.Quality); err != nil {
			return out, err
		}
		if len(out.Points) == 10000 {
			out.Truncated = true
			break
		}
		if err = json.Unmarshal(raw, &p.Values); err != nil {
			return out, err
		}
		out.Points = append(out.Points, p)
	}
	return out, rows.Err()
}
func (r *Repository) CollectionLogs(ctx context.Context) ([]domain.CollectionLog, error) {
	return list[domain.CollectionLog](ctx, r.pool, sqlBuilder.Select("timestamp", "source_id", "accepted", "duplicates", "rejected", "message").From("telemetry_collection_log").OrderBy("id DESC").Limit(200))
}

func (r *Repository) TelemetryBounds(ctx context.Context, well string) (out domain.TelemetryBounds, err error) {
	ctx, finish := observability.Database(ctx, "telemetry.bounds")
	defer func() { finish(err) }()
	q, args, err := build(sqlBuilder.Select("min(measured_at)", "max(measured_at)").From("telemetry_measurement").Where(sq.Eq{"well_id": well}))
	if err != nil {
		return out, err
	}
	err = r.pool.QueryRow(ctx, q, args...).Scan(&out.Start, &out.End)
	return out, err
}
