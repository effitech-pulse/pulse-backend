# Архитектура backend


Q0 добавил внутренний stored-file module: bounded CSV/XLSX → immutable raw/canonical snapshot в PostgreSQL schema12 → typed execution pin → strict `UploadPinned`. [Архитектура Q0](q0-architecture-2026-10-20.md) и [synthetic runtime proof](q0-runtime-2026-10-21.md) фиксируют принятую synthetic source/runtime/reproducer область (13PASS) и pending final docs/publication gate. Public saved-file/async routes, job/outbox/worker и automatic recovery относятся к Q1; текущие sync routes сохранены. Schema12 не объявляется активированной на actual PREP.
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

`pulse-contracts` — отдельный Git-репозиторий, Go-модуль и Python-пакет. Только он хранит `.proto` и generated SDK. Текущие исходники backend/ML используют Go `v0.1.2` / Python `0.1.2`, локальный **UNRELEASED** Q0 candidate contracts `272f23027e4a3ba636305b931f6ab83e5fca8166`. PREP0.1.1 остаётся отдельным историческим source binding. Их сборки не запускают protoc и не копируют схему.

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
