# Текущее состояние и согласованные изменения

Q1/F2 ·22.10.2026: source и обязательные runtime gates VERIFIED в synthetic-only
области; Q1-RUNTIME-01 CLOSED тем же Senior после actual post-fix stale PASS.
Отдельный default-off file-job путь реализован; `FILE_CHECK_ALLOW_REAL=false`
обязателен, actual PREP schema13 не активирована. [Протокол](../references/q1-file-jobs.md)
и [runtime proof](../references/q1-runtime.md) разделяют API-only resource PASS,
исторические physical4PASS/1staleFAIL, новый probe22.10 и принятый actual browser
scope. Финальная приёмка координатором и публикация closure-редакции выполняются
отдельно; реальные файлы/production/scientific этим proof не разрешаются.


Q0 ·21.10.2026: C/M/B+memory и P-SR04 source приняты; **RUN-2 synthetic implementation/runtime VERIFIED,13/13 PASS**. [Runtime proof](../references/q0-runtime.md) связывает frozen execution afae2c4 с resource8/replay1/fault4; source/runtime/reproducer ACCEPTED; final docs/publication PASS. Исторический Q0 не включал public saved-file routes/job/outbox/worker; текущий default-off Q1 описан выше. Actual PREP schema12/13 не активирована этим proof. SDK0.1.2localUNRELEASED/remoteCI OPEN; real-private policies OPEN; scientific user-deferred/not_assessed.
## Дополнение O2 · реализация и runtime проверены, 20.10.2026

Optional observability source Go/Python/stack прошёл Senior review; **implementation/runtime VERIFIED**, W1/P2 CLOSED, три UI cycles и final rollback PASS. Финальные docs review/publication PASS; O2 принята в согласованной области локального пилота. [O2 execution](../references/o2-q0-execution.md), [runtime evidence](../references/o2-runtime.md), [runbook](../references/observability-o2.md) фиксируют actual paths/outages и оставшиеся gates; это отдельное evidence, не пересмотр PREP/O1/F1 ниже. Изолированный PREP использует PostgreSQL55439, lane integration — PostgreSQL55432. Loopback3300 Grafana имеет native auth и отдельно разрешённый пользователем bridge с возможным egress; Collector/Loki/Tempo/Prometheus остаются во внутренних сетях; бизнес-сети не меняются. Lossless/exactly-once/production capacity не заявляются. После O2 идёт Q0→Q1/F2 по прежним зависимостям; [Q0 architecture](../references/q0-architecture.md) отражает принятый internal source; [паспорт/Excel/помощник](../references/well-operations-proposal.md) остаётся отдельным предложением. Scientific not_assessed/user-deferred.

Редакция: **PREP и программная приёмка O1/F1, 20.10.2026**. Предыдущая очередь DOC-01–07 принята; её исторические материалы сохраняют дату. Текущие факты сверены с [PREP ledger](../references/readiness-ledger.md), [архитектурой](../references/readiness-architecture.md) и [playbook](../references/readiness-runtime.md). Выполнение команд и browser QA принадлежат указанным в ledger исполнителям; этот текст не является новым прогоном тестов.

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

O1/F1 приняты отдельно в согласованной программной области: шесть вкладок и core SDK/logging прошли три task reviews и whole-wave Senior Spec/Standards PASS без findings. Новая сборка прошла runtime 12/12, browser 7/7 и дополнительный mobile controls 2/2, pageErrors 0; scoped visual/mobile-account PASS. Источники/ограничения и четыре новые локальные payload refs — [execution ledger](../references/o1-f1-execution.md), эксплуатация — [O1 contract](../references/observability-o1.md)/[runtime](../references/o1-f1-runtime.md). PREP manifest и результаты выше исторические и не заменены новым source. Приёмка не заявляет successful live scoring→persist, полный assembled parent graph, forced rollover или научное качество; provenance остаётся partial. F2/F3/read-state, O2/Q/брокер/jobs и остальные очереди не входят в эту приёмку. Пользователь 20.10 поручил продолжать по roadmap после её публикационного gate: следующая передача O2/Q0, затем Q1/F2 по зависимостям. Это не предположение о внешней доставке, file TTL, SSO или production SLA/RPO/RTO. Научная приёмка остаётся not_assessed.
