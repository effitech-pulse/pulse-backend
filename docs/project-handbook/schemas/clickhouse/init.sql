-- Схема телеметрии УЭЦН в ClickHouse — LONG (точки-изменения, deadband).
-- Применяется автоматически при первом старте контейнера
-- (монтируется в /docker-entrypoint-initdb.d/).
--
-- Идея: храним КАЖДУЮ реально пришедшую точку каждого параметра как отдельную
-- строку (param, ts, value) — родная дискретность канала 1-в-1, без ffill и без
-- сведения к общей сетке. Любые сетки/ffill/агрегаты считаются НА ЧТЕНИИ в SQL.

CREATE DATABASE IF NOT EXISTS telemetry;

CREATE TABLE IF NOT EXISTS telemetry.telemetry_raw
(
    well_id     UInt64,                                   -- индекс скважины (папка row_idx)
    device_id   UInt64,                                   -- UNITEK_ID
    param       LowCardinality(String),                   -- каноническое имя: F_out, R_izol, Vibration_X...
    ts          DateTime64(3, 'UTC') CODEC(DoubleDelta, ZSTD(1)),
    value       Float64              CODEC(Gorilla, ZSTD(1)),
    ingested_at DateTime DEFAULT now()
)
ENGINE = ReplacingMergeTree(ingested_at)   -- репарсинг того же окна не плодит дубли
PARTITION BY toYYYYMM(ts)
ORDER BY (well_id, param, ts);             -- основной паттерн: канал по скважине во времени

-- Пример реконструкции на чтении (НЕ часть схемы, для справки):
--   сетка 1 мин c last-in-bin (ffill-эквивалент):
--     SELECT well_id, param, toStartOfMinute(ts) m, argMax(value, ts) v
--     FROM telemetry.telemetry_raw GROUP BY well_id, param, m;
--   агрегаты быстрых каналов за час:
--     SELECT param, toStartOfInterval(ts, INTERVAL 1 hour) h,
--            avg(value), stddevPop(value), min(value), max(value)
--     FROM telemetry.telemetry_raw WHERE well_id = ? GROUP BY param, h;
