# Конфигурация и зависимости окружения

Q1 добавил default-off `FILE_CHECK_MODE=off|synthetic`, обязательный false
`FILE_CHECK_ALLOW_REAL`, explicit manifest/hash/run ID и две scoped broker URLs
только для worker. Synthetic требует local/cohort, полную100-well cohort,
matching manifest/params/pin/PG guard в выделенной test schema. Migration13 не
seed-ит guard и не переключает PREP. [Точная конфигурация](../references/backend-configuration.md)
и [Q1 proof](../references/q1-runtime.md). Один env switch не даёт synthetic applicability.


Q0 strict path использует operator-owned `ML_BUNDLE_REGISTRY` отдельно от mutable named models и immutable expected pin. Package0.1.2 UNRELEASED недостаточно: нужны reviewed source/wheel и actual installed runtime identity. Новая acceptance recipe включает synthetic limits20MiB raw/100000rows/128columns/64targets/50MiB canonical и exact parser identity. Это версия внутреннего module, не новая production HTTP configuration; source schema12 не означает actual PREP migration. [Architecture](../references/q0-architecture.md), [runbook](../references/q0-runtime.md).
Полная датированная [конфигурация backend](../references/backend-configuration.md) перечисляет все ключи публичного примера с defaults/профилями/моментом валидации. [Native/runtime инструкция ML](../references/runtime-installation.md) отделяет проверенный CPython 3.13/macOS профиль от metadata ≥3.11, Docker/CI и будущего release. Ключевые сведения ниже — обзор, а не свидетельство запуска всех профилей.

## Требования

Для локальной разработки backend нужен Go ≥1.25; frontend build/test удобнее выполнять Node 22 (тесты используют strip-types, минимум 22.18). Python-пакеты декларируют ≥3.11, Docker ML/Data используют Python 3.13. Параметры зависимостей смотрите в приложенных манифестах `../inventory/` и оригинальных репозиториях.

Вычислительная совместимость весов проверялась с версиями из [runtime-constraints.txt](../contracts/runtime-constraints.txt). Нельзя считать любое обновление numpy/scikit-learn/pickle-библиотек безопасным для старого комплекта модели только потому, что pip решил зависимости. Исходники model не зависят от Data; пакеты устанавливаются локально из соседних checkout.

## Backend

| Переменная | Назначение / значение по умолчанию |
|---|---|
| HTTP_ADDR | Listener; код: 127.0.0.1:8000; Docker: :8000 |
| DATABASE_URL | PostgreSQL esp_app; передавать DSN через окружение |
| DB_POOL_SIZE | 5, допустимо 2–1000; прогон удерживает выделенное соединение |
| API_V1_PREFIX | /api/v1 |
| AUTH_MODE | legacy или local; код default legacy; публичный пример и Compose: local |
| AUTH_COOKIE_SECURE | код default true; isolated HTTP loopback Compose: false; TLS-профиль: true |
| APP_SCOPE | код/пользовательский Compose: cohort; research только явный opt-in |
| COHORT_VERSION | Обязательна при cohort; PREP presentation-v1, compose.local требует явно заданную PULSE_COHORT_VERSION |
| API_KEY | Опциональный общий ключ legacy |
| CORS_ORIGINS | Разрешённые origins через запятую |
| PARAM_DICT_PATH | parameters_dict.json; формат map canonical → список подписей |
| ML_SERVICE_TARGET | gRPC host:port; в Docker ml:50051 |
| ML_SERVICE_API_KEY | Общий межсервисный ключ |
| ML_SERVICE_TIMEOUT_S | 300 секунд |
| ML_AUTORUN_ENABLED | В коде default true, **в текущем Compose явно false** |
| ML_AUTORUN_HOUR / TZ | 5 / Europe/Moscow, если плановый прогон включён |
| TELEMETRY_DATA_API_URL | http://data:8092 в Docker; native loopback только для native API |
| INTEGRATION_ALLOWED_HOSTS | Конкретные разрешённые hostname источников |
| INTEGRATION_SECRET_KEY | 32 байта в base64 для AES-GCM |
| METABASE_SITE_URL / EMBED_SECRET / DASHBOARDS | Опциональная встраиваемая аналитика; не настроена самим запуском Compose |
| METABASE_TOKEN_TTL_S | 3600 секунд |

При нативном запуске .env читается из текущего рабочего каталога; переменные процесса имеют приоритет. Некорректные числовые значения, timezone и JSON отвергаются.

## ML и Data

ML: ML_GRPC_BIND, ML_DATA_ROOT, ML_SERVICE_API_KEY. Bundle-only Upload использует models и не требует PG/cache/manifest. PG_HOST/PORT/DB/USER/PASSWORD относятся к отдельно включаемому research пути. Исследовательская PG — esp; операционная — app-БД выбранного deployment. Bind ML в Docker 0.0.0.0:50051 не означает публикацию порта на хост.

Data: CLICKHOUSE_HOST/PORT/DB/USER/PASSWORD; аргументы CLI `--profiles`, `--coverage`, `--scenario`, `--origin`, `--ramp-days`, `--seed`, `--host`, `--port`. Сценарий и calibration должны быть согласованы по отпечатку профилей. Переменные UNITEK_*/ARMITS_* для живых архивов задаются локально; их значения в комплект не включены.

Frontend: `VITE_API_PROXY_TARGET` для dev-прокси; штатный Vite port 3000, рабочий native preview запускался с переопределением 5174. В production API идёт через Nginx на том же origin. Секреты нельзя помещать в VITE_*: они попадают в клиентскую сборку.

## Локальные порты

| Назначение | Хост | Внутри сети |
|---|---|---|
| PREP UI / API | 127.0.0.1:8088 / :8018 | frontend:80 / backend:8000 |
| PREP app PostgreSQL | 127.0.0.1:55439 | Изолированный восстановленный pulse_prep; не integration test DB |
| O2 Grafana (optional local pilot) | 127.0.0.1:3300 | Только native-auth UI; bridge исключение с разрешённым egress |
| Исходный local UI | 127.0.0.1:8080 | frontend:80 |
| Go API | 127.0.0.1:8000 и :8002 | backend:8000 |
| ML | Не опубликован | ml:50051 |
| Pulse Data | Не опубликован | data:8092 |
| Существующая PG | localhost:5433 | grant-postgres:5432 |
| Существующий CH HTTP | localhost:8123 | grant-clickhouse:8123 |
| Изолированная тестовая PG | localhost:55432 | Отдельный test Compose |

O2 включается явно и только как supervised local pilot: [runbook/конфигурация](../references/observability-o2.md), [pins/budgets](../references/o2-architecture.md). Credentials остаются в private files вне Git; их значения/пути не копируются в комплект. Collector/stores/OTLP/metrics endpoints не публикуются на host. W1 memory и final rollback проверены независимо; final docs/publication gate PASS, точные runtime значения принадлежат [отчёту](../references/o2-runtime.md).

Порты и сети относятся к текущему рабочему месту; их наличие не описывает production SLA или отказоустойчивый кластер. Публиковать эти defaults во внешнюю сеть без отдельной настройки доступа нельзя.

## Профили дополнения PREP

[Runtime playbook](../references/readiness-runtime.md) описывает отдельную восстановленную PG, локальные ports и secret delivery; [compatibility manifest](../inventory/readiness-compatibility.json) фиксирует согласованные исходники семи модулей. Он не содержит private bindings/CSV/секретов. `.env.prep` и bootstrap/account credentials не входят в документацию. File defaults и уже запущенный процесс различаются: настройки не переключают 8080 и не применяют миграции автоматически.
