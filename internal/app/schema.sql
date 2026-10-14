-- Снимок миграций 0001..0012; только для новой БД.


CREATE TABLE alembic_version (
    version_num VARCHAR(32) NOT NULL, 
    CONSTRAINT alembic_version_pkc PRIMARY KEY (version_num)
);

-- Running upgrade  -> 0001_baseline

CREATE TABLE well (
    id BIGINT GENERATED ALWAYS AS IDENTITY, 
    unitek_id BIGINT NOT NULL, 
    armits_id BIGINT NOT NULL, 
    ngdu INTEGER NOT NULL, 
    department INTEGER NOT NULL, 
    active BOOLEAN DEFAULT true NOT NULL, 
    CONSTRAINT pk_well PRIMARY KEY (id), 
    CONSTRAINT uq_well_unitek_id UNIQUE (unitek_id), 
    CONSTRAINT uq_well_armits_id UNIQUE (armits_id)
);

CREATE TABLE prediction (
    well_id BIGINT NOT NULL, 
    status VARCHAR(100) NOT NULL, 
    parameters JSONB NOT NULL, 
    processing_date TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now() NOT NULL, 
    CONSTRAINT fk_predictions_well FOREIGN KEY(well_id) REFERENCES well (id) ON DELETE CASCADE, 
    CONSTRAINT uq_predictions_well_processing UNIQUE (well_id, processing_date)
);

CREATE TABLE run_archive (
    id BIGINT GENERATED ALWAYS AS IDENTITY, 
    dag_id VARCHAR(250) NOT NULL, 
    run_id VARCHAR(250) NOT NULL, 
    execution_date TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    well_id BIGINT, 
    ngdu INTEGER, 
    department INTEGER, 
    status_type VARCHAR(50) NOT NULL, 
    severity VARCHAR(20) NOT NULL, 
    source VARCHAR(100), 
    message TEXT, 
    parameters JSONB, 
    artifacts_path TEXT, 
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now() NOT NULL, 
    PRIMARY KEY (id), 
    CONSTRAINT fk_run_archive_well FOREIGN KEY(well_id) REFERENCES well (id) ON DELETE SET NULL
);

CREATE INDEX idx_run_archive_well ON run_archive (well_id);

CREATE INDEX idx_run_archive_execution ON run_archive (execution_date);

CREATE INDEX idx_run_archive_status ON run_archive (status_type);

CREATE INDEX idx_run_archive_run ON run_archive (dag_id, run_id);

CREATE UNIQUE INDEX uq_run_archive_event ON run_archive (dag_id, run_id, well_id, source);

CREATE TABLE complication_reference (
    id BIGINT GENERATED ALWAYS AS IDENTITY, 
    well_id BIGINT, 
    code VARCHAR(50) NOT NULL, 
    name VARCHAR(255) NOT NULL, 
    category VARCHAR(100), 
    description TEXT, 
    active BOOLEAN DEFAULT true NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now() NOT NULL, 
    PRIMARY KEY (id), 
    CONSTRAINT fk_complication_well FOREIGN KEY(well_id) REFERENCES well (id) ON DELETE SET NULL
);

CREATE INDEX idx_complication_well ON complication_reference (well_id);

CREATE INDEX idx_complication_code ON complication_reference (code);

CREATE TABLE department (
    id BIGINT GENERATED ALWAYS AS IDENTITY, 
    name VARCHAR(255) NOT NULL, 
    PRIMARY KEY (id)
);

INSERT INTO alembic_version (version_num) VALUES ('0001_baseline') RETURNING alembic_version.version_num;

-- Running upgrade 0001_baseline -> 0002_schema_improvements

ALTER TABLE prediction ADD COLUMN id BIGINT GENERATED ALWAYS AS IDENTITY;

ALTER TABLE prediction ADD CONSTRAINT pk_prediction PRIMARY KEY (id);

CREATE INDEX idx_prediction_well_date ON prediction (well_id, processing_date);

ALTER TABLE prediction ALTER COLUMN processing_date TYPE DATE USING processing_date::date;

ALTER TABLE well ADD CONSTRAINT fk_well_department FOREIGN KEY(department) REFERENCES department (id);

UPDATE alembic_version SET version_num='0002_schema_improvements' WHERE alembic_version.version_num = '0001_baseline';

-- Running upgrade 0002_schema_improvements -> 0003_notification_rule

CREATE TABLE notification_rule (
    id BIGINT GENERATED ALWAYS AS IDENTITY, 
    status_types JSONB NOT NULL, 
    severity VARCHAR(50), 
    recipients TEXT NOT NULL, 
    subject_template VARCHAR(500) NOT NULL, 
    priority INTEGER DEFAULT '100' NOT NULL, 
    active BOOLEAN DEFAULT true NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now() NOT NULL, 
    CONSTRAINT pk_notification_rule PRIMARY KEY (id)
);

CREATE INDEX idx_notification_rule_priority ON notification_rule (priority);

UPDATE alembic_version SET version_num='0003_notification_rule' WHERE alembic_version.version_num = '0002_schema_improvements';

-- Running upgrade 0003_notification_rule -> 0004_well_number

ALTER TABLE well ADD COLUMN well_number VARCHAR(50);

UPDATE alembic_version SET version_num='0004_well_number' WHERE alembic_version.version_num = '0003_notification_rule';

-- Running upgrade 0004_well_number -> 0005_drop_well_department_fk

ALTER TABLE well DROP CONSTRAINT fk_well_department;

UPDATE alembic_version SET version_num='0005_drop_well_department_fk' WHERE alembic_version.version_num = '0004_well_number';

-- Running upgrade 0005_drop_well_department_fk -> 0006_department_user_id

ALTER TABLE department ALTER COLUMN id DROP IDENTITY IF EXISTS;

UPDATE alembic_version SET version_num='0006_department_user_id' WHERE alembic_version.version_num = '0005_drop_well_department_fk';

-- Running upgrade 0006_department_user_id -> 0007_incident

CREATE TABLE incident (
    id BIGINT GENERATED ALWAYS AS IDENTITY, 
    well_id BIGINT NOT NULL, 
    failure_type VARCHAR(100) NOT NULL, 
    failure_date DATE NOT NULL, 
    days_before INTEGER DEFAULT '60' NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now() NOT NULL, 
    CONSTRAINT pk_incident PRIMARY KEY (id), 
    CONSTRAINT fk_incident_well FOREIGN KEY(well_id) REFERENCES well (id) ON DELETE CASCADE
);

CREATE INDEX idx_incident_well ON incident (well_id);

UPDATE alembic_version SET version_num='0007_incident' WHERE alembic_version.version_num = '0006_department_user_id';

-- Running upgrade 0007_incident -> 0008_action_log

CREATE TABLE action_log (
    id BIGINT GENERATED ALWAYS AS IDENTITY, 
    ts TIMESTAMP WITHOUT TIME ZONE DEFAULT now() NOT NULL, 
    actor VARCHAR(100), 
    method VARCHAR(10) NOT NULL, 
    path VARCHAR(300) NOT NULL, 
    status_code INTEGER NOT NULL, 
    CONSTRAINT pk_action_log PRIMARY KEY (id)
);

CREATE INDEX idx_action_log_ts ON action_log (ts);

UPDATE alembic_version SET version_num='0008_action_log' WHERE alembic_version.version_num = '0007_incident';

-- Running upgrade 0008_action_log -> 0009_ngdu

CREATE TABLE ngdu (
    id BIGINT NOT NULL, 
    name VARCHAR(255) NOT NULL, 
    CONSTRAINT pk_ngdu PRIMARY KEY (id)
);

UPDATE alembic_version SET version_num='0009_ngdu' WHERE alembic_version.version_num = '0008_action_log';

-- Running upgrade 0009_ngdu -> 0010_notification_groups

CREATE TABLE recipient_group (
    id BIGINT GENERATED ALWAYS AS IDENTITY, 
    name VARCHAR(255) NOT NULL, 
    description TEXT, 
    members JSONB NOT NULL, 
    active BOOLEAN DEFAULT true NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now() NOT NULL, 
    CONSTRAINT pk_recipient_group PRIMARY KEY (id), 
    CONSTRAINT uq_recipient_group_name UNIQUE (name)
);

ALTER TABLE notification_rule ADD COLUMN ngdu JSONB;

ALTER TABLE notification_rule ADD COLUMN department JSONB;

ALTER TABLE notification_rule ADD COLUMN well_ids JSONB;

ALTER TABLE notification_rule ADD COLUMN group_ids JSONB;

ALTER TABLE notification_rule ALTER COLUMN recipients SET NOT NULL;

ALTER TABLE notification_rule ALTER COLUMN recipients SET DEFAULT '';

UPDATE alembic_version SET version_num='0010_notification_groups' WHERE alembic_version.version_num = '0009_ngdu';

-- Running upgrade 0010_notification_groups -> 0011_ml_runs

CREATE TABLE ml_run (
    id BIGINT GENERATED ALWAYS AS IDENTITY, 
    started_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now() NOT NULL, 
    finished_at TIMESTAMP WITHOUT TIME ZONE, 
    status VARCHAR(20) NOT NULL, 
    trigger VARCHAR(20) NOT NULL, 
    wells_scored INTEGER, 
    alerts_count INTEGER, 
    error TEXT, 
    CONSTRAINT pk_ml_run PRIMARY KEY (id)
);

CREATE TABLE ml_alert (
    id BIGINT GENERATED ALWAYS AS IDENTITY, 
    run_id BIGINT NOT NULL, 
    ml_well_id BIGINT NOT NULL, 
    unitek_id BIGINT, 
    app_well_id BIGINT, 
    oilfield VARCHAR(100), 
    risk FLOAT NOT NULL, 
    window_end TIMESTAMP WITHOUT TIME ZONE, 
    status VARCHAR(20) NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE DEFAULT now() NOT NULL, 
    CONSTRAINT pk_ml_alert PRIMARY KEY (id), 
    CONSTRAINT fk_ml_alert_run FOREIGN KEY(run_id) REFERENCES ml_run (id) ON DELETE CASCADE, 
    CONSTRAINT fk_ml_alert_well FOREIGN KEY(app_well_id) REFERENCES well (id) ON DELETE SET NULL
);

CREATE INDEX idx_ml_alert_run ON ml_alert (run_id);

CREATE INDEX idx_ml_alert_unitek ON ml_alert (unitek_id);

UPDATE alembic_version SET version_num='0011_ml_runs' WHERE alembic_version.version_num = '0010_notification_groups';

-- Running upgrade 0011_ml_runs -> 0012_ml_run_drift

ALTER TABLE ml_run ADD COLUMN drift_verdict VARCHAR(20);

ALTER TABLE ml_run ADD COLUMN drift_psi_max FLOAT;

ALTER TABLE ml_run ADD COLUMN drift_top TEXT;

UPDATE alembic_version SET version_num='0012_ml_run_drift' WHERE alembic_version.version_num = '0011_ml_runs';



