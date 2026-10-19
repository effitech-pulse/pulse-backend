CREATE TABLE IF NOT EXISTS app_cohort (
 version text PRIMARY KEY,
 manifest_sha256 text NOT NULL UNIQUE CHECK(length(manifest_sha256)=64),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS app_cohort_member (
 version text NOT NULL REFERENCES app_cohort(version) ON DELETE RESTRICT,
 well_id bigint NOT NULL REFERENCES well(id) ON DELETE RESTRICT,
 unitek_id bigint,
 armits_id bigint,
 revision bigint NOT NULL,
 namespace text NOT NULL DEFAULT 'presentation',
 source text NOT NULL DEFAULT 'existing_catalog_capture',
 PRIMARY KEY(version,well_id),
 UNIQUE(version,unitek_id)
);
CREATE OR REPLACE FUNCTION reject_cohort_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'cohort records are immutable'; END $$;
DROP TRIGGER IF EXISTS app_cohort_immutable ON app_cohort;
CREATE TRIGGER app_cohort_immutable BEFORE UPDATE OR DELETE ON app_cohort FOR EACH ROW EXECUTE FUNCTION reject_cohort_mutation();
DROP TRIGGER IF EXISTS app_cohort_member_immutable ON app_cohort_member;
CREATE TRIGGER app_cohort_member_immutable BEFORE UPDATE OR DELETE ON app_cohort_member FOR EACH ROW EXECUTE FUNCTION reject_cohort_mutation();
INSERT INTO backend_schema_migration(version) VALUES(9) ON CONFLICT DO NOTHING;
