> Снимок документа 22.10.2026. Источник: `pulse-backend/docs/operations.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# Эксплуатация


Q0 synthetic process/resource проверки описаны в [runbook/proof](q0-runtime.md); их запускают на отдельной disposable PostgreSQL, не на PREP. Schema12 — версия исходников, не свидетельство миграции actual PREP. Новые accepts требуют parser v2; DDL читает historical v1/v2, без backfill. Для rollback оставляют additive tables и immutable registry, отключая новых callers; сохранённые входы не пересчитывают через mutable legacy Upload. Это историческая Q0 область. Q1 job/worker/recovery реализованы в отдельном default-off synthetic профиле; [датированный proof](q1-runtime.md) фиксирует actual outcomes и ограничения. Real-private TTL/encryption/access/backup policies остаются gate.
## Миграции

```sh
make migrate
```

- Новая БД: принимается baseline прежних миграций 0001–0012 и применяются последующие Go-миграции до текущей версии13 (Q1 jobs/outbox/inbox поверх Q0 saved-input primitives12).
- Существующий Alembic baseline: требуется `alembic_version=0012_ml_run_drift`; принятие фиксируется в `backend_schema_migration`. Существующая Go-БД обновляется последовательными аддитивными миграциями; cohort9, snapshot10, run profile/cohort identity11, additive file inputs/payload/budget/result12, jobs/outbox/inbox/guard/leases13. Migration13 не создаёт synthetic guard и не включает feature; actual PREP не мигрирован этой проверкой. Таблицы и прежние данные сохраняются.
- Другая ревизия или отсутствующий Alembic marker: команда завершается ошибкой. Сначала приведите БД к поддерживаемой ревизии прежним мигратором.

Приложение не выполняет миграции при HTTP-старте. Перед изменением нужен backup и проверка на отдельной восстановленной БД. Версия11 не backfill-ит profile/cohort_version старых ml_run: NULL означает исторически неизвестную identity. Не удаляйте nullable-поля/таблицы ради rollback приложения. Исходники прежнего монолита — только локальный archive.

## Docker

Контракты передаются как отдельный build context. Текущий Q0 source использует candidate v0.1.2 UNRELEASED, contracts `272f23027e4a3ba636305b931f6ab83e5fca8166`. Для воспроизведения PREP требуется прежний contracts `07213c430318affd140f55dea391cdc36220d93f` и соответствующий PREP consumer source: package0.1.1 само по себе их не различает. Не смешивайте эти профили; используйте проверенные соседние checkout:

```sh
# Из pulse-backend, с соседним pulse-contracts:
docker build --build-context contracts=../pulse-contracts \
  -f build/package/Dockerfile -t pulse-backend .
```

Backend не хранит копию SDK. В runtime-образ попадают только Go-бинарники; credentials доступа к приватному репозиторию не передаются в образ. В CI checkout контракта выполняется перед сборкой через read-only deploy key.

Образ содержит `/app/api`, `/app/migrate`, `/app/adminuser`, `/app/cohort`. Q1 `bin/worker` собирается нативной командой `make build`; текущий Dockerfile worker не упаковывает, готовый worker image этой документацией не заявляется. Настройте адреса БД и ML, доступные из контейнера, затем:

```sh
docker run --rm --env-file .env --entrypoint /app/migrate pulse-backend
docker run --rm --env-file .env -e HTTP_ADDR=:8000 -p 8000:8000 pulse-backend
```

## Задания и остановка

Ручной запуск: `POST /api/v1/ml/wells/refresh`. Повторный запуск во время действующего lock возвращает 409. История: `GET /api/v1/ml/runs`. Совместимый PATCH статуса тревоги принимает `new`, `acknowledged`, `false_positive`.

Предметная обработка через review — отдельная модель: `status=new|in_progress|closed`,
а при `closed` обязателен `conclusion=confirmed|not_confirmed|inconclusive` и комментарий.
Для `in_progress`/`closed` нужен ответственный, для изменения — ключ запроса и revision.
Review записывает историю и отображает состояние обратно в совместимое поле тревоги:
`new` → `new`, `in_progress` → `acknowledged`, `closed/not_confirmed` → `false_positive`,
`closed/confirmed` → `confirmed`, `closed/inconclusive` → `inconclusive`.
Поэтому чтение статуса может вернуть пять значений; расширять старый PATCH до пяти нельзя.
Статус обработки, заключение технолога и вычисленный ML-ярус — разные поля.
Источники: reviews.go (исходник `pulse-backend/internal/service/reviews.go`; в пакет не включён), руководство (исходник `pulse-backend/docs/telemetry-integrations.md`; в пакет не включён).

SIGINT/SIGTERM прекращает приём запросов и дожидается принятых прогонов. После аварии PostgreSQL освобождает lock; следующий прогон помечает незавершённые записи как `error`.

Timeout gRPC не прерывает уже выполняющуюся функцию pandas/CatBoost автоматически. Score не повторяется backend автоматически. Исполнитель ML пока один, с кэшем в памяти; долговечной очереди заданий нет.

## Диагностика

| Симптом | Проверка |
|---|---|
| `/health`: `database=error` | DATABASE_URL, доступность PostgreSQL и размер пула |
| HTTP 401 в legacy | При непустом `API_KEY` проверить точный `X-API-Key`; пустой ключ использует общий локальный сеанс |
| HTTP 401 в local | Проверить персональный вход, действительность `pulse_session`, Secure cookie/HTTPS и отсутствие истечения сеанса; общий API-key не заменяет вход |
| HTTP 403 в local | Проверить роль/разрешение действия и origin изменяющего запроса. Доступ вне scope может маскироваться как 404; это не ошибка ключа ML |
| ML HTTP 503 | ML listener, deadline и согласованность внутреннего ключа; gRPC UNAUTHENTICATED отличается от HTTP 401/403 |
| Прогон `error` | Поле `error` в `/api/v1/ml/runs` и логи обоих сервисов |
| Нет аналитики | Metabase URL, secret и whitelist дашбордов |

## Проверка ML и внутренний ключ

Профили HTTP и gRPC независимы. `AUTH_MODE=legacy` остаётся code default; текущие Compose задают `local` и cohort.
До запуска нужны явные миграции/capture/bootstrap первого администратора,
с точными CORS origins и подходящей Secure cookie. См. [все настройки и defaults](backend-configuration.md)
и порядок персонального входа (исходник `pulse-backend/docs/telemetry-integrations.md`; в пакет не включён).
Исходный работающий pulse-local8080 не переключался в PREP; file defaults и фактически запущенный экземпляр различаются.

В ML пустой/отсутствующий `ML_SERVICE_API_KEY` отключает проверку metadata. Непустой
ключ требует точного `x-ml-api-key` во всех RPC, включая Health. Если оператор включает
ключ, он должен **явно** задать одно значение в backend и в окружении ML-процесса;
Docker probe наследует окружение ML. Обычный compose.local сам не добавляет ключ и не
переносит переменные host shell в контейнер: используйте согласованный deployment
override/средство доставки секретов для обоих сервисов и перезапустите их. Проверку
выполняйте без echo, shell tracing и вывода разрешённой Compose-конфигурации с секретами.
HTTP `API_KEY`/`X-API-Key` этим ключом не являются.

Из установленного окружения ML или внутри его контейнера:

```sh
python -m pulse_ml.healthcheck
# Для другого согласованного loopback-порта:
python -m pulse_ml.healthcheck --target 127.0.0.1:50052
```

Probe обращается к `127.0.0.1:50051`, timeout 3 с, проверяет `status=ok`, закрывает
канал и возвращает 0 только при успехе. При непустом ключе читает его из env процесса,
не из `.env` и не из argv. При auth/transport/deadline/неверном status код ненулевой;
вывод содержит только безопасную категорию ошибки, без ключа/деталей RPC. Ключ для metadata должен состоять из printable ASCII (0x20–0x7e); Unicode/управляющие символы отклоняются без traceback, допустимые пробелы не обрезаются.
Docker/compose.local используют interval15с; isolated compose.prep —10с. Timeout5с и start_period30с; default probe RPC timeout3с.
`0.0.0.0` — bind сервера, не адрес клиента. Если сервер слушает другой адрес/порт,
endpoint probe меняется согласованно. Health подтверждает listener/доступ, но не
загрузку модели, наличие cache/manifest/PG или качество Score. Автопрогон в локальном
Compose остаётся выключенным. Plain-text ML listener не публикуется наружу;
используйте loopback/защищённую внутреннюю сеть.

## Сквозная проверка

`make check`, `make build` и `TEST_DATABASE_URL=... make integration` выполняются из pulse-backend. Integration использует отдельную PostgreSQL и временные схемы, а не рабочую esp_app. Тесты RPC/nullable/int64/экспорта и fixture ML не подтверждают научное качество.

Для реального контролируемого app→Data→ML пути используйте [изолированный PREP runtime](readiness-runtime.md): отдельная PG-копия, loopback8088/8018, model-only ML mount, local auth, membership100 и сохранённый full snapshot. Действующий pulse-local8080 при этой проверке не изменяется.

## Локальное приложение целиком в Docker

Файл `deployments/compose.local.yaml` запускает frontend (Nginx), Go backend, Python ML и Pulse Data. Использует уже существующие `grant-postgres` и `grant-clickhouse`, сети `postgres_default` и `clickhouse_default`, а также соседние split-репозитории и `workspace-data`. Существующие базы и их volumes не пересоздаются. Это конфигурация текущего локального рабочего места.

Перед первым использованием новых local/cohort defaults явно выполните backup/migration/bootstrap/capture, задайте `PULSE_COHORT_VERSION` и проверьте private env. Команды, требующие существующих рабочих БД, не являются безопасным smoke. Для нового проверочного запуска предпочтителен [изолированный профиль](readiness-runtime.md).

После подготовленного rollout (из pulse-backend):

```sh
docker compose -f deployments/compose.local.yaml config --quiet
docker compose -f deployments/compose.local.yaml build
docker compose -f deployments/compose.local.yaml up -d --wait
```

Интерфейс: <http://127.0.0.1:8080/>. API доступен через `/api/v1` на том же origin; `/health` проверяет соединение backend с операционной БД. Прямой API — `127.0.0.1:8000` и `127.0.0.1:8002`; порт 8000 сохраняет совместимость с Vite на 5174. Перед запуском остановите прежний нативный backend, чтобы освободить порт и не запускать второй сборщик.

Адрес Pulse Data из Docker-backend — `http://data:8092`, адрес JSON-источника — `http://data:8092/v1/stream`. `127.0.0.1:8092` внутри контейнера означает сам backend и не подходит. Для существующего источника адрес меняется через API/форму настройки, с сохранением ID, mapping и watermark. Модели, признаки и сценарии подключены read-only; живые `.env` в образы не копируются.

В текущем файле `AUTH_MODE=local`, `APP_SCOPE=cohort`, обязательна `PULSE_COHORT_VERSION`; автоматический ML-прогон выключен. Персональные аккаунты/bootstrap/capture не создаются автоматически. Keycloak не реализован. Порты опубликованы только на loopback; при изменении PULSE_HTTP_PORT согласуйте точный CORS origin. Уже работающий контейнер может сохранять прежний legacy-профиль до отдельного rollout.

Обычная остановка приложения (без удаления данных):

```sh
docker compose -f deployments/compose.local.yaml stop
```

Схема процессов, сетей и зависимостей: [C4 в draw.io](../diagrams/pulse-c4.drawio).

## Прогоны, происхождение и исторические записи PREP

В cohort пользовательский refresh читает app-телеметрию и вызывает bundle-only Upload для каждого подходящего участника. Все100 outcomes сохраняются атомарно с завершением/алертами: scored содержит result; archived/inactive/insufficient/error — null result и причину. Это синтетический presentation фонд, не ML holdout. Research Score доступен только отдельному профилю.

С версии11 попытка получает profile/cohort_version на BeginCohort до чтения members; ранний failed/running run не исчезает из истории. Внешний failure reason обезличен. Legacy pre11 записи остаются unknown; текущий cohort им задним числом не присваивается. Статус технолога — изменяемый read-side overlay; immutable decision snapshot/digest при его изменении не переписывается.

Сохранённый snapshot читается после ML restart без нового вычисления. Typed provenance имеет статус partial: bundle/runtime/source/policy/output identity и hash входа не заменяют сохранённые immutable входные bytes. Нельзя обещать полный replay/as-of или промышленное качество. Источники решения SHAP/окна и computed/training_median/zero_fallback не скрываются.

## Q1 synthetic worker / recovery

Сначала выбрать exclusive test schema/loopback ports и создать fixture manifest,
matching complete cohort и file_job_pilot_guard в отдельном harness. Выполнить
schema migration явно; API/worker сами не мигрируют и не seed-ят guard. Проверить
manifest/hash/schema/pin startup, затем запустить API и один `bin/worker`.
Capability/действия требуют настоящую local session; выключенная feature даёт503.
См. [Q1 protocol](q1-file-jobs.md) и [configuration](backend-configuration.md).

Broker pin `rabbitmq:4.3.6@sha256:3e5cc23235b0eb850f604e8ace10d9a9b6d05be3e36730e53522ee9ead707a9e`
проверен как arm64 synthetic image; перед иной платформой проверьте manifest/digest.
Версию amqp091-go1.15.0 не заменять floating dependency. Main/DLQ queues quorum,
каждая≤64messages/64KiB, mainTTL20min/DLQ1h, message≤1KiB. max_message_size1024,
channel16,connection8,heartbeat10s. Worker manual ack/prefetch1; retry delay в PG.

Broker outage после202 сохраняет pending_dispatch. Запустить dispatcher после
восстановления; unknown confirm безопасно повторяет EventID. Worker crash
восстанавливает reconciler по истечению45s lease с новым fence и конечным attempt
budget. Не использовать ручной INSERT result, reset counts, latest model fallback
или републикацию payload. Quarantine — явная остановка, не бесконечная очередь.
Malformed неизвестные references discard-ятся; известные проверяются/карантинятся
через persisted mapping, без raw error/body в broker, logs или PG action.

SIGTERM останавливает новые claims, отменяет RPC и ждёт bounded operations. Для
app rollback остановить API admission, worker/dispatcher/reconciler, сохранить
PG и Rabbit volumes; старый binary обслуживает legacy records. Pending jobs
возобновляются новым worker с тем же manifest/policy. `compose down -v`, очистка
inputs и schema downgrade не являются rollback этим сценарием.

Resource stop для synthetic proof: GoRSS1GiB,MLRSS2GiB,temp128MiB,brokerRSS512MiB,
PG relation+TOAST1GiB/WALdelta2GiB,free disk<2GiB; broker dedicated volume target512MiB.
Снять evidence/остановить admission при превышении. Это measured pilot limits,
не production SLA и не обещание OS hard cap. Секреты/raw data в evidence не писать.
