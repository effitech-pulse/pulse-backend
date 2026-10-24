ALTER TABLE auth_user
 ADD COLUMN job_title text CHECK (char_length(job_title)<=200 AND job_title=btrim(job_title) AND job_title<>''),
 ADD COLUMN workplace_organization text CHECK (char_length(workplace_organization)<=200 AND workplace_organization=btrim(workplace_organization) AND workplace_organization<>''),
 ADD COLUMN workplace_division text CHECK (char_length(workplace_division)<=200 AND workplace_division=btrim(workplace_division) AND workplace_division<>''),
 ADD COLUMN workplace_department text CHECK (char_length(workplace_department)<=200 AND workplace_department=btrim(workplace_department) AND workplace_department<>''),
 ADD COLUMN manager_account_id bigint REFERENCES auth_user(id) ON DELETE RESTRICT CHECK(manager_account_id<>id),
 ADD COLUMN profile_revision bigint NOT NULL DEFAULT 1 CHECK(profile_revision>=1);
CREATE INDEX auth_user_manager_account_idx ON auth_user(manager_account_id);
INSERT INTO backend_schema_migration(version) VALUES(17);
