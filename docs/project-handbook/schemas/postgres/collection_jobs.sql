ALTER TABLE telemetry_source ADD COLUMN IF NOT EXISTS writer_generation bigint NOT NULL DEFAULT 0;
ALTER TABLE telemetry_source ADD COLUMN IF NOT EXISTS writer_token_sha256 text;
ALTER TABLE telemetry_source ADD COLUMN IF NOT EXISTS writer_profile_version integer;
ALTER TABLE telemetry_measurement ADD COLUMN IF NOT EXISTS collection_provenance jsonb;
DO $$ BEGIN
IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='telemetry_source'::regclass AND conname='telemetry_writer_token') THEN
ALTER TABLE telemetry_source ADD CONSTRAINT telemetry_writer_token CHECK ((writer_token_sha256 IS NULL)=(writer_profile_version IS NULL) AND (writer_token_sha256 IS NULL OR writer_token_sha256 ~ '^[0-9a-f]{64}$') AND writer_generation>=0);
END IF;
END $$;
CREATE TABLE IF NOT EXISTS collection_job_guard (
 id boolean PRIMARY KEY CHECK(id), run_id text NOT NULL,
 manifest_sha256 text NOT NULL CHECK(manifest_sha256 ~ '^[0-9a-f]{64}$'),
 test_schema text NOT NULL, fixture_origin text NOT NULL,
 synthetic_only boolean NOT NULL CHECK(synthetic_only), accepting boolean NOT NULL,
 accepted_jobs integer NOT NULL DEFAULT 0 CHECK(accepted_jobs BETWEEN 0 AND 32),
 created_at timestamptz NOT NULL DEFAULT now(), expires_at timestamptz NOT NULL,
 CHECK(expires_at>created_at AND expires_at<=created_at+interval '24 hours')
);
CREATE TABLE IF NOT EXISTS collection_job (
 id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{32}$'), actor_account_id bigint NOT NULL REFERENCES auth_user(id) ON DELETE RESTRICT,
 replay_key_sha256 text NOT NULL CHECK(replay_key_sha256 ~ '^[0-9a-f]{64}$'),
 request_sha256 text NOT NULL CHECK(request_sha256 ~ '^[0-9a-f]{64}$'),
 intent jsonb NOT NULL CHECK(octet_length(intent::text)<=65536),
 state text NOT NULL CHECK(state IN ('pending_dispatch','running','retry_wait','succeeded','failed','cancelled','revoked','expired','quarantined')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0), generation integer NOT NULL DEFAULT 1 CHECK(generation BETWEEN 1 AND 8),
 attempt integer NOT NULL DEFAULT 0 CHECK(attempt BETWEEN 0 AND 3), fence bigint NOT NULL DEFAULT 0 CHECK(fence>=0),
 lease_token_sha256 text CHECK(lease_token_sha256 ~ '^[0-9a-f]{64}$'), lease_until timestamptz,
 physical_backend_pid integer, attempt_deadline timestamptz, next_attempt_at timestamptz,
 deadline timestamptz NOT NULL, error_code text,
 availability text NOT NULL DEFAULT 'unknown' CHECK(availability IN ('unknown','dense','explicit_empty','unavailable','invalid','conflict')),
 rejected integer NOT NULL DEFAULT 0 CHECK(rejected>=0),
 accepted_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), finished_at timestamptz,
 UNIQUE(actor_account_id,replay_key_sha256),
 CHECK((state='running')=(lease_token_sha256 IS NOT NULL AND lease_until IS NOT NULL AND physical_backend_pid IS NOT NULL AND attempt_deadline IS NOT NULL)),
 CHECK(state='running' OR (lease_token_sha256 IS NULL AND lease_until IS NULL AND physical_backend_pid IS NULL AND attempt_deadline IS NULL)),
 CHECK(error_code IS NULL OR error_code IN ('source_unavailable','source_timeout','invalid_capture','input_limit','input_conflict','measurement_conflict','unverifiable_existing','capture_corrupt','access_revoked','source_changed','binding_changed','attempts_exhausted','job_deadline','dispatch_exhausted','cancelled'))
);
CREATE TABLE IF NOT EXISTS collection_job_source_hold (
 source_id text PRIMARY KEY REFERENCES telemetry_source(id) ON DELETE RESTRICT, job_id text NOT NULL UNIQUE REFERENCES collection_job(id) ON DELETE RESTRICT,
 source_generation bigint NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS collection_job_capture (
 job_id text PRIMARY KEY REFERENCES collection_job(id) ON DELETE RESTRICT, raw bytea NOT NULL CHECK(octet_length(raw) BETWEEN 1 AND 2097152),
 raw_sha256 text NOT NULL CHECK(raw_sha256 ~ '^[0-9a-f]{64}$'),
 canonical bytea NOT NULL CHECK(octet_length(canonical) BETWEEN 1 AND 2097152),
 canonical_sha256 text NOT NULL CHECK(canonical_sha256 ~ '^[0-9a-f]{64}$'), provenance jsonb NOT NULL,
 raw_rows integer NOT NULL CHECK(raw_rows BETWEEN 0 AND 2880), unique_rows integer NOT NULL CHECK(unique_rows BETWEEN 0 AND 1440),
 repeated_rows integer NOT NULL CHECK(repeated_rows BETWEEN 0 AND 2880), marker_values integer NOT NULL CHECK(marker_values>=0),
 null_values integer NOT NULL CHECK(null_values>=0), excluded_setpoints integer NOT NULL CHECK(excluded_setpoints>=0),
 empty boolean NOT NULL, received_at timestamptz NOT NULL, captured_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS collection_job_checkpoint (
 job_id text PRIMARY KEY REFERENCES collection_job(id) ON DELETE RESTRICT, capture_sha256 text NOT NULL CHECK(capture_sha256 ~ '^[0-9a-f]{64}$'),
 inserted integer NOT NULL CHECK(inserted BETWEEN 0 AND 1440), identical integer NOT NULL CHECK(identical BETWEEN 0 AND 1440),
 committed_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS collection_job_coverage (
 job_id text PRIMARY KEY REFERENCES collection_job_checkpoint(job_id) ON DELETE RESTRICT, source_id text NOT NULL REFERENCES telemetry_source(id) ON DELETE RESTRICT,
 binding_sha256 text NOT NULL CHECK(binding_sha256 ~ '^[0-9a-f]{64}$'), data_revision text NOT NULL,
 start_at timestamptz NOT NULL, end_at timestamptz NOT NULL CHECK(end_at>start_at),
 state text NOT NULL CHECK(state IN ('complete','complete_empty')), maximum_measured_at timestamptz
);
CREATE TABLE IF NOT EXISTS collection_job_outbox (
 event_id text PRIMARY KEY CHECK(event_id ~ '^[0-9a-f]{32}$'), job_id text NOT NULL REFERENCES collection_job(id) ON DELETE RESTRICT,
 generation integer NOT NULL CHECK(generation BETWEEN 1 AND 8),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','claimed','confirmed','superseded')),
 claim_token_sha256 text CHECK(claim_token_sha256 ~ '^[0-9a-f]{64}$'), claim_until timestamptz,
 next_publish_at timestamptz NOT NULL DEFAULT now(), created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(job_id,generation)
);
CREATE TABLE IF NOT EXISTS collection_job_inbox (
 event_id text PRIMARY KEY CHECK(event_id ~ '^[0-9a-f]{32}$'), job_id text NOT NULL REFERENCES collection_job(id) ON DELETE RESTRICT,
 generation integer NOT NULL CHECK(generation BETWEEN 1 AND 8), fence bigint NOT NULL,
 state text NOT NULL CHECK(state IN ('processing','completed')),
 outcome text CHECK(outcome IN ('applied','retry','terminal','stale','ignored')), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS collection_job_action (
 id bigserial PRIMARY KEY, job_id text NOT NULL REFERENCES collection_job(id) ON DELETE RESTRICT, revision bigint NOT NULL,
 kind text NOT NULL CHECK(kind IN ('accepted','captured','claimed','retry','succeeded','failed','cancelled','revoked','expired','quarantined')),
 reason_code text, actor_account_id bigint REFERENCES auth_user(id) ON DELETE RESTRICT, created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(job_id,revision,kind)
);
CREATE INDEX IF NOT EXISTS collection_job_due ON collection_job(state,next_attempt_at,deadline);
CREATE INDEX IF NOT EXISTS collection_outbox_due ON collection_job_outbox(state,next_publish_at);
CREATE INDEX IF NOT EXISTS collection_inbox_job ON collection_job_inbox(job_id);
CREATE INDEX IF NOT EXISTS collection_action_job ON collection_job_action(job_id,id);
CREATE OR REPLACE FUNCTION collection_immutable_job() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF ROW(NEW.id,NEW.actor_account_id,NEW.replay_key_sha256,NEW.request_sha256,NEW.intent,NEW.accepted_at,NEW.deadline)
 IS DISTINCT FROM ROW(OLD.id,OLD.actor_account_id,OLD.replay_key_sha256,OLD.request_sha256,OLD.intent,OLD.accepted_at,OLD.deadline)
 THEN RAISE EXCEPTION 'immutable collection intent'; END IF; RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS collection_job_immutable ON collection_job;
CREATE TRIGGER collection_job_immutable BEFORE UPDATE ON collection_job FOR EACH ROW EXECUTE FUNCTION collection_immutable_job();
CREATE OR REPLACE FUNCTION collection_immutable_capture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'immutable collection capture'; END $$;
DROP TRIGGER IF EXISTS collection_capture_immutable ON collection_job_capture;
CREATE TRIGGER collection_capture_immutable BEFORE UPDATE OR DELETE ON collection_job_capture FOR EACH ROW EXECUTE FUNCTION collection_immutable_capture();
INSERT INTO backend_schema_migration(version) VALUES(15) ON CONFLICT DO NOTHING;
