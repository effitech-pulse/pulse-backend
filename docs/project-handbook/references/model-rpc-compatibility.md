> Снимок документа 21.10.2026. Источник: `pulse-ml-service/docs/model-rpc-compatibility.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# Совместимость моделей, RPC и происхождение результата

Спецификация DOC-04 принята 19.10.2026; документ актуализирован по реализации PREP. Runtime-изменения PREP прошли отдельное P1 Senior-ревью. Журналы исполнения, исходные находки и оставшиеся gates сохранены в локальном workspace: `../../tasks/pulse-backend/docs/documentation-queue-a-execution-2026-10-19.md`, `../../tasks/pulse-backend/docs/documentation-audit-2026-10-19.md` и `../../tasks/pulse-backend/docs/ready-for-development-2026-10-19.md`. Научная приёмка/продвижение causal-кандидата не выполнялись.

## Термины и источники

`context_version` определяет признаки, `policy_id` — правило решения, model_version — fingerprint bundle. Они не заменяют друг друга. Raw — выход модели, display risk — калиброванное значение при наличии calibrator. Число risk не доказывает достоверную вероятность отказа за14суток. `K` — число сработавших окон; lookback — глубина решения, не история для признаков. `window_end`, computed_at и data as_of различаются.

Источники: decision.py (исходник `pulse-ml-service/pulse_ml/decision.py`; в пакет не включён), ml_scoring.py (исходник `pulse-ml-service/pulse_ml/ml_scoring.py`; в пакет не включён), ml_upload.py (исходник `pulse-ml-service/pulse_ml/ml_upload.py`; в пакет не включён), engine.py (исходник `pulse-ml-service/pulse_ml/engine.py`; в пакет не включён), model_identity.py (исходник `pulse-ml-service/pulse_ml/model_identity.py`; в пакет не включён), server.py (исходник `pulse-ml-service/pulse_ml/server.py`; в пакет не включён), [typed proto](../contracts/ml.proto); backend cohort scoring (исходник `pulse-backend/internal/service/cohort_scoring.go`; в пакет не включён) и snapshot storage (исходник `pulse-backend/internal/repository/postgres/ml_snapshot.go`; в пакет не включён). Модель и причинные ограничения: [baseline](causal-baseline.md).

## Реализованная матрица решений PREP

Общий `DecisionRule/evaluate_decision` сохраняет различные legacy политики явно. Все сравнения выполняются до округления; display risk округляется до4знаков, поэтому равенство округлённых ответов не восстанавливает решение.

| Версия / RPC | Шкала / оператор | K и область | Поддержка / ограничения |
|---|---|---|---|
| legacy_v0 / Score | legacy_score_v0: calibrated `>=` calibrated_threshold (default0.7), high calibrated_threshold fallback1.0; без calibrator raw>=0.7/1.0 | K_watch per_well или1, K_high high_tier или K_watch; весь доступный history | Воспроизводит прежние веса/признаки; не является причинной временной приёмкой |
| legacy_v0 / Upload | legacy_upload_v0: raw `>` raw_threshold (fallback0.7), high raw_threshold fallback0.9; calibrator только display | K_watch per_well или5, K_high high_tier или4; весь файл | Отдельный неполный контекст; минимум30суток файла не гарантирует достаточных running windows. Insufficient остаётся отдельным состоянием |
| legacy_v0 / History | Новые raw/watch_fired/high_fired и accumulated counts получены из общего Score policy | Accumulated counts относятся к полному decision interval; limit(default400) ограничивает возвращаемые точки | Старые fired/windows_fired сохраняют display-risk>=alert_threshold и tail(limit) для совместимости; они не являются полным протоколом яруса |
| causal_v1 / Score и History | policy causal_v1: operating_point должен быть raw `>` | K/lookback_days из operating_point; интервал `(t−lookback,t]`; watch-only, high выключен | Обязательны train normalizer и совместимые признаки. History передаёт evidence общего evaluator; поддержка алгоритма не равна научной приёмке кандидата |
| causal_v1 / Upload | Runtime guard `unsupported_feature_recipe` **реализован** до построения features | Legacy fallback на causal не применяется | Отсутствующий фондовый feature recipe не подменяется нулями под видом поддержки causal Upload |
| unknown context_version | Runtime error `unsupported_context_version` | Нет неявного legacy fallback | Не поддерживается |

Isotonic plateau не делает raw `>` эквивалентным display `>=`. Legacy Score и Upload не обязаны совпадать при различных policy и признаках. Последний низкий риск/data_ok=false не снимает накопленный tier; alert в snapshot означает наличие яруса, включая watch. `min_risk` фильтрует display risk и не пересчитывает tier.

## Bundle-only Upload и пользовательский фонд

PREP отделил Upload bundle от research get_context/Score. Thread-safe cache читает только артефакты модели; fingerprint проверяется на каждом запросе и до/после загрузки. Смена bundle вызывает reload, изменение во время чтения отклоняется. Health bundle не загружает. Research Score/History по-прежнему зависят от cache/manifest/справочников и своего ленивого context; Upload без этих входов проверен отдельно.

Upload строит48-часовые окна с шагом12часов из файла. Недоступные features получают training medians drift_profile, иначе zero fallback; прочие NaN —0. Contribution.value_source раскрывает computed/training_median/zero_fallback. Cold start/недостаток признаков не является наблюдением «норма». Отмена RPC не гарантирует остановку уже выполняющегося pandas/CatBoost.

В APP_SCOPE=cohort backend использует ReadApp→telemetryCSV→Upload и сохраняет100 outcomes зарегистрированного cohort. Membership независимо от active/archive/ML-eligible; non-scored result/risk=null и явная причина. Research Score не вызывается вместо пользовательского bulk. Показательный синтетический источник не является независимым ML holdout. Технологический review — изменяемый read-side overlay, не изменение immutable decision facts; источник peak/latest SHAP и value_source сохраняются.

## Реализованный wire и provenance

Typed optional additivity в contracts: SnapshotResponse provenance/decision_rule; WellRisk decision; HistoryResponse provenance/decision_rule/accumulated counts; RiskPoint и TimelinePoint raw_score/watch_fired/high_fired; UploadResponse provenance/decision_rule плюс прежние identity16–18. ThresholdRule содержит scale/comparison/threshold/K/optional lookback; DecisionRule — context_version/policy_id/rule_sha256/watch/optional high/high_enabled. Номера/package/старые requests не менялись, SDK официально сгенерированы. Contracts metadata0.1.1 не означает опубликованный новый tag; PREP source commit `07213c430318affd140f55dea391cdc36220d93f`.

Envelope содержит snapshot_id/computed_at, bundle identity, context version, recipe/schema/runtime/source hashes и typed manifests, input_kind/context_completeness/provenance_status. Upload input digest вычисляется по точным CSV/XLSX bytes; model identity — по тому же загруженному bundle (meta/model/calibrator/normalizer/drift_profile с учётом отсутствующих файлов). Snapshot/History делят provenance текущего in-memory context.

**Статус provenance всегда partial.** У research нет immutable input cache/manifest/PG, единого event-time cutoff и достоверного input_snapshot_id/as_of: отсутствующие поля не выдумываются. Source manifest описывает доступные файлы установки, а не immutable загруженный код. Bundle/source/runtime/input hashes без сохранённых входных bytes и доступного runtime не гарантируют повторный расчёт. Public envelope не включает реальные well ID, локальные абсолютные пути, DSN, credentials; digest не заменяет управление доступом к закрытым manifests.

Backend сохраняет полный output snapshot в PG JSONB атомарно с алертами/finish. Чтение выбранного run после refresh/ML restart возвращает сохранённый ответ без нового ML. Это проверка неизменности выхода, не replay входа. Ошибка/рестарт не публикует неполный snapshot как done; версия11 фиксирует profile/cohort_version при BeginCohort до потенциальной ошибки Members. Повторная запись не создаёт вторую несовместимую идентичность.

Новый consumer со старым server читает отсутствие envelope как unknown. Старые consumers читают прежние поля нового сервера. Исторические runs/manual checks остаются доступными без retrofill текущими model/as_of/cohort. Pre11 attempt profile остаётся NULL; отсутствие identity не выводится из started_at/model_name.

## Q0: отдельный strict UploadPinned · 21.10.2026

Текущий local SDK candidate0.1.2 **UNRELEASED**, contracts source `272f23027e4a3ba636305b931f6ab83e5fca8166`, добавляет отдельный client-streaming `UploadPinned`; legacy PREP wire/source и матрица решений выше сохраняются. Первый chunk содержит typed pin/input, последующие — только canonical content≤1MiB/chunk и≤50MiB/file. UploadPinned input SHA относится к canonical bytes; legacy Upload input SHA остаётся hash переданного raw file. Старый peer `UNIMPLEMENTED` не разрешает downgrade на Upload.

ML `pinned_upload` разрешает bundle digest в отдельном operator-owned immutable registry; verified loader читает private verified snapshot, повторно проверяя файлы/optional absence даже при warm cache. До features сравниваются bundle/context/decision rule, actual feature recipe/schema/source resources/runtime и canonical hash/length. Отсутствие/повреждение/несовпадение pin или unsupported recipe fail-closed; causal Upload по-прежнему unsupported. `describe_execution_pin` — trusted local helper без inference, публичного describe RPC нет. Source/runtime должны быть frozen: hot reload не поддерживается, смена установленного окружения требует нового pin.

Backend Q0 сохраняет immutable raw/canonical input и parser v2 recipe/schema/config identity в schema12, отдельно immutable result. [RUN-2 synthetic proof](q0-runtime.md) подтвердил13/13 cases, два kill-after-commit/restart/replay барьера и strict faults до features; partial scientific provenance не превращается в complete replay для research/исторических PREP outputs. Public saved-file/async routes/job/outbox/worker отсутствуют; actual PREP schema12 не активирована. Source/runtime/reproducer ACCEPTED; final docs/publication gate PASS; remote fetch/release/cloud CI OPEN, cold install UNTESTED, real-private policies OPEN, scientific user-deferred/not_assessed. Полный [strict transport/registry contract](ml-service-readme.md#q0-строгая-проверка-сохранённого-synthetic-input) описывает bounds/trust и допустимые ошибки.

## Что остаётся целевым

Полный воспроизводимый пересчёт требует immutable входных bytes/справочников/availability cutoff, точного bundle/runtime, retention и измеренного tolerance raw/risk на заявленной platform. Tier/пороговое решение не должны расходиться даже в пределах tolerance. PREP не сохраняет immutable input archive и не утверждает complete replay. Брокер/новый сервис не требуются для реализованного partial provenance.

Научная приёмка отложена пользователем до новых данных: physical oilfield mapping, непрерывное наблюдение нормального/отказного фонда, закрытый test и согласованная стоимость ошибок. Метрики7–14/7–30суток и industrial quality не выводятся из software tests. DOC-04 и PREP не продвигают causal или отклонённый regime-кандидат.

## Проверки и свидетельства

P1 Senior fixed diff: clean,19/19 hashes совпали; ML suite78PASS; contracts Buf lint/breaking относительно f5d51b6 и official regeneration без drift, Go/Python optional/null/false/0/int64 checks. Проверены equality/plateau/lookback, tail-vs-accumulated counts, unknown/unsupported, bundle cache invalidation и Upload без research context. Native/Linux и пользовательский bulk evidence с оставшимися финальными gates ведёт PREP ledger; не следует объявлять общую PREP принятой только по P1.
