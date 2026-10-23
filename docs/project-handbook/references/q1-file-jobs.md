> Снимок документа 23.10.2026. Источник: `pulse-backend/docs/q1-file-jobs.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# Сохранённые проверки файлов Q1

Q1 — отдельный synthetic-only путь, выключенный по умолчанию. Существующие
`POST /ml/upload` и period checks сохраняют прежние ответы и назначение.
Реальные файлы и production enable не входят в эту конфигурацию. Source DB/SW и узкий execution_busy fix приняты Senior; actual runtime/browser gates и fixed bindings находятся в [датированном proof/runbook](q1-runtime.md).

Пользователь с действующей local session отправляет CSV/XLSX для одной
разрешённой скважины. Server-owned manifest содержит raw SHA, канонический
client parse spec, exact ExecutionPin, accounts, полную cohort и имя test schema.
Имя файла, поля клиента и API-ключ не доказывают synthetic applicability.
Raw20MiB, canonical50MiB,100000rows; JSON mapping≤64KiB. Маркеры и валидные нули
нормализуются существующим Q0 parser без нового алгоритма.

`202` означает commit input+quota+job+outbox одной PG transaction. Идентификатор
job равен input ID; request_id16–80 символов принадлежит namespace actor/file_check.
Повтор той же заявки возвращает тот же job/pin без нормализации и новой квоты;
изменённый commitment даёт409. Закрытие browser и logout не отменяют заявку.

API читает metadata без payload/ML/broker. Dispatcher публикует только
`version,event_id,job_id,generation` в RabbitMQ. Worker отдельно получает lease,
заново проверяет текущие права в PG и читает сохранённый input/pin. Browser
Principal для worker не создаётся. Серверные lease token/fence не публикуются.

States: pending_dispatch → queued/running → succeeded. Только allowlisted transient compute error (`ml_unavailable`, `ml_deadline`, exact
`execution_busy`) переходит в retry_wait с persisted10/30s delay. Cancel running возвращает
cancel_requested; heartbeat отменяет локальный RPC, а fenced transaction сохраняет
cancelled. Cancel queued/pending/retry не запускает ML. Terminal states:
succeeded,failed,cancelled,expired,revoked. Quarantined останавливает auto-processing;
redrive доступен только application/operator seam владельцу admin, без HTTP route.

Lease45s, heartbeat10s, reconcile5s; deadline20min, compute≤5min, finalize≤10s.
Один PG runtime slot, worker1/prefetch1,≤3attempts,≤6generations,≤8publishes/event.
Late/stale worker не сохраняет результат. Q1-specific adapter сохраняет safe
`execution_busy` только для exact RESOURCE_EXHAUSTED reason; прочие resource limits
остаются deterministic, public Q0 `UploadPinned` mapper сохранён. Ack разрешён лишь после persisted
terminal/retry/quarantine/duplicate disposition. Неизвестный сбой PG оставляет
сообщение unacked и закрывает consumer channel с bounded reconnect.

Publisher использует mandatory+confirms, inflight1. Buffered return обрабатывается
до объявления ack успешным, даже когда select первым увидел confirm. Returned,
nack и unknown не считаются queued. Unknown допускает повтор того же EventID;
ранний worker claim не регрессирует из running в queued при позднем confirm.

Успех сохраняет один Q0 result и private FileSavedCheck projection (`q1_<jobID>`),
без INSERT в ManualCheck и без изменения фондового риска/яруса. Insufficient —
успешно сохранённый бизнес-результат с недостаточностью, а не норма или retry.
Полуоткрытые start/end покрывают реальные canonical timestamps; одна строка
имеет интервал1µs, который не добавляет измерений или ML evidence.

Доступ к job/result/review требует текущего owner account, technologist/admin,
well/cohort/guard. Другой admin не получает чужой private check. Viewer сохраняет
старые period reads. Review — отдельное mutable заключение kind `file_check` с прежним request_id /
revision протоколом; чтение и изменение повторно проверяют права внутри PG tx.

HTTP: `/file-check-capabilities`; `/wells/{well_id}/file-check-jobs` POST/GET;
`/{job_id}` GET, `/result` GET, `/review` GET/PUT, `/cancel` POST. Новый well_id в
JSON — decimal string, legacy DTO остаются прежними. Status содержит safe
input/execution summaries, nullable result/cancel URLs; payload, mapping, actor,
lease и tokens отсутствуют. Deep link `tab=checks&check=q1_<jobID>` выбирает reader
этой projection. Чтение результата/review не вызывает ML.

Ошибки имеют `{code,message,detail:null}` и безопасный диагностический X-Request-ID.
401 session,403 role/Origin,404 private unavailable,409 commitment/revision/state,
413 raw/body cap,422 invalid/nonfixture,429 bounded admission (Retry-After10),
503 default-off/admission unavailable. Cancelled/expired result даёт410;
nonterminal result409 с Retry-After2. Полный wire contract: [OpenAPI](../contracts/openapi.json).

Schema13 additive поверх12; migration выполняется явно только в выделенной test
schema. Старый binary читает legacy period/check/alert records, private projection
не попадает в его namespace. Rollback приложения: остановить admission, drain
worker, остановить dispatcher/reconciler; сохранить PG/Rabbit volumes и pending
намерения. Schema downgrade и удаление retained inputs здесь не предусмотрены.

Настройка: [configuration](backend-configuration.md), эксплуатация: [operations](backend-operations.md).
Редактируемая схема: [q1-file-jobs.drawio](../diagrams/q1-file-jobs.drawio). Реальные scientific
качество/production SLA и real-private TTL/encryption/backup policy не заявляются.
