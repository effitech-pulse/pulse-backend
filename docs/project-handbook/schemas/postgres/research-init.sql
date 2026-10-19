-- Реляционная схема PostgreSQL для grant: справочники (НГДУ / ЦДНГ / установки)
-- + суточные замеры дебита (ARMITS). Применяется при первом старте контейнера
-- (монтируется в /docker-entrypoint-initdb.d/).
--
-- Разделение хранилищ: плотная телеметрия → ClickHouse (telemetry.telemetry_raw),
-- разреженные замеры/справочники → PostgreSQL (здесь).

-- НГДУ (нефтегазодобывающее управление)
CREATE TABLE IF NOT EXISTS ngdu (
    id   SMALLINT PRIMARY KEY,   -- код НГДУ (10, 20, 30, ...) как естественный ключ
    name TEXT
);

-- ЦДНГ (цех добычи нефти и газа). Готова к заполнению, когда появятся данные.
CREATE TABLE IF NOT EXISTS cdng (
    id      SERIAL PRIMARY KEY,
    ngdu_id SMALLINT NOT NULL REFERENCES ngdu(id),
    code    INT,
    name    TEXT,
    UNIQUE (ngdu_id, code)
);

-- Установка (скважина с УЭЦН) — справочник.
CREATE TABLE IF NOT EXISTS well (
    id          BIGSERIAL PRIMARY KEY,
    ngdu_id     SMALLINT REFERENCES ngdu(id),
    cdng_id     INT      REFERENCES cdng(id),
    armits_id   BIGINT UNIQUE,
    unitek_id   BIGINT UNIQUE,
    well_number TEXT,
    pump_type   TEXT,              -- тип насоса (пока нет данных — NULL)
    depth_m     DOUBLE PRECISION,  -- глубина спуска (пока нет данных — NULL)
    created_at  TIMESTAMP NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_well_ngdu ON well (ngdu_id);

-- Суточные замеры дебита (ARMITS, qg).
CREATE TABLE IF NOT EXISTS measurement (
    id          BIGSERIAL PRIMARY KEY,
    well_id     BIGINT NOT NULL REFERENCES well(id) ON DELETE CASCADE,
    measured_at DATE   NOT NULL,            -- dat_zam
    qg          DOUBLE PRECISION,           -- фактический дебит жидкости, м³/сут (qg)
    qg_tex      DOUBLE PRECISION,           -- технологический/ожидаемый дебит жидкости (qg_tex)
    qn          DOUBLE PRECISION,           -- дебит нефти, т/сут (qn_tex_tek)
    source      VARCHAR(100),               -- si_nam (ГЗУ и т.п.)
    created_at  TIMESTAMP NOT NULL DEFAULT now(),
    CONSTRAINT uq_measurement UNIQUE (well_id, measured_at, source)
);
CREATE INDEX IF NOT EXISTS idx_measurement_well_date ON measurement (well_id, measured_at DESC);

-- Справочник типов отказов/осложнений (13 типов технологов + норма).
CREATE TABLE IF NOT EXISTS complication_reference (
    id          SERIAL PRIMARY KEY,
    code        VARCHAR(50) UNIQUE NOT NULL,
    num         SMALLINT,                 -- номер типа по таблице технологов (1..13), NULL для нормы
    name        TEXT NOT NULL,
    category    VARCHAR(50),              -- бакет динамики: SLOW/MECHANICAL/FAST/SYSTEM/LEAK/NORMAL
    description TEXT,                     -- сигнатура технолога (по чему распознаётся)
    active      BOOLEAN NOT NULL DEFAULT TRUE
);
