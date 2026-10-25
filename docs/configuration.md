# Конфигурация

Q1 synthetic source и его actual runtime gates: [proof/runbook](q1-runtime-2026-10-21.md).
Ни таблица конфигурации, ни наличие OpenAPI routes не активируют профиль на PREP.

Базовая таблица сверена 19.10.2026; Q1-параметры ниже сверены 21.10.2026, Q2 —22.10.2026: [config.Load](../internal/config/config.go), [bootstrap](../internal/bootstrap/application.go) и [пример окружения](../configs/app.env.example). Backend читает `.env` в рабочем каталоге; уже заданные переменные процесса имеют приоритет. Таблицы перечисляют ключи примера. Default означает отсутствие переменной, а не пустую строку: явно заданное пустое значение не заменяется default и для чисел/boolean обычно даёт ошибку.

| Параметр | Default / пример или локальный Compose | Профиль, проверка и область доверия |
|---|---|---|
| `HTTP_ADDR` | `127.0.0.1:8000`; Compose `:8000` | Оба профиля. Bind проверяет HTTP listener; Compose публикует только loopback |
| `DATABASE_URL` | `postgresql://esp:esp@localhost:5433/esp_app` (default разработки); пример заменяет пароль на `CHANGE_ME` | Оба. Секрет операционной БД; разбор/подключение в pgx. Legacy DSN-префикс `postgresql+psycopg2://` преобразуется в `postgresql://` |
| `DB_POOL_SIZE` | `5` | Оба. Целое 2–1000; соединение прогона удерживает advisory lock |
| `API_V1_PREFIX` | `/api/v1` | Оба. Удаляется завершающий `/`; отдельного валидатора маршрута нет |
| `AUTH_MODE` | `legacy` в коде; `local` в примере и Compose | Только `legacy` или `local`. Переключение не создаёт администратора/миграции автоматически |
| `APP_SCOPE` | `cohort` в коде/примере/Compose | Только `cohort` или `research`. Cohort ограничивает и admin; research выбирается явно |
| `COHORT_VERSION` | Пусто в коде; пример `presentation-v1` | Обязательное непустое значение при cohort; membership должен быть сохранён через bin/cohort. Не задано/не найдено — fail-closed без research fallback |
| `AUTH_COOKIE_SECURE` | `true` в коде/примере; false в loopback Compose | ParseBool при старте; cookie персональных сеансов local. Для HTTPS оставить true; false допустим только для loopback HTTP разработки |
| `API_KEY` | Пусто | Только legacy: непустой требует точного HTTP `X-API-Key` для `/api/v1/*`; пустой даёт локальный общий сеанс. Local использует отдельную авторизацию, этот ключ её не заменяет |
| `CORS_ORIGINS` | `http://localhost:3000,http://localhost:5173`; Compose перечисляет 8080/5174 | Оба. Разделение по запятой, trim, пустые элементы удаляются; точное совпадение origin. Local дополнительно проверяет origin изменяющих запросов; это не список исходящих хостов |
| `PARAM_DICT_PATH` | `parameters_dict.json` | Оба. Путь к внешнему справочнику; отсутствие файла даёт пустой список |
| `ML_SERVICE_TARGET` | `127.0.0.1:50051`; Compose `ml:50051` | Оба. gRPC target без `http://`; ошибка транспорта выявляется при RPC. Сам по себе target не включает TLS |
| `ML_SERVICE_API_KEY` | Пусто в коде; пример `CHANGE_ME`, Compose не задаёт | Независим от HTTP-профиля. Backend передаёт `x-ml-api-key`; непустой ключ ML-процесса требует точного совпадения во всех RPC, включая Health |
| `ML_SERVICE_TIMEOUT_S` | `300` | Оба. Положительное конечное число секунд, результат не меньше 1 нс; deadline каждого RPC |
| `ML_AUTORUN_ENABLED` | `true` в коде; **false** в примере/локальном Compose | Оба. ParseBool; автоматический прогон модели, отдельно от сбора телеметрии |
| `ML_AUTORUN_HOUR` | `5` | Оба. Целое 0–23 в `TZ` |
| `TZ` | `Europe/Moscow` | Оба. time.LoadLocation; расписание и naive timestamps БД |
| `METABASE_SITE_URL` | Пусто | Оба. Адрес Metabase; удаляется завершающий `/`, отдельного URL-валидатора в Config.Load нет |
| `METABASE_EMBED_SECRET` | Пусто | Оба. Секрет HS256 подписанных iframe URL; не включать в клиентскую конфигурацию/логи |
| `METABASE_DASHBOARDS` | `[]` | Оба. JSON-массив `{id,name}`; ошибка JSON отвергается при старте, null нормализуется в пустой массив |
| `METABASE_TOKEN_TTL_S` | `3600` | Оба. Целое ≥1, срок жизни подписанного URL |
| `INTEGRATION_ALLOWED_HOSTS` | Пусто в коде; пример `127.0.0.1,localhost`, Compose добавляет внутренние имена | Оба. Читается bootstrap, не Config.Load. Точные hostname без схемы/порта, регистр не важен; разрешённость проверяется адаптером при обращении к внешнему источнику и Pulse Data. Пустой список не разрешает произвольный хост |
| `INTEGRATION_SECRET_KEY` | Пусто | Оба. Отдельный постоянный ключ AES-GCM: ровно 32 байта после base64-декодирования. Проверяется при Seal/Open непустого секрета, а не при старте. Пустой допускает источники без сохраняемого секрета; не позволяет сохранить непустые credentials |
| `TELEMETRY_DATA_API_URL` | Пусто в коде; пример `http://127.0.0.1:8092`, Compose `http://data:8092` | Оба. Bootstrap передаёт в Archive; http/https и allowed host проверяются при запросе, redirects запрещены. URL внутри контейнера должен адресовать Pulse Data, не loopback backend. Это чтение архива, не адрес источника коллектора |

## Профили доступа

`legacy` сохраняет общий API-key/локальный сеанс с административными правами. `local` включает персональные сеансы и роли; сначала оператор явно применяет миграции и создаёт первого администратора через `bin/adminuser` с паролем из защищённого stdin, затем меняет профиль и перезапускает API. Порядок и ограничения: [персональный доступ](telemetry-integrations.md#этап-4--персональный-доступ-и-будущий-sso). PREP согласовал включение local: оба Compose-файла теперь задают local/cohort. Это не мигрирует уже работающий экземпляр. Для compose.local требуется PULSE_COHORT_VERSION; для изолированного compose.prep — PREP_COHORT_VERSION (default presentation-v1). Backup/migration/bootstrap/capture обязательны до запуска; [порядок PREP](readiness-runtime.md).

HTTP `API_KEY`, внутренний `ML_SERVICE_API_KEY`, ключ шифрования интеграций и пароли БД — разные секреты. Наличие одного не включает другой. Plain-text gRPC ограничивается loopback/защищённой внутренней сетью. Как согласованно задать внутренний ключ и проверить probe без его вывода: [эксплуатация](operations.md#проверка-ml-и-внутренний-ключ).

Некорректные числа, boolean, timezone и JSON, перечисленные в Config.Load, отвергаются при старте; адреса интеграций/ключ AES проверяются при использовании. CORS не является аутентификацией. Секреты, пароли и живые `.env` в Git и журналы не включаются.

## Граница cohort

Версия фиксирует ровно100 зарегистрированных Pulse ID независимо от active/archive. Изменение lifecycle не меняет membership. Видимый набор пересекается с principal scope; чужие ID, статистика, сохранённые runs и Excel не обходят границу. Глобальные записи НГДУ/цехов в cohort-профиле запрещены403 даже admin: справочник может быть общим с research. Настройка профиля не удаляет исторические данные.

## Изолированный Q1 synthetic профиль

`FILE_CHECK_MODE=off` по умолчанию; `FILE_CHECK_ALLOW_REAL=false` обязательно,
значение true останавливает startup. Для synthetic нужны AUTH_MODE=local,
APP_SCOPE=cohort и полная текущая100-well cohort. Явные FILE_CHECK_MANIFEST,
FILE_CHECK_MANIFEST_SHA256 (`sha256:` точных bytes), FILE_CHECK_RUN_ID должны
соответствовать server-owned JSON и readonly PG guard/current_schema.
Обычная migration guard не создаёт. Профиль не включает продуктовый Compose.

Manifest≤1MiB: `run_id,test_schema,account_ids,schema_path,schema_sha256,policy`.
Policy — typed `domain.FileJobPolicy`: RunID,CohortVersion,CohortManifestSHA256,
AllowedInputs,ActiveGlobal≤8,ActivePerAccount≤2. ManifestSHA256 в hashed JSON пустой;
loader подставляет внешний hash, TestSchema и AllowedAccountIDs из top-level.
AllowedInputs содержит WellID,RawSHA256,Format,ClientSpecJSON,ClientSpecSHA256,
ExecutionPin. ClientSpecJSON получают через `service.FileJobClientSpecJSON`:
Q0 нормализация spec до разрешения mapping, очищенный schema hash, sorted mappings.
SchemaPath — доверенный локальный params snapshot, проверенный по SchemaSHA256.

`bin/worker` использует FILE_JOB_PRODUCER_AMQP_URL и FILE_JOB_AMQP_URL для двух
отдельных scoped connections producer/consumer. API broker URI не нужен.
Credentials не передаются browser и не выводятся в logs. Synthetic broker:
[compose.q1-synthetic.yaml](../deployments/compose.q1-synthetic.yaml), exact
RabbitMQ4.3.6/digest, собственные vhost/network/volume и loopback port.
Bootstrap user задаётся оператором, затем runtime использует отдельные scoped
users. Никакой management UI, default shared credential или production enable.

## Изолированный Q2-C1 synthetic профиль

[Q2 protocol](q2-cohort-jobs.md) описывает internal API-only путь; [runtime evidence](q2-runtime-2026-10-22.md)
отдельно фиксирует фактические проверки. По умолчанию профиль выключен; Compose
не включает cohort consumer. Нужны `AUTH_MODE=local`, `APP_SCOPE=cohort`, существующая
полная100-well cohort и текущий admin с explicit scope на всех100, входящий в
server-owned allowlist. API key/legacy auth эту authority не заменяет.

| Переменная | Default | Synthetic проверка |
|---|---|---|
| `COHORT_JOB_MODE` | `off` | Только `off` или `synthetic` |
| `COHORT_JOB_ALLOW_REAL` | `false` | ParseBool; true или malformed отвергаются даже при mode off |
| `COHORT_JOB_MANIFEST` | Пусто | Trusted local descriptor file, обязателен в synthetic |
| `COHORT_JOB_MANIFEST_SHA256` | Пусто | `sha256:` +64 lowercase hex, hash точных descriptor bytes |
| `COHORT_JOB_RUN_ID` | Пусто | Непустой pilot identity, должен совпадать с descriptor и PG guard |
| `COHORT_JOB_TEST_SCHEMA` | Пусто | Explicit isolated schema, regex `[a-z][a-z0-9_]{2,62}`, не public/pg_*/information_schema; current_schema совпадает |
| `COHORT_JOB_ML_TARGET` | Пусто | Непустой dedicated gRPC target, отличается от `ML_SERVICE_TARGET`; Q2 worker RPC timeout10s |
| `COHORT_JOB_AMQP_URL` | Пусто | Scoped consumer amqp/amqps URL с path `/pulse-q2-synthetic`, без query/fragment |
| `COHORT_JOB_PRODUCER_AMQP_URL` | Пусто | Отдельное scoped producer connection, тот же isolated vhost |

В Q2 URL не добавляют `?heartbeat=10`: любой query отвергается strict config.
AMQP adapter сам задаёт heartbeat10s. Initial proof ошибку такого private URL
сохраняет как failed setup, исправление URL не требует изменения product source.

Descriptor≤512KiB имеет поля `run_id`, `test_schema`, `allowed_account_ids` (1–16),
`cohort_version`, `cohort_manifest_sha256`, `fixtures` (1–2). Fixture содержит typed
`manifest` и `inputs` с `well_id,path`; root-relative regular paths разрешаются
от каталога descriptor, symlink/traversal отвергаются. Manifest включает fixture
identity, UTC as_of, full execution pin, capture recipe и100 members с canonical
hash либо no-compute reason. Точную структуру задаёт
[SyntheticCohortManifest](../internal/bootstrap/cohort_jobs.go) и
[CohortFixtureManifest](../internal/domain/cohort_jobs.go). Hash/commitments получают
shared Go domain helpers, не собственной сериализацией JSON.

API проверяет capture recipe `BackendBuildSHA256` против actual API executable,
загружает≤2fixtures/48MiB и валидирует точный seven-channel C1 CSV;32channels — общий
resource ceiling, не допустимое расширение header. Descriptor создают после build.
Worker загружает только trusted descriptor/policy/fixture permits: original input
files и API capture SHA против worker binary не проверяются повторно. Для recovery
обязательны неизменённые accepted PG inputs, pin и соответствующая current policy;
original input files после202 могут отсутствовать. Worker binary hash записывают
отдельно в proof. Обе роли сверяют PG guard; миграция/API/worker guard не создают.

`ML_SERVICE_API_KEY` согласуется с выделенным Q2 ML process. AMQP/DB/key остаются
private environment; URL/credentials не передаются browser/logs. Приложение читает
`COHORT_JOB_*`; `Q2_*` в [process test](q2-runtime-2026-10-22.md#воспроизведение-из-standalone-checkout)
относятся к test harness. Параметры не включают real data или production rollout.

Принятое [owned-runtime gate уточнение](q2-cohort-jobs.md#принятое-уточнение-owned-runtime-gate)
не вводит новых env или повышенных timeout: Next/Renew/Checkpoint total5s вместе
с serializer/TX/gate wait, Finish total10s и gate query≤min(remaining,5s). WAIT только
для четырёх операций уже owned claim connection; admission/legacy/new claim/cancel/
outbox/reconcile TRY сохраняются. Correction SOURCE APPROVED, все8findings CLOSED;
latest module gates и3actual cases PASS, internal synthetic runtime gates VERIFIED.
Measured resources и teardown подтверждены в dated proof; проверки публикации комплекта учитываются отдельно, real/production/scientific не приняты.

## Изолированный Q2-C2 synthetic профиль

[Протокол](q2-collection-jobs.md) и [датированный runtime](q2-collection-runtime-2026-10-22.md)
описывают отдельный collection path; source статус актуализирован23.10.2026.
Source config имеет ровно семь COLLECTION_JOB_*
переменных; COLLECTION_JOB_ALLOW_REAL не существует. Mode принимает только off/synthetic.

| Переменная | Default | Synthetic проверка |
| --- | --- | --- |
| `COLLECTION_JOB_MODE` | `off` | Только `off` / `synthetic` |
| `COLLECTION_JOB_MANIFEST` | Пусто | Explicit trusted regular local JSON file ≤65536 bytes; symlink/FIFO отвергаются |
| `COLLECTION_JOB_MANIFEST_SHA256` | Пусто | Ровно 64 lowercase hex **без sha256:**, SHA точных manifest bytes |
| `COLLECTION_JOB_RUN_ID` | Пусто | `[A-Za-z0-9_-]{1,64}`, совпадает policy/guard |
| `COLLECTION_JOB_TEST_SCHEMA` | Пусто | `[a-z][a-z0-9_]{0,62}`, не public/information_schema/pg_*, совпадает current_schema/guard |
| `COLLECTION_JOB_AMQP_URL` | Пусто | Scoped consumer amqp/amqps URL, path `/pulse-q2c2-synthetic`, без query/fragment |
| `COLLECTION_JOB_PRODUCER_AMQP_URL` | Пусто | Отдельный scoped producer connection, тот же isolated vhost |

Обязательны `AUTH_MODE=local`, `APP_SCOPE=research`, non-public disposable DB/schema
и matching explicit guard. Manifest top-level: run_id, test_schema,
manifest_sha256 (в файле пусто, loader подставляет actual hash), fixture_origin,
allowed_account_ids (1–2), bindings (1–2), fixtures (1–4). Binding: source_id,
well_id (Pulse), external_id (отрицательная decimal string), identity_kind
unitek/armits. Fixture: id/protocol/horizon/retention_start/empty/channels/mappings.
Нужны current allowlisted account и explicit scope на presentation well; full100
и ML lane не требуются. Fixed actual origin `http://fixture:8080`; endpoint source
точно `/v1/collection-capture`, kind=json_pull/status=running, credentials отсутствуют.

`ML_AUTORUN_ENABLED=false` — точное existing env имя; AUTO_RUN не env параметр.
Source interval_seconds=86400 и next_poll в будущем задаются explicit own seed,
чтобы legacy scheduler не выполнял unrelated collection. Forbidden ML RPC counter
всё равно обязан наблюдать 0. Pool ≤8/process, workers ≤2/1inflight, лимиты
[заданы протоколом](q2-collection-jobs.md#схема-budgets-и-откат), не новым набором env.

`bin/collection-worker` объединяет consumer/dispatcher/reconciler. API в mode off
проверяет schema15 и предоставляет current authorized GET без manifest/guard/broker;
POST/cancel возвращают503. Startup не применяет миграции и не создаёт synthetic seed.
Не переносите C1 vhost/hash prefix/ML target в C2 профиль.

Current C2 source-status23.10.2026: SamplerSuccessDeadline30/H20 SOURCE APPROVED,78Python controls
PASS;3core+20harness findings CLOSED. Sole host transport — fixed bounded stdio;
redundant PG/Rabbit Docker `-p` отсутствуют. Provision создаёт own readonly
90-collection-test-metrics.conf до boot, collector=false сохраняется после restart;
bootstrap выполняется с --user rabbitmq. Ready/quarantine требуют4observed
nonnegative integer gauges с messages=ready+unacked; missing не значит0, A04 ждёт
unacked1 до KILL. [Точный runtime guide](q2-collection-runtime-2026-10-22.md)
разделяет source/probe/prior failures и принятый ACT14 runtime; publication PASS отдельно.

C2 worker reconnect budget конечный8/backoffs на Consume lifetime; после exhaustion
operator/orchestrator restart нужен вне процесса. Для этого proof не добавлены env,
auto-restart policy или гарантии бесконечного daemon lifetime; durable attempts3,
lease30s, fences и исходные resource caps прежние. Source SamplerSuccessDeadline30/H20,
78Python tests отделены от actual ACT14 all11 PASS; runtime VERIFIED, publication PASS.

C2 standalone source-state test workflow: сохранение source PUT всегда draft,
activation — отдельный existing status PATCH, который resets next_poll=now(). Для
own synthetic proof helper временно guard-ит exact ID/running config update,
атомарно задавая future next_poll, и снимает trigger finally. Source/job configs,
COLLECTION_JOB_* env, production migrations и resource limits не меняются.


## Режим RBAC

`RBAC_MODE=compat` по умолчанию; допустимы только `compat` и `enforce`.
Local compatibility writers dual-write-ят фиксированные builtin assignments,
modern access mutations запрещены до cutover. Enforce требует persisted activation;
compat после activation запрещён при старте API и всех workers. Legacy остаётся
отдельным trusted auth profile с недоступным graph management.

См. [RBAC runbook](rbac.md) для backup/drain, `bin/adminuser -activate-rbac`,
startup schema policy, восстановления и rollback. Применение миграции16 само
не активирует RBAC и не меняет default-off feature flags.

## Персональные уведомления

`NOTIFICATIONS_UI_ENABLED=false` — default. Boolean включает только персональную
выдачу; capture читает однократную persisted activation независимо от UI.
Требуются local sessions и готовая schema19 даже при UI-off; startup только
проверяет готовность. [Контракт и ограничения](notifications.md).
