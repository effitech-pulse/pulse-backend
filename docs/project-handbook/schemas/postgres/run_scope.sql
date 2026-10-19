ALTER TABLE ml_run ADD COLUMN IF NOT EXISTS profile text;
ALTER TABLE ml_run ADD COLUMN IF NOT EXISTS cohort_version text REFERENCES app_cohort(version) ON DELETE RESTRICT;
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='ml_run_scope_identity' AND conrelid='ml_run'::regclass) THEN
  ALTER TABLE ml_run ADD CONSTRAINT ml_run_scope_identity CHECK (
   (profile IS NULL AND cohort_version IS NULL) OR
   (profile IS NOT NULL AND (
    (profile = 'research' AND cohort_version IS NULL) OR
    (profile = 'cohort' AND cohort_version IS NOT NULL)
   ))
  );
 END IF;
END $$;
INSERT INTO backend_schema_migration(version) VALUES(11) ON CONFLICT DO NOTHING;
