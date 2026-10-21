CREATE TABLE IF NOT EXISTS file_job_pilot_guard (
 id bool PRIMARY KEY CHECK(id), run_id text NOT NULL, manifest_sha256 text NOT NULL,
 synthetic_only bool NOT NULL CHECK(synthetic_only)
);
CREATE UNIQUE INDEX IF NOT EXISTS file_input_job_identity ON file_input(id,actor_account_id,well_id);
CREATE TABLE IF NOT EXISTS file_job (
 id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{32}$'), input_id text NOT NULL UNIQUE REFERENCES file_input(id) ON DELETE RESTRICT CHECK(input_id=id),
 actor_account_id bigint NOT NULL REFERENCES auth_user(id) ON DELETE RESTRICT, well_id bigint NOT NULL REFERENCES well(id) ON DELETE RESTRICT,
 cohort_version text NOT NULL, cohort_manifest_sha256 text NOT NULL, pilot_run_id text NOT NULL,
 state text NOT NULL CHECK(state IN ('pending_dispatch','queued','running','retry_wait','cancel_requested','quarantined','succeeded','failed','cancelled','expired','revoked')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision BETWEEN 1 AND 9007199254740991),
 generation integer NOT NULL DEFAULT 1 CHECK(generation BETWEEN 1 AND 6), attempt_count integer NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 3), redrive_count integer NOT NULL DEFAULT 0 CHECK(redrive_count BETWEEN 0 AND 1), fence bigint NOT NULL DEFAULT 0 CHECK(fence>=0),
 lease_token_sha256 text, lease_until timestamptz, heartbeat_at timestamptz, next_attempt_at timestamptz,
 created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, deadline_at timestamptz NOT NULL,
 started_at timestamptz, finished_at timestamptz, check_id text UNIQUE CHECK(check_id IS NULL OR check_id='q1_'||id), cancel_requested_at timestamptz, error_code text CHECK(length(error_code)<=64),
 start_at timestamptz NOT NULL, end_at timestamptz NOT NULL CHECK(end_at>start_at), points_count integer NOT NULL CHECK(points_count BETWEEN 1 AND 100000),
 FOREIGN KEY(input_id,actor_account_id,well_id) REFERENCES file_input(id,actor_account_id,well_id) ON DELETE RESTRICT,
 CHECK((state IN ('running','cancel_requested'))=(lease_token_sha256 IS NOT NULL AND lease_until IS NOT NULL)),
 CHECK((lease_token_sha256 IS NULL)=(lease_until IS NULL)),
 CHECK((state IN ('succeeded','failed','cancelled','expired','revoked'))=(finished_at IS NOT NULL)),
 CHECK((state='succeeded')=(check_id IS NOT NULL)), CHECK(deadline_at=created_at+interval '20 minutes')
);
CREATE TABLE IF NOT EXISTS file_job_attempt (
 job_id text NOT NULL REFERENCES file_job(id) ON DELETE RESTRICT, attempt integer NOT NULL CHECK(attempt BETWEEN 1 AND 3), generation integer NOT NULL CHECK(generation BETWEEN 1 AND 6), fence bigint NOT NULL CHECK(fence>0), worker_instance text NOT NULL CHECK(worker_instance ~ '^[0-9a-f]{32}$'), started_at timestamptz NOT NULL, ended_at timestamptz,
 outcome text CHECK(outcome IN ('succeeded','retry','failed','cancelled','expired','revoked','lease_expired')), error_code text CHECK(length(error_code)<=64), PRIMARY KEY(job_id,attempt), UNIQUE(job_id,fence)
);
CREATE TABLE IF NOT EXISTS file_job_outbox (
 event_id text PRIMARY KEY CHECK(event_id ~ '^[0-9a-f]{32}$'), job_id text NOT NULL REFERENCES file_job(id) ON DELETE RESTRICT, generation integer NOT NULL CHECK(generation BETWEEN 1 AND 6), kind text NOT NULL DEFAULT 'file_check.ready.v1' CHECK(kind='file_check.ready.v1'), status text NOT NULL CHECK(status IN ('pending','leased','confirmed','superseded','exhausted')), publish_attempts integer NOT NULL DEFAULT 0 CHECK(publish_attempts BETWEEN 0 AND 8), next_publish_at timestamptz NOT NULL, claim_token_sha256 text, claim_until timestamptz, confirmed_at timestamptz, created_at timestamptz NOT NULL, last_error_code text CHECK(length(last_error_code)<=64), UNIQUE(job_id,generation), CHECK((claim_token_sha256 IS NULL)=(claim_until IS NULL))
);
CREATE TABLE IF NOT EXISTS file_job_inbox (
 event_id text PRIMARY KEY REFERENCES file_job_outbox(event_id) ON DELETE RESTRICT, job_id text NOT NULL REFERENCES file_job(id) ON DELETE RESTRICT, generation integer NOT NULL CHECK(generation BETWEEN 1 AND 6), fence bigint NOT NULL, state text NOT NULL CHECK(state IN ('processing','completed','superseded')), first_seen_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, outcome text CHECK(outcome IN ('succeeded','retry','failed','cancelled','expired','revoked','lease_expired'))
);
CREATE TABLE IF NOT EXISTS file_job_runtime (
 key text PRIMARY KEY CHECK(key='execution'), job_id text REFERENCES file_job(id) ON DELETE RESTRICT, fence bigint, lease_until timestamptz,
 CHECK((job_id IS NULL AND fence IS NULL AND lease_until IS NULL) OR (job_id IS NOT NULL AND fence IS NOT NULL AND lease_until IS NOT NULL))
);
INSERT INTO file_job_runtime(key) VALUES('execution') ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS file_job_action (
 job_id text NOT NULL REFERENCES file_job(id) ON DELETE RESTRICT, revision bigint NOT NULL, kind text NOT NULL CHECK(kind IN ('cancel','redrive','quarantine')), actor_account_id bigint REFERENCES auth_user(id) ON DELETE RESTRICT, created_at timestamptz NOT NULL, reason_code text NOT NULL CHECK(length(reason_code)<=64), PRIMARY KEY(job_id,revision)
);
ALTER TABLE case_review ADD COLUMN IF NOT EXISTS file_job_id text REFERENCES file_job(id) ON DELETE RESTRICT;
ALTER TABLE case_review DROP CONSTRAINT IF EXISTS case_review_kind_check;
ALTER TABLE case_review DROP CONSTRAINT IF EXISTS case_review_check;
ALTER TABLE case_review DROP CONSTRAINT IF EXISTS case_review_target_check;
ALTER TABLE case_review ADD CONSTRAINT case_review_kind_check CHECK(kind IN ('check','alert','file_check'));
ALTER TABLE case_review ADD CONSTRAINT case_review_target_check CHECK(
 (kind='check' AND check_id IS NOT NULL AND check_id=target_id AND alert_id IS NULL AND file_job_id IS NULL) OR
 (kind='alert' AND alert_id IS NOT NULL AND alert_id::text=target_id AND check_id IS NULL AND file_job_id IS NULL) OR
 (kind='file_check' AND file_job_id IS NOT NULL AND file_job_id=target_id AND check_id IS NULL AND alert_id IS NULL));
CREATE INDEX IF NOT EXISTS file_job_owner_history ON file_job(actor_account_id,well_id,created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS file_job_retry ON file_job(next_attempt_at,id) WHERE state='retry_wait';
CREATE INDEX IF NOT EXISTS file_job_lease ON file_job(lease_until,id) WHERE state IN ('running','cancel_requested');
CREATE INDEX IF NOT EXISTS file_job_deadline ON file_job(deadline_at,id) WHERE state NOT IN ('succeeded','failed','cancelled','expired','revoked');
CREATE INDEX IF NOT EXISTS file_job_publish_due ON file_job_outbox(next_publish_at,event_id) WHERE status IN ('pending','leased');
CREATE INDEX IF NOT EXISTS case_review_file_job ON case_review(file_job_id) WHERE kind='file_check';
INSERT INTO backend_schema_migration(version) VALUES(13) ON CONFLICT DO NOTHING;
