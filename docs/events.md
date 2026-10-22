# История сохранённых событий

`GET /api/v1/events` и `GET /api/v1/events/{event_id}` читают обычные сохранённые
основания: `manual_check.saved.v1`, `check.review_changed.v1`,
`alert.review_changed.v1`, `well.metadata_changed.v1`, `well.archived.v1`,
`well.restored.v1`. Настраиваемый `API_V1_PREFIX` применяется к обоим маршрутам.
Это история предметных фактов; технический `/audit`, Q1 private file jobs,
Q2 cohort jobs и прогоны остаются отдельными API. Чтение не запускает ML
и не меняет состояние обработки.

## Параметры и ответы

List принимает только следующие однократные параметры:

| Параметр | Значение |
|---|---|
| `limit` | По умолчанию25, целое1–100; без clamp |
| `types` | CSV из шести точных типов выше, без повторов; по умолчанию все |
| `well_id` | Положительный канонический decimal int64 |
| `from`, `to` | RFC3339 со смещением, не более6 дробных знаков; UTC, `[from,to)`, `from<to` |
| `archive` | `all` (default), `current`, `archived`; текущее состояние карточки |
| `cursor` | Непрозрачный серверный token; фильтры и limit сохраняются |

Неизвестные/повторяющиеся ключи, неизвестные/повторяющиеся типы и неверные
границы возвращают422. `active=false` не скрывает историю. Ответ:
`{items:[],has_more:false,next_cursor:null}`; total/count отсутствуют.
Все ID, ревизии, счётчики и `ThresholdRule.k_windows` в F3 — decimal strings,
включая nullable metadata скважины. Внутри Go/PostgreSQL используются int64.
Timestamps выдаются UTC с6 знаками микросекунд.

Summary имеет закрытый набор `event_id,event_type,occurred_at,well,target,actor,outcome`.
Компактный well — текущие `id,well_number,active,archived_at`; target —
`kind,id,revision` (у manual revision=null). Outcome не восстанавливается из
текущего риска: verdict `alert/watch/norm/insufficient`, неизвестный→`unknown`,
отсутствующий/null→null; review status `new/in_progress/closed`, conclusion
`confirmed/not_confirmed/inconclusive`; неизвестные значения→`unknown`.
Summary не содержит comments, owner, reason, raw verdict/result, filename,
внешних ID или URL. Автор берётся только из сохранённого snapshot:
`user:<positive int64>`+true→person, `shared`/`service`+false→соответствующий вид;
неполный/неверный snapshot и manual→unknown с null полями. Имя аккаунта
не перечитывается.

Exact Get возвращает `{event,detail}` по сохранённому locator, без подмены latest.
`detail.kind=check`: check header `id,well_id,created_at,start,end` и typed saved
UploadResult (metrics, timeline, contributions, provenance и decision rules;
filename исключён). `kind=review`: `review_kind,revision,created_at,before,after,comment`.
`kind=well`: `revision,created_at,change_kind,before,after,reason`; snapshots Well
относятся к моменту события. Нули, false, null и nullable диагностические точки
сохраняются. Неизвестные JSONB keys не передаются. Локальные контрактные примеры:
`internal/transport/httpapi/testdata/events_examples.json`.

## Доступ и продолжение

Для local viewer/technologist применяется текущий фонд/НГДУ/цех Principal;
admin обходит well scope. Явный legacy Principal сохраняет ordinary admin чтение.
Отсутствие Principal не означает unrestricted. Режим research задаётся явно;
cohort требует текущие полные100 участников, version и manifest. Admin также
ограничен cohort. Fence повторно проверяется в той же read-only RepeatableRead
транзакции, что чтение. Недоступная политика/неполный cohort→503.

Review check/alert требует реально связанный primary target и текущую скважину;
NULL case_review.well_id допустим только при таком target. Mismatch, orphan,
unlinked alert и file_check скрыты. Прямой well_id/exact hidden target→404.
Scope применяется до UNION/keyset/limit+1: запрещённые факты не сокращают страницу.

Порядок DESC: `(source timestamp,source rank,target C text,revision)`;
ранги manual1, review.check2, review.alert3, well4; manual revision0.
Это время источника, не commit sequence. Cursor AES-256-GCM v1 связан с
нормализованными фильтрами/limit и текущими mechanism/account/role/scope/mode/cohort.
TTL30мин, случайный process key; restart/иная replica требуют обновить список.
Cursor не даёт прав: каждый запрос повторно проверяет текущую авторизацию.
Страницы live, без общей MVCC snapshot: новая строка перед anchor появится после
обновления начала; строка позади anchor может появиться в продолжении. Удаление
anchor не ломает keyset; исчезнувший source больше не виден и Get возвращает404.

## Ошибки и бюджеты

Каждый ответ F3, включая auth errors, имеет `Cache-Control:no-store`.
Envelope `{code,message,detail}`:401 unauthorized,403 forbidden,404 not_found,
422 validation_error,503 external_service_error,500 internal_error.
Все причины отклонения cursor дают одинаковые422 и
`detail:"cursor_restart_required"`; остальные detail=null. SQL/секреты наружу не выдаются.

Token и полный `e1.` locator ограничены2048 ASCII bytes; decoded cursor JSON≤2048.
Уже escaped pathname+search каждого API/SPA/well exact/continuation URL≤4096 bytes.
Oversized source/emission→весь ответ503, без пропуска фактов или truncation.
Proxy может отклонить произвольно большую request-line как414 до Go.
Encoded full detail≤16MiB проверяется перед записью ответа; превышение→503,
частичный JSON не выдаётся. Request context5s включает pool/fence/read;
transaction-local statement_timeout3s. Это ограничения ресурса, не latency SLO.

## Хранение и rollback

Источники application-immutable; F3 не добавляет writer, migration, индекс,
backfill, purge или TTL. Полнота прошлого и вечное хранение не обещаются.
Нет tombstones для исчезнувшего source; восстановление использует существующие
backup/source recovery. Rollback — предыдущий backend/frontend binary;
первичные данные остаются прежними. Runtime, нагрузочная и научная приёмка
учитываются отдельно от наличия source API.
