CREATE TABLE IF NOT EXISTS cohort_job_guard (
 id boolean PRIMARY KEY CHECK(id), run_id text NOT NULL, manifest_sha256 text NOT NULL CHECK(manifest_sha256 ~ '^sha256:[0-9a-f]{64}$'), synthetic_only boolean NOT NULL CHECK(synthetic_only)
);
CREATE TABLE IF NOT EXISTS cohort_job (
 id text PRIMARY KEY CHECK(id ~ '^[0-9a-f]{32}$'), run_id bigint NOT NULL UNIQUE REFERENCES ml_run(id) ON DELETE RESTRICT,
 actor_account_id bigint NOT NULL REFERENCES auth_user(id) ON DELETE RESTRICT, actor_revision bigint NOT NULL, actor_role text NOT NULL CHECK(actor_role='admin'), actor_scope jsonb NOT NULL,
 operation_kind text NOT NULL DEFAULT 'cohort_score' CHECK(operation_kind='cohort_score'), request_key text NOT NULL CHECK(request_key ~ '^[A-Za-z0-9_-]{16,80}$'), request_commitment_sha256 text NOT NULL CHECK(request_commitment_sha256 ~ '^sha256:[0-9a-f]{64}$'),
 pilot_run_id text NOT NULL, cohort_version text NOT NULL REFERENCES app_cohort(version) ON DELETE RESTRICT, cohort_manifest_sha256 text NOT NULL,
 snapshot_id text NOT NULL UNIQUE CHECK(snapshot_id ~ '^[0-9a-f]{32}$'), as_of timestamptz NOT NULL, reserved_bytes bigint NOT NULL CHECK(reserved_bytes BETWEEN 37748736 AND 62914560),
 state text NOT NULL CHECK(state IN ('pending_dispatch','queued','running','retry_wait','cancel_requested','quarantined','succeeded','failed','cancelled','expired','revoked')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0), generation integer NOT NULL DEFAULT 1 CHECK(generation BETWEEN 1 AND 8), fence bigint NOT NULL DEFAULT 0 CHECK(fence>=0),
 attempt_count integer NOT NULL DEFAULT 0 CHECK(attempt_count BETWEEN 0 AND 3), compute_starts integer NOT NULL DEFAULT 0 CHECK(compute_starts BETWEEN 0 AND 102),
 lease_token_sha256 text CHECK(lease_token_sha256 IS NULL OR lease_token_sha256 ~ '^sha256:[0-9a-f]{64}$'), lease_until timestamptz, physical_backend_pid integer, heartbeat_at timestamptz,
 created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, deadline_at timestamptz NOT NULL, started_at timestamptz, finished_at timestamptz, next_attempt_at timestamptz, error_code text CHECK(length(error_code)<=64),
 UNIQUE(actor_account_id,operation_kind,request_key), CHECK(deadline_at=created_at+interval '30 minutes'),
 CHECK((state IN ('running','cancel_requested'))=(lease_token_sha256 IS NOT NULL AND lease_until IS NOT NULL AND physical_backend_pid IS NOT NULL)),
 CHECK((state IN ('succeeded','failed','cancelled','expired','revoked'))=(finished_at IS NOT NULL))
);
CREATE TABLE IF NOT EXISTS cohort_job_manifest (
 job_id text PRIMARY KEY REFERENCES cohort_job(id) ON DELETE RESTRICT,
 fixture_sha256 text NOT NULL CHECK(fixture_sha256 ~ '^sha256:[0-9a-f]{64}$'), input_manifest_sha256 text NOT NULL CHECK(input_manifest_sha256 ~ '^sha256:[0-9a-f]{64}$'), execution_pin_sha256 text NOT NULL CHECK(execution_pin_sha256 ~ '^sha256:[0-9a-f]{64}$'), job_manifest_sha256 text NOT NULL CHECK(job_manifest_sha256 ~ '^sha256:[0-9a-f]{64}$'),
 fixture_json bytea NOT NULL CHECK(octet_length(fixture_json)<=524288), manifest_json bytea NOT NULL CHECK(octet_length(manifest_json)<=524288)
);
CREATE TABLE IF NOT EXISTS cohort_job_member (
 job_id text NOT NULL REFERENCES cohort_job(id) ON DELETE RESTRICT, well_id bigint NOT NULL REFERENCES well(id) ON DELETE RESTRICT, ordinal integer NOT NULL CHECK(ordinal BETWEEN 1 AND 100),
 member_json bytea NOT NULL CHECK(octet_length(member_json)<=8192), input_id text, no_compute_reason text CHECK(no_compute_reason IN ('archived','inactive','insufficient_telemetry','synthetic_source_unavailable')),
 PRIMARY KEY(job_id,well_id), UNIQUE(job_id,ordinal), UNIQUE(input_id), CHECK((input_id IS NULL)<>(no_compute_reason IS NULL))
);
CREATE TABLE IF NOT EXISTS cohort_job_input (
 job_id text NOT NULL, well_id bigint NOT NULL, input_id text NOT NULL UNIQUE CHECK(input_id ~ '^[0-9a-f]{32}$'), source_kind text NOT NULL CHECK(source_kind='cohort_snapshot'), canonical_sha256 text NOT NULL CHECK(canonical_sha256 ~ '^sha256:[0-9a-f]{64}$'), fingerprint_sha256 text NOT NULL CHECK(fingerprint_sha256 ~ '^sha256:[0-9a-f]{64}$'), canonical_length integer NOT NULL CHECK(canonical_length BETWEEN 1 AND 524288), canonical bytea NOT NULL CHECK(octet_length(canonical)=canonical_length),
 PRIMARY KEY(job_id,well_id), FOREIGN KEY(job_id,well_id) REFERENCES cohort_job_member(job_id,well_id) ON DELETE RESTRICT
);
CREATE TABLE IF NOT EXISTS cohort_job_member_attempt (
 job_id text NOT NULL, well_id bigint NOT NULL, attempt integer NOT NULL CHECK(attempt BETWEEN 1 AND 3), fence bigint NOT NULL, started_at timestamptz NOT NULL,
 PRIMARY KEY(job_id,well_id,attempt), FOREIGN KEY(job_id,well_id) REFERENCES cohort_job_member(job_id,well_id) ON DELETE RESTRICT
);
CREATE TABLE IF NOT EXISTS cohort_job_checkpoint (
 job_id text NOT NULL, well_id bigint NOT NULL, fence bigint NOT NULL, digest_sha256 text NOT NULL CHECK(digest_sha256 ~ '^sha256:[0-9a-f]{64}$'), checkpoint_json bytea NOT NULL CHECK(octet_length(checkpoint_json)<=262144), created_at timestamptz NOT NULL,
 PRIMARY KEY(job_id,well_id), FOREIGN KEY(job_id,well_id) REFERENCES cohort_job_member(job_id,well_id) ON DELETE RESTRICT
);
CREATE TABLE IF NOT EXISTS cohort_job_outbox (
 event_id text PRIMARY KEY CHECK(event_id ~ '^[0-9a-f]{32}$'), job_id text NOT NULL REFERENCES cohort_job(id) ON DELETE RESTRICT, generation integer NOT NULL CHECK(generation BETWEEN 1 AND 8),
 kind text NOT NULL DEFAULT 'cohort.ready.v1' CHECK(kind='cohort.ready.v1'), status text NOT NULL CHECK(status IN ('pending','leased','confirmed','superseded','exhausted')),
 publish_attempts integer NOT NULL DEFAULT 0 CHECK(publish_attempts BETWEEN 0 AND 6), next_publish_at timestamptz NOT NULL, claim_token_sha256 text, claim_until timestamptz, confirmed_at timestamptz, created_at timestamptz NOT NULL, last_error_code text CHECK(length(last_error_code)<=64), UNIQUE(job_id,generation), CHECK((claim_token_sha256 IS NULL)=(claim_until IS NULL))
);
CREATE TABLE IF NOT EXISTS cohort_job_inbox (
 event_id text PRIMARY KEY REFERENCES cohort_job_outbox(event_id) ON DELETE RESTRICT, job_id text NOT NULL REFERENCES cohort_job(id) ON DELETE RESTRICT, generation integer NOT NULL, fence bigint NOT NULL, state text NOT NULL CHECK(state IN ('processing','completed','superseded','duplicate')), outcome text, updated_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS cohort_job_action (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, job_id text NOT NULL REFERENCES cohort_job(id) ON DELETE RESTRICT, revision bigint NOT NULL, kind text NOT NULL, reason_code text NOT NULL CHECK(length(reason_code)<=64), actor_account_id bigint REFERENCES auth_user(id) ON DELETE RESTRICT, created_at timestamptz NOT NULL
);
DO $$ DECLARE target text; BEGIN
 FOREACH target IN ARRAY ARRAY['cohort_job_manifest','cohort_job_member','cohort_job_input','cohort_job_checkpoint','cohort_job_action','cohort_job_member_attempt'] LOOP
  IF NOT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass(target) AND tgname='cohort_job_immutable') THEN
   EXECUTE format('CREATE TRIGGER cohort_job_immutable BEFORE UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION reject_cohort_mutation()',target);
  END IF;
 END LOOP;
END $$;
CREATE INDEX IF NOT EXISTS cohort_job_due ON cohort_job(state,next_attempt_at,deadline_at);
CREATE INDEX IF NOT EXISTS cohort_job_publish_due ON cohort_job_outbox(next_publish_at,event_id) WHERE status IN ('pending','leased');
INSERT INTO backend_schema_migration(version) VALUES(14) ON CONFLICT DO NOTHING;

CREATE OR REPLACE FUNCTION reject_cohort_job_identity_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF ROW(NEW.id,NEW.run_id,NEW.actor_account_id,NEW.actor_revision,NEW.actor_role,NEW.actor_scope,NEW.operation_kind,NEW.request_key,NEW.request_commitment_sha256,NEW.pilot_run_id,NEW.cohort_version,NEW.cohort_manifest_sha256,NEW.snapshot_id,NEW.as_of,NEW.reserved_bytes,NEW.created_at,NEW.deadline_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.run_id,OLD.actor_account_id,OLD.actor_revision,OLD.actor_role,OLD.actor_scope,OLD.operation_kind,OLD.request_key,OLD.request_commitment_sha256,OLD.pilot_run_id,OLD.cohort_version,OLD.cohort_manifest_sha256,OLD.snapshot_id,OLD.as_of,OLD.reserved_bytes,OLD.created_at,OLD.deadline_at)
 THEN RAISE EXCEPTION 'cohort job identity is immutable'; END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS cohort_job_identity_immutable ON cohort_job;
CREATE TRIGGER cohort_job_identity_immutable BEFORE UPDATE ON cohort_job FOR EACH ROW EXECUTE FUNCTION reject_cohort_job_identity_mutation();
