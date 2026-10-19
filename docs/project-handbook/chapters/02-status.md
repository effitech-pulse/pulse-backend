# Текущее состояние и согласованные изменения

Редакция: **дополнение PREP, 20.10.2026**. Предыдущая очередь DOC-01–07 принята; её исторические материалы сохраняют дату. Текущие факты сверены с [PREP ledger](../references/readiness-ledger.md), [архитектурой](../references/readiness-architecture.md) и [playbook](../references/readiness-runtime.md). Выполнение команд и browser QA принадлежат указанным в ledger исполнителям; этот текст не является новым прогоном тестов.

## Проверенный локальный профиль

Отдельные контейнеры PREP на 127.0.0.1:8088/8018 используют восстановленную app-БД `pulse_prep`, local sessions и `APP_SCOPE=cohort`, `COHORT_VERSION=presentation-v1`. Исследовательские PostgreSQL/ClickHouse сети и рабочая БД не подключены; ML получает bundle модели без research manifest/cache/PG. Исходный экземпляр 8080 не перезапускался. Файловые defaults обычного compose.local теперь local/cohort; перед его запуском обязательны backup, явные migrations/bootstrap/capture и COHORT_VERSION. Изменение файла само по себе не мигрирует старый runtime.

| Область | Подтверждено PREP | Граница |
|---|---|---|
| Когорта | Capture ровно 100 существующих ID Pulse; неизменяемая версия/hash, серверные guards для списка, direct ID, телеметрии, checks/reviews, статистики и Excel | Не LIMIT 100 и не удаление research фонда. Scoped пользователь видит разрешённое пересечение, которое может быть пустым |
| Доступ | Local login/me/logout, viewer/technologist/admin, Origin/cookie; admin не обходит membership. Глобальные NGDU/department writes в cohort запрещены 403 | SSO/OIDC и удалённый TLS-профиль не принимались |
| Расчёт | ReadApp → CSV → Upload, единый as_of и 100 outcomes. `scored`/`archived`/`inactive`/`insufficient`/`error` не смешиваются; non-scored result/risk=null | Успешная обработка не доказывает прогнозную пригодность модели |
| ML runtime | Bundle-only Upload, общий typed evaluator с явными legacy policy, causal Upload/unknown context отклоняются | Causal-кандидат не продвинут; research Score остаётся отдельным opt-in |
| Сохранение | Полный immutable output и digest, исторический snapshot читается после refresh/restart; обработка alert — mutable overlay | Provenance partial: immutable input bytes/replay не обеспечены. Старые строки не получают выдуманный профиль |
| Попытки | Migration 11 сохраняет profile/cohort_version при BeginCohort до чтения Members/Upload; `running`/`error` видимы без snapshot | Записи до migration 11 остаются unknown, даже если у них есть сохранённый snapshot |
| UI | Peak/latest SHAP разделены, value_source видим, null outcome не показывает график. Mobile resize/table/keyboard/account исправлены; browser gate 22/22 PASS | Полный Figma pixel audit, screen reader/zoom и WCAG не заявлены |
| Проверки и версия | Native/Linux smoke, PG/race/integration и повторные Senior reviews отражены в ledger; [manifest](../inventory/readiness-compatibility.json) связывает семь исходников | Локальные commits не означают push, remote availability или cloud CI PASS |

## Что показали фиксированные прогоны

В изолированной среде сохранены полные расчёты на 100 участниках; смешанный исторический snapshot содержит 98 scored, один archived и один inactive. Последующие изменения lifecycle не меняют этот результат и membership. Чтение прежнего scored snapshot после перезапуска ML подтвердило сохранность output, а не восстановимость исходных bytes. Финальный прогон 33 после migration11 завершён: `done`, `profile=cohort`, `cohort_version=presentation-v1`, 100 scored, 0 ошибок и 0 тревог за 97.035779 с. Временные lifecycle fixtures восстановлены; restricted reader видит 0 участников, snapshots30/32 не изменились. Identity была видна уже в running; результат не объявлялся terminal до завершения. Это локальный технический замер, не SLA и не метрика качества.

Browser 20.10.2026: 22/22 assertions PASS, fail=0, pageErrors=0, 14/14 source hashes; отдельно сохранён предыдущий реальный auth-flow 6/6. В browser fixtures не было alert rows: сохранение нового реального alert/review этим проходом не подтверждается и не подменяется изготовленным результатом. Backend/adapter tests этой границы учитываются отдельно.

## Научная приёмка и оставшиеся продуктовые задачи

Пользователь сообщил об отсутствии новых внешних данных и намерении подключить ML позднее. Научный gate **deferred / not_assessed**: валидатор протокола реализован и проверен, но не устанавливает независимость/качество данных. Нет новых precision/recall, подтверждения горизонта 7–14 суток или promotion. Пример [readiness protocol](../examples/readiness-protocol.example.json) намеренно незаполнен и должен блокировать gate.

Сохраняются согласованные, но не объявляемые завершёнными задачи: полный порядок вкладок/единый файловый сценарий, предметный журнал, постоянные уведомления и рассылки, SSO, остальной перенос Figma, брокер, Kubernetes, удалённая эксплуатация и SLA/RPO/RTO. PREP не означает автоматического выполнения этой продуктовой очереди.
