# Архитектура backend

## Q2-C1 · additive durable cohort source, 22.10.2026

Отдельный default-off synthetic модуль сохраняет все100 inputs/markers, membership,
lifecycle, UTC as_of и exact execution pin до202 одной PG transaction с run/job,
reservations и outbox. `bin/cohort-worker` использует payload-free Rabbit events,
durable checkpoints и отдельный strict `cohort_snapshot` pinned ML target.
Per-member stage не меняет published risk: только fenced all100 final transaction
записывает snapshot/alerts/run/job/inbox. Исторический output/JSON/digest не меняется.
Current restricted/empty snapshot projection очищает глобальный Execution metadata.

Claim держит physical PG session74019321; Next/renew/checkpoint/final transactions
исполняются на этой же connection. Общий capacity gate74019323 добавляет узкую
взаимоисключающую проверку Q1/legacy/Q2; Q1 same-key replay сохраняет bounded wait≤5s,
Q2 admission/new claim и legacy используют TRY. Для уже owned Next/Renew/Checkpoint/
Finish реализовано отдельное bounded WAIT уточнение на том же gate,
Q2-DB-03 CLOSED тем же Senior. Next/Renew/Checkpoint total5s включают serializer и gate wait;
Finish total10s, gate query≤min(remaining,5s). Остальные TRY paths сохранены. Terminal Q1 deliveries получают persisted disposition
до capacity check. Q1 private semantics/Redrive и существующие HTTP paths сохраняются.

API проверяет capture recipe SHA against actual API executable и полный trusted
catalogue. Worker загружает descriptor policy metadata; accepted inputs читает из
PG даже после удаления original fixture bytes. Actual worker SHA связывается отдельно.
Schema14 на PostgreSQL16 применяется явно; guard/bootstrap и real enable не создаются
миграцией. Source implementation, review, runtime и docs/publication статусы разделены
в [Q2 proof/runbook](q2-runtime-2026-10-22.md): source APPROVED,8findings CLOSED;
latest module gates и3actual cases PASS; physical stale/capacity/measured resources/
teardown VERIFIED в internal synthetic-only scope. Проверки публикации комплекта учитываются отдельно; scientific/real/production не приняты.
[Протокол](q2-cohort-jobs.md) и [editable C4](q2-cohort-jobs.drawio) содержат точные
границы, limits/recovery/rollback. Collection и переключение портала не входят в C1.

## Q1 · реализованный synthetic source, 21.10.2026

`FileJobs` принимает input+quota+job+outbox одной PG transaction. `cmd/worker`
совмещает dispatcher, consumer и reconciler; RabbitMQ переносит только opaque
references, без payload/actor/lease token. Worker получает доверенное persisted
намерение из PG, повторно проверяет current account/well/cohort/guard, lease и fence
перед чтением входа и при finalization; browser Principal ему не создаётся.
Result+terminal job+inbox фиксируются атомарно. Compute может повторяться при
at-least-once delivery; принимается один result/check effect, exactly-once compute
не обещается.

Saved result — отдельная private `FileSavedCheck` projection `q1_<jobID>` поверх Q0
result; `manual_check` и фондовый риск/ярус не изменяются. Review использует отдельный
kind `file_check` и dedicated routes, включая current rights внутри review tx.
Другой admin не получает private данные владельца. `FILE_CHECK_MODE=off` default;
synthetic требует server-owned manifest/guard/accounts/well/pin; real enable
отвергается startup validation. Это описание кода и изолированного proof,
не переключение PREP/production. Статусы source/runtime/browser разделены в
[датированном свидетельстве](q1-runtime-2026-10-21.md).


Q0 добавил внутренний stored-file module: bounded CSV/XLSX → immutable raw/canonical snapshot в PostgreSQL schema12 → typed execution pin → strict `UploadPinned`. [Архитектура Q0](q0-architecture-2026-10-20.md) и [synthetic runtime proof](q0-runtime-2026-10-21.md) фиксируют историческую принятую Q0 область (13PASS, docs/publication PASS). Q1 добавил отдельный default-off synthetic модуль: [протокол](q1-file-jobs.md), [C4](q1-file-jobs.drawio), [runtime proof](q1-runtime-2026-10-21.md). Q1 schema13 проверена в выделенных test schemas; actual PREP этим не мигрирован. Текущая source schema14 добавляет Q2, описанный выше. Sync routes сохранены.
## Границы ответственности

Frontend обращается к HTTP API на Gin. Backend хранит операционные данные в PostgreSQL и вызывает Python ML по gRPC. Обучение, признаки и модель остаются в `pulse-model`; backend не устанавливает этот пакет.

```text
HTTP → service → domain
             ↓ интерфейсы
       PostgreSQL / gRPC / XLSX / файлы
```

`bootstrap` связывает конкретные адаптеры с сервисами. Остальные пакеты зависят от интерфейсов потребителя, а не от сборщика приложения.

SQL строится Squirrel и выполняется pgx. Динамические фильтры, обновления и подзапросы составляются из Sqlizer; PostgreSQL placeholders нумеруются после сборки всего запроса. Миграционный DDL остаётся SQL-снимком.

| Пакет | Ответственность | Не содержит |
|---|---|---|
| `domain` | Модели, nullable-поля, типы ошибок | HTTP, SQL, protobuf |
| `service` | Валидация, классификация, связывание ID, задания | pgx, grpc-go, HTTP-обработчики |
| `repository/postgres` | Запросы, транзакции, перенос статусов, locks | HTTP-запросы и вычисление риска |
| `adapter/grpcml` | RPC, deadline, metadata, преобразование protobuf | SQL и бизнес-решения |
| `adapter/spreadsheet` | Чтение/запись XLSX | Доступ к БД |
| `transport/httpapi` | Gin handlers, DTO, маршруты, ошибки, CORS, API-key | SQL, владение моделью |

В `transport/httpapi` маршруты собраны в Gin RouterGroup под `API_V1_PREFIX`. JSON binding выполняется Gin с сохранением проверки одного JSON-документа. Middleware обрабатывают ключ API, CORS, восстановление после panic и аудит окончательного статуса. Сервисы получают обычный `context.Context` из запроса. Multipart-загрузка использует Gin с лимитами 64 MiB на тело и 2 MiB памяти. Listener, HTTP timeout и graceful shutdown остаются в `cmd/api`.

Направление импортов проверяет `test/architecture`. Динамические JSON-объекты используются только для расширяемых параметров телеметрии и метаданных моделей; сущности и результаты ML типизированы.

## Жизненный цикл прогона

1. Сервис резервирует PostgreSQL advisory lock через интерфейс `RunStore`.
2. Фоновый сценарий создаёт запись `ml_run` и вызывает gRPC `Score`.
3. Репозиторий записывает тревоги и завершение прогона одной транзакцией.
4. Статусы технолога переносятся по `unitek_id`.
5. Выделенное соединение освобождает lock при любом исходе.

Ярус приходит из ML. Низкий риск последнего окна и `data_ok=false` не отменяют ранее присвоенный ярус. Optional/null, int64, false и нули сохраняются при переходе protobuf → модель → JSON.

## Контракты

`pulse-contracts` — отдельный Git-репозиторий, Go-модуль и Python-пакет. Только он хранит `.proto` и generated SDK. Текущие исходники backend/ML используют Go `v0.1.2` / Python `0.1.2`, локальный **UNRELEASED** candidate. Q2 G2 contracts source checkpoint — `1d0b2b62d2cb73d18ab69738431089edf69621f8`; capability/docs изменены без wire/generated delta. Исторический Q0 binding — `272f23027e4a3ba636305b931f6ab83e5fca8166`. PREP0.1.1 остаётся отдельным историческим source binding. Их сборки не запускают protoc и не копируют схему.

Схема проверяется Buf (lint и breaking checks), SDK создают официальные генераторы. gRPC-адаптер Go преобразует generated-типы в domain напрямую, сохраняя optional и int64 без JSON. Python использует официальный protobuf json_format на границе словарей движка.

Локальная разработка использует отдельный `go.work` и проверенный contracts checkout; Q0 process proof использует изолированные установленные Python wheels. Package version не заменяет exact source/wheel identity. Remote availability/fetch, release и cloud CI для0.1.2 остаются OPEN; выпуск описан в README приватного [pulse-contracts](https://github.com/effitech-pulse/pulse-contracts).

## Диаграммы C4

Редактируемый источник: [pulse-c4.drawio](diagrams/pulse-c4.drawio). Открывается в draw.io Desktop или diagrams.net; блоки и связи остаются отдельными объектами.

| Страница | Область | Превью |
|---|---|---|
| C1 — контекст | Пользователи, Pulse и внешние промысловые архивы | [PNG](diagrams/pulse-c4-context.png) |
| C2 — контейнеры | SPA, Nginx, Go API/сборщик, Python ML, Pulse Data и хранилища | [PNG](diagrams/pulse-c4-containers.png) |
| C3 — компоненты | Внутренние компоненты `pulse-backend`, порты и адаптеры | [PNG](diagrams/pulse-c4-components.png) |
| Deployment | Экземпляры приложений, существующие БД, сети и порты Docker | [PNG](diagrams/pulse-c4-deployment.png) |

Схемы отражают код и `deployments/compose.local.yaml` на 18.10.2026. C4 используется для описания уровней архитектуры; Deployment — дополнительное представление, а не уровень Code. См. [официальное описание C4](https://c4model.com/diagrams).

`pulse-model` — библиотека вычислений внутри Python-процессов; `pulse-contracts` — библиотека контрактов/SDK при сборке. Отдельных сетевых сервисов для этих репозиториев нет. Обучение и причинная оценка запускаются отдельно в `pulse-model`; модель не зависит от `pulse-data`. `pulse-landing` — самостоятельный публичный сайт, в рабочем потоке диагностики не участвует.

Периодический сбор через JSON и запуск модели — разные сценарии. Коллектор сохраняет измерения и watermark; ручная проверка передаёт выбранный период в ML и сохраняет результат с версией модели. В локальном Docker автоматический прогон выключен. Сценарии генератора не присваивают риск и ярус.

Keycloak/OIDC пока является целевым расширением. В локальном запуске действует legacy-доступ, персональный вход доступен при отдельной настройке local. Подключённого SSO и работающей почтовой рассылки схемы не заявляют.

## Предметная история Events

`Events` объединяет existing manual_check, check/alert review_event и well_event
за consumer-defined read store. ACL/cohort fence применяются внутри read-only RR
до UNION/keyset/limit+1. HTTP использует собственные закрытые string-int64 DTO.
[Live consistency, exact detail, бюджеты и rollback](events.md).
