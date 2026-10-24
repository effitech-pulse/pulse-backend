# Настраиваемые права доступа

RBAC v1 хранит роли, группы и назначения в PostgreSQL. Сеанс содержит identity,
а каждое значимое изменение данных повторно проверяет текущий account, epoch,
union разрешений и организационную область. Имя роли и deprecated поле `role`
не предоставляют полномочий. [Компоненты и границы доверия](rbac.drawio) доступны
в редактируемом C4 component view без добавления сетевых сервисов.

## Разрешения и область

Полный каталог и зависимости определены в `internal/domain/authorization.go`
(`PermissionCatalog`). Неизвестное разрешение запрещает действие; отсутствие
prerequisite не восполняется автоматически. Прямые роли и роли групп образуют
один union. Удаление одного источника сохраняет право, выданное другим источником.

| Каталог | Действия |
| --- | --- |
| `wells.*` | чтение, создание, изменение, импорт, экспорт, архив, восстановление, удаление, история |
| `telemetry.*` | чтение, импорт, совместимое чтение и scoring |
| `risks.*`, `checks.*`, `alerts.*` | риски и объяснения, проверки, решения технолога |
| `filechecks.*` | private saved-file чтение, запуск, отмена, review; отдельный legacy sync запуск |
| `incidents.*` | чтение, создание и удаление отказов |
| `references.*`, `parameters.read` | справочники и параметры |
| `runs.*`, `cohort.runs.read` | чтение, экспорт, запуск и scoped снимки |
| `services.read`, `metrics.read`, `analytics.read`, `audit.read` | состояние сервисов и глобальные служебные данные |
| `presentation.scenario.read` | служебный сценарий презентационного фонда |
| `integrations.*` | источники, status/check, журналы, collect и ingest |
| `collectionjobs.*`, `cohortjobs.*` | чтение, запуск и отмена durable заданий |
| `users.*` | чтение, создание, изменение, назначение ролей, групп и области |
| `roles.read`, `roles.manage`, `groups.read`, `groups.manage`, `access.manage_global` | управление графом доступа |

Личная область `organization`, `ngdu`, `department` или `none` применяется одинаково
ко всем источникам разрешений. `department` — пара НГДУ/цех, без построения новой
корпоративной иерархии. Мигрированный сложный legacy scope сохраняет исходный
предикат двух массивов. Технический `scope.funds` пересекается с личной областью;
исторические null, пустые массивы, нулевые и отрицательные ID сохраняются.

Глобальное управление доступом не разрешает читать чужие скважины. Локальный
управляющий изменяет пользователя только при включении его текущих и новых
полномочий, области и funds в полномочия управляющего. Общие определения ролей
и состав общих групп изменяет global manager. Нельзя удалить последнего active
manager с полным фактическим набором management permissions.

## HTTP и совместимость

Маршруты остаются под `/api/v1`; [OpenAPI](../api/openapi.json) содержит типы и
политику routes. Local использует существующий персональный вход, cookie,
origin и CSRF-проверки. Legacy — отдельный проверенный server profile, управление
RBAC в нём недоступно. Отсутствующий context или произвольный `Role="admin"`
не является системным actor.

Новые DTO включают `access_model`, прямые `role_ids`, `group_ids`, effective роли,
`permission_keys`, источники с actual role IDs/revisions, `organization_scope`,
`legacy_scope`, `authz_epoch`, `graph_revision`. Новые ID и версии в HTTP —
десятичные строки; существующие числовые поля не меняют тип.

Поле `role` остаётся non-null deprecated projection. Только неизменённая одна
builtin роль без групп представима старым ключом; иначе `role="viewer"`,
`legacy_role=null`. Projection не используется для authorization и не заменяет
название фактической роли. Старый UI должен ориентироваться на capabilities.

Modern writes используют read-only preview, graph revision, actor epoch и
fingerprint. Apply заново проверяет текущий граф под transaction locks. Preview
не сохраняет draft и не содержит пароль. Stale receipt возвращает409 без writes.
Пароль передаётся отдельно и не входит в preview, fingerprint или audit.

В compat modern mutations недоступны, legacy POST/PUT dual-write-ят seed links.
В enforce старый POST запрещён; PUT допустим только для представимого legacy
account и не меняет modern multi-role/group/scope назначения. Сложные назначения
не превращаются в fallback viewer assignment при сохранении старым клиентом.

## Schema16 и исполнение jobs

Schema16 аддитивна: граф ролей/групп, epochs, activation state и access audit.
Seed выполняется при первом применении: admin, technologist, viewer получают
фиксированные наборы данного релиза. Повторный Apply не восстанавливает удалённые
назначения, изменённые permissions или будущие ключи каталога.

Каждый новый account job сохраняет отдельный `access_v1` snapshot: account ID,
epoch и actual role summaries. Snapshot не входит в input/output/manifest hashes.
File/cohort owner остаётся в прежнем `actor_account_id`, collection authority —
в immutable intent, legacy ML — в `ml_run`. Исторические role/scope/bytes не
переписываются. Новые независимые immutable triggers переживают повтор Apply
исторических schema12–15.

Rabbit передаёт opaque wakeup; actor из payload не принимается. Admission,
input read, renew/checkpoint и final publication перечитывают текущие права.
Публикация result/check, all100 snapshot/alerts или measurements/coverage выполняется
в одной существующей fenced transaction. Account lock берётся до union и
удерживается до commit. Worker/data paths не берут graph state lock: graph edits
используют порядок graph → sorted affected users. RPC и Fetch выполняются вне
этих transactions.

`actor_role` — история. Новые custom-role cohort jobs могут иметь отображение
viewer только вместе с valid `access_v1`; исторический NULL frame сохраняет
старое admin-only ограничение. В enforce NULL/historical compute запрещён.
Исторические terminal results доступны владельцу по его текущему session scope,
без требования совпадения старого job epoch с новым входом.

Manual ML и collect различают account, профильный server scheduler и verified
legacy HTTP. Nil snapshot не означает system. Manual run сохраняет initiator
через detached context; публикация читает persisted frame. Source writer frame
связан с generation/token и очищается вместе с lease. Revoked publication не
пишет product output; fenced cleanup освобождает свой lease и завершает ошибку.
Неизвестная SQL-ошибка сохраняет retry/fence policy и не выдаётся за revoke.

Прежние default-off modes, synthetic-only manifests, pilot allowlists, owner,
FundScope, lifecycle/binding checks, capacity/capture/physical session locks,
immutable outputs и лимиты попыток действуют дополнительно к permissions.

## Включение и восстановление

Это процедура оператора. Наличие source и успешных test migrations не активирует
рабочий runtime и не разрешает real mode.

1. На отдельной тестовой БД проверить fresh15→16, повтор Apply, scope truth table,
   malformed/owner/immutable constraints и worker revoke/fence проверки. Schema16
   должна содержать итоговые authority columns, validators, CHECK и triggers.
   Старый неполный вариант16 не удовлетворяет startup readiness.
2. Выпустить API и все workers с `RBAC_MODE=compat` (default). Schema применяется
   отдельно существующей maintenance командой. Не смешивать старые пишущие
   binaries без dual-write с новым backend. Modern graph mutations закрыты.
3. Выпустить capability-aware frontend. Проверить fallback старого клиента и
   сохранность non-null `role`; до cutover `access_model=legacy`.
4. Остановить admission/API writers/schedulers, дождаться terminal состояний всех
   pending/running/retry/quarantined jobs и legacy runs, остановить workers.
   Проверить отсутствие file runtime, source holds/tokens/живых leases и physical
   ML lock. Снять согласованный полный backup, проверить его restore на тестовой
   БД. Пароли, identity bindings, результаты и граф входят в backup.
5. В maintenance окружении выполнить `bin/adminuser -activate-rbac`. Режим
   взаимоисключающий с `-login`/`-name`, stdin-пароль для него не нужен. Команда под
   capacity→graph→sorted users locks проверяет drain/last-manager, устанавливает
   activation timestamp, увеличивает epochs и удаляет local sessions атомарно.
   Повторная activation — no-op; команда не убивает jobs и не освежает их epochs.
6. Запустить все API/workers с `RBAC_MODE=enforce`. Enforce до activation и compat
   после activation отказывают в startup. Неизвестные persisted permissions и
   неполная schema policy тоже препятствуют запуску. Войти заново, проверить
   scoped allowed/denied и сохранность default-off guards.

До activation допустим возврат старых binaries/frontend после остановки writers,
если modern назначения не выполнялись. После activation backend rollback возможен
только на RBAC-capable версию с той же schema policy; frontend — на
capability-aware версию. Возврат на schema15 требует остановленного runtime и
восстановления целого pre-cutover backup с согласованием утраты последующих writes.
Автоматического flatten, down/drop или compat downgrade нет.

## Accepted well identity и research compatibility

Q1 сохраняет отдельный immutable `file_job_well_v1` frame с Pulse ID, общей
Revision, фондом, НГДУ/цехом, active/archive и обеими nullable внешними bindings.
Он не входит в input/digest. Current well сравнивается с accepted frame при
чтении/compute checkpoints/publication; Q2 использует тот же набор полей из
frozen100 members. Рост Revision прекращает исполнение даже после ABA или
редактирования только метаданных. NULL historical frame не реконструируется в
enforce. Неизменившийся frame допускает повторную доставку с прежними fences.

Небуферизованные raw/unbound IDs исторического research профиля допускаются
только при server-owned `AUTH_MODE=legacy` вместе с `APP_SCOPE=research`, явном
verified legacy principal, полном scope и точных operation permissions с
prerequisites. Adapter policy по умолчанию закрыта. Account/system/cohort actors,
synthetic pilots, scoped Fund/Frozen paths и явно заданные `pulse:<id>` используют
обычную binding проверку. Отсутствие binding отличается от SQL error; ошибка
запроса никогда не включает compatibility fallback. ML проверяет persisted run
kind/profile, collect — persisted writer kind/frame и token/generation/profile
fences; caller не может заменить их legacy discriminator.

Review closure: source definitions use typed create/update/status mutations with
current user/action/full-scope checks before source row locks. Status merges only
ID/status into the locked current config; update without a new secret preserves
the locked ciphertext and runtime watermarks. Manual cohort publication compares
all100 captured Metadata records against sorted locked live wells before run and
output writes; unchanged inactive/archive outcomes remain valid. Q2/C2 compat
permissions retain their fixed adapter while their persisted personal scope still
applies separately. An account audit snapshot making a collection intent exceed
65536 bytes returns HTTP400 (`code=invalid`, fixed
`message=collection_intent_too_large`, `detail=null`) with no job/hold/outbox writes.


## Профили сотрудников

Schema17 добавляет nullable должность, организацию, подразделение, отдел и
ссылку на аккаунт руководителя. Это информационные сведения: они не меняют
фонды, область скважин, роли, группы и worker authority. Уровни места работы
могут быть неизвестны независимо. Руководитель может быть отключён; self и
циклы запрещены. Строки trim → пустое в null, максимум200 Unicode-символов.

`GET /api/v1/people` и `GET /api/v1/people/{account_id}` доступны действующим
персональным local-сеансам в compat/enforce, включая аккаунты без users.read.
Legacy/shared-key получает403, anonymous401. Ответ содержит только id/name/
active/job_title/workplace/manager; manager — однократная безопасная ссылка.
Login, роли, scope, даты входа и security fields отсутствуют. Неактивная цель
доступна напрямую; список по умолчанию содержит только active. Все ответы
имеют Cache-Control: no-store. Административные user endpoints сохраняют свои
управленческие проверки и concealed targets.

Поиск идёт по имени, должности и трём строкам места работы (до200 Unicode),
без login. `%` и `_` трактуются буквально. `include_inactive=true|false`,
`limit=1..100` (default25), `cursor` до1024 байт. ID ascending keyset привязан
к SHA256 нормализованного поиска и флагу include_inactive; смена фильтров требует первой страницы. Между
страницами список live. Неизвестные/некорректные query дают400.

Запись выполняется только через существующий modern Preview/Apply. Optional
`change.user.profile` — полная замена revision/job_title/workplace/manager_id;
внутри workplace обязательны organization/division/department. Nullable поля
очищаются явным null, whole profile:null запрещён, отсутствие profile сохраняет
сведения. Revision и manager_id — canonical decimal string; create revision="0",
edit — текущая positive revision. Managed detail и apply user возвращают profile.

Preview включает `profile_change` и `session_effect=none|revoke_on_apply`;
apply — `session_effect=none|revoked`. Profile-only повышает только profile.revision,
сохраняет account revision, authz_epoch, graph_revision, сеансы и worker authority.
No-op не повышает revision. Изменение name/login/active/password/assignments/scope
сохраняет существующий отзыв сеансов. Запрос без profile сохраняет прежнюю
семантику. Общий create/access/profile/audit commit атомарен; manager/revision/
management recheck выполняется под graph serializer и sorted user locks.
Stale profile даёт409 с безопасным message `profile_revision_conflict` в прежнем
error envelope. Нет automatic replay или business idempotency: после потери
ответа сначала перечитать managed account/profile.

Миграция17 выполняется явно после backup/restore проверки. Старые записи
получают profile revision1 и null metadata без scope backfill; повтор Apply
не перетирает данные. Новый API binary проверяет marker17 и наличие колонок
при запуске. Worker readiness/authority остаётся schema16. Рабочая БД и activation
при source/test проверках не переключаются. Возврат к предыдущему RBAC binary
допускается только после отдельной проверки его readiness на schema17; новые
колонки сохраняются, автоматического down/DROP нет. Восстановление pre17 backup
теряет последующие записи. Старый frontend без profile сохраняет сведения;
новый frontend выпускается после profile-capable API.

## Производная структура организации

`GET /api/v1/people/organization/tree?search=&include_inactive=false` возвращает
целое read-only дерево organization→division→department→employees из профилей.
Это место работы, не reporting line руководителей и не scope скважин. Отдельных
оргединиц/CRUD/миграции нет. Ответ: `complete:true`, `employee_count`, `organizations`;
каждый branch содержит key/value/employee_count и дочерний массив. Employee leaf
содержит только string id, name, nullable job_title, active. Counts относятся только
к найденным сотрудникам; это не штатная численность. Пустой ответ имеет пустой массив.

В одном RepeatableRead/read-only snapshot перечитываются active/epoch actor и
matched rows. Доступ совпадает с people: активный персональный local session в
compat/enforce, включая zero permissions; shared-key403, anonymous/revoked401.
`Cache-Control: no-store`. Список и его cursor API не меняются; дерево не собирается
из одной страницы или последовательности страниц. После изменения профиля нужен
новый запрос; directory revision/live subscriptions не добавлены.

Query принимает только одиночные search (trim, ≤200 Unicode) и include_inactive
(true/false, default false). Поиск literal case-insensitive по собственным name/
job_title/трём workplace strings, до группировки; `%`, `_` и backslash буквальные.
Malformed encoding/UTF8/duplicate/unknown/cursor/limit →400 с code `invalid`,
message `Некорректный запрос структуры`, detail:null. Совпавшие листья сохраняют
всех предков. При include_inactive=true видны active и inactive.

Равенство путей точное, без case/NFC merge. Null на каждом уровне — отдельный
узел, отличный от любого реального текста placeholder; пропущенные уровни не
удаляются. key — base64url без padding от compact JSON typed full path:
`["organization",org]`, `["division",org,division]`,
`["department",org,division,department]`. Одноимённые отделы под разными родителями
не сливаются. Реальные значения сортируются по UTF8 bytes, null последним;
листья по name, затем numeric ID. Клиент сохраняет серверные ключи/порядок.

Safety policy: максимум5000 matching employees, SELECT LIMIT5001 без manager
join/N+1, общий deadline5s для acquisition/actor/SELECT, максимум8388608 bytes
конечного UTF8 JSON **вместе с завершающим newline**. Это защитные пределы, не
измеренная production capacity. Marshal и проверка byte bound выполняются до
отправки headers/body. Превышение row/byte bound даёт503 `external_service_error` /
`organization_tree_too_large` / detail:null; deadline —503 с тем же code и
`organization_tree_timeout`. Частичного200/неполных totals нет. Ошибки не раскрывают
SQL. Клиент может сузить поиск или вернуться к списку;404/405/501 старого сервера
означают unsupported, без автоматического cursor-all fallback.
