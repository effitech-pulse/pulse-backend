CREATE TABLE IF NOT EXISTS auth_user (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 login text NOT NULL,
 name text NOT NULL,
 password_hash text NOT NULL,
 role text NOT NULL CHECK (role IN ('admin','technologist','viewer')),
 scope jsonb NOT NULL,
 active boolean NOT NULL DEFAULT true,
 revision bigint NOT NULL DEFAULT 1,
 created_at timestamptz NOT NULL DEFAULT now(),
 last_login timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS auth_user_login ON auth_user(lower(login));
CREATE TABLE IF NOT EXISTS auth_session (
 token_hash text PRIMARY KEY,
 user_id bigint NOT NULL REFERENCES auth_user(id) ON DELETE CASCADE,
 created_at timestamptz NOT NULL DEFAULT now(),
 expires_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS auth_session_user ON auth_session(user_id);
CREATE INDEX IF NOT EXISTS auth_session_expiry ON auth_session(expires_at);
CREATE TABLE IF NOT EXISTS auth_identity (
 provider_id text NOT NULL,
 subject text NOT NULL,
 user_id bigint NOT NULL REFERENCES auth_user(id) ON DELETE CASCADE,
 PRIMARY KEY(provider_id,subject)
);
CREATE INDEX IF NOT EXISTS auth_identity_user ON auth_identity(user_id);
INSERT INTO backend_schema_migration(version) VALUES (7) ON CONFLICT DO NOTHING;
