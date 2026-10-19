-- Внутренний ID Pulse уже назначается identity sequence.
-- Существующие внешние ID сохраняются как необязательные связи с источниками.
ALTER TABLE well ALTER COLUMN unitek_id DROP NOT NULL;
ALTER TABLE well ALTER COLUMN armits_id DROP NOT NULL;
INSERT INTO backend_schema_migration(version) VALUES (3) ON CONFLICT DO NOTHING;
