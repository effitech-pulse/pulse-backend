> Снимок документа 21.10.2026. Источник: `pulse-ml-service/README.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# Пульс скважины · ML-сервис

Python gRPC-сервис исполнения модели: риск по скважинам, SHAP, история окон, дрейф и проверка файла телеметрии. Операционные задания и обратная связь остаются в Go-backend; алгоритмы и обучение — в `pulse-model`.

## Запуск

Для проверенного native-профиля используйте CPython 3.13, фиксированные
`runtime-constraints.txt` и wheels согласованных исходников model/contracts/service.
Подробный порядок чистой установки, hashes и её ограничения:
[установка runtime](runtime-installation.md). `requires-python>=3.11` в metadata
не подтверждает совместимость всех фиксированных вычислительных версий с 3.11.

В split workspace `.venv` — ссылка на общую среду: не пересоздавайте её и не меняйте
зависимости ради smoke. Новую native-среду создавайте в отдельном каталоге `/tmp`.
Для запуска в уже подготовленном workspace используйте существующую `.venv`.

Настройте `.env`:

| Параметр | Назначение |
|---|---|
| `ML_GRPC_BIND` | Listener, по умолчанию `127.0.0.1:50051` |
| `ML_SERVICE_API_KEY` | При непустом значении требует точного `x-ml-api-key` во всех RPC, включая Health; пустой/отсутствующий отключает проверку. Backend и probe должны получить согласованное значение |
| `ML_DATA_ROOT` | Каталог модели, манифеста и кэшей |
| `PG_*` | Только research Score/History: PostgreSQL `esp` для контекстных признаков; bundle-only Upload её не требует |

Для research Score ожидается структура ниже; bundle-only Upload требует только `models/stage1_binary_v1/`:

```text
ML_DATA_ROOT/
  models/stage1_binary_v1/
  cache/features_v3/
  manifest/grant_manifest_1000.xlsx
```

```sh
.venv/bin/python -m pulse_ml.server
```

При запуске из исходников сервис читает `.env` из корня этого модуля (переменные процесса приоритетны). В wheel-развёртывании передавайте настройки через окружение процесса; probe `.env` не читает. Данные, веса и секреты в Git не включаются.

## Контракт

Схема и generated SDK находятся в отдельном [pulse-contracts](https://github.com/effitech-pulse/pulse-contracts). ML-сервис использует metadata `effitech-pulse-contracts==0.1.2` (Q0 local candidate, UNRELEASED) и импортирует `pulse.ml.v1`. Локальных `.proto` и generated копий здесь нет; protoc в runtime не нужен. Текущие GitHub/GitLab CI-конфигурации закрепляют accepted local contracts source `272f23027e4a3ba636305b931f6ab83e5fca8166`, model source `f5c2a3de031ff4794194a219f048c9bf8c7c4f45`; старые package version/tag сами по себе не идентифицируют новые поля. SDK0.1.2 остаётся UNRELEASED: локальные install/build используют принятую source/wheel identity. Доступность этого commit на private remotes остаётся OPEN; remote push/cloud job не выполнялись и публичная публикация не предполагается.

`Score` выполняется синхронно. Backend устанавливает deadline и хранит результат прогона. Metadata `x-ml-api-key` проверяется во всех RPC **при непустом ключе ML-процесса**. Upload — поток чанков до 1 MiB, предел файла 50 MiB; минимум 30 суток истории. Два месяца — рекомендация. Целиком недоступные признаки заменяются training median, иначе 0; прочие NaN — 0. `value_source` раскрывает computed/training_median/zero_fallback. Текущий Upload использует legacy raw `>` и K по всему файлу, не гарантирует эквивалентность Score; displayed risk не подтверждён как вероятность будущего отказа. Полный перечень RPC — в README контрактов.

## Q0: строгая проверка сохранённого synthetic input

`UploadPinned` — отдельный streaming RPC SDK0.1.2; первый chunk содержит только
`PinnedUploadHeader{pin,input}`, остальные — только canonical content (≤1 MiB/chunk,
≤50 MiB total). Старый Upload сохраняет прежнее поведение. Сервер проверяет API key,
mandatory identity, exact canonical SHA/length и перед features сравнивает actual
bundle, decision rule, schema, recipe, source/resources и runtime с полным pin.
Несовпадение/утрата bundle даёт FAILED_PRECONDITION; invalid header/hash/CSV —
INVALID_ARGUMENT; пределы/result cap — RESOURCE_EXHAUSTED. Downgrade на Upload нет.

`ML_BUNDLE_REGISTRY` указывает на отдельный operator-owned registry, отличный от
mutable `ML_DATA_ROOT/models`. Ни browser, ни RPC не импортируют pickle. Доверенный
оператор может использовать `python -m pulse_ml.bundle_registry import --source
<trusted-local-bundle> --registry <separate-root>`: importer проверяет basename/file
inventory, копирует файлы без symlinks/hardlinks, fsync и atomic no-replace публикует
`<registry>/<64hex>/`. Существующий digest не заменяется; GC не выполняется.
Hash удостоверяет целостность, а доверие к executable pickle остаётся обязанностью
оператора. На каждой попытке, включая warm cache (max1, отдельно от legacy), все
файлы/optional absence проверяются заново; loader читает private verified snapshot.

Trusted local helper `describe_execution_pin(name, digest, registry_root=Path(...))`
в `pulse_ml.pinned_upload` возвращает JSON-compatible typed pin из actual loaded
policy/schema/config/source/runtime без вычисления features/predict и без путей.
Код/установленные зависимости должны быть read-only, без hot reload; после смены
wheel/runtime нужно получить новый pin. Это helper, публичного describe RPC нет.
Strict CSV принимает header timestamp + sorted known canonical measurements/debit,
UTC RFC3339Nano с Z, sorted unique timestamps, finite numeric или empty missing;
не выполняет alias guessing/resample/source reads. Bounds: 100000 rows,64 targets,
64KiB field,30–366days; результат≤2MiB с явным отказом без truncation. Hygiene до
агрегации сохраняет marker/valid zero semantics, raw `>`/K и norm/insufficient.

Тестовый экспорт `tests/q0_synthetic.py:export_fixture(root)` создаёт только fixed
serializable fake без fit и producer/ESP hourly fixture; `PYTHONPATH=tests` нужен
новому test process для десериализации fake. Воспроизводится actual feature path,
а quality остаётся `not_assessed`, context/provenance partial. Сохранённый input
не является durable job/worker. Real-private enable закрыт до owner policy для
TTL/encryption/operator access/backup/quota; реальные bundles не импортировались.
Новый contracts candidate используется только из принятого frozen wheel/source;
public package/tag/download не предполагаются. Docker named contexts сохраняются.

## Работа исполнителя

- Один процесс владеет моделью и кэшем. Несколько реплик потребуют общего хранилища результатов.
- Пересчёт сериализован. Успешный контекст публикуется атомарно; ошибка оставляет предыдущий. Чтение готового контекста работает во время пересчёта.
- После рестарта research context и отдельный Upload bundle загружаются лениво; Upload не вызывает Score/get_context и не требует research cache/manifest/PG. Bundle fingerprint проверяется на каждом запросе и до/после загрузки. `Health` не загружает модель.
- Отмена RPC проверяется до/после вычисления и между чанками; уже выполняющийся pandas/CatBoost вызов автоматически не прерывается.
- SIGTERM/SIGINT даёт активным RPC до 300 секунд на завершение.
- Транспорт по умолчанию без TLS: loopback или защищённая внутренняя сеть.

## Liveness и внутренний ключ

```sh
python -m pulse_ml.healthcheck
# Только если listener согласованно перенесён на другой loopback-порт:
python -m pulse_ml.healthcheck --target 127.0.0.1:50052
```

Единый Docker/Compose probe проверяет Health на `127.0.0.1:50051` с timeout 3 с и
ожидает `status=ok`. Канал закрывается при любом исходе; ошибки RPC, транспорта,
deadline и иной status дают ненулевой код. Он читает `ML_SERVICE_API_KEY` только
из окружения процесса: непустой ключ передаётся в metadata, пустой — без metadata.
Ключ и детали ответа не выводятся. Probe отклоняет ключ вне printable ASCII (0x20–0x7e), включая Unicode/управляющие символы, с безопасной ошибкой без traceback; допустимые пробелы не обрезаются. Health не загружает модель и не проверяет
готовность Score/cache/manifest/PG или качество прогноза.

При включении ключа оператор явно задаёт одно значение backend и ML-процессу;
probe в контейнере наследует env ML. Compose не импортирует ключ host shell
автоматически — нужен согласованный deployment override/доставка секретов обоим
сервисам. Ключ по умолчанию этим изменением не включается. HTTP `API_KEY` и
`AUTH_MODE` backend независимы от этого механизма. Транспорт остаётся plain-text:
ML listener доступен только через loopback или защищённую внутреннюю сеть;
`0.0.0.0` используется для bind, не как адрес probe.

## Проверки и выпуск

```sh
.venv/bin/python -m pytest
.venv/bin/python -m pip wheel --no-deps . -w dist
```

Тест всей подписи признаков пропускается без боевой модели; SKIP не является модельным PASS.
Чистый runtime smoke без model/PG, платформенные ограничения и установка release wheels
описаны в [runtime-installation.md](runtime-installation.md). GitHub требует read-only
`MODEL_DEPLOY_KEY` и `CONTRACTS_DEPLOY_KEY`; GitLab — доступ runner к `CONTRACTS_REPOSITORY`
и разрешённый `CI_JOB_TOKEN` модели. Refs закреплены точными source SHA выше; для cloud они должны быть доступны private remotes.
Значения секретов в документацию не включаются. CI и Docker применяют runtime constraints при первоначальной установке model/contracts/service и выполняют pip check; девять constraints не являются полным transitive lock. Локальный эквивалент CI не является выполненным cloud job. Схему меняйте в
`pulse-contracts`, затем обновляйте версии обоих потребителей.


## Локальный перенос и контракт признаков

В текущем workspace data/ и .venv/ являются ссылками на общее хранилище и окружение. .env ссылается на локальную конфигурацию workspace; ML_DATA_ROOT уже указывает на workspace-data.

Для старых stage1_binary_v1 весов сервис явно сохраняет legacy_v0 расчёт, чтобы изменение библиотеки не подменило входные признаки. causal_v1 комплект требует своего normalizer.pkl и правила тревоги в config.operating_point. Оно использует сырой P > threshold и ограниченную историю; калиброванный балл предназначен для отображения и не доказывает частоту будущих отказов. Новый исследовательский кандидат автоматически не выбирается. Проверка такого кандидата требует совместимого кода модели, а не исходного тега v0.1.0.

## Матрица RPC/происхождение

[Матрица совместимости и provenance](model-rpc-compatibility.md) описывает фактические шкалы, операторы, K/lookback и ограничения Score/Upload/History для legacy_v0 и causal_v1. PREP реализовал shared evaluator, typed decision/provenance поля и явный guard: causal Upload возвращает unsupported_feature_recipe, unknown context отклоняется. Causal Score/History требуют normalizer/operating_point; promotion не выполнялся. Legacy Score/Upload сохраняют разные именованные политики. Provenance всегда partial: bundle/code/runtime identity и input digest не гарантируют replay без immutable входа. Backend хранит полный cohort run snapshot; historical absence остаётся unknown.


## Состояние документации — 19.10.2026

Исходные находки и журнал очереди A сохранены в локальном workspace: `../tasks/pulse-backend/docs/documentation-audit-2026-10-19.md` и `../tasks/pulse-backend/docs/documentation-queue-a-execution-2026-10-19.md`. [Матрица проверок](verification-matrix.md) различает обязательный gate, настроенный CI, локальный PASS и допустимый SKIP. [Автономный handbook](../README.md) содержит снимок межмодульных документов и политику обновления источников.

Межмодульные ссылки рассчитаны на соседние checkout split workspace и не добавляют runtime-зависимость модуля. В standalone checkout используйте локальные README/AGENTS; межмодульный комплект доступен после получения pulse-backend. Очередь A документационная; продуктовая очередь B и промышленная приёмка модели ею не выполнены.

## PREP evidence и граница приёмки

Native macOS clean wheels:78tests/0skip и pip check; fresh Linux ML image:78tests; bundle-only Upload и пользовательский bulk проверены в isolated runtime без research cache/PG. Совместимость RPC и модельных комплектов описана в [постоянной спецификации](model-rpc-compatibility.md). Подробные команды и свидетельства PREP сохранены в локальном workspace: `../tasks/pulse-backend/docs/ready-for-development-2026-10-19.md`. Новая научная оценка отложена пользователем до подключения независимых данных; качество/7–14 суток не переоценены.

## O1: локальная наблюдаемость транспорта

gRPC executable пишет в stdout ограниченные JSON records (не более 8192 байт):
UTC timestamp, severity/event, service identity и безопасные status/duration.
`x-request-id` — ID попытки, отдельно от бизнес `request_id`: одно значение
`[A-Za-z0-9_-]{16,64}`, иначе новый случайный 32-символьный hex. W3C `traceparent`
валидируется официальным propagator (одно видимое значение, до 512 байт);
`baggage`/`tracestate` и ключ `x-ml-api-key` не попадают в события/spans.
В grpcio 1.78 C-core нормализует повторные wire `traceparent` до последнего
значения ещё перед Python interceptor: этот слой проверяет доступное ему значение,
а распознавание исходных wire-дублей не обещается. ID/parent не влияют на auth.

Core OTel API/SDK **1.38.0**, semconv **0.59b0**: manual SERVER span охватывает
выполнение unary и весь stream-unary Upload, включая чтение чанков, создание
результата, отказ авторизации, ошибку и отмену. Контекст очищается в `finally`.
Отмена по-прежнему не гарантирует прерывания pandas/CatBoost. В production provider
с локальным 100% sampler нет exporter, сети, retention списка или batch queue;
exporter используется только в тестах. Resource строится явным allowlist,
`OTEL_RESOURCE_ATTRIBUTES` не импортируется. `SERVICE_VERSION` (по умолчанию
`unknown`) и `DEPLOYMENT_ENVIRONMENT` (`local`) допускают до 128 безопасных ASCII
символов; instance — случайный opaque ID. Span limits: attributes 32, events 16,
links 8, string attribute 256. Debug выключен по умолчанию.

Только фиксированные события и разрешённые поля проходят форматтер: без bodies,
имён файлов, телеметрии, metadata maps, raw exception/message/stack и credentials.
Неисправный formatter/stdout sink не меняет ответ RPC; потеря диагностики возможна.
Stdout сам не гарантирует retention или отсутствие OS blocking. Runtime profile
координатора должен задавать Docker `local`, `max-size=10m`, `max-file=3`;
CLI retention задаёт launcher. Collector и централизованное хранение относятся к O2.

Проверка: `python -m pytest tests/test_observability.py`; реальные listener-тесты
требуют разрешённого loopback. API/SDK требуют Python >=3.9 и не повышают declared
floor проекта >=3.11. Сохранённый численный runtime-profile с numpy 2.5.1 требует
Python >=3.12; нативный O1 smoke выполнен на Python 3.13. grpcio 1.78.0/protobuf
6.33.6 и численные constraints сохранены. Это software gate, не оценка качества модели.
