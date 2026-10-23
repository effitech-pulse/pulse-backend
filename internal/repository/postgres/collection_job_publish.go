package postgres

import (
	"context"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func collectionOutbox(ctx context.Context, tx pgx.Tx, id string, generation int, due time.Time) error {
	event, e := fileJobID()
	if e != nil {
		return e
	}
	_, e = execute(ctx, tx, sqlBuilder.Insert("collection_job_outbox").Columns("event_id", "job_id", "generation", "next_publish_at").Values(event, id, generation, due).Suffix("ON CONFLICT (job_id,generation) DO NOTHING"))
	return e
}
func collectionInbox(ctx context.Context, tx pgx.Tx, m domain.CollectionMessage, fence int64, outcome string) error {
	_, e := execute(ctx, tx, sqlBuilder.Insert("collection_job_inbox").Columns("event_id", "job_id", "generation", "fence", "state", "outcome").Values(m.EventID, m.JobID, m.Generation, fence, "completed", outcome).Suffix("ON CONFLICT(event_id) DO UPDATE SET state='completed',outcome=EXCLUDED.outcome,updated_at=clock_timestamp()"))
	return e
}
func (r *Repository) ClaimCollectionOutbox(ctx context.Context, dispatcher string) (*domain.CollectionPublishLease, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if dispatcher == "" {
		return nil, collectionError(domain.Invalid, "invalid_collection_request")
	}
	tx, e := collectionTX(ctx, r.pool)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if _, _, e = r.collectionGuard(ctx, tx, false); e != nil {
		return nil, e
	}
	var event, id string
	var generation int
	e = tx.QueryRow(ctx, `SELECT o.event_id,o.job_id,o.generation FROM collection_job_outbox o JOIN collection_job j ON j.id=o.job_id WHERE o.generation=j.generation AND j.state IN ('pending_dispatch','retry_wait') AND (o.state='pending' OR o.state='claimed' AND o.claim_until<clock_timestamp()) AND o.next_publish_at<=clock_timestamp() ORDER BY o.created_at FOR UPDATE OF o SKIP LOCKED LIMIT 1`).Scan(&event, &id, &generation)
	if e == pgx.ErrNoRows {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	token, e := fileJobID()
	if e != nil {
		return nil, e
	}
	now, e := dbJobNow(ctx, tx)
	if e != nil {
		return nil, e
	}
	until := now.Add(10 * time.Second)
	_, e = execute(ctx, tx, sqlBuilder.Update("collection_job_outbox").Set("state", "claimed").Set("claim_token_sha256", telemetryTokenSHA(token)).Set("claim_until", until).Set("updated_at", now).Where(sq.Eq{"event_id": event}))
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(ctx); e != nil {
		return nil, e
	}
	return &domain.CollectionPublishLease{Message: domain.CollectionMessage{Version: 1, Kind: "collection.ready.v1", EventID: event, JobID: id, Generation: generation}, Token: token, Until: until}, nil
}
func (r *Repository) CompleteCollectionPublish(ctx context.Context, l domain.CollectionPublishLease, outcome domain.CollectionPublishOutcome) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	switch outcome {
	case domain.CollectionPublishConfirmed, domain.CollectionPublishReturned, domain.CollectionPublishNack, domain.CollectionPublishUnknown:
	default:
		return collectionError(domain.Invalid, "invalid_collection_request")
	}
	state := "pending"
	if outcome == domain.CollectionPublishConfirmed {
		state = "confirmed"
	}
	tag, e := execute(ctx, r.pool, sqlBuilder.Update("collection_job_outbox").Set("state", state).Set("claim_token_sha256", nil).Set("claim_until", nil).Set("next_publish_at", sq.Expr("clock_timestamp()+interval '1 second'")).Set("updated_at", sq.Expr("clock_timestamp()")).Where(sq.Eq{"event_id": l.Message.EventID, "job_id": l.Message.JobID, "generation": l.Message.Generation, "state": "claimed", "claim_token_sha256": telemetryTokenSHA(l.Token)}).Where("claim_until>clock_timestamp()"))
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return collectionError(domain.Duplicate, "stale_lease")
	}
	return nil
}
