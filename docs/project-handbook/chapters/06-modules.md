# Разделение кода по модулям

Backend C2 добавляет `cmd/collection-worker`, typed domain/service/store,
HTTP fixture и AMQP adapters, PG durable module и общий telemetry writer.
Это backend-only change; Data/model/contracts/ML/UI не изменяются для collection.
[Границы](../references/q2-collection-jobs.md), [C4](../diagrams/q2-collection-jobs.drawio).

## Рабочее место

Разработка ведётся только в семи соседних репозиториях `repository-split/pulse-*`. Старые `esp/src/scripts/tests/frontend/landing/utils/deploy` в корне grant не восстанавливаются. `archive` и `reference` — исторические материалы, не текущие исходники. `data` и `.venv` проектов ссылаются на общее хранилище и окружение.

| Репозиторий | Технология | Точки входа | Основные каталоги |
|---|---|---|---|
| pulse-model | Python ≥3.11, CatBoost; опционально PyTorch | `python -m scripts.eval_honest`, make targets | `esp/`, `scripts/`, `tests/`, `docs/` |
| pulse-data | Python ≥3.11, requests, pandas, CH/PG clients | `utils.parsers.run_batch`, `utils.telemetry_emulator.server` | `utils/parsers/`, `utils/loaders/`, `utils/telemetry_emulator/`, `scripts/`, `deploy/` |
| pulse-ml-service | Python ≥3.11, grpcio | `python -m pulse_ml.server` | `pulse_ml/`, `tests/`, Dockerfile |
| pulse-contracts | Protobuf, Buf, Go/Python SDK | `make generate`, `make lint`, `make breaking` | `proto/`, `gen/go/`, `gen/python/`, `tests/` |
| pulse-backend | Go ≥1.25, Gin, pgx/Squirrel | `cmd/api`, `cmd/migrate`, `cmd/adminuser`, `cmd/cohort` | `internal/`, `api/`, `deployments/`, `test/` |
| pulse-frontend | React 18, TypeScript, Vite 5, Ant Design 5 | `src/main.tsx`, `src/router.tsx` | `src/api`, `auth`, `components`, `hooks`, `lib`, `pages`, `theme` |
| pulse-landing | Python-генератор, HTML/CSS/JS/SVG | `python3 build.py` | `build.py`, `planned-demo.html`, `assets/`, `design/` |

## Backend

HTTP на Gin → service → domain. SQL допускается только в PostgreSQL repository; protobuf — на границе gRPC-адаптера. Типы domain не зависят от Gin, pgx или generated protobuf. `bootstrap` создаёт pool, clients, adapters и services.

| Пакет | Содержимое |
|---|---|
| `cmd/api` | Listener, сигналы, graceful shutdown, запуск расписаний |
| `cmd/migrate` | Явное принятие/обновление операционной схемы |
| `cmd/adminuser` | Bootstrap первого администратора; пароль только stdin |
| `cmd/cohort` | Capture существующего фиксированного membership; без создания/восстановления скважин |
| `internal/service/cohort*` | Membership guard, Upload orchestration, scope результатов |
| `internal/config` | Окружение, валидация конфигурации |
| `internal/bootstrap` | Сборка конкретных зависимостей |
| `internal/domain` | Well, access scope, telemetry, model DTO, проверки, заключения, ошибки |
| `internal/service/catalog*`, `well_lifecycle*` | Реестр, reference, версии, архив/история |
| `internal/service/access*` | Учётные записи, вход, сеансы, связь внешнего subject |
| `internal/service/integrations*` | Mapping, чтение, ingest/import, lease, планировщик |
| `internal/service/checks*`, `reviews*` | Сохранённые ручные проверки и решения технолога |
| `internal/service/ml*` | Прогоны, текущие оценки и locks |
| `internal/repository/postgres` | Squirrel SQL, транзакции, optimistic concurrency |
| `internal/adapter/grpcml` | Generated SDK → domain, deadline и metadata |
| `internal/adapter/telemetry` | HTTP connector, archive API, AES-GCM secrets |
| `internal/adapter/spreadsheet` | XLSX и совместимость экспорта |
| `internal/adapter/parameterfile` | Чтение справочника параметров |
| `internal/transport/httpapi` | Маршруты, binding, ответы, роли, CORS, аудит |

## Frontend

`main.tsx` связывает QueryClient, AuthProvider, FundProvider, FundFiltersProvider, ThemeProvider и RouterProvider. Маршруты лениво загружают страницы. `apiClient` централизует HTTP-ошибки; TanStack Query кэширует серверное состояние. При выходе/истечении сеанса кэш пользователя очищается.

`AppLayout` отвечает за навигацию и контекст; `FundFilterBar` — общие фильтры. `TelemetryWorkspace` реализует каналы, периоды, сравнение, таблицы и файловые действия. `TelemetryChart` — график и взаимодействие. `ManualChecks`, `ReviewPanel`, `WellHistoryPanel` — сохранённые проверки и независимые истории. `lib/telemetry*` содержит чистые функции канонизации, нормирования и форматов файлов.

`landingDemo.tsx`, `vite.demo.config.ts`, `design/v3/preview.html`, `pulse-landing/planned-demo.html` относятся к прототипам/лендингу. В рабочем Docker собирается обычный `npm run build`, а не автономное демо.

## ML и данные

В model: `esp/naming.py` канонизирует имена; `esp/rules` выполняет гигиену/правила; `esp/features` строит признаки и контекст; `esp/models` содержит модели/registry; `esp/monitoring` — дрейф; `esp/io` — чтение исследовательских данных. Полный список каталогов и файлов находится в [инвентаризации кода](18-code-inventory.md).

В ML-сервисе: `server.py` — gRPC transport, `engine.py` — операции, `ml_scoring.py` — расчёт снимка и SHAP, `ml_upload.py` — проверка файла, `decision.py` — общий evaluator и typed policy, `model_identity.py` — отпечаток bundle, `database.py`/`paths.py` — доступ к внешним данным.

В Data: `utils/parsers` сохраняет совместимые CLI архивов; `utils/loaders` загружает БД; `utils/telemetry_emulator` хранит archive/fleet/scenario/server; `scripts/connect_telemetry_fleet.py` подключает фонд через API. Сценарий генератора не импортируется моделью и не подменяет её ответ.

## Версионирование

Python model/data — 0.1.0; contracts — 0.1.1; frontend — 1.0.0 по манифестам. Версия пакета не равна версии обученных весов или точному source commit. Дополнение PREP связывает семь локальных source refs в [compatibility manifest](../inventory/readiness-compatibility.json); отсутствие push/проверенного remote release отмечено отдельно. Рабочие деревья содержат изменения после Git HEAD: точные HEAD и перечень исходников с SHA256 сохранены в `../inventory/source-manifest.json` и `source-files.csv`. Проверять соответствие следует по этим данным, а не по одной дате релиза.
