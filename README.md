# Пульс скважины · Backend

Q2-C2 ·статус23.10.2026: добавлен отдельный конечный synthetic-only collection path:
`POST /api/v1/collection-jobs` → immutable captured response → атомарные
measurements/checkpoint/coverage. `COLLECTION_JOB_MODE=off` по умолчанию;
допустимы только off/synthetic. [Протокол](docs/q2-collection-jobs.md),
[редактируемый C4](docs/q2-collection-jobs.drawio),
[статусы и runtime](docs/q2-collection-runtime-2026-10-22.md).
Core и harness source APPROVED тем же Senior,3core+20harness findings CLOSED.
Full module check/integration/build и ACT14 all11/runtime/resources/ML0/teardown PASS.
Code source commit `1a824a63c068f828a9d4dfa663b8036cd426e191`; docs/publication gate PASS (standalone validation/source-check), docs commit оформляется отдельно. App schema15 применяется явно.
C2 не вызывает ML и не продвигает legacy last_measured. Рабочие PREP/UI/сбор,
real enable и scientific acceptance не переключены. Ниже C1 — историческая область;
C2 добавляет следующий самостоятельный slice.

Q2-C1 ·22.10.2026: реализован отдельный internal synthetic-only durable cohort путь
с full100 freeze до202, persisted checkpoints и атомарной публикацией полного
snapshot. `COHORT_JOB_MODE=off` default, `COHORT_JOB_ALLOW_REAL=false` обязательно.
[Протокол/API](docs/q2-cohort-jobs.md), [редактируемый C4](docs/q2-cohort-jobs.drawio)
и [runtime gates/runbook](docs/q2-runtime-2026-10-22.md) разделяют source freeze,
review, actual process/resource и publication evidence. Source APPROVED тем же Senior,
все8findings CLOSED; latest module gates и все3actual cases PASS. Технические runtime
gates VERIFIED в internal synthetic-only scope: max/restart/mixed/rollback, physical
stale, capacity rejection, measured resources и teardown. Проверки публикации комплекта учитываются отдельно; scientific not_assessed, real/production не приняты. Рабочий refresh/UI и collection
не переключены; collection — отдельный следующий slice.


Q0: внутренние stored-file primitives и strict `UploadPinned` прошли synthetic RUN-2: **13/13 PASS** на одном frozen binary; source/runtime/reproducer приняты, final docs/publication gate PASS. [Архитектура](docs/q0-architecture-2026-10-20.md), [результаты и воспроизводимые команды](docs/q0-runtime-2026-10-21.md). Это историческая принятая Q0-область. Q1/F2 source реализует отдельные opt-in saved-file routes, PostgreSQL outbox/inbox и Go worker; [Q1 proof/runbook](docs/q1-runtime-2026-10-21.md) отделяет source approval от runtime/browser gates. `FILE_CHECK_MODE=off` по умолчанию, `FILE_CHECK_ALLOW_REAL=false` обязательно; actual PREP не переключён. Real-private enable и scientific acceptance остаются закрыты/отложены соответственно.
## Актуальный портал · 21.10.2026

Рабочая локальная сборка: `http://127.0.0.1:8088/`, API8018, существующая
изолированная БД pulse-prep. Для frontend-релиза используйте overlay
`deployments/compose.portal.yaml` после compose.prep.yaml и compose.o1-f1.yaml.
Он не перезапускает backend/ML/Data. Старые pulse-local app-контейнеры8080 удалены;
БД и сохранённые результаты сохранены. Источник выборки в продукте — Pulse Connector.
Приёмка и команды: перенос портала (локальный архив задач: `../tasks/pulse-frontend/docs/portal-refresh-2026-10-21.md`).


HTTP API системы диагностики УЭЦН. Backend ведёт реестр скважин и отказов, отдаёт данные интерфейсу, запускает ML-прогоны и сохраняет обратную связь технологов.

**Стек:** Go · Gin · PostgreSQL (pgx + Squirrel) · gRPC/protobuf. Вычисления модели и обучение выполняются в Python-проектах `pulse-ml-service` и `pulse-model`.

Настраиваемый RBAC v1: [права, workers и staged activation](docs/rbac.md),
[редактируемая C4-схема](docs/rbac.drawio). `RBAC_MODE=compat` — default; переход
в enforce выполняется отдельным maintenance cutover. Source не активирует runtime.

## Быстрый запуск

Нужны Go 1.25+, PostgreSQL 16 и соседний `pulse-contracts`. Для страниц риска также запустите `pulse-ml-service`.

### 1. Подключите локальные контракты

В подготовленном каталоге `repository-split` уже есть `go.work`. При работе с отдельными checkout расположите репозитории рядом и создайте workspace в их общей папке:

```sh
go work init ./pulse-backend ./pulse-contracts
go work edit -replace=github.com/effitech-pulse/pulse-contracts@v0.1.2=./pulse-contracts
cd pulse-backend
```

Замену используем только в локальном workspace. `go.mod` фиксирует локальный candidate SDK `v0.1.2` (**UNRELEASED**); Q2 G2 contracts source checkpoint — `1d0b2b62d2cb73d18ab69738431089edf69621f8` (capability/docs delta, wire/generated SDK не менялись). Исторический Q0 binding — `272f23027e4a3ba636305b931f6ab83e5fca8166`; public tag/checksum не предполагается; remote fetch/pipeline не проверены и требуют отдельной разрешённой публикации contracts. Локальная сборка использует этот exact reviewed source; исходники контрактов в backend не копируются. Docker build использует этот принятый checkout через named context `--build-context contracts=../pulse-contracts` (build only). Для Q0 применяйте отдельный GOWORK; общий workspace `go.work` не изменяется. Для PREP эта package version недостаточна: используйте проверенный source commit contracts `07213c430318affd140f55dea391cdc36220d93f`, а не прежний release tag. [Compatibility manifest](docs/readiness-compatibility.json) фиксирует все7reviewed runtime payload refs, зависимости и фактические Docker image IDs. Backend payload ref остаётся базой для последующего README/docs-only commit, hash которого не включается сам в себя.

### 2. Настройте окружение

```sh
cp configs/app.env.example .env
```

Заполните основные параметры:

| Переменная | Для чего | Пример |
|---|---|---|
| `DATABASE_URL` | Операционная PostgreSQL БД | `postgres://user:password@localhost:5432/esp_app` |
| `HTTP_ADDR` | Адрес HTTP API | `127.0.0.1:8000` |
| `API_KEY` | Общий ключ в режиме legacy | Ваш ключ |
| `AUTH_MODE` | legacy или персональные сеансы local | `local` в примере и Compose; code default legacy |
| `APP_SCOPE` | Граница пользовательского фонда | `cohort` (default); research выбирается явно |
| `COHORT_VERSION` | Версия сохранённых 100 участников | `presentation-v1`; обязательна при cohort |
| `RBAC_MODE` | compat или enforce; согласован с persisted activation | `compat` |
| `AUTH_COOKIE_SECURE` | Secure cookie персональных сеансов | `true` |
| `ML_SERVICE_TARGET` | Адрес Python ML-сервиса | `127.0.0.1:50051` |
| `ML_SERVICE_API_KEY` | Общий ключ backend и ML-сервиса | Одинаковое значение в обоих сервисах |

Все настройки и значения по умолчанию: [Конфигурация](docs/configuration.md).

### 3. Подготовьте БД и запустите API

```sh
make migrate
make build
./bin/api
```

Миграции выполняются явно; текущая Go-схема имеет версию16 (additive RBAC и persisted worker authority поверх collection schema15, Q2-C1 schema14, Q1 schema13 и Q0 schema12). PostgreSQL server —16; версия схемы приложения16 — отдельный счётчик миграций. Source/test migration не активирует её на actual PREP. Старый Alembic baseline принимается с `0012_ml_run_drift`, существующие Go-версии обновляются аддитивно. Перед запуском cohort нужно выполнить backup, миграции, `bin/cohort -version presentation-v1` и bootstrap первого администратора через защищённый stdin. См. [изолированный PREP запуск](docs/readiness-runtime.md) и [миграции](docs/operations.md#миграции).

Проверьте доступность:

```sh
curl http://127.0.0.1:8000/health
# В local сначала персональный вход; анонимный /api/v1/wells возвращает 401.
```

При недоступности ML реестр и scoped справочники остаются доступны. Прямые ML-запросы могут вернуть503; cohort bulk сохраняет отдельные error/insufficient outcomes и не подменяет их нулевым риском.

## Что предоставляет API

| Раздел | Маршруты | Возможности |
|---|---|---|
| Доступ | `/api/v1/auth/*` | Сеансы, роли и область доступа; подготовлен адаптерный интерфейс SSO |
| Скважины | `/api/v1/wells` | CRUD, импорт и экспорт XLSX |
| Структура | `/api/v1/ngdu`, `/api/v1/departments` | НГДУ и цеха |
| Отказы | `/api/v1/wells/{id}/incidents`, `/api/v1/incidents/stats` | История и статистика отказов |
| Данные | `/api/v1/wells/{id}/predictions`, `complications`, `classification` | Прогнозы и осложнения |
| ML | `/api/v1/ml/*` | Риск, SHAP, история, загрузка файла, прогоны, статусы тревог |
| Q2 internal synthetic | `POST /api/v1/ml/cohort-jobs`, `GET /api/v1/ml/cohort-jobs/{job_id}`, `POST /api/v1/ml/cohort-jobs/{job_id}/cancel` | Durable submit, status по ID и revision cancel; [точный контракт](docs/q2-cohort-jobs.md#http) |
| Q2-C2 internal synthetic | `POST /api/v1/collection-jobs`, `GET /api/v1/collection-jobs/{job_id}`, `POST /api/v1/collection-jobs/{job_id}/cancel` | Конечный collection/capture/receipt; [контракт](docs/q2-collection-jobs.md#http) |
| Наблюдаемость | `/health`, `/api/v1/audit`, `/api/v1/runs` | Проверка БД, аудит и события |
| Аналитика | `/api/v1/analytics/*` | Подписанные Metabase URL |

Настройка персонального входа, ограничения scope, будущий Keycloak/OIDC и общие фильтры: [интеграции и рабочие сценарии](docs/telemetry-integrations.md).

Полный HTTP-контракт: [api/openapi.json](api/openapi.json), доступен также по `/openapi.json`.

## Разработка

```sh
make check       # unit-тесты, границы слоёв, race detector, vet, форматирование
make build       # bin/api, bin/migrate, bin/adminuser, bin/cohort, bin/worker, bin/cohort-worker, bin/collection-worker
```

Интеграционные тесты:

```sh
make test-db-up
TEST_DATABASE_URL='postgres://postgres:pulse-test@localhost:55432/pulse_test?sslmode=disable' make integration
make test-db-down
```

Тесты создают и удаляют уникальные схемы. Используйте отдельную тестовую БД.

## Структура

```text
cmd/                  Точки входа api, migrate, adminuser, cohort, worker, cohort-worker, collection-worker
internal/
  bootstrap/          Сборка зависимостей и жизненный цикл
  config/             Настройки окружения
  domain/             Типизированные модели и ошибки
  service/            Сценарии приложения и интерфейсы адаптеров
  repository/postgres/ SQL, транзакции и блокировки
  adapter/            gRPC-клиент, XLSX и справочник параметров
  transport/httpapi/  HTTP-маршруты, запросы, ответы и middleware
  migrations/         Явное создание или принятие схемы БД
api/                  OpenAPI
configs/              Пример конфигурации
build/package/        Dockerfile
deployments/          Тестовое окружение PostgreSQL
test/                 Интеграционные и архитектурные проверки
```

Расположение каталогов следует [golang-standards/project-layout](https://github.com/golang-standards/project-layout). Границы пакетов описаны в [архитектуре](docs/architecture.md), правила изменений — в [CONTRIBUTING.md](CONTRIBUTING.md).

## Документация

- [Архитектура и зависимости](docs/architecture.md)
- [Конфигурация](docs/configuration.md)
- [Миграции, Docker, задания и диагностика](docs/operations.md)
- [Правила разработки](CONTRIBUTING.md)
- [Контракты ML и Buf](https://github.com/effitech-pulse/pulse-contracts)


## Состояние документации — 19.10.2026

Датированный аудит (локальный архив задач: `../tasks/pulse-backend/docs/documentation-audit-2026-10-19.md`) сохраняет исходные находки; журнал очереди A (локальный архив задач: `../tasks/pulse-backend/docs/documentation-queue-a-execution-2026-10-19.md`) — изменения, приёмку и результаты проверок. [Матрица проверок](docs/verification-matrix.md) различает обязательный gate, настроенный CI, локальный PASS и допустимый SKIP. [Автономный handbook](docs/project-handbook/README.md) содержит снимок межмодульных документов и политику обновления источников.

Межмодульные ссылки рассчитаны на соседние checkout split workspace и не добавляют runtime-зависимость модуля. В standalone checkout используйте локальные README/AGENTS; межмодульный комплект доступен после получения pulse-backend. Очередь A документационная; продуктовая очередь B и промышленная приёмка модели ею не выполнены.

## PREP — текущая техническая готовность

[Scope и ledger PREP](docs/project-handbook/references/readiness-ledger.md) фиксирует техническую приёмку PREP-01–07 и отделяет её от научной приёмки и финальной handbook публикации. Пользовательский refresh оценивает сохранённый cohort через app telemetry→ML Upload, хранит полный snapshot с nullable исходами и partial provenance; research Score остаётся отдельным профилем. Membership100 не равен числу scored: archived/inactive/insufficient/error не становятся нулевым риском.

Compose-файлы теперь задают local auth и cohort; существующий `pulse-local` на8080 автоматически не мигрировался и в PREP не перезапускался. Browser-проверка на rebuilt image:22/22,fail0/pageErrors0; финальный after11run33done:100members/100scored,restricted0; прежние snapshots30/32 сохранены. Проверки выполнялись в [изолированном runtime](docs/readiness-runtime.md) на8088/8018 с копией app-БД. Научная оценка отложена пользователем до подключения новых данных; обучение/promotion и промышленная готовность не заявлены.

Техническая подготовка принята20.10.2026 после независимого Senior review и runtime/browser checks. [Native artifact hashes](docs/readiness-native-artifacts.json) сохраняют119source/50wheel identities. Финальная handbook сборка/автономная проверка и README/docs-only commit выполняются отдельным публикационным gate; remote push/cloud/release не выполнялись.

Диагностика O1: [корреляция, spans и ограничения локальных JSON logs](docs/observability-o1.md).

## O2 — реализация и runtime проверены, 20.10.2026

Optional Collector/Loki/Tempo/Prometheus/Grafana описаны в [O2 runbook](docs/observability-o2.md); фактические проверки — в [runtime evidence](docs/project-handbook/references/o2-runtime.md), требования и статус — в [execution ledger](docs/project-handbook/references/o2-q0-execution.md). Source и runtime Senior-проверки пройдены, W1/P2 закрыто; финальный rollback сохранил13phase результатов по прежним ID/SHA и recovered ML-failure ID. O2 принята в согласованной области локального пилота после финального docs review и автономной проверки пакета; production capacity не заявляется.

Пилот использует изолированные UI/API 8088/8018 и PREP PostgreSQL **55439**. Порт **55432** предназначен для отдельной lane integration test DB; это разные БД. Исходный 8080/live DB не затрагиваются. Grafana публикуется только на `127.0.0.1:3300` с native authentication; отдельная сеть `grafana-ui` — явно разрешённое пользователем исключение локального пилота с возможным исходящим трафиком, не egress deny. Collector/Loki/Tempo/Prometheus остаются во внутренних сетях; бизнес-сети не меняются.

Запуск ограничен supervisor/budget и требует оператора; stdout fallback сохраняется. RAM counters/очереди/file offsets не обещают lossless/exactly-once, центральные diagnostics не заменяют business audit. Далее — Q0→Q1/F2 по [roadmap](docs/project-handbook/references/functional-roadmap.md). Паспорт/Excel/помощник остаются [отдельным рассматриваемым предложением](docs/project-handbook/references/well-operations-proposal.md), scientific acceptance — user-deferred/not_assessed.

Q1 сохранённые file jobs реализованы отдельным default-off synthetic профилем:
[протокол и HTTP](docs/q1-file-jobs.md), [настройка](docs/configuration.md),
[recovery/rollback](docs/operations.md), [датированный runtime proof](docs/q1-runtime-2026-10-21.md). `make build` собирает также `bin/worker`.
Настройка не переключает существующий `/ml/upload` или рабочий Compose;
real-file enable запрещён startup validation. Synthetic proof не является
научной приёмкой модели или production deployment.
