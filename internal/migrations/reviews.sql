CREATE TABLE IF NOT EXISTS case_review (
 kind text NOT NULL CHECK (kind IN ('check','alert')),
 target_id text NOT NULL,
 check_id text REFERENCES manual_check(id) ON DELETE RESTRICT,
 alert_id bigint REFERENCES ml_alert(id) ON DELETE RESTRICT,
 well_id bigint REFERENCES well(id) ON DELETE RESTRICT,
 state jsonb NOT NULL,
 revision bigint NOT NULL DEFAULT 0,
 PRIMARY KEY(kind,target_id),
 CHECK ((kind='check' AND check_id IS NOT NULL AND check_id=target_id AND alert_id IS NULL) OR (kind='alert' AND alert_id IS NOT NULL AND alert_id::text=target_id AND check_id IS NULL))
);
CREATE TABLE IF NOT EXISTS review_event (
 kind text NOT NULL,
 target_id text NOT NULL,
 revision bigint NOT NULL,
 request_id text NOT NULL,
 fingerprint text NOT NULL,
 created_at timestamptz NOT NULL,
 actor jsonb NOT NULL,
 comment text NOT NULL,
 before_state jsonb NOT NULL,
 after_state jsonb NOT NULL,
 PRIMARY KEY(kind,target_id,revision),
 UNIQUE(kind,target_id,request_id),
 FOREIGN KEY(kind,target_id) REFERENCES case_review(kind,target_id) ON DELETE RESTRICT
);
CREATE INDEX IF NOT EXISTS case_review_well ON case_review(well_id);
INSERT INTO backend_schema_migration(version) VALUES (5) ON CONFLICT DO NOTHING;
