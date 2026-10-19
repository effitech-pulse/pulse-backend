-- Результаты ручных проверок не меняют ярус или статус тревог фонда.
CREATE TABLE IF NOT EXISTS manual_check (
 id text PRIMARY KEY,
 well_id bigint NOT NULL REFERENCES well(id) ON DELETE RESTRICT,
 request_id text NOT NULL,
 start_at timestamptz NOT NULL,
 end_at timestamptz NOT NULL CHECK (end_at > start_at),
 created_at timestamptz NOT NULL,
 input_sha256 text NOT NULL,
 points_count integer NOT NULL CHECK (points_count > 0),
 resolution text NOT NULL,
 result jsonb NOT NULL,
 UNIQUE (well_id, request_id)
);
CREATE INDEX IF NOT EXISTS manual_check_well_history ON manual_check(well_id, created_at DESC, id DESC);
INSERT INTO backend_schema_migration(version) VALUES (4) ON CONFLICT DO NOTHING;
