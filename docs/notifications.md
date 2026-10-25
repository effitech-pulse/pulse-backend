# Персональные уведомления

F4 добавляет персональную ленту двух типов сохранённых фактов: тревога нового
прогона и подтверждённая ошибка попытки сбора. Прочтение уведомления отдельно
от решения технолога и от открытия, закрытия или перехода из панели.

Подготовленный source требует schema19. Runtime PREP и рабочая БД автоматически
не мигрируются и не включаются. `NOTIFICATIONS_UI_ENABLED=false` по умолчанию
управляет выдачей ленты, но не capture. Новых permission keys, grants, сервисов,
брокеров, ML вызовов или email нет.

## HTTP

Все пути ниже имеют prefix `/api/v1`. Требуется local персональный сеанс;
legacy общий API key не даёт персональной ленты. Текущие effective permissions
проверяются и в compat, и в enforce; имя роли само по себе доступа не даёт.

| Метод и путь | Поведение |
| --- | --- |
| GET `/notifications/capabilities` | `enabled`, `reason` (enabled/feature_off/no_families), `families` (alerts/collection). Off и отсутствие семейств —200. |
| GET `/notifications` | Только state=unread/all, limit=1..100 (default25), cursor. Default unread. |
| GET `/notifications/unread-count` | value=0..99, capped=true при100 и более. Ошибка не становится нулём. |
| PUT `/notifications/{notification_id}/read` | Пустое тело, без query. Серверное первое read_at сохраняется при повторе. Hidden/absent/deleted —404. |
| GET `/wells/{well_id}/runs/{run_id}/alerts/{alert_id}` | Exact сохранённая тревога и исходные risk/window, без latest substitution. alerts.read и текущий well scope; cohort.runs.read не требуется. |
| GET `/notification-targets/source?source_id=…` | Только безопасный текущий обзор. integrations.read и full scope, без integrations.logs.read. |

Source ID — opaque string; query encoder сохраняет `/`, literal `%2F`, `%`, `?`,
`+` и Unicode. Сервер декодирует query один раз. Старый path-param overview не
поддерживается. Frontend target — `/integrations/source-overview?source_id=…`.
Exact alert UI — `/wells/{well_id}/alerts/{alert_id}?run_id=…`.

Unknown/duplicate/empty query values, malformed encoding, nonempty body и
неканонические decimal IDs возвращают422. Все новые bigint IDs — JSON strings,
факт имеет стабильный namespace `nf1_<positive-int64>`. Null,0,false сохраняются.
Время — UTC RFC3339 с шестью знаками микросекунд. Все success/error ответы
имеют `Cache-Control: no-store` и безопасный error envelope с detail=null.
401 — сеанс,403 — права,404 — скрытая/удалённая цель,422 — запрос/курсор,
503 — недоступность БД, deadline, выключенная выдача или превышение budget.

Cursor — canonical base64url AES256-GCM, TTL30min, максимум4096bytes. Привязан
к account, текущим epoch/graph/permissions/org/funds, cohort, activation,
state/limit/API version. Store проверяет fresh boundary перед выборкой.
Перезапуск инвалидирует cursor; fact ID и read-state остаются стабильными.
Страница выбирается по recorded_at/id, текущий scope применяется до limit/count.

Ответ ограничен256KiB с newline; display labels —256 Unicode codepoints.
Oversized opaque source ID не обрезается: безопасный503. Ошибки/config/logs
источника, чужие snapshot outcomes, SHAP и run.error не входят в DTO.
Legacy alert может иметь saved_result=null; это честное отсутствие snapshot.
Target readers доступны независимо от UI flag.

## Миграция и эксплуатация

Порядок будущего maintenance: backup schema18 → явная schema19 migration →
сверка исторических ID/results/reviews/RBAC → запуск всех новых writers →
drained activation barrier через `bin/notifications-activate`. CLI генерирует
случайную activation identity. Startup `ValidateNotifications` проверяет
schema/constraints/seed и не выполняет DDL или activation. Отсутствующая
готовность — ошибка запуска даже при выключенном UI.

Seed inactive. После однократной activation фиксируется cutoff публикации;
история до него не становится непрочитанной. Facts публикуются атомарно с
исходными alerts/terminal receipts; UI-off capture не прекращает.

Backup/restore должен включать activation, facts, reads, terminal receipts и
их исходные объекты в согласованном snapshot. Проверяются неизменные public
IDs, activation ID/time и первое read_at. Выборочный reset read/cutoff и
разрушительная down migration не поддерживаются. Для rollback старого binary
нужно остановить все затронутые публикации на весь интервал. Отключение UI
не заменяет такую остановку. Рабочие Q1/Q2/consumer switches этим source не меняются.

Service budget5s, statement timeout3s. Ошибка не выдаёт partial page или false
count0. Полный wire contract: [OpenAPI](../api/openapi.json).
