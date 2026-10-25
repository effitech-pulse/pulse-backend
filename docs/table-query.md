# Фильтры колонок и множественная сортировка

Существующие списки поддерживают opt-in `table_query`: JSON версии1 с `filters` и
упорядоченным `sort`. Фильтры применяются ко всему текущему разрешённому набору
до SQL LIMIT или пагинации полного графа доступа. Прежние клиенты без этого
параметра сохраняют свои параметры, формат ответа и лимиты.

```json
{"version":1,"filters":[{"field":"actor","op":"in","values":[null,"Технолог"]}],"sort":[{"field":"actor","direction":"asc"},{"field":"ts","direction":"desc"}]}
```

Передайте URL-encoded JSON в `table_query`, `page_size` от1 до100 (по умолчанию25)
и при продолжении opaque `cursor` из `next_cursor`. Одновременные legacy `before`
или `limit` запрещены. Query-режим возвращает `items` и nullable `next_cursor`;
страница ровно из25 элементов может быть последней. RBAC-страницы дополнительно
сохраняют `graph_revision`/`total_visible`; preview сохраняет исходные receipt,
fingerprint и counts до фильтров, Events — `has_more`.

| Маршрут | Поля |
|---|---|
| `/audit` | ts, actor, method, path, status_code |
| `/integrations/logs` | timestamp, source_id, accepted, duplicates, rejected, message |
| `/wells/{id}/checks` | created_at, period (пересечение start/end), verdict, model_name |
| `/wells/{id}/file-check-jobs` | created_at, state, model_name |
| `/wells/{id}/history` | created_at, kind, actor_name, changes, reason |
| `/people` | name, job_title, workplace_organization/division/department, manager_name, active |
| `/events` | event_type, well_id, well_number, archive, occurred_at, actor_name, actor_kind, verdict, review_status, conclusion |
| `/auth/users`, `/auth/groups/{id}/members` | name, login, role_ids, group_ids, organization_kind, ngdu_id, department_id, active |
| `/auth/groups` | name, description, role_ids |
| `/auth/roles` | name, description, permission_keys, permission_count |
| `/auth/access/preview` (POST) | user_name, before/after_organization, before/after_active, added/removed_permissions, source_role/group_ids |

Префикс таблицы — `/api/v1`. Точные operators и типы каждого поля заданы в
[OpenAPI](../api/openapi.json) (`x-table-query-fields`, `TableQuery`). Неизвестное
поле/оператор, повторный ключ sort, неверный диапазон или смешение режимов дают400
с безопасным `message: invalid_table_query`. JSON ограничен16KiB, фильтры/ключи —64,
`in` —100 значений, текст —120 Unicode символов. Decimal int64 selectors передаются
строками; числовые range-границы также принимают canonical decimal string без float64
округления; ID и номер скважины имеют разные смыслы.

Между колонками действует И, внутри `in` — ИЛИ; пустой выбор выключает условие.
`contains` ищет буквальный текст, включая `%`, `_` и обратную косую черту.
`range`/`overlap` включают обе границы. Null,0 и false различаются. `period`
сортируется по началу окна. `changes` — тот же текст изменений before/after:
фиксированные подписи карточки, null=`—`, boolean=`Да`/`Нет`, строки разделены LF.
Query DTO журнала сбора добавляет реальный DB `id` decimal string; legacy DTO
не расширяется.

Все пользовательские ключи выполняются в указанном порядке, затем добавляется
уникальный ID; Events используют source_rank/target_key/revision. Неизвестные
значения всегда в конце, включая DESC. При пустом sort возвращается исходный
порядок: audit ts/id DESC; ingest id DESC; checks/jobs created_at/id DESC; history
revision DESC; People и граф доступа ID ASC; Events occurred_at/source_rank/
target_key/revision DESC. Текстовая сортировка и contains используют явную замену
ASCII A–Z, русских А–Я/Ё и татарских ӘӨҮҖҢҺ на lowercase, затем Unicode scalar
порядок (`COLLATE "C"` в PostgreSQL). Пробелы и регистр остальных алфавитов
сохраняются; строковые номера не становятся числами.

Большинство времён — instant RFC3339 с обязательным offset, нормализуемый UTC.
Ненулевые доли после6знаков отвергаются400: PostgreSQL не может сохранить такую
границу без округления. Дополнительные нулевые знаки разрешены.
Особый случай — `audit.ts`: историческая колонка `TIMESTAMP WITHOUT TIME ZONE`,
её пояс неизвестен. Для неё диапазон — wall clock
`YYYY-MM-DDTHH:mm:ss[.fraction до6]` без offset. Ответ и сравнение не приписывают
UTC; подпись интерфейса — «Время записи · пояс не задан». Retention аудита остаётся
7суток. Прежняя Events `from/to` семантика `[from,to)` сохраняется вне query-режима.

Курсор подписывается случайным ключом процесса, связывается с нормализованными
условиями, endpoint, parent, page size, текущими account/permissions/scope и полным
sort tuple. Он не даёт прав: источники и private actor/well/pilot/cohort проверяются
повторно в транзакции чтения. Смена запроса/пользователя/области, повреждение токена
или перезапуск API дают400 `message: invalid_table_cursor`; клиент очищает cursor
stack и запрашивает первую страницу с теми же условиями. RBAC token дополнительно
связан с текущими graph revision/actor epoch и preview fingerprint.

Это живой keyset-список. Данные и mutable state между запросами могут измениться;
фиксированный snapshot, total/count всей истории и SLA сложных JSON/text predicates
не обещаются. Refresh получает первую страницу актуального набора. SQL остаётся в
PostgreSQL adapter с allowlist выражений и bind parameters, чтение ограничено5сек.
Схема БД, runtime feature flags и рабочие данные этой возможностью не изменяются.

Для доступных пользователю summary-options ролей/групп таблицы сотрудников служит
`GET /auth/users/filter-options?field=role_ids|group_ids|ngdu_id|department_id&search=...&page_size=25`.
Требуется только `users.read`; возвращаются `value` decimal string и `label`
назначений уже видимых `CanManageAccount` сотрудников, `graph_revision` и cursor.
Это не раскрывает definitions недоступных ролей/групп. Точные права источников
Events описаны в [Events](events.md), граф доступа — в [RBAC](rbac.md).

Для groups-only доступа аналогичный `/auth/groups/filter-options?field=role_ids`
требует только `groups.read`, возвращает distinct role_ids видимых groupVisible
групп с labels `ID N`; не читает пользователей и определения ролей.

Query-mode preview добавляет optional `table_filter_options.source_role_ids` и
`source_group_ids`: полные summaries из AFTER permission sources всех authorized
impacts до фильтров/пагинации. Это не поиск по before-only sources; receipt/counts
сохраняются. Для нового draft role/group ID может быть `-1`; legacy не содержит
этого поля.

Фасеты ngdu_id/department_id требуют только users.read и выводятся из Organization
полного already-visible AccountAccessView. Они не требуют users.scope.assign,
не раскрывают outside/privileged accounts и сохраняют0/отрицательные int64 коды
строками; labels — `ID N`.

Экспорт реестра с `identity=pulse` (включая lifecycle) записывает PULSE_ID как десятичный текст XLSX. Значения int64 выше 2^53 сохраняются точно при чтении браузером и обратном импорте; числовые НГДУ/цех и boolean ACTIVE сохраняют прежние типы.

Проверка API и PostgreSQL: задайте `TEST_DATABASE_URL` для отдельной тестовой БД и выполните `make integration`. Полный suite с race detector использует `INTEGRATION_TIMEOUT=40m`; при необходимости лимит переопределяется командой `make integration INTEGRATION_TIMEOUT=60m`. Тесты создают собственные временные схемы; Go defaults10m недостаточно для полного набора с реальными lease/expiry сценариями.
