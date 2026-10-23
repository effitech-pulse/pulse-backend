-- Применяется один раз под migration lock; повтор Apply не восстанавливает граф.
ALTER TABLE auth_user ADD COLUMN organization_scope jsonb;
ALTER TABLE auth_user ADD COLUMN authz_epoch bigint NOT NULL DEFAULT 1 CHECK(authz_epoch >= 1);
ALTER TABLE auth_session ADD COLUMN authz_epoch bigint NOT NULL DEFAULT 0 CHECK(authz_epoch >= 0);
ALTER TABLE auth_user ADD CONSTRAINT auth_user_organization_shape CHECK (
 organization_scope IS NULL OR COALESCE((
 jsonb_typeof(organization_scope)='object' AND organization_scope ?& ARRAY['kind','ngdu_id','department_id']
 AND organization_scope - ARRAY['kind','ngdu_id','department_id']='{}'::jsonb
 AND organization_scope->>'kind' IN ('none','organization','ngdu','department')
 AND CASE WHEN organization_scope->>'kind' IN ('none','organization') THEN organization_scope->'ngdu_id'='null'::jsonb AND organization_scope->'department_id'='null'::jsonb
 ELSE jsonb_typeof(organization_scope->'ngdu_id')='number' AND (organization_scope->>'ngdu_id') ~ '^-?[0-9]+$' AND (organization_scope->>'ngdu_id')::numeric BETWEEN -9223372036854775808 AND 9223372036854775807
 AND CASE WHEN organization_scope->>'kind'='ngdu' THEN organization_scope->'department_id'='null'::jsonb ELSE jsonb_typeof(organization_scope->'department_id')='number' AND (organization_scope->>'department_id') ~ '^-?[0-9]+$' AND (organization_scope->>'department_id')::numeric BETWEEN -9223372036854775808 AND 9223372036854775807 END END
 ),false));
CREATE TABLE auth_role (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 name text NOT NULL CHECK(char_length(name) BETWEEN 1 AND 120), description text NOT NULL DEFAULT '' CHECK(char_length(description)<=2000),
 builtin_key text UNIQUE, revision bigint NOT NULL DEFAULT 1 CHECK(revision>=1)
);
CREATE TABLE auth_role_permission(role_id bigint NOT NULL REFERENCES auth_role(id) ON DELETE CASCADE, permission_key text NOT NULL, PRIMARY KEY(role_id,permission_key));
CREATE TABLE auth_user_role(user_id bigint NOT NULL REFERENCES auth_user(id) ON DELETE CASCADE, role_id bigint NOT NULL REFERENCES auth_role(id) ON DELETE RESTRICT, PRIMARY KEY(user_id,role_id));
CREATE INDEX auth_user_role_reverse ON auth_user_role(role_id,user_id);
CREATE TABLE auth_group (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 name text NOT NULL CHECK(char_length(name) BETWEEN 1 AND 120), description text NOT NULL DEFAULT '' CHECK(char_length(description)<=2000), revision bigint NOT NULL DEFAULT 1 CHECK(revision>=1)
);
CREATE TABLE auth_group_role(group_id bigint NOT NULL REFERENCES auth_group(id) ON DELETE CASCADE, role_id bigint NOT NULL REFERENCES auth_role(id) ON DELETE RESTRICT, PRIMARY KEY(group_id,role_id));
CREATE INDEX auth_group_role_reverse ON auth_group_role(role_id,group_id);
CREATE TABLE auth_group_member(group_id bigint NOT NULL REFERENCES auth_group(id) ON DELETE CASCADE, user_id bigint NOT NULL REFERENCES auth_user(id) ON DELETE CASCADE, PRIMARY KEY(group_id,user_id));
CREATE INDEX auth_group_member_reverse ON auth_group_member(user_id,group_id);
CREATE TABLE auth_rbac_state(id boolean PRIMARY KEY DEFAULT true CHECK(id), revision bigint NOT NULL DEFAULT 1 CHECK(revision>=1), activated_at timestamptz);
INSERT INTO auth_rbac_state(id) VALUES(true);
CREATE TABLE auth_access_event (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 actor_id bigint NOT NULL REFERENCES auth_user(id) ON DELETE RESTRICT,
 actor_roles jsonb NOT NULL,
 before_state jsonb NOT NULL, after_state jsonb NOT NULL,
 request_id text NOT NULL, snapshot_version text NOT NULL DEFAULT 'access_v1' CHECK(snapshot_version='access_v1'),
 created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO auth_role(name,builtin_key) VALUES('Администратор','admin'),('Технолог','technologist'),('Наблюдатель','viewer');
INSERT INTO auth_role_permission(role_id,permission_key) SELECT r.id,k FROM auth_role r CROSS JOIN unnest(ARRAY['wells.read','wells.create','wells.update','wells.import','wells.export','wells.archive','wells.restore','wells.delete','wells.history.read','telemetry.read','telemetry.import','telemetry.compat.read','telemetry.compat.score','risks.read','risks.explain','risks.export','risks.external.read','checks.read','checks.run','checks.review','filechecks.read','filechecks.run','filechecks.cancel','filechecks.review','filechecks.legacy.run','alerts.read','alerts.review','incidents.read','incidents.create','incidents.delete','references.read','parameters.read','references.create','references.update','references.delete','runs.read','runs.export','runs.start','cohort.runs.read','services.read','metrics.read','analytics.read','audit.read','presentation.scenario.read','integrations.read','integrations.create','integrations.update','integrations.check','integrations.status','integrations.logs.read','integrations.collect','integrations.ingest','collectionjobs.read','collectionjobs.run','collectionjobs.cancel','cohortjobs.read','cohortjobs.run','cohortjobs.cancel','users.read','users.update','users.scope.assign','users.create','roles.read','groups.read','users.roles.assign','users.groups.assign','roles.manage','groups.manage','access.manage_global']) k WHERE r.builtin_key='admin';
INSERT INTO auth_role_permission(role_id,permission_key) SELECT r.id,k FROM auth_role r CROSS JOIN unnest(ARRAY['wells.read','wells.export','wells.history.read','telemetry.read','risks.read','risks.explain','risks.export','checks.read','alerts.read','incidents.read','references.read','parameters.read','cohort.runs.read']) k WHERE r.builtin_key='viewer';
INSERT INTO auth_role_permission(role_id,permission_key) SELECT r.id,k FROM auth_role r CROSS JOIN unnest(ARRAY['wells.read','wells.export','wells.history.read','telemetry.read','risks.read','risks.explain','risks.export','checks.read','alerts.read','incidents.read','references.read','parameters.read','cohort.runs.read','checks.run','checks.review','alerts.review','incidents.create','filechecks.read','filechecks.run','filechecks.cancel','filechecks.review']) k WHERE r.builtin_key='technologist';
INSERT INTO auth_user_role(user_id,role_id) SELECT u.id,r.id FROM auth_user u JOIN auth_role r ON r.builtin_key=u.role;
UPDATE auth_user SET organization_scope = CASE
 WHEN role='admin' THEN jsonb_build_object('kind','organization','ngdu_id',NULL,'department_id',NULL)
 WHEN scope->'ngdus'='[]'::jsonb OR scope->'departments'='[]'::jsonb THEN jsonb_build_object('kind','none','ngdu_id',NULL,'department_id',NULL)
 WHEN COALESCE(scope->'ngdus','null'::jsonb)='null'::jsonb AND COALESCE(scope->'departments','null'::jsonb)='null'::jsonb THEN jsonb_build_object('kind','organization','ngdu_id',NULL,'department_id',NULL)
 WHEN jsonb_typeof(scope->'ngdus')='array' AND jsonb_array_length(scope->'ngdus')=1 AND COALESCE(scope->'departments','null'::jsonb)='null'::jsonb THEN jsonb_build_object('kind','ngdu','ngdu_id',scope->'ngdus'->0,'department_id',NULL)
 WHEN jsonb_typeof(scope->'ngdus')='array' AND jsonb_array_length(scope->'ngdus')=1 AND jsonb_typeof(scope->'departments')='array' AND jsonb_array_length(scope->'departments')=1 THEN jsonb_build_object('kind','department','ngdu_id',scope->'ngdus'->0,'department_id',scope->'departments'->0)
 ELSE NULL END;
INSERT INTO backend_schema_migration(version) VALUES(16);
