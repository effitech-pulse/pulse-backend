ALTER TABLE ml_alert ALTER COLUMN ml_well_id DROP NOT NULL;
CREATE TABLE IF NOT EXISTS ml_run_snapshot (
 run_id integer PRIMARY KEY REFERENCES ml_run(id) ON DELETE RESTRICT,
 snapshot_id text NOT NULL UNIQUE,
 format_version integer NOT NULL,
 cohort_version text NOT NULL REFERENCES app_cohort(version) ON DELETE RESTRICT,
 as_of timestamptz NOT NULL,
 provenance_status text NOT NULL CHECK(provenance_status IN ('complete','partial','historical_unknown')),
 output_sha256 text NOT NULL CHECK(length(output_sha256)=64),
 snapshot jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
DROP TRIGGER IF EXISTS ml_run_snapshot_immutable ON ml_run_snapshot;
CREATE TRIGGER ml_run_snapshot_immutable BEFORE UPDATE OR DELETE ON ml_run_snapshot FOR EACH ROW EXECUTE FUNCTION reject_cohort_mutation();
INSERT INTO backend_schema_migration(version) VALUES(10) ON CONFLICT DO NOTHING;
