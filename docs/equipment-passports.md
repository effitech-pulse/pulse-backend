# Паспорт оборудования · schema18

Паспорт описывает установленный насос, ПЭД и станцию управления в пределах
Pulse-скважины. Он хранит номинальные сведения, неизвестные значения, экземпляры,
эпизоды установки и неизменяемую историю утверждений. Телеметрия, управляющие
уставки и риск не заполняют паспорт. API не вызывает ML, Rabbit или коллектор.
Паспортная revision независима от административной well.revision.

## Доступ и маршруты

Все маршруты находятся под `/api/v1`, требуют персональный `AUTH_MODE=local`
сеанс и `RBAC_MODE=enforce`; legacy/API-key и compat не разрешают паспортные
операции. Права включают полную рекурсивную requires-цепочку из RBAC-каталога.
PostgreSQL повторно проверяет active account, authz epoch, права, технический
fund/cohort, organization scope, архив и revisions в своей транзакции.

| Метод и путь | Право и результат |
| --- | --- |
| GET `/wells/{well_id}/equipment-passport` | `equipment_passports.read`; current slots, revision, provenance |
| GET `/wells/{well_id}/equipment-passport/history` | `equipment_passports.history.read`; TablePage |
| POST `/equipment-passports/previews` | `equipment_passports.update`; manual JSON preview |
| POST `/equipment-passports/import/previews` | `equipment_passports.import`; multipart XLSX preview |
| GET / DELETE `/equipment-passports/previews/{preview_id}` | `equipment_passports.read`; private read / отмена204 |
| POST `/equipment-passports/previews/{preview_id}/commit` | `equipment_passports.update`; атомарный commit/receipt |
| GET `/equipment-passports/receipts/{receipt_id}` | `equipment_passports.read`; private receipt |
| POST `/equipment-passports/template` | `equipment_passports.import`; XLSX шаблон, export не нужен |
| POST `/equipment-passports/export` | `equipment_passports.export`; history также требует history.read |

Preview и receipt доступны только владельцу, включая администраторов; ссылка на
receipt в provenance не обходит этот запрет. Потеря текущего доступа к скважине
также закрывает receipt. Все ответы passport handlers имеют `Cache-Control: no-store`.
Чтение текущего паспорта не требует history.read: last_change загружается с
current values одним authorized snapshot. Это последнее материальное изменение
данной установки; неизменённый импорт и исправление отдельного закрытого
предшественника не меняют автора текущей записи. Происхождение отдельных значений
прослеживается через исходные события before/after. Неизвестные metadata остаются null.

Полные схемы запросов и ответов: [OpenAPI](../api/openapi.json). IDs/revisions
передаются только точными десятичными **строками int64**, включая значения >2⁵³;
номер скважины/serial/model не являются join keys. Отсутствующий паспорт:
`passport_id=null`, `revision="0"`, пустые slots/provenance. Decimal HTTP values
также строки; целое pump_stage_count — JSON integer.

## Проверить → сохранить

Manual input: `{schema_version:"equipment-passport-v1",rows:[...]}`. Каждая строка
содержит row_key, well_id, expected_well_revision, expected_passport_revision,
slot и action; точные target IDs нужны для существующего эпизода. PATCH плоский:
поле отсутствует — no-change; null — явная очистка nullable поля; значение — set.
Пустая строка не является очисткой. На register/replace отсутствующие значения
становятся null, атрибуты предшественника не наследуются. Actor, source,
recorded_at и неизвестные JSON keys отклоняются; source задаётся маршрутом.

- `register`: новый экземпляр и установка пустого слота.
- `update`: исправление текущего экземпляра/даты; no-op не повышает revision.
- `replace`: новый экземпляр; `replacement_on` обязателен как дата или null.
- `correct`: исправление current/closed эпизода; связанная граница меняется через
  predecessor target + linked_installation_id + replacement_on, обе стороны в diff.
- `close`: снятие текущего, removed_on обязателен как дата или null.
- `void`: ошибочная регистрация; слот освобождается без восстановления предшественника.

Reason обязателен для материального update и replace/correct/close/void. Даты —
календарные `YYYY-MM-DD`, без времени и будущего по Config.Location (default
Europe/Moscow). Известный конец строго позже начала; неизвестная граница — null,
а не бесконечный интервал. Все nullable паспортные поля могут быть неизвестны,
но register/replace требуют хотя бы model/manufacturer/serial_number.

Preview содержит normalized commands, полный diff, row/field issues, counts,
digest и expires_at. Error issues блокируют весь пакет; warnings требуют явного
подтверждения. Commit body: `{digest,acknowledge_warnings}`; header
`Idempotency-Key` — canonical UUID. Все изменения, события, heads, key и receipt
фиксируются одной транзакцией; скрытого partial save нет. Lock order:
actor FOR SHARE → well IDs по возрастанию → passport heads/slots, без graph lock.

При409 сохранить черновик, перечитать current и сделать новый preview; merge
не автоматический. При timeout повторить **тот же** preview/digest/key: после
успешного commit сервер вернёт прежний receipt, проверив текущие права, без
повторного эффекта и без проверки устаревших expected revisions. При другом key
потреблённый preview возвращает410 `preview_consumed`; истёкший —410
`preview_expired`. Receipt и key fingerprint хранятся бессрочно с историей.

Errors:400 validation/JSON/workbook/query,401 personal session,403 permission/
profile,404 private/unavailable object,409 conflict,410 expiry/consumed,413 bounds,
429 quota/busy,503 timeout/cancellation/unavailable. Feature envelope:
`{code,message,detail:{issues?:[...]}}`. Унаследованный auth middleware на401/403 может вернуть detail=null; клиент принимает оба варианта. Общий legacy mapper сохраняет свой422.

History поддерживает table_query v1 + page_size≤50 + cursor. Alias limit≤50
допустим только без table_query/page_size. Default sort recorded_at desc,id desc.
Allowlist: recorded_at datetime/range; action,slot,source enum/in;
actor_name,reason,changes text/contains. SQL применяет фильтры и keyset до limit;
cursor связан с actor/epoch/scope/well. History export использует тот же query.

## XLSX v1

Template body `{well_ids:["..."]}`; export body
`{well_ids:["..."],mode:"current_components"|"history",query?:TableQuery}`.
Импорт — единственный multipart part `file`, без других полей/файлов. Сначала
проверить книгу, затем отдельно подтвердить private preview. Ошибочные строки
нельзя исключить для частичного сохранения. Оригинальные bytes не сохраняются.

Шаблон имеет только листы meta и components. Meta `key,value` содержит
`template_version=equipment-passport-v1`, `mode=current_components`,
`date_format=YYYY-MM-DD`. Все заголовки обязательны, порядок фиксирован шаблоном:

```text
row_key,well_id,expected_well_revision,expected_passport_revision,slot,action,component_id,installation_id,installed_on,replacement_on,change_reason,source_reference,model,manufacturer,serial_number,pump_nominal_flow_m3_day,pump_nominal_head_m,pump_stage_count,motor_rated_power_kw,motor_rated_voltage_v,motor_rated_current_a,motor_nominal_frequency_hz,control_rated_power_kw,control_rated_voltage_v,control_rated_current_a,control_output_frequency_min_hz,control_output_frequency_max_hz,clear_fields
```

Excel разрешает только register/update/replace. Blank update — no-change;
clear_fields — ключи через `;`, без дублей и одновременно заданного значения.
Create/replace blank — null, clear_fields запрещён. IDs/revisions/serial/даты —
текст, даты только ISO; Excel serial dates запрещены. Decimal cells — native
numeric либо decimal text с точкой/запятой, без exponent/разделителей тысяч.
Пустые строки пропускаются; source_row.index сохраняет реальную physical row.
Дубли row_key/(well_id,slot), несовместимые revisions и неизвестные headers — ошибка.

Current export пропускает пустые слоты, сохраняет nullable поля/единицы и exact
string IDs, action=update, clear_fields пуст. Без правок повторный импорт даёт
no-op. Meta дополнительно содержит exported_at, empty_slot_count и read-only
provenance.<row_key> JSON. Импорт игнорирует эти сведения для mutations/authority.
History export — отдельная read-only книга mode=history с before/after;
импорт history запрещён. Текст `=,+,-,@` записывается явной string cell,
не формулой и без изменения исходного значения. Экспорт не подтверждает достоверность.

## Пределы и очистка

| Ресурс | Жёсткий предел |
| --- | --- |
| Операция | 100 скважин / 300 command rows |
| JSON request | 512KiB |
| XLSX compressed / expanded | 2MiB / 16MiB |
| Multipart body | 2MiB + 64KiB envelope; сам file≤2MiB |
| ZIP entries / cells | 64 / 10000 на книгу |
| Обычная ячейка, включая components и metadata keys | 512 Unicode символов |
| Только read-only provenance JSON meta values и history before/after JSON | 16384 Unicode символа; не общее исключение для листа |
| История | page≤50; export≤1000 событий, без тихого truncation |
| Preview | TTL30мин; ≤2 на actor /16 global; payload≤1MiB; persisted≤16MiB |
| Parse/export | ≤1 на actor /2 на процесс; deadline10с |
| Commit / SQL lock wait | 5с /1с |
| Payload purge | startup + каждые5мин; expired bytes недоступны сразу, retention≤24ч при работающем API |

Import не принимает hidden sheets/rows/columns, merged cells, formulas/cached
formulas, macros, external links, OLE, encryption или zip traversal. Парсер не
обращается в сеть. Выходной XLSX полностью проверяется и собирается в bounded
buffer до HTTP headers/body: ошибка не отдаёт частичный ZIP. Контекст/закрытие
body прерывают stalled read и освобождают admission. GC при shutdown отменяется
и дожидается завершения до закрытия pool; receipts/events/keys он не удаляет.
Logs содержат IDs/counts/error codes, без workbook contents. Source/test gate
не доказывает production capacity: предельные книги/Unicode JSON измеряются
отдельным release acceptance gate; увеличение лимитов требует согласования.

Текст: model/manufacturer≤200, serial≤120, row_key≤64, source_reference≤200,
reason≤500 символов. Числа: precision≤15, scale≤6, ≤10⁹, положительные;
control_output_frequency_min_hz допускает0, при известных min/max min≤max.
Stage count1…100000. Фиксированные единицы: flow м³/сут, head м, power кВт,
voltage В, current А, frequency Гц. Перевод единиц не выполняется.

## Подготовка, explicit enrollment и rollback

Schema18 применяется **явно** штатным migrate после согласованного backup и
restore rehearsal в отдельной БД. Startup проверяет schema18/tables и purge;
код не выполняет DDL/activation/grants автоматически. Миграция не выдаёт новых
прав ни seed, ни existing custom ролям; восстановление builtin admin также
использует frozen прежние права. Перед включением сверить builds, schema,
accepted resource measurements и [RBAC activation runbook](rbac.md).

Первое назначение выполняется отдельно через
`bin/adminuser -enroll-equipment-permissions < protected-enrollment.json`.
Не совмещать с login/name/bootstrap/activation или positional args. Stdin —
один закрытый JSON объект≤16KiB, без shell history/secrets. Поля:
request_id UUID, target_account_id, expected_account_revision, expected_epoch,
expected_graph_revision (IDs/версии — строки), permission_keys. Allowlist:
пять equipment_passports прав и wells.read с полной requires closure.
Target — exact active account с полным effective manager permissions; имя
admin/builtin_key само по себе не даёт authority. CLI не изображает target
session actor: durable audit/receipt имеет actor_kind=maintenance, before/after,
digest/time. Создаётся отдельная роль/назначение, старые роли/scope/funds не меняются.
Graph→target locks, epoch/session invalidation обязательны; тот же request_id и
payload возвращают прежний receipt, другой payload того же UUID — conflict.
После enrollment нужен новый вход; дальнейшая выдача — обычный RBAC preview/apply.

Source/test готовность не включает actual deployment/enrollment или PREP/live
миграцию. Включение выполняет оператор отдельным согласованным gate. Перед
rollback остановить приём passport routes на ingress и всех passport writers,
дождаться in-flight operations, сохранить backup и maintenance receipts.
С текущим совместимым binary отозвать все новые grants/assignments через
контролируемый RBAC process и проверить отсутствие effective equipment_passports
permissions у всех accounts, включая групповые роли. **Старый binary с новыми
grants запускать нельзя:** он не знает их requires/проекцию. Затем выбрать
schema-compatible RBAC binary либо восстановить целый pre18 backup после
явного согласования потери последующих записей. Additive tables/history не
удалять; auto-down/DROP и автоматического переключения в legacy/compat нет.
