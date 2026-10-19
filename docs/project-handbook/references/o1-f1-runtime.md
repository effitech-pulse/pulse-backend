> Снимок документа 20.10.2026. Источник: `pulse-backend/docs/o1-f1-runtime.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# Изолированная проверка O1 и F1

Профиль дополняет [PREP runtime](readiness-runtime.md) на отдельной копии PostgreSQL и loopback8088/8018. Исходный pulse-local8080 и рабочие PG/ClickHouse не переключаются. Входы модели, секреты и аккаунты остаются в существующих закрытых локальных файлах; их значения не выводятся в команды/отчёт.

## Сборка и запуск

Из pulse-backend после готовности исходников:

```sh
docker compose --env-file .env.prep -f deployments/compose.prep.yaml -f deployments/compose.o1-f1.yaml config --quiet
docker compose --env-file .env.prep -f deployments/compose.prep.yaml -f deployments/compose.o1-f1.yaml build backend frontend ml data
docker compose --env-file .env.prep -f deployments/compose.prep.yaml -f deployments/compose.o1-f1.yaml up -d --no-deps --wait --wait-timeout 180 backend frontend ml data
```

Перед переключением PostgreSQL изолированного PREP должна быть healthy; `--no-deps` ограничивает пересоздание четырьмя сервисами.

Теги o1-f1 отделяют новую сборку от сохранённых prep-образов. Базовые mounts/сеть/БД и local/cohort policy не меняются. Override ограничивает локальные Docker-логи четырёх сервисов: driver=local, max-size=10m, max-file=3. Это ограничение ротации диагностических файлов, не срок хранения предметной истории и не гарантия сохранности каждого лога.

## Свидетельства приёмки

Статус выполнения и результаты ведутся в [журнале O1/F1](o1-f1-execution.md). Фактическая проверка 20.10.2026: runtime12/12 PASS; browser7/7 и дополнительные2/2 роли/мобильное меню PASS, ошибок страницы0. Все четыре образа собраны, LogConfig local10m/3 подтверждён. Docker HealthCheck frontend/ML/Data healthy; backend не имеет Docker HealthCheck и проверен HTTP `/health`200. Источник полных команд/ограничений — журнал.

- Проверить syntax Nginx, четыре текущих image IDs и LogConfig, Health/локальные роли.
- На реальных HTTP/gRPC вызовах проверить X-Request-ID, traceparent, связь JSON-записей и сохранение существующих ответов. Проверять фактические PG/ML/Data ветки; тестовый in-memory exporter отдельно доказывает lifecycle spans.
- На контролируемых synthetic canaries проверить отсутствие секретов, тела файла, query/SQL values и private exception text в application logs/spans. В этом Nginx per-request native error stream отключён для исключения URI/секретов; стартовые pre-config ошибки остаются нативными. Их не считать санитизированными application JSON.
- В браузере проверить все шесть вкладок, прежние ссылки и Back/Forward, выбранный saved run/check, роли/null/архивные состояния, обе темы, desktop/390px и клавиатуру.
- Не передавать .env/пароли/session cookies в stdout. Не выполнять миграции/reset/down -v рабочей БД. При окончании тестов восстановить только временно изменённые состояния изолированной копии через существующий API.

O1 использует JSON stdout и реальные SDK spans без внешнего exporter/store. Центральный поиск Collector/Loki/Tempo/Grafana, broker/jobs и новые ML-фазы остаются последующими очередями.

Runtime проверяет фактический Upload RPC с отклонённым синтетическим CSV422 и несохранённую проверку Go→Data, чей health-object отклонён как не-массив records503. Совпадение trace/attempt IDs и отсутствие canary подтверждены; успешный live ML scoring/persist и telemetry ingest этим не заявлены. Успешное сохранение check проверено отдельными реальными PG integration fixtures, lifecycle — SDK/test exporters. Принудительное заполнение логов для rollover, нагрузка, центральное хранение и научная приёмка сюда не входят.
