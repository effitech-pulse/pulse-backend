# Данные и схемы хранения

Текущая source schema15 additive: source writer fence/token/profile,
nullable `telemetry_measurement.collection_provenance`, collection guard/job/hold/
capture/checkpoint/coverage/outbox/inbox/action. Global wide-row PK прежний.
Same full provenance+payload → identical; unattributed old row не доказывает replay.
Coverage receipt конкретной revision/interval отличается от nullable maximum timestamp;
C2 не продвигает legacy last_measured. [Протокол](../references/q2-collection-jobs.md),
[DDL](../schemas/postgres/collection_jobs.sql). Explicit migration не активирует PREP.


Q0 schema12 добавляет `file_input`, `file_input_payload`, `file_input_budget`, `file_check_result`: raw/canonical bytes и exact typed identity immutable; metadata/list не читают payload. New acceptance требует `file_parser_v2` с embedded6sources/actual9dependencies, schema/config hashes; historical v1 metadata/result читаются без backfill. [Schema12](../schemas/postgres/file_inputs.sql), [архитектура](../references/q0-architecture.md), [synthetic RUN-2](../references/q0-runtime.md). Эти primitives не включены на actual PREP и не означают разрешение хранения реальных частных файлов.
## Разделение хранилищ

| Хранилище | Что хранится | Кто использует |
|---|---|---|
| PostgreSQL `esp_app` | Пользовательский реестр, привязки, сеансы, измерения сборщика, проверки, заключения, прогоны и аудит | Backend |
| PostgreSQL `esp` | Исследовательские `well/ngdu/cdng`, дебит `measurement`, справочник осложнений | ML, исследовательские CLI, loaders |
| ClickHouse `telemetry.telemetry_raw` | LONG-поток отдельных событий каналов с deadband | Pulse Data, model I/O |
| workspace-data | Parquet/CSV-кэши, manifest, модели, сценарии и отчёты | Model, Data, ML |

Одинаковое имя таблицы `well` в esp и esp_app не означает одинаковый namespace ID. Во всех внешних API приложения основным является ID Pulse. `unitek_id`, `armits_id`, исследовательский `well_id`, `device_id` ClickHouse и внутренний `pulse:<id>` телеметрии различаются.

## Операционные сущности

| Сущность | Ключ / связи | Существенные правила |
|---|---|---|
| well | bigint id | Внешние ID nullable; active не равен archived_at; revision возрастает |
| telemetry_source | text id | config JSONB, отдельные актуальные времена, error, lease_until/next_poll |
| telemetry_source_profile | source_id + version | История сопоставлений; версия увеличивается при сохранении |
| telemetry_measurement | well_id(text) + measured_at | JSONB каналов; конфликт не перезаписывает существующую точку |
| telemetry_collection_log | bigint id | Принято/дубли/отклонено, время, source_id, сообщение |
| manual_check | text id; FK well RESTRICT | Уникальность well_id + request_id; период, SHA256 входа, полный result |
| case_review | kind + target_id | Текущее решение по check или alert и revision |
| review_event | kind + target_id + revision | Автор, комментарий, before/after, request_id/fingerprint |
| well_event | well_id + revision | metadata/archive/restore; причина, автор, before/after |
| auth_user/session/identity | id / token_hash / provider_id+subject | Пароль bcrypt, digest сеанса, внешняя привязка |
| app_cohort / app_cohort_member | version / version+well_id | Неизменяемая версия/hash и membership существующих ID Pulse, независимо от lifecycle |
| ml_run / ml_alert | bigint id | Попытка с profile/cohort_version с момента Begin; тревоги и отдельная обработка |
| ml_run_snapshot | run_id / unique snapshot_id | Неизменяемый полный cohort output, as_of, output_sha256, provenance status |
| action_log | bigint id | Технический HTTP-аудит |

Полные DDL находятся в [schemas/postgres](../schemas/postgres/schema.sql). Дополнительные файлы являются миграциями, а не альтернативными схемами. Старые `notification_rule` и `recipient_group` в базовом DDL не доказывают наличие работающей рассылки: соответствующая доставка в текущей системе не реализована.

## ClickHouse

Поля LONG-таблицы: `well_id UInt64`, `device_id UInt64`, `param LowCardinality(String)`, `ts DateTime64(3,'UTC')`, `value Float64`, `ingested_at DateTime`. Движок — ReplacingMergeTree; партиция по месяцу, сортировка `(well_id,param,ts)`. Дедупликация движка происходит при слияниях и не означает немедленной уникальности любого чтения. Конкретные запросы должны соблюдать семантику архива.

Схема и ключ сортировки приведены в [ClickHouse DDL](../schemas/clickhouse/init.sql). Плотная сетка не сохраняется вместо сырых событий; агрегация задаётся при чтении. Pulse Data проверяет карту `device_id → research well_id`, отбирая только подтверждённые audit-сопоставления.

## Параметры и качество

Канонический набор — large.json в `pulse-model/esp/params`. В alias есть префикс регистра (`4x010D F_out`); canonical name — `F_out`. Парсер может выдавать русское displayname; сначала выполняется канонизация, затем правила.

Маркеры 9999/999.9 очищаются по параметру до агрегации. Они не являются универсальным запретом любого значения 9999. F_out=0, ток=0 и дебит=0 при остановке — допустимые нули. `server_parameter.is_set=true` и `set_*` относятся к уставкам и исключаются из измерительных признаков.

Часовой архив Pulse Data использует последнее событие в бакете, правую границу интервала и очистку до argMax. Не заполняет пропуски сквозным ffill. Deadband сам по себе не доказывает ни потерю связи, ни исправную связь; для диагностики учитываются время и состояние ТМС.

## Миграции

| Версия | Файл | Изменение |
|---|---|---|
| 1 | schema.sql | Базовая операционная схема / принятие проверенного legacy Alembic head |
| 2 | integrations.sql | Источники, профили, измерения и журналы |
| 3 | well_identity.sql | Внешние ID становятся необязательными |
| 4 | manual_checks.sql | Сохранённые ручные проверки |
| 5 | reviews.sql | Версии решений и предметная история |
| 6 | fund_type.sql | Разделение operational/presentation |
| 7 | access.sql | Пользователи, сеансы и внешние identity |
| 8 | well_lifecycle.sql | Revision, responsible, archived_at и well_event |
| 9 | [cohort.sql](../schemas/postgres/cohort.sql) | app_cohort/member, неизменяемая версия и membership |
| 10 | [ml_snapshot.sql](../schemas/postgres/ml_snapshot.sql) | Полный immutable output snapshot и digest |
| 11 | [run_scope.sql](../schemas/postgres/run_scope.sql) | Nullable profile/cohort_version у попытки с Begin; старые строки без backfill |

Миграции выполняются только явно через `cmd/migrate`. Apply работает в транзакции и берёт advisory lock. Существующая схема принимается только при проверенном legacy head `0012_ml_run_drift`; произвольная база без версии не принимается автоматически. Автоматического rollback-комплекта down-миграций нет: перед обновлением нужен проверенный backup.

## Output и входы PREP

Полный snapshot сохраняет outcomes всех участников, включая non-scored с result=null; reader выдаёт только разрешённое пересечение. Обработка alert/status читается отдельно и не изменяет decision JSON/digest. `ml_run.profile/cohort_version` фиксируются до операций, которые могут завершиться ошибкой: `running`/`error` попытка различима даже без snapshot. Совместимы только пары unknown `(NULL,NULL)`, research `(research,NULL)` и cohort `(cohort,version)`; существующие строки не получают текущий профиль задним числом.

Immutable output не равен полному воспроизводимому вычислению: точные входные CSV/архивные bytes не сохраняются, поэтому provenance partial. В публичном комплекте только DDL, протоколы и агрегированные технические свидетельства; membership CSV, реальные bindings, dump, телеметрия и секреты не публикуются.
