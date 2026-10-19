ALTER TABLE well ADD COLUMN IF NOT EXISTS fund_type text NOT NULL DEFAULT 'operational' CHECK (fund_type IN ('operational','presentation'));
-- Принятие ранее явно зарезервированного показательного фонда.
UPDATE well SET fund_type='presentation' WHERE ngdu=-900 AND department=-900 AND unitek_id BETWEEN -900099 AND -900000;
CREATE INDEX IF NOT EXISTS well_fund_type ON well(fund_type,id);
INSERT INTO backend_schema_migration(version) VALUES (6) ON CONFLICT DO NOTHING;
