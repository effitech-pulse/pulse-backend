> Снимок документа 23.10.2026. Источник: `pulse-backend/docs/readiness-architecture-2026-10-19.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# PREP: архитектура готовности Pulse, 19.10.2026

Владелец: architect-analyst. Требования: поручение tech-lead и [спецификация PREP](readiness-ledger.md), сверенная после её записи 19.10. Статус: **контракт принят координатором 19.10 для реализации PREP**; runtime/Senior gates ещё открыты. Этот документ сам не включает новый runtime, не мигрирует рабочую БД и не подтверждает качество модели. Уже разрешённый scope не требует повторного одобрения пользователя.

**Goal:** воспроизводимый набор семи репозиториев, проверяемый isolated runtime, единая пользовательская когорта и сохранённые доказательства каждого расчёта.

**Architecture:** расширить существующие Go orchestration, Python Upload и PostgreSQL persistence. Не выделять новый сетевой модуль; обучение остаётся в model, получение телеметрии — в data, бизнес-решения/авторизация/сохранение — в backend.

**Tech stack:** Go/pgx/Squirrel/Gin, Python/pandas/CatBoost, gRPC/protobuf, PostgreSQL, существующие React/Vite и Docker Compose.

**Для исполнителей:** работать пакетами ниже через разрешённый командный процесс; прочитать applicable AGENTS, `test-driven-development` и `executing-plans`/`subagent-driven-development` перед изменением кода. Сохранять чужой diff. Push, release, promotion, пересоздание данных и схем исключены.

## 1. Проверенный текущий поток

| Поток / interface | Реализация / существенное следствие |
|---|---|
| Реестр → карточка → экспорт | `internal/service/catalog.go`, `well_filter.go` → `repository/postgres/wells.go`; сейчас `Principal.Allows` обходит ограничения для admin, общего cohort guard нет |
| Refresh → Score → запись | `service/ml.go:execute` → `adapter/grpcml/client.go` → `pulse_ml/engine.py:score` → `ml_scoring.py:score_all_wells_full`; PostgreSQL advisory lock уже исключает параллельные прогоны |
| Фондовый ML | Score читает **исследовательские** cache/manifest и PG-справочник. Его `well_id`, внешний Unitek ID и ID Pulse — разные namespace |
| Ручная проверка телеметрии | `service/checks.go` → `Integrations.ReadApp` → `telemetryCSV` → `MLClient.Upload`; input SHA256 и immutable result уже сохраняются в `manual_check` |
| Сохранение фонового результата | `repository/postgres/ml.go:lease.Persist` атомарно пишет **только алертные** `ml_alert` и завершает `ml_run`; неалертные результаты и полный Snapshot сейчас теряются |
| History/Explain | читают текущий in-memory ML context; не связаны с прежним `ml_run`; `History.tail(limit)` меняет диагностический fired count |
| 100 показательных скважин | `pulse-data/utils/telemetry_emulator/fleet.py` создаёт external IDs −900000…−900099; `scripts/connect_telemetry_fleet.py` регистрирует их и возвращает назначенные БД ID Pulse. Файла со 100 ID Pulse до capture нет |
| Запуск первого Upload | `engine.ml_upload → get_context()` сейчас может запустить полный research Score; это лишняя зависимость пользовательского файлового сценария от research cache/PG |
| Frontend | `src/api/ml.ts` ожидает research `WellRisk`; адаптация нового cohort envelope должна различать namespaces и отсутствие оценки |
| Auth/runtime | `deployments/compose.local.yaml` явно содержит `AUTH_MODE: legacy`; local sessions/RBAC реализованы отдельно. Health ML не загружает модель |

Семантика трёх RPC документирована в [DOC-04](model-rpc-compatibility.md). Исторические причины и scientific limits — в [causal_baseline](causal-baseline.md), текущее состояние — в [handbook](../chapters/02-status.md). GLOSSARY/ADR каталога для этой области нет; существующие решения handbook не пересматриваются.

**Deepening:** module `FundScope` концентрирует membership, mapping и пересечение с пользовательскими правами за одним interface. Module `decision` концентрирует порог, оператор и накопление окон. Их удаление вернуло бы сложность в множество callers — deletion test пройден. SQL и protobuf adapters остаются на существующих seams. Новые repository/service layers «на будущее» не добавляются. Это даёт locality исправлений и leverage одного набора контрактных тестов.

## 2. Выбранное минимальное решение

1. Не фильтровать research Score до пустого «успешного» результата: его реальные скважины не совпадают с synthetic cohort.
2. Backend cohort refresh переиспользует **ReadApp → telemetryCSV → Upload**, как ручная проверка, с одним `as_of` и immutable outcomes. Исследовательский Score остаётся только в явно выбранном research-профиле.
3. Python Upload загружает bundle без полного research расчёта. Модель не импортирует data; генератор передаёт только измерения через существующий telemetry interface.
4. Необработанные участники получают status/reason и `result=null`; им не назначается нулевой риск или «норма». Полный результат запуска сохраняется атомарно.
5. Один evaluator использует явные protocol/policy; legacy responses сохраняются. Поддержка causal Upload включается только при совместимом feature recipe; до этого он отклоняется явно, без legacy fallback.

Альтернативы: LIMIT 100/active=true меняет состав и не изолирует обращения по ID; frontend-only фильтр оставляет API/экспорт открытыми; общий research Score не оценивает показательный фонд; новый bulk RPC дублирует действующий Upload и требует нового input pipeline. Эти варианты не выбраны. Новый микросервис увеличил бы deployment, секреты, version matrix и число отказов без независимой нагрузки или ответственности.

## 3. Cohort interface и server scope

### 3.1 Неизменяемое membership

Версия когорты состоит из `cohort_version`, SHA256 канонического списка и **ровно 100 различных ID Pulse**. У каждого участника зафиксированы external bindings, namespace и источник. Membership независимо от `active`, `archived_at`, качества истории и права конкретного пользователя. Версия не обновляется на месте; новое membership — новая версия.

Явная admin CLI capture читает уже зарегистрированные presentation-объекты, проверяет внешние IDs −900000…−900099, `fund_type=presentation`, NGDU/department −900 и отсутствие конфликтов. Она **не создаёт и не восстанавливает скважины**. Если участников меньше/больше 100 или mapping неоднозначен, capture завершается ошибкой. Список Pulse ID берётся из БД; арифметика external ID → Pulse ID запрещена. Runtime стартует с зафиксированной версией, а не повторно выбирает первые 100 записей.

Предлагаемые interfaces в `internal/service/cohort.go`:

```go
type FundScope interface {
    Members(context.Context) (domain.Cohort, []domain.Well, error)
    RequireWell(context.Context, int64) (domain.Well, error)
    RequireUnitek(context.Context, int64) (domain.Well, error)
}
```

`RequireWell` сначала проверяет cohort membership, затем обычные права пользователя. Admin не обходит membership в cohort-профиле. Архив доступен только через разрешённые archive/history read-сценарии; active, archive и права не смешиваются. Внутренний background run фиксирует полные 100 членов, а HTTP выдаёт разрешённое пересечение. Глобальная полнота 100 проверяется admin; у scoped user список закономерно меньше. Stored snapshot не переписывается под права читателя.

### 3.2 Обязательные callers guard

| Interface | Правило |
|---|---|
| `/wells`, filter, stats, references, export | membership ∩ principal scope; затем archive/active/search. Экспорт использует тот же разрешённый список |
| Карточка, telemetry/bounds/score, checks/reviews, history, predictions/incidents/complications | guard до обращения к данным и до мутации; чужой/внекогортный ID возвращает 404, не раскрывая существование |
| ByUnitek | только однозначный mapping внутри membership; 0, null и отрицательный ID сохраняют свой смысл |
| ML model-ID Explain/History | только research-профиль с явной авторизацией; model ID нельзя трактовать как ID Pulse |
| Cohort risk/history | читаются по ID Pulse и сохранённому snapshot; SHAP берётся из сохранённого Upload result, а не свежей модели |
| Alert/check review по ID результата | сначала resolve target → Pulse ID → тот же guard; admin bypass из текущего Reviews.target убирается только для membership, обычный RBAC сохраняется |
| Raw `/telemetry*`, ingest/import, source collection | external ID проходит resolver membership перед чтением/записью; смешанный пакет отклоняется целиком, без частичной записи |
| Общие журналы/exports | строки с объектом фильтруются по разрешённым ID; неразделимые исторические research агрегаты не выдаются как cohort data |
| Create/bulk/import well | в cohort-профиле нельзя расширить membership обычной мутацией; создание новой версии — отдельная admin CLI, не скрытый side effect |

`APP_SCOPE=cohort` — default пользовательского запуска; `COHORT_VERSION` обязателен. Некорректная/неполная версия останавливает запуск, fallback на весь research фонд запрещён. `APP_SCOPE=research` — явный opt-in для исследовательского runtime и не включён в обычный Compose. Legacy auth сам по себе не означает research scope.

## 4. Cohort orchestration и HTTP-совместимость

`service/cohort_scoring.go` получает `FundScope`, `Integrations` через узкий interface и существующий `MLClient.Upload`. Циклических зависимостей нет: Integrations использует gRPC adapter, а не ML orchestrator.

```go
type CohortTelemetry interface {
    ReadApp(context.Context, domain.Well, time.Time, time.Time) (domain.TelemetryResult, error)
}
```

Run фиксирует UTC `as_of` до чтения и период `[as_of−60d, as_of]`; 60 суток — existing Upload recommendation, а не доказательство достаточности. Данные строго после `as_of` не используются. ReadApp уже отвергает truncated/пустую выборку через `telemetryCSV`; это ограничение сохраняется. На старте concurrency=1: нет измерений для выбора большего значения. Общий timeout конфигурируется, каждый запрос ограничивается оставшимся deadline; рестарт/отмена не обещают прерывание уже работающего CatBoost. Таймаут помечает оставшихся как `error/run_timeout` и сохраняет терминальный snapshot, если PG доступна.

Каждый outcome имеет `app_well_id`, `status`, `reason_code`, nullable `result`, `input_sha256`, bounds, resolution и model provenance. Статусы:

| status | reason / result |
|---|---|
| `scored` | реальный Upload result; `norm/watch/alert` взяты из evaluator |
| `archived` | `archived_at` на capture запуска; result null |
| `inactive` | active=false, не архив; result null |
| `insufficient` | пустая/короткая история, недостаточные running windows, недоступные обязательные каналы или truncation; result null, диагностические сведения отдельно |
| `error` | ML/source failure, unsupported protocol/feature recipe, timeout; result null |

Оценка Upload с legacy fallback features остаётся `input_kind=synthetic` / `context_completeness=partial` и не называется промышленно подтверждённым прогнозом. Verdict `insufficient` сохраняется как диагностическое поле, но не попадает в scored/norm count. После archive/restore membership hash не меняется; новый run фиксирует актуальный lifecycle. Metadata/revision читаются в едином PG snapshot до расчёта и включаются в run; изменение binding после capture не подменяет вход.

В `/api/v1/ml/wells` добавляются `profile=cohort|research`, `cohort_version`, `snapshot_id`, `as_of`, `outcomes`, `coverage` и `provenance_status`. OpenAPI описывает два ответа через oneOf с discriminator; TypeScript использует отдельный union. В cohort-профиле count равен visible membership, основной список — outcomes; legacy wells не заполняется фиктивными строками. Research legacy `wells`/thresholds/count сохраняют прежний формат в research-профиле. В cohort-профиле основной источник — `outcomes`; не создаются fake research WellRisk с `well_id=0` или `well_id=PulseID`. `coverage` содержит раздельные member/visible/scored/archived/inactive/insufficient/error counts. У ограниченного principal все counts относятся только к видимому пересечению; global member count и private full-snapshot digest не выдаются. `min_risk` и alert_only применяются к derived scored view; outcomes/coverage сохраняют полный видимый состав независимо от порога, в том числе отсутствие результата.

До первого run возвращаются 100 outcomes с `insufficient/not_evaluated`, `snapshot_id=null`, `result=null`; это read-модель, не сохранённый успешный run. После refresh читается последний сохранённый cohort snapshot. `wells_scored` — только реально scored, не membership count; ноль scored даёт явный `evaluation_status=no_scored_members`, а не зелёную надпись об оценке 100 скважин. `ml_run.status=done` означает завершение оркестрации; оценка качества и coverage — отдельные поля.

Добавляется чтение `/api/v1/ml/runs/:run_id/snapshot`; все ID/данные внутри проходят guard. Для существующих `/wells/:id/risk` и `/risk/history` добавляется optional `run_id`; отсутствие использует последний сохранённый cohort run. History/экспорт выбранного run не вызывает новый ML расчёт. Результат unscored возвращает status/reason и nullable result, без графика из нулей. Клиентские типы этого нового профиля — отдельный discriminated union, не ослабление research `MlWell.risk: number`.

## 5. Decision protocol: одно правило, явные legacy policy

Новый private module `pulse_ml/decision.py`:

```python
def decision_rule(context: dict, operation: str) -> DecisionRule: ...
def evaluate_decision(windows: pd.DataFrame, rule: DecisionRule) -> DecisionResult: ...
```

`windows` содержит well_id, window_end, **неокруглённые** raw/display score и running. Rule содержит context_version, policy_id, watch/high scale/operator/threshold/K/lookback, enabled tiers и cold-start policy. Неизвестная версия, NaN/inf threshold, K<1, causal без normalizer/оператора/recipe — ошибка, не fallback. Калибратор применяется для display; ступенчатое isotonic plateau не используется для восстановления raw-сравнения.

| protocol / policy | Сохраняемая семантика |
|---|---|
| legacy_v0 / legacy_score_v0 | calibrated risk >= calibrated thresholds, либо прежние raw defaults без calibrator; counts по всей доступной истории, K из прежних meta/default |
| legacy_v0 / legacy_upload_v0 | raw **>**, прежние raw thresholds/K и fallbacks, весь файл; running insufficiency по прежнему правилу |
| legacy_v0 / legacy_history_display_v0 | старые `fired/windows_fired` остаются display >= threshold и tail(limit); additive decision evidence отдельно указывает rule исходного Score |
| causal_v1 / causal_v1 | raw **>**, `(t−lookback, t]`, watch K; high tier disabled; calibration только display |

Это единая реализация с версионированной политикой, а не объявление исторически разных правил эквивалентными. На новом cohort bulk применяется **тот же legacy_upload_v0**, что на ручном Upload этих же байтов. Тождество результата гарантируется при одинаковом input/model/rule/fill recipe, а не между разными входами research Score и Upload.

History сохраняет p_raw, watch/high fired и counts в context до округления. Additive evidence отражает полный decision interval до tail; старый tail counter не подменяется накопленным. `data_ok=false` и последнее низкое окно не гасят накопленный tier. Сценарий генератора, сценарный класс и `truth` не передаются в feature matrix.

`causal_v1` Upload сейчас имеет несовместимый feature path; в PREP минимальный безопасный контракт — явный `unsupported_feature_recipe`, без model promotion. Его поддержка требует отдельного подтверждённого causal feature recipe и доступного train-normalizer; изменение одного порога не делает вход совместимым. Causal Score/History можно проверять на исследовательском комплекте отдельно. Legacy runtime продолжает использовать прежний bundle.

## 6. Additive protobuf / provenance

Единственный владелец wire — pulse-contracts; generated меняется только официальным generate. Не изменять request type `Score(Empty)`, package, существующие поля/номера/семантику. Предлагаемые свободные номера проверены по текущему `ml.proto`:

| Message | Добавление |
|---|---|
| SnapshotResponse | `optional Provenance provenance = 7`, `optional DecisionRule decision_rule = 8` |
| WellRisk | `optional DecisionEvidence decision = 15` |
| HistoryResponse | `optional Provenance provenance = 7`, `optional DecisionRule decision_rule = 8`, optional accumulated decision counts = 9/10 |
| RiskPoint | optional raw_score = 4, watch_fired = 5, high_fired = 6 |
| UploadResponse | optional Provenance provenance = 19, optional DecisionRule decision_rule = 20 |
| TimelinePoint | optional raw_score = 3, watch_fired = 4, high_fired = 5 |

`DecisionRule` типизирован: context_version/policy_id/rule digest, watch/high `ThresholdRule` (scale, comparison, threshold, K, optional lookback), high_enabled. `DecisionEvidence`: nullable raw/latest score и accumulated watch/high counts; отсутствие отличимо от false/0. `Provenance`: envelope_version, snapshot_id, computed_at, model_name/version/saved_at, context_version, input_sha256/input_snapshot_id, optional as_of, feature_recipe_sha256, feature_schema_sha256, runtime/source manifest digest, input_kind, context_completeness, provenance_status. Списки ревизий/артефактов — typed entries, не непроверенный произвольный JSON.

`provenance_status=complete|partial|historical_unknown` описывает воспроизводимость происхождения; это не качество модели. `outcome_completeness=complete` отдельно означает все 100 terminal outcomes. Незаполненные code/data refs дают partial; текущий model hash не подставляется историческому запуску. Полный bundle hash уже реализован в `model_identity.py` и сохраняется. Bundle загружается один раз на bulk run; если model_version/rule меняется между Upload, run фиксирует `bundle_changed` и не склеивается в однородную оценку.

Старый peer игнорирует новые поля; новый consumer воспринимает отсутствие envelope как historical_unknown и не реконструирует его из сегодняшнего context. Номера 16–18 Upload identity уже существуют в dirty baseline и не переиспользуются. Проверки Buf breaking — относительно зафиксированного pre-PREP SHA, а независимая regeneration сверяется с принятым SDK baseline, а не всего dirty tree с HEAD. Совместимые Go/Python SDK версии изменяются синхронно; локальные wheels не объявляются опубликованным релизом. Правила эволюции — [официальный protobuf guide](https://protobuf.dev/programming-guides/proto3/).

## 7. PostgreSQL migration, immutable snapshot и rollback

Новые DDL рядом с существующими migration SQL; версии 9 (cohort) и 10 (snapshot) после фактической текущей 8, без изменения старых файлов schema. Apply добавляет их под существующим advisory transaction lock и вносит запись backend_schema_migration.

| Таблица | Ключ / содержание / индекс |
|---|---|
| app_cohort | version text PK, manifest_sha256 UNIQUE, created_at; immutable metadata |
| app_cohort_member | (version, well_id) PK, FK cohort / well ON DELETE RESTRICT, captured external binding+revision; UNIQUE(version, unitek_id) при ненулевом binding |
| ml_run_snapshot | run_id PK/FK ml_run RESTRICT, snapshot_id UNIQUE, format_version, cohort_version, as_of, provenance_status, output_sha256, snapshot JSONB, created_at; snapshot содержит **все** outcomes и полные Upload timeline/evidence |

Cardinality=100 проверяет capture transaction до commit; runtime повторяет проверку. CHECK одной строки не притворяется межстрочным ограничением. Не добавлять JSONB GIN и индексы по risk до измеренного запроса. Извлечение выбранного run использует PK; history списка — существующий run order. Старые runs не backfill-ятся: LEFT JOIN без snapshot означает historical_unknown.

`RunLease.Persist` расширяется атомарно: immutable snapshot insert + новые alerts только от scored alert/watch + перенос review status по стабильному Pulse ID/cohort + завершение ml_run. Research legacy status по unitek сохраняется в research path. В новой миграции `ALTER TABLE ml_alert ALTER COLUMN ml_well_id DROP NOT NULL`: cohort alert использует настоящий app_well_id, а ml_well_id=NULL; фиктивный research ID запрещён. Старый writer по-прежнему пишет свой настоящий ml_well_id. Подробный decision result живёт в snapshot, пользовательский review — отдельно и не меняет факт расчёта.

Digest считается по каноническому JSON всех результатов/metadata без самого digest, до redaction ответа. Повтор Persist того же run с тем же digest идемпотентно возвращает сохранённую запись; иной digest для уже существующего snapshot отклоняется и не обновляет alerts. Terminal update проверяет прежний status=running. Private input/cohort manifests, source hashes с закрытыми ID и полный digest остаются в закрытом storage/evidence; opaque snapshot_id разрешён читателю, но не раскрывает скрытый состав.

Сохраняется точный hash фактических CSV bytes каждого члена, bounds/resolution и input watermark. **Один hash не даёт повторного получения входа.** Принятый минимальный PREP сохраняет полный result snapshot и отмечает provenance partial, пока неизменяемые input bytes/refs и комплект недоступны; новую таблицу входных bytes в PREP не вводить. Будущий побайтовый replay требует отдельно согласованного immutable input storage с измеренным объёмом, доступными артефактами и проверенным восстановлением. Нельзя объявлять complete только по hash mutable telemetry. Хранение входов и retention — отдельный измеряемый storage gate, не автоматическое удаление research data.

Проверка миграции: чистая test schema; повторный Apply; старая schema с alert/manual_check/history fixture; сравнение count/hash содержимого до/после; FK/unique; rollback транзакции при injected error между snapshot/alerts/finish. Проверка старого writer: новая nullable/additive schema не ломает старые insert/select. Снапшоты не редактируются методами приложения.

Rollout: backup рабочей app schema → восстановить копию в isolated PG → Apply+integration → compatible readers/contracts → Python → backend/user scope+frontend → smoke. Production Apply требует отдельного разрешённого шага и проверенного backup; PREP не выполняет его скрыто. Rollback — предыдущие образы/config и выключение нового writer при сохранении additive tables. Не drop-ать snapshots/cohort, не пересоздавать PG/CH volumes; возврат research scope ради обхода guard не является допустимым rollback пользовательского фонда.

## 8. Runtime, совместимость, производительность

База воспроизведения: `/private/tmp/pulse-prep-20261019` хранит manifest/status семи репозиториев и 502 public baseline files. HEAD отдельно не воспроизводит сотни dirty changes. Принять новый локальный manifest с source hashes, diff/new files, dependency locks, wheels и toolchain; исключить secrets/data/models. Remotes смешанные GitLab/GitHub; URL не переписывать и не предполагать доступность private release. Независимые сборки устанавливают согласованные local wheels/Go workspace либо зафиксированные commits, не старый tag с новым runtime claim.

Существующие pins: model/data требуют pandas>=2,<2.4; ML constraints фиксируют pandas2.3.3, grpc1.78/protobuf6.33.6 и вычислительные версии. Backend Go1.25, contracts Go1.24; frontend tests требуют Node>=22.18. Проверить pip check, freeze, installed wheel/source digests, Go module graph и package-lock; не обновлять версии ради «актуальности». Наличие версии в constraints не доказывает наличие Linux/architecture wheel и совместимость старого pickle — нужны clean native и Docker load/inference smoke. DOC-06 native свидетельство не переносится на Linux автоматически. Preflight обнаружил неполный `/private/tmp/pulse-go-toolchain/go1.27.2` с пустым stdlib: его ошибки не являются дефектами кода. Координатор сообщил make check PASS exit0 с полной Go1.25.0 toolchain и fresh module cache; окончательный gate после runtime diff выполняется повторно. Использовать эту toolchain, `GOPATH=/private/tmp/pulse-prep-20261019/go-path` (fresh cache; прежний `/private/tmp/pulse-go-path` неполон), `GOCACHE=/private/tmp/pulse-go-cache`; версия/stdlib проверяются до CI-equivalent commands.

Для isolated smoke использовать новый Compose project, отдельную test app PG/volume, отдельные loopback ports, pinned image IDs и read-only артефакты. Текущий `compose.local` с внешними grant-postgres/ClickHouse сетями не является isolated fixture. Backend integration использует **pulse_test на 55432** или выделенный test service, уникальную schema; перед запуском сверяются host/port/database. Никогда не использовать esp/esp_app/grant volumes как TEST_DATABASE_URL. Не запускать compose down -v над текущим runtime.

Local auth проверяется отдельным явным профилем `AUTH_MODE=local` существующей реализации: bootstrap admin, login/me/logout, cookie/origin и API RBAC. Секрет bootstrap передаётся через env, не в Git/log; на HTTP loopback Secure=false только для local smoke, в TLS-профиле Secure=true. Legacy runtime smoke записывается отдельно; local PASS не доказывает SSO. ML API key и probe используют один профиль.

Измерения при bulk100: elapsed/peak RSS Python, длительность одного Upload и всего run, bytes input/snapshot, rows/eligible counts, PG transaction duration; три повторения одного immutable input/bundle, указать cold/warm. Не брать «1.5 секунды на 207k окон» из docstring как текущее измерение. Не увеличивать concurrency и не назначать SLA без результатов. При недоступной PG terminal snapshot не заявляется сохранённым; прежний completed snapshot читается с его timestamp и stale state.

Polars/DuckDB **не вводятся в PREP**. Конкретный кандидат на будущий benchmark — concat/groupby/window_count над research feature cache и offline audit joins; Upload уже передаёт pandas в существующие features/CatBoost, а PG читает по индексу. У Polars нет pandas index, что затрагивает `.loc`, time-index и alignment ([primary guide](https://docs.pola.rs/user-guide/migration/pandas/)); DuckDB потребует отдельной проверки conversion/materialization и memory limit ([primary guide](https://duckdb.org/docs/stable/guides/performance/how_to_tune_workloads)). Это наш вывод о совместимости текущего кода, не измеренный проигрыш/выигрыш. Перед заменой: одинаковые representative cache/telemetry данные, точное равенство окон/NaN/0/9999/уставок/timezones/deadband/rolling interval, wall time и peak RSS; фиксировать версии/CPU/threads/cold-warm. Без benchmark сохранение pandas — минимальное решение.

## 9. ML: что измеримо и что остаётся внешним gate

Файловый аудит `workspace-data/reports/database_audit/summary.json` датирован 14.10: 3124 PG references, F_out для 982 скважин, 962 с дебитом, pump/depth/cdng заполнены у 0; 407 incidents уже в manifest; новых хвостов >1 суток нет; максимум около 120 дней на скважину, 180 суток нет, >30 дней после отказа нет; 7 device IDs не сопоставлены PG. Это датированный read-only inventory, не свежая проверка доступности базы и не доказательство непрерывной связи по deadband.

Сегодня можно честно проверить: source/cache hashes; causal/leakage invariants; равенство DB→feature replay на **указанной** подвыборке; decomposition train/calibration/selection/test; отчёты прежнего диагностического архива с fixed seeds/splits; latency/coverage/drop reasons; API и synthetic pipeline без claims precision/recall. Перепросмотренный test остаётся diagnostic.

Отдельные output directories/datasets: `--lead-days 7 --horizon 14` и `--lead-days 7 --horizon 30`, с разными dataset SHA256 и horizon-aware purge (`horizon + 2d`, existing embargo3d). Нельзя сравнить метрики разных горизонтов как выигрыш модели. `eval_honest.py` default horizon=30; прежний 7–30 report не подтверждает цель 7–14. Не использовать `--save-candidate`, не менять рабочие weights. На одном immutable dataset сравнивать один и тот же protocol; отчёт включает все seeds/rotations и failed/empty parts, supports, confusion matrix/F1 и доступные class recalls. В бинарном baseline отсутствие 13-class output указывается явно.

Внешние blockers: физическое oilfield mapping вместо НГДУ; сверенные ID; непрерывный одновременный контрольный фонд и история до/после ремонта; достаточные нормальные примеры во всех ранних частях; независимый непросмотренный holdout; технологически подтверждённые времена/типы отказов и дата доступности метки; согласованная цена FP/FN и cost-sensitive threshold по selection. Текущий train/calibration без контрольных скважин нельзя исправить перестановкой поздних normal окон назад. Данные не выдумываются, ошибки протокола не скрываются synthetic cohort.

## 10. Пакеты реализации и владельцы файлов

Порядок: P0 → P1/P2 → P3 → P4 → P5 → Senior → tech-lead evidence. Координатор назначает единственного writer на файл; ниже роли не создают дочерних агентов сами.

| Пакет / владелец | Файлы / результат / interface |
|---|---|
| P0 reproducibility + isolated runtime / координатор | baseline manifest, pinned local wheels, build inputs; backend `deployments/compose.prep.yaml` и `docs/verification-matrix.md` только по назначению; без live schema mutation |
| P1 decision + wire / backend-middle | contracts `proto/pulse/ml/v1/ml.proto`, official gen, Go/Python tests; ML `pulse_ml/decision.py`, `ml_scoring.py`, `ml_upload.py`, `engine.py`, `server.py`; tests `test_decision.py`, scoring/upload/service_contract; Produce typed DecisionRule/Evidence/Provenance и bundle-only Upload loader |
| P2 membership/guard / backend-middle | backend `internal/domain/cohort.go`, `service/cohort.go`, `repository/postgres/cohort.go`, `internal/migrations/cohort.sql`, `migrate.go`, `cmd/cohort/main.go`, config/bootstrap; guard callers catalog/checks/reviews/integrations/presentation/ml and raw routes; Produce FundScope + capture CLI |
| P3 snapshot orchestration / backend-middle | backend `domain/cohort_scoring.go`, `service/cohort_scoring.go`, `service/ml.go`, `ports.go`, `repository/postgres/ml_snapshot.go`, `ml.go`, migrations `ml_snapshot.sql`, adapter mappings, transport/ml/routes, OpenAPI; Consume FundScope/typed Upload provenance, Produce CohortSnapshot/Outcome/coverage and selected-run read |
| P4 minimal client adaptation / designer | frontend `src/api/ml.ts` + потребители ML/risk/history в существующих pages/components; read DESIGN/HANDOFF, согласовать union с архитектором; nullable/non-scored states, app IDs и snapshot binding, без нового экрана/редизайна/Figma mutation |
| P5 measurement/data audit / координатор + backend-middle | isolated PG integration + Docker smoke logs; model separate report paths и data inventory; отсутствие external data записать blocked, не менять pipeline/promotion |
| Review / backend-senior | fixed diff от pre-PREP source manifest, additive wire/storage compatibility, scope обходы, bundle changes, legacy equality/plateau, failure atomicity; решения вернуть архитектору через координатора |

### Review focus и обязательные проверки

- [ ] Scope: внекогортная скважина доступна admin в старом store; все HTTP ID/raw/export/review paths возвращают 404/deny, прямой service test тоже запрещает. Полный cohort=100 при archived=1/inactive=1; lifecycle не меняет hash.
- [ ] Decision: equality `raw==threshold`, isotonic plateau по обе стороны threshold, oldest window exactly t−lookback исключено, latest low/data_ok=false сохраняет накопленный tier; legacy fixtures byte/field-equivalent до новых optional полей.
- [ ] Upload isolation: cold-start Upload работает с bundle без research cache/PG; causal unsupported recipe возвращает явную ошибку; marker/0/is_set/value_source не теряются.
- [ ] Bulk: mixed scored/archived/inactive/insufficient/error даёт ровно100 outcomes; no_scored_members не маскируется под successful predictions; bundle change mid-run отклонён; retry не создаёт два завершения и не теряет review.
- [ ] Immutable history: повторное чтение run после refresh/restart/model replacement равно сохранённому result; старый run historical_unknown; null/false/0/int64 roundtrip; injected transaction error оставляет ни snapshot, ни alerts/finish.
- [ ] Migration повторяема и сохраняет исходные records; isolated TEST_DATABASE_URL проверен; go check/integration/build, Buf lint/breaking/test/regeneration, Python suites/wheels/pip check, npm test/build и landing build выполнены с exact source hashes.
- [ ] Local auth: 401 без cookie, viewer запрещённая мутация403, scoped technologist читает/экспортирует только разрешённых, logout/expired session очищает query cache, forbidden origin не создаёт запись; legacy и local свидетельства разделены.
- [ ] ML reports: отдельные7–14/7–30 dirs и dataset hashes, все split supports/failed statuses, реальные oilfield/holdout/cost blockers перечислены, рабочий bundle hash не изменён.

P1–P4 следуют циклу: failing contract test → зафиксированный FAIL → минимальная реализация → targeted PASS → требуемый module gate. До Senior контракт считается согласованным по дизайну, а реализация — непроверенной. Отсутствующие обязательные gates остаются OPEN.

## 11. Передача исполнителям

**Backend-middle, PREP-P1/P2/P3.** Источник — TL spec + этот контракт + DOC-04. Вход — baseline семи repositories, текущие migrations/tests. Разрешены только назначенные координатором файлы P1–P3; сначала точные nullable fields/guards/migration fixtures, затем implementation. При несовместимости конкретного номера/DDL изменить дизайн через архитектора до кода. Результат координатору и Senior: fixed file manifest/diff, команды/exit codes/counts, migration evidence, API samples с null и coverage, legacy equality, partial provenance limitations. Не один в checkout; чужие изменения не откатывать.

**Designer, PREP-P4.** Источник — TL spec, этот HTTP union и существующий Figma DESIGN/HANDOFF. Владеет только назначенными frontend files. Согласовать с архитектором discriminated union `status/result`, namespace Pulse ID и selected snapshot; не рисовать риск/норму для null. Использовать существующие таблицу/карточку/history и состояния загрузки/ошибки. Проверить unknown historical provenance, archived/insufficient/failed, stale prior snapshot, две темы и existing routes. Результат координатору: exact files, npm test/build и адресная браузерная проверка; live Figma/new UI вне scope.

## 12. C4 и использованные скиллы

Редактируемый источник: [readiness-architecture-2026-10-19.drawio](../diagrams/readiness-architecture.drawio): C1 current/proposed и C2 current/proposed; протоколы/хранилища/trust zones разделены. Container — runnable process/storage; model/contracts — библиотеки, не сетевые контейнеры ([C4 primary reference](https://c4model.com/diagrams/container)).

| Скилл / прочитанный SKILL.md | Применённое действие → влияние |
|---|---|
| improve-codebase-architecture — `/Users/daniilkoreskov/.codex/skills/improve-codebase-architecture/SKILL.md` | Прослежен выбранный scope, применён deletion test к scope/decision; выбран deepening существующих modules вместо bulk микросервиса. Диалог выбора кандидата заменён уже утверждённым TL brief, самостоятельные subagents не создавались по поручению координатора |
| codebase-design — `/Users/daniilkoreskov/.codex/skills/codebase-design/SKILL.md` | Малые FundScope/decision interfaces и existing adapters/seams; общий test surface устраняет отдельную фильтрацию в UI и пересчёт decision из display |
| writing-plans — `../../../.agents/skills/writing-plans/SKILL.md` | Зафиксированы files/ownership/dependencies/interfaces и review-focus tests; handoff не оставляет исполнителю выбор namespace, threshold policy и смысла100 |
| verification-before-completion — `../../../.agents/skills/verification-before-completion/SKILL.md` | Документ и drawio проверяются локально перед передачей; runtime/ML gates отделены от дизайна и не заявлены выполненными по старым свидетельствам |

`using-superpowers` прочитан, но SUBAGENT-STOP исключает его применение в этой роли. `brainstorming` прочитан: исходные требования уже заданы/одобрены и повторные вопросы не запускаются. `research` прочитан; отдельный background agent не создавался по явному ограничению координатора, поэтому как использованный навык не заявлен. Первичные технические страницы проверены непосредственно, ссылки стоят рядом с выводами.


## 13. Принятое уточнение после Senior P4: обработка тревоги и окно SHAP

Источник: `/private/tmp/pulse-prep-20261019/p4-senior-review.md`, замечания P4-F1/P4-S1/P4-S2. Координатор принял этот ограниченный read-side feedback seam в PREP. Замечания проверены по текущим `CohortOutcome`, `CohortWells`, `PersistCohort`, frontend `mlSnapshot.ts`/`ExplainBars.tsx` и Upload producer: alert rows создаются, но frontend терял linkage; contributions вычислены на `argmax(raw)`, а подпись утверждала latest; value_source сохранён в DTO, но скрыт отображением. Это исправление существующего сценария, не новый notification workflow.

### Mutable processing overlay

HTTP `CohortOutcome` аддитивно возвращает `alert_id: int64|null` и `alert_status: AlertStatus|null` на верхнем уровне. Это текущее состояние обработки **конкретной** строки `ml_alert`, связанной с `(run_id, app_well_id)`. Поля не включаются в immutable decision facts и не меняют `result`, исходные status/reason, input/model/policy, snapshot identity или output digest. Перед persistence/hash используется проекция facts без overlay (либо явно очищаются оба overlay поля); повторное сохранение прочитанного результата после review не меняет canonical digest.

Read interface: `CohortAlerts(ctx, run_id) -> []CohortAlert{app_well_id,id,status}` в существующем PG adapter; service обогащает **только разрешённые** outcomes после membership/RBAC. Query использует точный выбранный run; текущая/последняя тревога другого run не подставляется. Существующая схема/реальный alert ID достаточны: новая миграция/protobuf не требуются. Нерассчитанный/неалертный outcome или отсутствие соответствующей записи дают null/null; status=new не выдумывается. Lookup по review target продолжает resolve реальный Pulse ID и тот же scope guard.

Frontend `CohortOutcomeBase` типизирует эти nullable поля; `mlOutcomeRow` переносит реальные значения в `MlDisplayWell`, сохраняя настоящий alert ID. Существующие WorkspaceMenu queue и ReviewPanel используют этот linkage без новых экранов. Открытие выбранного исторического run ведёт к обработке именно его alert; прочие статусы/таймлайны не пересчитываются. Existing queue tier/status semantics сохраняются.

Проверки P4-F1: scored watch/alert с реальным id/status=new доступен соответствующей существующей очереди/ReviewPanel; переходы acknowledged/false_positive/confirmed видны после invalidation/refetch; выбранный старый run сохраняет свой alert ID; foreign scope не раскрывает linkage; non-scored=null/null; чтение/обработка не меняют stored snapshot JSON/digest. Наличие полей в dirty backend ещё не означает PASS: нужны fresh integration/frontend regressions и повторное Senior review.

### Explanation window и источник значения

Frontend display row получает `explanation_window_kind: peak|latest|unknown` и `explanation_window_end: string|null` отдельно от latest `window_end`. Для persisted cohort Upload: kind=peak, end=`result.peak_window_end`, contributions уже сохранены от окна `argmax(raw)`; подпись — «TreeSHAP по окну максимального сырого score» с фактической датой. Latest risk/date остаются latest и не переименовываются в peak. Для research Explain сохраняется kind=latest; неизвестная дата не восстанавливается по сегодняшнему времени. Старый response без известной привязки маркируется unknown.

ExplainBars принимает явную привязку окна и перестаёт безусловно утверждать latest для переданных contributions. Все persisted call sites (MlRiskPage, WellDetailPage, Dashboard) передают её из adapter; research fetch сохраняет свой существующий путь. Tooltip/подпись каждого contribution показывает источник: computed → «Рассчитано по данным», training_median → «Медиана обучения», zero_fallback → «Заполнение нулём», absent/null/неизвестное значение → «Источник не передан». Ноль остаётся значением0, не отсутствием.

Проверки P4-S1/S2: peak/latest имеют разные даты и raw максимум; подпись совпадает с producer и не утверждает latest; каждый source и исторический unknown видимы в SSR/компонентной проверке; source=zero_fallback,value=0 не исчезает. Общий SHAP вклад/решение не пересчитываются в UI.

### Строгое владение

Backend-middle сохраняет прежнее владение: `internal/domain/cohort.go`, `internal/service/cohort_scoring.go` (его store interface), `internal/repository/postgres/ml_snapshot.go` и адресные service/integration tests. Designer сохраняет прежнее владение frontend: `src/api/ml.ts`, `src/lib/mlSnapshot.ts`, `src/components/ml/ExplainBars.tsx`, существующие call sites `src/pages/{MlRiskPage,WellDetailPage,Dashboard}.tsx`, `tests/ml-snapshot.test.mjs` и адресная component/SSR regression. WorkspaceMenu/ReviewPanel меняются только если необходима совместимость существующего read-side linkage, не для нового UI.

Architect меняет только этот документ и `/private/tmp/pulse-prep-20261019/architect-report.md`; runtime files, TL spec, migration/proto, чужие ownership не меняются. Backend и frontend owner работают параллельно через согласованный nullable contract; координатор выполняет финальную integration/browser проверку.

### Использованные скиллы уточнения

- receiving-code-review — `/Users/daniilkoreskov/PycharmProjects/grant/.agents/skills/receiving-code-review/SKILL.md`: замечания сверены с producer→DTO→adapter→queue/SHAP вместо автоматического принятия; подтверждены точная peak-семантика и уже существующие alert rows, поэтому выбран узкий read overlay без новой схемы/notification feature.
- codebase-design — `/Users/daniilkoreskov/.codex/skills/codebase-design/SKILL.md`: seam между immutable decision facts и mutable processing read-моделью оформлен в существующих adapters; одна typed nullable связь обслуживает очередь и review, не смешивая изменение статуса с ML output digest.
- verification-before-completion — `/Users/daniilkoreskov/PycharmProjects/grant/.agents/skills/verification-before-completion/SKILL.md`: code inspection подтверждает основание дизайна, но frontend/backend исправления и Senior повторное ревью не объявляются завершёнными; локально проверяются только целостность документа и новый handoff.

## 14. Принятое уточнение Senior backend F1–F3: безопасная история попыток

Источник: `/private/tmp/pulse-prep-20261019/backend-senior-review.md`. Координатор разрешил ограниченное исправление fail/restart/auth-scope в PREP. Код проверен read-only: Reference writes сейчас обращаются к global store без cohort guard; перенос статуса исключает new до выбора последнего; CohortRuns пропускает ml_run без snapshot, а Begin не фиксирует профиль. На момент архитектурной передачи реализации и re-review были OPEN. Итог20.10.2026: исправления реализованы, F1–F3 CLOSED по backend-senior-rereview; независимые10PG/race и lease checks PASS. Ниже сохранены принятые требования.

**F1 — global reference writes.** В cohort-профиле `Catalog.CreateReference`, `UpdateReference`, `DeleteReference` запрещают все глобальные мутации NGDU/department на входе service, до validation/lookup/store mutation. Ответ — одинаковый403 без раскрытия существования ID и без admin bypass. Это относится и к связанному с разрешённым участником reference: объект может быть общим с research фондом. Scoped reads сохраняются; research профиль сохраняет прежний контракт. Простого UI/middleware запрета недостаточно. Проверки: create/PUT/DELETE для обоих kinds через HTTP и прямой service, allowed/foreign/nonexistent IDs одинаково denied, DB bytes/count неизменны.

**F2 — актуальная обработка.** Наследовать статус последнего предшествующего alert той же пары(cohort_version, app_well_id), включая явно восстановленный new. SQL не исключает new до ORDER/LIMIT. Не искать старый non-new и не менять уже сохранённые decision facts. Регрессия run1 false_positive → run2 explicit reset new → run3 new; обычные acknowledged/confirmed/false_positive transfer и изоляция cohort/Pulse ID сохраняются.

**F3 — identity при создании run.** Новая additive migration `internal/migrations/run_scope.sql`, версия11 после cohort9/snapshot10:

- `ml_run.profile text NULL` и `cohort_version text NULL` с FK на `app_cohort(version) ON DELETE RESTRICT`.
- CHECK допускает только пары(NULL,NULL),('research',NULL),('cohort',nonnull). Существующие строки остаются NULL,NULL: ни research, ни текущая когорта не подставляются задним числом.
- Новые `Begin` research rows явно получают research,NULL; новый `BeginCohort(ctx, trigger, cohortVersion) (int64,error)` пишет cohort/version на том же зарезервированном lease/соединении до Members/телеметрии/Upload, чтобы ранний failure тоже имел идентичность.
- `RunOptions.Profile/CohortVersion` поступают из уже проверенного startup config/bootstrap. Scope version не получают через операцию Members, которая сама может закончиться ошибкой. Для cohort отсутствие поддерживаемого BeginCohort/версии — fail-closed, не fallback на research Begin.

Reserve/advisory lock, владелец соединения, Close и existing abandonment lifecycle сохраняются. BeginCohort не открывает обходной pool connection и не создаёт run без lock. При отметке ранее прерванного run его исходные profile/version не меняются. Restart/reopen не превращает failed attempt в отсутствие истории и не меняет прежний completed snapshot.

Read interface в существующем adapter: `CohortRuns(ctx, cohortVersion, limit) -> []domain.MLRun` выбирает ml_run по profile=cohort/version, LEFT JOIN snapshot, а не требует INNER/snapshot existence. В `ML.Runs` сначала определяется разрешённое пересечение FundScope; пустому scope не выдаются ни ID попыток, ни running/error metadata. Наличие хотя бы одного разрешённого участника даёт безопасные общие orchestration metadata той же когорты: id/time/status/trigger/profile/version; это не сведения о скрытых скважинах. Чужая версия, новый research и исторический NULL identity не включаются в пользовательскую cohort history. Все существующие лимиты и порядок по id DESC сохраняются.

Для running/error без snapshot: snapshot_id=NULL, provenance=NULL (вычислительный provenance ещё неизвестен), wells_scored/alerts_count/drift=NULL; это не пустой successful Snapshot. В completed run counts recompute только из разрешённого сохранённого snapshot, глобальные ml_run totals не раскрываются restricted user. Верхний running indicator относится к идентифицированным cohort attempts; advisory lock остаётся механизмом взаимоисключения, не источником приватных research metadata.

`ml_run.error` сохраняет диагностический текст закрыто. Public HTTP Error в cohort run не содержит raw SQL/source/SDK/внутренний exception: для error возвращается фиксированное «Расчёт завершился ошибкой. Повторите попытку или обратитесь к администратору.» и additive `failure_reason_code=run_failed`; для прочих статусов Error/code null. Это правило действует также для admin HTTP, а не только scoped users. Private error не попадает в snapshot fingerprint/public export. Сообщение не утверждает, что модель или данные были рассчитаны. P4 stale notice получает latest failed/run ID, поэтому может честно пояснить, почему показан прежний сохранённый результат.

Проверки F3: BeginCohort→Fail до snapshot виден; running без snapshot виден; ошибка Members после Begin видна; reopen/restart не теряет error row; old NULL/research/foreign cohort не видны; zero-member principal не видит shared metadata; restricted completed counts не глобальные; sentinel privateerror отсутствует в HTTP/exports; fresh/repeat migration оставляет old rows NULL,NULL; текущие Reserve/concurrent-run/atomic Persist regressions сохраняются. Финальный new diff/manifest и повторное Senior review обязательны.

**Строгое владение:** backend-middle сохраняет прежние runtime files и получает `internal/migrations/{run_scope.sql,migrate.go}`, `internal/domain/ml.go`, `internal/service/{ports,ml,cohort_scoring,catalog}.go`, `internal/repository/postgres/{ml,ml_snapshot}.go`, `internal/bootstrap/application.go`, при необходимости config, адресные tests/OpenAPI. Другой writer туда не назначается. Architect меняет только §14 этого документа и append `/private/tmp/pulse-prep-20261019/architect-report.md`; migrations/runtime/TL docs не редактирует. Коммиты/новые agents не создаются.

**Использованные скиллы уточнения:** receiving-code-review (`/Users/daniilkoreskov/PycharmProjects/grant/.agents/skills/receiving-code-review/SKILL.md`) — Senior воспроизведения сверены с текущими service/lease/SQL, поэтому F1 решён полным запретом shared writes, F3 — identity до потенциально failing capture; codebase-design (`/Users/daniilkoreskov/.codex/skills/codebase-design/SKILL.md`) — BeginCohort/CohortRuns расширяют существующий lease/read seam, сохраняя lock и разделение attempt metadata от immutable result; verification-before-completion (`/Users/daniilkoreskov/PycharmProjects/grant/.agents/skills/verification-before-completion/SKILL.md`) — recorded design принят, но runtime fix/test/re-review не объявлен PASS по одному изменению документа.

Rollout boundary v11: migration CLI выводит фактический CurrentVersion=11, без hardcoded10. Уже существовавшие pre11 ml_run (включая локальные snapshot results30/31) сохраняют profile/cohort_version=NULL и исторически неизвестную attempt identity; по текущему окружению их не backfill-ить. Сам immutable snapshot при этом не удаляется и не меняется. При backup/restore сравнении старых строк additive NULL columns исключаются из before/after projection (для локальной прежней базы id<=29), иначе JSON fingerprint ошибочно объявит новые nullable keys потерей данных. Новый after11 run фиксирует идентичность при BeginCohort.

## 15. Принятое ограниченное browser-исправление P4: responsive и keyboard

Источник: `/private/tmp/pulse-prep-20261019/browser-qa/browser-results.json`, screenshots ml-light/dark390 и well-light390. Проверены actual AppLayout/MlRiskPage/WellDetailPage/index.css и существующие frontend DESIGN/HANDOFF (Figma source/навигация3:52, те же tokens и компоненты). В1440 документ помещается; в390 ML body=798, Well body=1318 при retained canvas1102; Sider zeroWidth trigger имеет role=null/tabIndex=-1. Наблюдаемые logout/login timeouts идут после недоступного мобильного меню; сами по себе они не доказывают серверный дефект auth. При этом CSS `.app-account {display:none}` в mobile скрывает существующее user menu — его доступность также входит в узкий фикс.

Координатору/designer передано **approval существующих component fixes без редизайна/новой продуктовой фазы**:

- `MlRiskPage`: Table получает локальную horizontal scroll.x/overflow, чтобы широкие колонки прокручивались внутри таблицы, а не расширяли body. Card head/title/extra и toolbar переносятся в узком viewport, flex/grid children имеют min-width:0/max-width:100%. Колонки/сортировка/фильтры/actions и реальные данные не удаляются ради fitting.
- `WellDetailPage`: tabs/card/chart контейнеры могут сжиматься; табы используют локальный overflow существующей Ant Tabs навигации. График actual кода — **@ant-design/charts Line/G2, не ECharts**; autoFit уже включён. Использовать поддерживаемый lifecycle resize/ResizeObserver этого Line/G2 при изменении реального контейнера1440→390→1440. Новый chart engine/dependency не нужен. Изменение canvas width не меняет risk data/axes/time semantics.
- `AppLayout`: штатный недоступный zeroWidth trigger Sider отключается и заменяется native button/Ant Button, управляющим существующим collapsed state. Используются существующая nav/composition, menu icon и tokens. Кнопка находится вне скрытого Sider, получает понятный aria-label «Открыть навигацию»/«Закрыть навигацию», aria-expanded, aria-controls на существующую навигацию, visible focus; доступна через Tab и активируется Enter/Space. Скрытая collapsed navigation не оставляет невидимые focusable links. Не вводятся новый навигационный экран или modal workflow.
- Существующий account Dropdown/logout остаётся достижимым на mobile компактной icon button с тем же aria-label/items/server action. Не вводится новый user menu; display:none заменяется ограниченным responsive представлением существующего control. После logout сохраняется штатная очистка session/query cache, без обводного localStorage auth.

Запрещено маскировать дефект общим body overflow-x:hidden с обрезанием controls/таблиц или статическим width390, удалять данные/controls, менять темы/токены/маршруты/API/snapshot/auth semantics. Локальный overflow таблицы допустим; общий document/body горизонтально не расширяется. Скрытые внутри корректно ограниченного scroll/clip графические элементы не принимаются за отдельный document overflow без измерения scrollWidth.

**Strict ownership:** designer прежний владелец frontend и получает только `src/pages/{MlRiskPage,WellDetailPage}.tsx`, `src/components/layout/AppLayout.tsx`, `src/index.css`, адресные tests/browser QA. Узкий существующий chart wrapper допустим только если требуется lifecycle; иной новый module/библиотека не вводится. Связанные renderer/DTO и backend files вне этого исправления. Architect правит только §15 и append architect-report.md; live Figma/UI source не изменяется и новое Figma-согласование не запускается.

Проверка: обе темы390, desktop1440 и переход1440→390→1440 после уже созданного canvas; document/body scrollWidth<=viewport (с обычным округлением1px), chart CSS width<=host; локальная таблица прокручивается до всех колонок; title/extra не перекрывают друг друга. Keyboard: Tab→toggle, Enter/Space меняют aria-expanded и реально открывают/закрывают navigation, видимый focus, скрытые links не попадают в Tab; existing account menu/«Выйти» достижимы с клавиатуры на390. После layout fix заново пройти реальные admin logout→restricted0→viewer/technologist flows без route mocks и без cached private data. npm test/build и свежий frozen diff передать Senior; browser PASS не заявлять по одной сборке или прежним desktop screenshots. Полное Figma/WCAG соответствие этим bounded gate не утверждается.

**Использованные скиллы:** receiving-code-review (`/Users/daniilkoreskov/PycharmProjects/grant/.agents/skills/receiving-code-review/SKILL.md`) — browser evidence сопоставлена с actual Ant Line/G2, Table и Sider, поэтому исключена ошибочная ECharts-рекомендация и обнаружен mobile display:none account control; design:accessibility-review (`/Users/daniilkoreskov/.codex/plugins/cache/claude-cowork/design/1.2.0/skills/accessibility-review/SKILL.md`) — keyboard/name-role-state/visible-focus checks применены к существующему mobile toggle, scoped audit не выдан за полный WCAG; verification-before-completion (`/Users/daniilkoreskov/PycharmProjects/grant/.agents/skills/verification-before-completion/SKILL.md`) — подтверждено основание и допустимый scope, на момент передачи runtime fix/повторный browser/Senior оставались OPEN. Итог20.10.2026: source Senior PASS,30frontend tests/build PASS, браузер22/22 fail0/pageErrors0; ссылка на итог в ready-for-development-2026-10-19.md.
