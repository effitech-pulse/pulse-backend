# Журналы события и уведомления

C2 Rabbit events `collection.ready.v1` несут только version/event_id/job_id/
generation. PG outbox/inbox/reconcile, confirm+return и ACK после durable disposition
не дают exactly-once source Fetch до capture; committed capture больше не читается
с upstream. collection_job_action и safe collection log — durable diagnostics,
не уведомления пользователя. [Протокол](../references/q2-collection-jobs.md).

## Что работает сейчас

Технический `action_log` фиксирует изменяющие HTTP-запросы. Экран «Журнал» показывает method/path/status_code — это аудит API, а не полная предметная история. В отдельных сущностях уже сохранены события well_event, review_event, manual_check, ml_run и telemetry_collection_log.

Колокольчик открывает текущие alert со статусом new и ошибки источников для администратора. Он читает состояние API, не реализует очередь доставки, подтверждение прочтения, объединение событий или отправку email. Старые таблицы notification_rule/recipient_group не подтверждают, что эти сценарии завершены.

## O1: технические logs и spans

В текущем O1 source Go API и Python ML/Data используют core OpenTelemetry API/trace SDK 1.38.0 и allowlist JSON logging. Общие поля: `timestamp`, `severity_text`, `event_name`, `service.name`, `service.version`, `service.instance.id`, `deployment.environment.name`; при активном контексте — `http_request_id`, `trace_id`, `span_id`. Optional typed duration/status и static `error_code` не содержат сырые error/stack/payload. Record≤8192 UTF-8 bytes; секреты, metadata, SQL params, query/path IDs, filename/telemetry/comment и персональные данные не сериализуются. Неизвестные diagnostics получают фиксированный fallback event.

Spans охватывают реальные HTTP/gRPC/PG-вызовы; Upload заканчивается с terminal result/error, без span на chunk. PG-only check не получает фиктивный Data leg. Background operation связан с инициатором и сохраняет прежний lifecycle. Runtime provider имеет sampler 100%/bounded span limits, без сетевого exporter, очереди или retention list; test exporter используется только в тестах. O1 не предоставляет центральный поиск и не создаёт предметный журнал/уведомления. [O1 contract](../references/observability-o1.md) и [execution ledger](../references/o1-f1-execution.md) фиксируют software acceptance: root runtime 12/12 подтвердил actual Go→ML invalid-CSV Upload 422/no-persist и Go→Data unsaved connection check 503/no-import с общими trace/attempt IDs, whole-wave Senior PASS. Полный assembled parent-ID graph и успешный ingest/scoring→persist этим smoke не заявлены; точные parent/link relations подтверждены отдельными SDK/exporter/listener tests.

## Согласованный предметный журнал

Проверенная реализация O2 добавляет optional централизованный поиск технических diagnostics, а не новый business/employee журнал: [runbook](../references/observability-o2.md), [runtime](../references/o2-runtime.md). Log→trace/linked roots и metrics относятся к исполнению; operation request key, committed event и HTTP attempt ID различаются. Очереди/offsets/cumulative RAM counters не дают lossless/exactly-once; повтор diagnostics после Collector restart допустим и не означает повтор business результата. W1/final rollback закрыты; final docs/publication gate PASS, scientific not_assessed не меняется.

| Событие | Основание | Переход пользователя |
|---|---|---|
| Получены измерения | Успешный завершённый цикл сбора | Источник / период телеметрии |
| Ошибка сбора | Зафиксированная ошибка источника | Настройки и журнал источника |
| Проверка выполнена | Сохранённый manual_check | Конкретный неизменяемый результат |
| Новая тревога | Сохранённый результат прогона/проверки согласно принятому правилу | Карточка скважины и основание |
| Заключение сохранено | Commit review_event | История обработки случая |
| Скважина изменена/архивирована/восстановлена | Commit well_event | История карточки |
| Прогон завершён/неуспешен | Завершённая запись ml_run | Результат прогона |

Целевой конверт: event_id, event_type, occurred_at, well_id/source_id/target_id, actor, severity, outcome, correlation_id и ссылка на первичный объект. Event_type имеет версию. Успешное событие создаётся по факту commit; HTTP 200 сам по себе не заменяет предметное событие.

## Надёжность и доставка

Рекомендуемый способ — transactional outbox в той же транзакции, что предметное изменение. Отдельный dispatcher публикует/обрабатывает события; потребитель устраняет повторы по event_id. Повтор доставки не должен создавать второе заключение или второй факт отказа. В текущем коде общего outbox/dispatcher ещё нет.

По умолчанию пользователь выбрал **новые тревоги и ошибки сбора**. Ошибки глобальных источников не раскрываются роли, которая не имеет к ним доступа. Короткие сообщения автоматически скрываются через 4 секунды, максимум три одновременно; закрытие сообщения не меняет обработку случая. Прочитано, отправлено в SMTP, принято SMTP и доставлено адресату — разные состояния.

Для почты ещё требуются группы, scope, расписание/cron, шаблоны, подтверждение тестовой отправки, очередь и журнал попыток. До настройки адресатов/SMTP письма не отправляются. Предлагаемая очередь RabbitMQ или Kafka не является уже работающей подсистемой.

## Ordinary Events F3

Реализован additive read API сохранённых проверок, check/alert ревизий обработки
и lifecycle карточек. Это предметная история источников с текущими правами,
без новых writers/доставки/read-state. Технический audit, Q1 и Q2 остаются отдельно.
[Точный HTTP/ops контракт](../../events.md) описывает string-int64, exact detail,
cohort, live pagination, budgets, ошибки и отсутствие perpetual retention guarantee.
