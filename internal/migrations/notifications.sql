CREATE TABLE notification_activation (
 id boolean PRIMARY KEY CHECK(id), activation_id text UNIQUE, activated_at timestamptz,
 CHECK ((activation_id IS NULL AND activated_at IS NULL) OR (activation_id IS NOT NULL AND activation_id ~ '^[0-9a-f]{32}$' AND activated_at IS NOT NULL))
);
INSERT INTO notification_activation VALUES(true,NULL,NULL);
CREATE FUNCTION notification_activation_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR OLD.activation_id IS NOT NULL OR NEW.id IS DISTINCT FROM OLD.id OR NEW.activation_id IS NULL THEN
  RAISE EXCEPTION 'notification activation is immutable';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER notification_activation_immutable BEFORE UPDATE OR DELETE ON notification_activation FOR EACH ROW EXECUTE FUNCTION notification_activation_immutable();
ALTER TABLE ml_run_snapshot ALTER COLUMN run_id TYPE bigint;
ALTER TABLE telemetry_collection_log ADD COLUMN outcome text NOT NULL DEFAULT 'historical_unknown' CHECK(outcome IN ('historical_unknown','completed','failed')),
 ADD COLUMN attempt_profile_version integer, ADD COLUMN attempt_generation bigint, ADD COLUMN attempt_token_sha256 text,
 ADD CONSTRAINT telemetry_collection_attempt CHECK(
 (attempt_profile_version IS NULL AND attempt_generation IS NULL AND attempt_token_sha256 IS NULL AND outcome <> 'failed') OR
 (attempt_profile_version IS NOT NULL AND attempt_profile_version>=1 AND attempt_generation IS NOT NULL AND attempt_generation>=1 AND attempt_token_sha256 IS NOT NULL AND attempt_token_sha256 ~ '^[0-9a-f]{64}$' AND outcome IN ('completed','failed')));
CREATE UNIQUE INDEX telemetry_collection_terminal_attempt ON telemetry_collection_log(source_id,attempt_profile_version,attempt_generation) WHERE attempt_generation IS NOT NULL;
CREATE UNIQUE INDEX telemetry_collection_log_identity ON telemetry_collection_log(id,source_id);
CREATE UNIQUE INDEX ml_alert_notification_identity ON ml_alert(id,run_id,app_well_id);
CREATE TABLE notification_fact (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 activation_id text NOT NULL REFERENCES notification_activation(activation_id) ON DELETE RESTRICT,
 kind text NOT NULL CHECK(kind IN ('alert.created.v1','collection.failed.v1')),
 recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 alert_id bigint, run_id bigint, well_id bigint REFERENCES well(id) ON DELETE CASCADE,
 collection_log_id bigint, source_id text REFERENCES telemetry_source(id) ON DELETE CASCADE, code text NOT NULL,
 CHECK((kind='alert.created.v1' AND alert_id IS NOT NULL AND run_id IS NOT NULL AND well_id IS NOT NULL AND collection_log_id IS NULL AND source_id IS NULL AND code='alert_created') OR
 (kind='collection.failed.v1' AND collection_log_id IS NOT NULL AND source_id IS NOT NULL AND alert_id IS NULL AND run_id IS NULL AND well_id IS NULL AND code='collection_failed')),
 FOREIGN KEY(alert_id,run_id,well_id) REFERENCES ml_alert(id,run_id,app_well_id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
 FOREIGN KEY(collection_log_id,source_id) REFERENCES telemetry_collection_log(id,source_id) ON DELETE CASCADE
);
CREATE FUNCTION notification_fact_immutable() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'notification fact is immutable'; END $$;
CREATE TRIGGER notification_fact_immutable BEFORE UPDATE ON notification_fact FOR EACH ROW EXECUTE FUNCTION notification_fact_immutable();
CREATE INDEX notification_fact_order ON notification_fact(recorded_at DESC,id DESC);
CREATE UNIQUE INDEX notification_fact_alert_source ON notification_fact(alert_id) WHERE kind='alert.created.v1';
CREATE UNIQUE INDEX notification_fact_alert_pair ON notification_fact(run_id,well_id) WHERE kind='alert.created.v1';
CREATE UNIQUE INDEX notification_fact_collection_source ON notification_fact(collection_log_id) WHERE kind='collection.failed.v1';
CREATE INDEX notification_fact_well_order ON notification_fact(well_id,recorded_at DESC,id DESC) WHERE kind='alert.created.v1';
CREATE INDEX notification_fact_source_order ON notification_fact(source_id,recorded_at DESC,id DESC) WHERE kind='collection.failed.v1';
CREATE TABLE notification_read (
 account_id bigint NOT NULL REFERENCES auth_user(id) ON DELETE CASCADE,
 notification_id bigint NOT NULL REFERENCES notification_fact(id) ON DELETE CASCADE,
 read_at timestamptz NOT NULL DEFAULT clock_timestamp(), PRIMARY KEY(account_id,notification_id)
);
CREATE INDEX notification_read_fact ON notification_read(notification_id);
INSERT INTO backend_schema_migration(version) VALUES(19);
