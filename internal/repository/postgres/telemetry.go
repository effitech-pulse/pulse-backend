package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
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
func (r *Repository) MutateSource(ctx context.Context, in domain.SourceMutation) (domain.TelemetrySource, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var out domain.TelemetrySource
	permission := domain.Permission("")
	switch in.Kind {
	case domain.SourceCreate:
		permission = "integrations.create"
	case domain.SourceUpdate:
		permission = "integrations.update"
	case domain.SourceStatus:
		permission = "integrations.status"
	default:
		return out, domain.NewError(domain.Invalid, "invalid_source_mutation")
	}
	if in.Source.ID == "" || in.Kind == domain.SourceStatus && in.ReplaceSecret {
		return out, domain.NewError(domain.Invalid, "invalid_source_mutation")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	var authority domain.AuthorizationSnapshot
	p, ok := domain.CurrentPrincipal(ctx)
	if ok && p.AccountID != nil {
		authority, err = jobAuthority(ctx, tx, *p.AccountID, nil, permission, false)
		if err == nil {
			var current domain.AuthorizationSnapshot
			current, err = authorization(ctx, tx, *p.AccountID)
			if err == nil && !current.FullApplicationScope() {
				err = domain.NewError(domain.Forbidden, "full application scope required")
			}
		}
	} else {
		authority, err = currentScopeAuthorization(ctx, tx, permission)
	}
	if err != nil {
		return out, err
	}
	if !authority.FullApplicationScope() {
		return out, domain.NewError(domain.Forbidden, "full application scope required")
	}
	q, args, err := build(sqlBuilder.Select("config", "encrypted_secret", "last_received", "last_measured", "error").From("telemetry_source").Where(sq.Eq{"id": in.Source.ID}).Suffix("FOR UPDATE"))
	if err != nil {
		return out, err
	}
	old, lookupErr := scanSource(tx.QueryRow(ctx, q, args...))
	if lookupErr != nil && !isMissing(lookupErr) {
		return out, lookupErr
	}
	if in.Kind == domain.SourceCreate {
		if lookupErr == nil {
			return out, domain.NewError(domain.Duplicate, "source_exists")
		}
		out = in.Source
		out.Status = "draft"
		out.ProfileVersion = 1
		out.LastReceived = nil
		out.LastMeasured = nil
		out.EncryptedSecret = ""
	} else {
		if lookupErr != nil {
			return out, lookupErr
		}
		if in.Kind == domain.SourceStatus {
			out = old
			out.Status = in.Source.Status
			if out.Status != "running" && out.Status != "paused" {
				return out, domain.NewError(domain.Invalid, "invalid_source_status")
			}
		} else {
			out = in.Source
			out.Status = "draft"
			out.EncryptedSecret = old.EncryptedSecret
		}
		out.LastReceived = old.LastReceived
		out.LastMeasured = old.LastMeasured
		out.ProfileVersion = old.ProfileVersion + 1
	}
	if in.ReplaceSecret {
		out.EncryptedSecret = in.Source.EncryptedSecret
	}
	out.Error = ""
	if in.Kind != domain.SourceStatus || out.Status == "running" {
		if err = domain.ValidateTelemetrySource(out); err != nil {
			return out, err
		}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return out, err
	}
	if in.Kind == domain.SourceCreate {
		_, err = execute(ctx, tx, sqlBuilder.Insert("telemetry_source").Columns("id", "config", "encrypted_secret").Values(out.ID, data, out.EncryptedSecret))
	} else {
		_, err = execute(ctx, tx, sqlBuilder.Update("telemetry_source").Set("config", data).Set("encrypted_secret", out.EncryptedSecret).Set("error", "").Set("next_poll", sq.Expr("now()")).Set("writer_generation", sq.Expr("writer_generation+1")).Set("writer_token_sha256", nil).Set("writer_profile_version", nil).Set("writer_execution_actor", nil).Set("writer_authorization", nil).Set("lease_until", sq.Expr("'-infinity'::timestamptz")).Where(sq.Eq{"id": out.ID}))
	}
	if err != nil {
		return out, err
	}
	if _, err = execute(ctx, tx, sqlBuilder.Insert("telemetry_source_profile").Columns("source_id", "version", "config").Values(out.ID, out.ProfileVersion, data)); err != nil {
		return out, err
	}
	q, args, err = build(sqlBuilder.Select("config", "encrypted_secret", "last_received", "last_measured", "error").From("telemetry_source").Where(sq.Eq{"id": out.ID}))
	if err != nil {
		return out, err
	}
	out, err = scanSource(tx.QueryRow(ctx, q, args...))
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func telemetryTokenSHA(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (r *Repository) ClaimSource(ctx context.Context, id string, due bool) (*domain.TelemetrySourceLease, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, e := collectionTX(ctx, r.pool)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	kind, auth, e := beginExecutionTx(ctx, tx, "integrations.collect", domain.ExecutionSystemCollect)
	if e != nil {
		return nil, e
	}
	current, e := executionAuthorityTx(ctx, tx, kind, auth, "integrations.collect", domain.ExecutionSystemCollect)
	if e != nil {
		return nil, e
	}
	if !current.FullApplicationScope() {
		return nil, domain.NewError(domain.Forbidden, "access_revoked")
	}
	var exists string
	if e = tx.QueryRow(ctx, "SELECT id FROM telemetry_source WHERE id=$1 FOR UPDATE", id).Scan(&exists); e == pgx.ErrNoRows {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	token, e := fileJobID()
	if e != nil {
		return nil, e
	}
	q := sqlBuilder.Update("telemetry_source").Set("writer_execution_actor", kind).Set("writer_authorization", auth).Set("lease_until", sq.Expr("clock_timestamp()+interval '60 seconds'")).Set("writer_generation", sq.Expr("writer_generation+1")).Set("writer_token_sha256", telemetryTokenSHA(token)).Set("writer_profile_version", sq.Expr("(config->>'profile_version')::int")).Where(sq.Eq{"id": id}).Where("config->>'status'='running'").Where("lease_until<clock_timestamp()").Where("NOT EXISTS(SELECT 1 FROM collection_job_source_hold WHERE source_id=telemetry_source.id)")
	if due {
		q = q.Where("next_poll<=clock_timestamp()")
	}
	sql, args, e := build(q.Suffix("RETURNING writer_generation,writer_profile_version,lease_until"))
	if e != nil {
		return nil, e
	}
	out := &domain.TelemetrySourceLease{SourceID: id, Token: token, ExecutionActor: kind, Authorization: auth}
	e = tx.QueryRow(ctx, sql, args...).Scan(&out.Fence, &out.ProfileVersion, &out.Until)
	if e == pgx.ErrNoRows {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	return out, tx.Commit(ctx)
}
func checkTelemetryWriter(ctx context.Context, tx pgx.Tx, w domain.TelemetryWrite) error {
	if w.SourceID == "manual" {
		if w.ProfileVersion != 0 || w.Release || w.Lease != nil {
			return domain.NewError(domain.Duplicate, "stale_lease")
		}
		return nil
	}
	var locked string
	if err := tx.QueryRow(ctx, "SELECT id FROM telemetry_source WHERE id=$1 FOR UPDATE", w.SourceID).Scan(&locked); err != nil {
		return err
	}
	var profile int
	var status string
	var generation int64
	var token *string
	var until time.Time
	var held bool
	var now time.Time
	err := tx.QueryRow(ctx, `SELECT (config->>'profile_version')::int,config->>'status',writer_generation,writer_token_sha256,GREATEST(lease_until,'1970-01-01'::timestamptz),EXISTS(SELECT 1 FROM collection_job_source_hold WHERE source_id=telemetry_source.id),clock_timestamp() FROM telemetry_source WHERE id=$1 FOR UPDATE`, w.SourceID).Scan(&profile, &status, &generation, &token, &until, &held, &now)
	if err != nil {
		return err
	}
	if held || status != "running" || profile != w.ProfileVersion {
		return domain.NewError(domain.Duplicate, "stale_lease")
	}
	if w.Release {
		l := w.Lease
		if l == nil || l.SourceID != w.SourceID || l.ProfileVersion != profile || l.Fence != generation || token == nil || *token != telemetryTokenSHA(l.Token) || !until.After(now) {
			return domain.NewError(domain.Duplicate, "stale_lease")
		}
	}
	return nil
}
func (r *Repository) CommitTelemetry(ctx context.Context, w domain.TelemetryWrite) (log domain.CollectionLog, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ctx, finish := observability.Database(ctx, "telemetry.save")
	defer func() { finish(err) }()
	log = domain.CollectionLog{SourceID: w.SourceID, Timestamp: time.Now().UTC(), Rejected: w.Rejected, Message: w.Message}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return log, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='2s'"); err != nil {
		return log, err
	}
	if err = r.telemetryWriteAuthority(ctx, tx, w); err != nil {
		return log, err
	}
	log.Accepted, log.Duplicates, err = insertTelemetryRows(ctx, tx, w.SourceID, w.Points, nil)
	if err != nil {
		return log, err
	}
	if err = checkTelemetryWriter(ctx, tx, w); err != nil {
		return log, err
	}
	if _, err = execute(ctx, tx, sqlBuilder.Insert("telemetry_collection_log").Columns("timestamp", "source_id", "accepted", "duplicates", "rejected", "message").Values(log.Timestamp, w.SourceID, log.Accepted, log.Duplicates, w.Rejected, w.Message)); err != nil {
		return log, err
	}
	if w.SourceID != "manual" {
		var latest *time.Time
		for _, p := range w.Points {
			if latest == nil || p.Timestamp.After(*latest) {
				v := p.Timestamp
				latest = &v
			}
		}
		q := sqlBuilder.Update("telemetry_source").Set("last_received", log.Timestamp).Set("error", "").Set("last_measured", sq.Expr("GREATEST(last_measured,?::timestamptz)", latest)).Where(sq.Eq{"id": w.SourceID})
		if w.Release {
			q = q.Set("lease_until", sq.Expr("'-infinity'::timestamptz")).Set("writer_token_sha256", nil).Set("writer_profile_version", nil).Set("writer_execution_actor", nil).Set("writer_authorization", nil).Set("next_poll", sq.Expr("clock_timestamp()+(config->>'interval_seconds')::int*interval '1 second'"))
		}
		if _, err = execute(ctx, tx, q); err != nil {
			return log, err
		}
	}
	return log, tx.Commit(ctx)
}
func (r *Repository) FailCollection(ctx context.Context, l domain.TelemetrySourceLease, message string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = checkTelemetryWriter(ctx, tx, domain.TelemetryWrite{SourceID: l.SourceID, ProfileVersion: l.ProfileVersion, Lease: &l, Release: true}); err != nil {
		return err
	}
	if _, err = execute(ctx, tx, sqlBuilder.Update("telemetry_source").Set("error", message).Set("lease_until", sq.Expr("'-infinity'::timestamptz")).Set("writer_token_sha256", nil).Set("writer_profile_version", nil).Set("writer_execution_actor", nil).Set("writer_authorization", nil).Set("next_poll", sq.Expr("clock_timestamp()+interval '60 seconds'")).Where(sq.Eq{"id": l.SourceID})); err != nil {
		return err
	}
	if _, err = execute(ctx, tx, sqlBuilder.Insert("telemetry_collection_log").Columns("source_id", "accepted", "duplicates", "rejected", "message").Values(l.SourceID, 0, 0, 0, message)); err != nil {
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

// telemetryWriteAuthority лочит user перед source fence и проверяет persisted lease frame.
func (r *Repository) telemetryWriteAuthority(ctx context.Context, tx pgx.Tx, w domain.TelemetryWrite) error {
	var a domain.AuthorizationSnapshot
	var kind domain.ExecutionActorKind
	permission := domain.Permission("integrations.collect")
	var err error
	if w.Lease != nil {
		frame, e := one[struct {
			Kind          domain.ExecutionActorKind `json:"writer_execution_actor"`
			Authorization *domain.JobAuthorization  `json:"writer_authorization"`
		}](ctx, tx, sqlBuilder.Select("writer_execution_actor", "writer_authorization").From("telemetry_source").Where(sq.Eq{"id": w.SourceID}))
		if e != nil {
			return e
		}
		kind = frame.Kind
		a, err = executionAuthorityTx(ctx, tx, frame.Kind, frame.Authorization, "integrations.collect", domain.ExecutionSystemCollect)
		if err != nil {
			return err
		}
		if !equalJobJSON(frame.Authorization, w.Lease.Authorization) || frame.Kind != w.Lease.ExecutionActor {
			return domain.NewError(domain.Duplicate, "stale_lease")
		}
		if !a.FullApplicationScope() {
			return domain.NewError(domain.Forbidden, "access_revoked")
		}
	} else {
		permission = domain.Permission("telemetry.import")
		if w.SourceID != "manual" {
			permission = "integrations.ingest"
		}
		actualKind, saved, e := beginExecutionTx(ctx, tx, permission, domain.ExecutionSystemCollect)
		if e != nil {
			return e
		}
		kind = actualKind
		a, err = executionAuthorityTx(ctx, tx, kind, saved, permission, domain.ExecutionSystemCollect)
		if err != nil {
			return err
		}
	}
	if err = checkTelemetryWriter(ctx, tx, w); err != nil {
		return err
	}
	allowUnbound := r.legacyResearchUnboundAllowed(ctx, kind, a, permission)
	pulseIDs, externalIDs := []int64{}, []int64{}
	requested := map[string]bool{}
	for _, point := range w.Points {
		if requested[point.WellID] {
			continue
		}
		requested[point.WellID] = true
		pulse := strings.HasPrefix(point.WellID, "pulse:")
		raw := point.WellID
		if pulse {
			raw = strings.TrimPrefix(raw, "pulse:")
		}
		id, e := strconv.ParseInt(raw, 10, 64)
		if e != nil {
			if !pulse && allowUnbound {
				continue
			}
			return domain.NewError(domain.Missing, "well_missing")
		}
		if pulse {
			pulseIDs = append(pulseIDs, id)
		} else {
			externalIDs = append(externalIDs, id)
		}
	}
	if len(requested) == 0 {
		return nil
	}
	wells, e := list[domain.Well](ctx, tx, sqlBuilder.Select(wellColumns).From("well").Where(sq.Or{sq.Eq{"id": pulseIDs}, sq.Eq{"unitek_id": externalIDs}}).OrderBy("id").Suffix("FOR SHARE"))
	if e != nil {
		return e
	}
	found := map[string]bool{}
	for _, well := range wells {
		if !a.AllowsWell(well) || !well.Active || well.ArchivedAt != nil {
			return domain.NewError(domain.Forbidden, "access_revoked")
		}
		found["pulse:"+strconv.FormatInt(well.ID, 10)] = true
		if well.UnitekID != nil {
			found[strconv.FormatInt(*well.UnitekID, 10)] = true
		}
	}
	for key := range requested {
		if !found[key] && !(allowUnbound && !strings.HasPrefix(key, "pulse:")) {
			return domain.NewError(domain.Missing, "well_missing")
		}
	}
	return nil
}

// AuthorizeTelemetrySourceLease проверяет current authority непосредственно перед Fetch.
func (r *Repository) AuthorizeTelemetrySourceLease(ctx context.Context, l domain.TelemetrySourceLease) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	w := domain.TelemetryWrite{SourceID: l.SourceID, ProfileVersion: l.ProfileVersion, Lease: &l, Release: true}
	if err = r.telemetryWriteAuthority(ctx, tx, w); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
