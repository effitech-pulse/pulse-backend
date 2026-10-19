CREATE TABLE IF NOT EXISTS telemetry_source (
    id text PRIMARY KEY,
    config jsonb NOT NULL,
    encrypted_secret text NOT NULL DEFAULT '',
    last_received timestamptz,
    last_measured timestamptz,
    error text NOT NULL DEFAULT '',
    next_poll timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz NOT NULL DEFAULT '-infinity'
);
CREATE TABLE IF NOT EXISTS telemetry_measurement (
    well_id text NOT NULL,
    measured_at timestamptz NOT NULL,
    values jsonb NOT NULL,
    quality text NOT NULL DEFAULT '',
    source_id text NOT NULL,
    PRIMARY KEY (well_id, measured_at)
);
CREATE TABLE IF NOT EXISTS telemetry_source_profile (
    source_id text NOT NULL REFERENCES telemetry_source(id),
    version integer NOT NULL,
    config jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, version)
);
CREATE TABLE IF NOT EXISTS telemetry_collection_log (
    id bigserial PRIMARY KEY,
    timestamp timestamptz NOT NULL DEFAULT now(),
    source_id text NOT NULL,
    accepted integer NOT NULL,
    duplicates integer NOT NULL,
    rejected integer NOT NULL,
    message text NOT NULL
);
CREATE INDEX IF NOT EXISTS telemetry_collection_log_time ON telemetry_collection_log (timestamp DESC);
INSERT INTO backend_schema_migration(version) VALUES (2) ON CONFLICT DO NOTHING;
