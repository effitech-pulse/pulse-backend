-- Q0 additive immutable synthetic snapshots; no jobs, outbox or GC.
CREATE TABLE IF NOT EXISTS file_input (
 id text PRIMARY KEY CHECK (id ~ '^[0-9a-f]{32}$'),
 well_id bigint NOT NULL REFERENCES well(id) ON DELETE RESTRICT,
 actor_account_id bigint NOT NULL REFERENCES auth_user(id) ON DELETE RESTRICT,
 actor jsonb NOT NULL CHECK (octet_length(actor::text)<=8192),
 operation_kind text NOT NULL DEFAULT 'file_check' CHECK (operation_kind='file_check'),
 request_key text NOT NULL CHECK (request_key ~ '^[A-Za-z0-9_-]{16,80}$'),
 source_kind text NOT NULL CHECK (source_kind='uploaded_file'),
 created_at timestamptz NOT NULL,
 captured_at timestamptz NOT NULL,
 request_commitment_sha256 text NOT NULL CHECK (request_commitment_sha256 ~ '^sha256:[0-9a-f]{64}$'),
 fingerprint_sha256 text NOT NULL CHECK (fingerprint_sha256 ~ '^sha256:[0-9a-f]{64}$'),
 raw_sha256 text NOT NULL CHECK (raw_sha256 ~ '^sha256:[0-9a-f]{64}$'),
 canonical_sha256 text NOT NULL CHECK (canonical_sha256 ~ '^sha256:[0-9a-f]{64}$'),
 parse_recipe_sha256 text NOT NULL CHECK (parse_recipe_sha256 ~ '^sha256:[0-9a-f]{64}$'),
 schema_sha256 text NOT NULL CHECK (schema_sha256 ~ '^sha256:[0-9a-f]{64}$'),
 parse_spec jsonb NOT NULL CHECK (octet_length(parse_spec::text)<=65536 AND jsonb_typeof(parse_spec)='object' AND COALESCE(parse_spec->>'version'='file_input_v1',false) AND COALESCE(parse_spec->>'format' IN ('csv','xlsx'),false) AND COALESCE(parse_spec->>'schema_sha256'=schema_sha256,false)),
 parse_identity_bytes bytea NOT NULL CHECK (octet_length(parse_identity_bytes) BETWEEN 1 AND 65536),
 execution_pin jsonb NOT NULL CHECK (octet_length(execution_pin::text)<=65536 AND jsonb_typeof(execution_pin)='object' AND COALESCE(execution_pin->>'version'='upload_pin_v1',false) AND COALESCE(execution_pin->>'bundle_sha256' ~ '^sha256:[0-9a-f]{64}$',false)),
 parse_recipe jsonb NOT NULL CHECK (octet_length(parse_recipe::text)<=8192 AND COALESCE(parse_recipe->>'version' IN ('file_parser_v1','file_parser_v2'),false)),
 raw_length bigint NOT NULL CHECK (raw_length BETWEEN 1 AND 20971520),
 canonical_length bigint NOT NULL CHECK (canonical_length BETWEEN 1 AND 52428800),
 rows_count integer NOT NULL CHECK (rows_count BETWEEN 1 AND 100000),
 columns_count integer NOT NULL CHECK (columns_count BETWEEN 1 AND 128),
 ignored_columns jsonb NOT NULL CHECK (jsonb_typeof(ignored_columns)='array' AND octet_length(ignored_columns::text)<=65536),
 UNIQUE(actor_account_id,operation_kind,request_key),
 UNIQUE(id,raw_length,canonical_length)
);
CREATE INDEX IF NOT EXISTS file_input_well_history ON file_input(well_id,created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS file_input_actor_history ON file_input(actor_account_id,created_at DESC,id DESC);
CREATE TABLE IF NOT EXISTS file_input_payload (
 input_id text PRIMARY KEY,
 raw_length bigint NOT NULL,
 canonical_length bigint NOT NULL,
 raw_bytes bytea NOT NULL CHECK (octet_length(raw_bytes)=raw_length AND raw_length BETWEEN 1 AND 20971520),
 canonical_bytes bytea NOT NULL CHECK (octet_length(canonical_bytes)=canonical_length AND canonical_length BETWEEN 1 AND 52428800),
 FOREIGN KEY(input_id,raw_length,canonical_length) REFERENCES file_input(id,raw_length,canonical_length) ON DELETE RESTRICT
);
CREATE TABLE IF NOT EXISTS file_input_budget (
 budget_key text PRIMARY KEY CHECK (budget_key='global' OR budget_key ~ '^account:[1-9][0-9]*$'),
 retained_bytes bigint NOT NULL DEFAULT 0 CHECK (retained_bytes>=0 AND ((budget_key='global' AND retained_bytes<=268435456) OR (budget_key<>'global' AND retained_bytes<=134217728))),
 retained_count bigint NOT NULL DEFAULT 0 CHECK (retained_count BETWEEN 0 AND 16)
);
INSERT INTO file_input_budget(budget_key) VALUES ('global') ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS file_check_result (
 input_id text PRIMARY KEY REFERENCES file_input(id) ON DELETE RESTRICT,
 fingerprint_sha256 text NOT NULL CHECK (fingerprint_sha256 ~ '^sha256:[0-9a-f]{64}$'),
 execution_pin jsonb NOT NULL CHECK (COALESCE(execution_pin->>'version'='upload_pin_v1',false)),
 computed_at timestamptz NOT NULL,
 result jsonb NOT NULL CHECK (octet_length(result::text)<=2097152 AND jsonb_typeof(result)='object')
);
CREATE OR REPLACE FUNCTION reject_file_input_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'immutable_file_input' USING ERRCODE='55000'; END $$;
DO $$
DECLARE target text;
BEGIN
 FOREACH target IN ARRAY ARRAY['file_input','file_input_payload','file_check_result'] LOOP
  IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass(target) AND tgname='file_input_immutable') THEN
   EXECUTE format('CREATE TRIGGER file_input_immutable BEFORE UPDATE OR DELETE ON %I FOR EACH ROW EXECUTE FUNCTION reject_file_input_mutation()',target);
  END IF;
 END LOOP;
END $$;
INSERT INTO backend_schema_migration(version) VALUES (12) ON CONFLICT DO NOTHING;
