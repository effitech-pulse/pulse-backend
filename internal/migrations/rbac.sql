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


-- Snapshot authority отделена от immutable input/output и исторического role.
CREATE FUNCTION valid_job_authorization(v jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE r jsonb; seen jsonb := '[]'::jsonb; k text;
BEGIN
 IF v IS NULL OR jsonb_typeof(v) IS DISTINCT FROM 'object' OR NOT (v ?& ARRAY['version','account_id','epoch','roles']) OR v-ARRAY['version','account_id','epoch','roles']<>'{}'::jsonb OR v->>'version'<>'access_v1' OR jsonb_typeof(v->'version')<>'string' OR jsonb_typeof(v->'roles') IS DISTINCT FROM 'array' THEN RETURN false; END IF;
 FOREACH k IN ARRAY ARRAY['account_id','epoch'] LOOP
  IF jsonb_typeof(v->k) IS DISTINCT FROM 'number' OR (v->>k)!~'^[1-9][0-9]*$' THEN RETURN false; END IF;
  IF (v->>k)::numeric>9223372036854775807 THEN RETURN false; END IF;
 END LOOP;
 FOR r IN SELECT value FROM jsonb_array_elements(v->'roles') LOOP
  IF jsonb_typeof(r) IS DISTINCT FROM 'object' OR NOT (r ?& ARRAY['id','name','revision','builtin_key']) OR r-ARRAY['id','name','revision','builtin_key']<>'{}'::jsonb OR jsonb_typeof(r->'name') IS DISTINCT FROM 'string' OR jsonb_typeof(r->'builtin_key') NOT IN ('string','null') THEN RETURN false; END IF;
  FOREACH k IN ARRAY ARRAY['id','revision'] LOOP
   IF jsonb_typeof(r->k) IS DISTINCT FROM 'number' OR (r->>k)!~'^[1-9][0-9]*$' THEN RETURN false; END IF;
   IF (r->>k)::numeric>9223372036854775807 THEN RETURN false; END IF;
  END LOOP;
  IF seen @> jsonb_build_array(r->'id') THEN RETURN false; END IF;
  seen := seen || jsonb_build_array(r->'id');
 END LOOP;
 RETURN true;
END $$;
ALTER TABLE file_job ADD COLUMN actor_authorization jsonb;
ALTER TABLE file_job ADD CONSTRAINT file_job_authorization_shape CHECK(actor_authorization IS NULL OR COALESCE(valid_job_authorization(actor_authorization) AND (actor_authorization->>'account_id')::bigint=actor_account_id,false));
ALTER TABLE cohort_job ADD COLUMN actor_authorization jsonb;
ALTER TABLE cohort_job ADD CONSTRAINT cohort_job_authorization_shape CHECK(actor_authorization IS NULL OR COALESCE(valid_job_authorization(actor_authorization) AND (actor_authorization->>'account_id')::bigint=actor_account_id,false));
ALTER TABLE cohort_job DROP CONSTRAINT cohort_job_actor_role_check;
ALTER TABLE cohort_job ADD CONSTRAINT cohort_job_actor_role_access CHECK ((actor_authorization IS NULL AND actor_role='admin') OR (actor_authorization IS NOT NULL AND valid_job_authorization(actor_authorization) AND actor_role IN ('admin','technologist','viewer')));
ALTER TABLE ml_run ADD COLUMN execution_actor text NOT NULL DEFAULT 'historical';
ALTER TABLE ml_run ADD COLUMN actor_authorization jsonb;
ALTER TABLE ml_run ADD CONSTRAINT ml_run_authorization_shape CHECK ((execution_actor='account' AND actor_authorization IS NOT NULL AND COALESCE(valid_job_authorization(actor_authorization),false)) OR (execution_actor IN ('historical','system_ml','legacy_http','cohort_job') AND actor_authorization IS NULL));
ALTER TABLE telemetry_source ADD COLUMN writer_execution_actor text;
ALTER TABLE telemetry_source ADD COLUMN writer_authorization jsonb;
ALTER TABLE telemetry_source ADD CONSTRAINT telemetry_source_authorization_shape CHECK (
 (writer_execution_actor IS NULL AND writer_authorization IS NULL) OR
 (writer_execution_actor IS NOT NULL AND writer_token_sha256 IS NOT NULL AND ((writer_execution_actor='account' AND writer_authorization IS NOT NULL AND COALESCE(valid_job_authorization(writer_authorization),false)) OR (writer_execution_actor IN ('system_collect','legacy_http') AND writer_authorization IS NULL)))
);
ALTER TABLE collection_job ADD CONSTRAINT collection_job_authorization_shape CHECK (
 NOT (intent ? 'authorization') OR COALESCE(valid_job_authorization(intent->'authorization') AND (intent->'authorization'->>'account_id')::bigint=actor_account_id AND (intent->'authorization'->>'account_id')::bigint=(intent->>'actor_account_id')::bigint,false)
);
CREATE FUNCTION file_job_rbac_authority_immutable_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF ROW(NEW.actor_account_id,NEW.actor_authorization,NEW.well_id,NEW.well_authority) IS DISTINCT FROM ROW(OLD.actor_account_id,OLD.actor_authorization,OLD.well_id,OLD.well_authority) THEN RAISE EXCEPTION 'immutable job authority'; END IF; RETURN NEW; END $$;
CREATE TRIGGER file_job_rbac_authority_immutable BEFORE UPDATE ON file_job FOR EACH ROW EXECUTE FUNCTION file_job_rbac_authority_immutable_fn();
CREATE FUNCTION cohort_job_rbac_authority_immutable_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF ROW(NEW.actor_account_id,NEW.actor_authorization) IS DISTINCT FROM ROW(OLD.actor_account_id,OLD.actor_authorization) THEN RAISE EXCEPTION 'immutable job authority'; END IF; RETURN NEW; END $$;
CREATE TRIGGER cohort_job_rbac_authority_immutable BEFORE UPDATE ON cohort_job FOR EACH ROW EXECUTE FUNCTION cohort_job_rbac_authority_immutable_fn();
CREATE FUNCTION ml_run_rbac_authority_immutable_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF ROW(NEW.execution_actor,NEW.actor_authorization) IS DISTINCT FROM ROW(OLD.execution_actor,OLD.actor_authorization) THEN RAISE EXCEPTION 'immutable run authority'; END IF; RETURN NEW; END $$;
CREATE TRIGGER ml_run_rbac_authority_immutable BEFORE UPDATE ON ml_run FOR EACH ROW EXECUTE FUNCTION ml_run_rbac_authority_immutable_fn();
CREATE FUNCTION telemetry_source_rbac_authority_fence_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.writer_token_sha256 IS NOT NULL AND NEW.writer_generation=OLD.writer_generation AND ROW(NEW.writer_execution_actor,NEW.writer_authorization) IS DISTINCT FROM ROW(OLD.writer_execution_actor,OLD.writer_authorization) THEN RAISE EXCEPTION 'immutable writer authority'; END IF; RETURN NEW; END $$;
CREATE TRIGGER telemetry_source_rbac_authority_fence BEFORE UPDATE ON telemetry_source FOR EACH ROW EXECUTE FUNCTION telemetry_source_rbac_authority_fence_fn();


CREATE FUNCTION valid_rbac_int64(v jsonb, positive boolean) RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$ BEGIN
 IF jsonb_typeof(v) IS DISTINCT FROM 'number' OR v::text!~'^-?(0|[1-9][0-9]*)$' THEN RETURN false; END IF;
 RETURN v::text::numeric BETWEEN CASE WHEN positive THEN 1 ELSE -9223372036854775808 END AND 9223372036854775807;
END $$;
CREATE FUNCTION valid_file_job_well_authority(v jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE k text;
BEGIN
 IF v IS NULL OR jsonb_typeof(v) IS DISTINCT FROM 'object' OR NOT (v ?& ARRAY['version','well_id','revision','fund_type','ngdu','department','active','archived_at','unitek_id','armits_id']) OR v-ARRAY['version','well_id','revision','fund_type','ngdu','department','active','archived_at','unitek_id','armits_id']<>'{}'::jsonb OR jsonb_typeof(v->'version') IS DISTINCT FROM 'string' OR jsonb_typeof(v->'fund_type') IS DISTINCT FROM 'string' OR v->>'version'<>'file_job_well_v1' OR v->>'fund_type' NOT IN ('operational','presentation') OR jsonb_typeof(v->'active') IS DISTINCT FROM 'boolean' THEN RETURN false; END IF;
 FOREACH k IN ARRAY ARRAY['well_id','revision','ngdu','department'] LOOP
  IF NOT valid_rbac_int64(v->k,k IN ('well_id','revision')) THEN RETURN false; END IF;
 END LOOP;
 FOREACH k IN ARRAY ARRAY['unitek_id','armits_id'] LOOP
  IF jsonb_typeof(v->k)<>'null' AND NOT valid_rbac_int64(v->k,false) THEN RETURN false; END IF;
 END LOOP;
 IF jsonb_typeof(v->'archived_at')<>'null' THEN
  IF jsonb_typeof(v->'archived_at')<>'string' OR (v->>'archived_at')!~'^[0-9]{4}-(0[1-9]|1[0-2])-[0-3][0-9]T([01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](\.[0-9]{1,9})?(Z|[+-][0-2][0-9]:[0-5][0-9])$' THEN RETURN false; END IF;
  BEGIN PERFORM (v->>'archived_at')::timestamptz; EXCEPTION WHEN OTHERS THEN RETURN false; END;
 END IF;
 RETURN true;
END $$;
ALTER TABLE file_job ADD COLUMN well_authority jsonb;
ALTER TABLE file_job ADD CONSTRAINT file_job_well_authority_shape CHECK(well_authority IS NULL OR COALESCE(valid_file_job_well_authority(well_authority) AND (well_authority->>'well_id')::bigint=well_id,false));
ALTER TABLE file_job ADD CONSTRAINT file_job_authority_frames CHECK((actor_authorization IS NULL)=(well_authority IS NULL));
INSERT INTO backend_schema_migration(version) VALUES(16);
