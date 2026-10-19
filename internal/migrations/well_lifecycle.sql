ALTER TABLE well ADD COLUMN IF NOT EXISTS revision bigint NOT NULL DEFAULT 1;
ALTER TABLE well ADD COLUMN IF NOT EXISTS archived_at timestamptz;
ALTER TABLE well ADD COLUMN IF NOT EXISTS responsible text;
CREATE INDEX IF NOT EXISTS well_archive ON well(archived_at,id);
CREATE TABLE IF NOT EXISTS well_event (
 well_id bigint NOT NULL REFERENCES well(id) ON DELETE RESTRICT,
 revision bigint NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 kind text NOT NULL CHECK (kind IN ('metadata','archive','restore')),
 actor jsonb NOT NULL,
 reason text NOT NULL,
 before_state jsonb NOT NULL,
 after_state jsonb NOT NULL,
 PRIMARY KEY(well_id,revision)
);
INSERT INTO backend_schema_migration(version) VALUES(8) ON CONFLICT DO NOTHING;
