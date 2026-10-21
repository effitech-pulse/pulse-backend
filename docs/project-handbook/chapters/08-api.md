# HTTP API и gRPC контракты


Q0 SDK0.1.2 (**local UNRELEASED**) добавляет отдельный stream-unary `UploadPinned`: header pin/input, затем canonical chunks; registry/policy/recipe/runtime проверяются до features, без legacy fallback. Это внутренний service/RPC seam: production saved-file/async HTTP routes пока отсутствуют. Старые HTTP Upload/period-check ниже сохраняют прежний контракт и не превращаются в202. [RPC semantics](../references/model-rpc-compatibility.md), [Q0 proof](../references/q0-runtime.md); release/remote availability/cloud CI OPEN.
## Источники контракта

[Полный OpenAPI JSON](../contracts/openapi.json) — копия `pulse-backend/api/openapi.json` текущего рабочего дерева. [ml.proto](../contracts/ml.proto) — копия единственного protobuf-контракта из pulse-contracts. Каталог операций ниже сформирован из OpenAPI; реализацию и серверные права определяют также `transport/httpapi/routes.go`, `access.go` и сервисы.

HTTP-префикс по умолчанию `/api/v1`. Браузер использует тот же origin (PREP `http://127.0.0.1:8088`, исходный локальный адрес `:8080`), Nginx проксирует API. В локальном режиме сеанс передаётся cookie `pulse_session`; в legacy может применяться общий `X-API-Key`, если ключ задан. Наличие operation в OpenAPI не предоставляет право вызова любой роли.

## Идентификаторы и типы

Пользовательские well routes принимают bigint ID Pulse. Не подставляйте в них Unitek/Armits/device ID. Для источника отдельно задаётся `well_identity`; преобразование выполняется backend. `null`, отсутствующее поле, `false`, ноль и пустой массив не взаимозаменяемы. int64 сохраняется в protobuf; входной JSON-адаптер использует числовой тип без раннего float-преобразования. JavaScript-клиент не должен предполагать безопасность произвольного bigint за пределами safe integer; это отдельная граница web-контракта.

## Версии и повторные запросы

Для PATCH карточки персональный режим требует `revision`; при архивировании/восстановлении передаются revision и reason. Legacy DELETE выполняет мягкое архивирование; в local требует If-Match. UI не должен автоматически повторять действие с подставленной новой версией при конфликте: нужно показать актуальное состояние и сохранить пользовательский черновик для осознанного решения.

Ручная проверка использует `(well_id,request_id)`. Повтор того же запроса возвращает сохранённый результат, другой период с тем же ключом конфликтует. Решение технолога также имеет request_id и ожидаемую revision. Это не универсальная гарантия exactly-once для любого API.

## Ответы и лимиты

| Код | Смысл |
|---|---|
| 200 / 201 / 204 | Чтение/изменение, создание, успешное действие без тела |
| 401 | Нет действующей аутентификации/ключа |
| 403 | Недостаточно прав или недопустимый origin |
| 404 | Объект отсутствует или недоступен в scope |
| 409 | Конфликт версии, повторного ключа или занятого источника/прогона |
| 422 | Ошибка параметров, периода, сопоставления или структуры файла |
| 429 | Ограничение частоты входа |
| 503 | Недоступность источника/ML |

Ошибки новых маршрутов обычно имеют `{code,message,detail}`; часть legacy сохраняет `{detail}` для совместимости. Backend не возвращает наружу SQL и секреты.

Общее HTTP тело ограничено 64 MiB; Nginx допускает 64m. Внешний JSON-пакет — до 8 MiB и 10000 записей. Чтение телеметрии — до 10000 точек с `truncated`; неполный период нельзя молча отправлять на модель. Импорт телеметрии на сервере — 1–100000 точек. UI чтения файла ограничен 20 MB/100000 строк, а gRPC Upload — 50 MiB суммарно и 1 MiB на чанк. Эти ограничения относятся к разным слоям и не заменяют друг друга.

## Методы MLService

| RPC | Назначение |
|---|---|
| Health | Доступность транспорта; не доказывает качество или готовность всех артефактов |
| GetSnapshot | Текущий контекст оценок; может вычислять холодный контекст |
| Score | Явный пересчёт снимка |
| Explain | Вклад признаков для выбранной скважины |
| History | Риск и срабатывания по окнам |
| Metrics | Метаданные моделей; исследовательские метрики |
| Upload(stream UploadChunk) | Передача файла и оконная проверка |
| ModelHealth | Оценка оснований для пересмотра модели |

Generated SDK: Go в `gen/go/pulse/ml/v1`, Python в `gen/python/pulse/ml/v1`. Изменения схемы проходят Buf lint/breaking, официальную генерацию и тесты обеих сторон. Номера удалённых полей резервируются, несовместимый API получает новый namespace. Contribution.value_source и metadata модели должны сохраняться через всю цепочку.

## Пример ручной проверки

[Пример JSON](../examples/manual-check.json) содержит технический ID/период для адаптации под доступную скважину. Запрос `POST /api/v1/wells/{well_id}/checks` не меняет ярус и обработку тревоги фонда. Успешный ответ сохраняется и открывается по `GET /api/v1/wells/{well_id}/checks/{check_id}`.

## O1: HTTP-корреляция

`X-Request-ID` обозначает конкретную HTTP-попытку, gRPC передаёт его как `x-request-id`. Допустим один ID из 16–64 ASCII alnum/underscore/hyphen; недопустимый или отсутствующий заменяется случайными 32 hex. Nginx всегда создаёт свой ID и возвращает его также на локальной ошибке; direct Go/Data валидируют независимо. В error UI optional `http_request_id` берётся только из безопасного response header и отображается отдельным «Код обращения». Тело ответа и DTO ради O1 не расширяются, network-only ошибка не получает выдуманного ID. Стабильный бизнес `request_id` и fingerprint повторной проверки/review сохраняются при retry. Корреляция не даёт прав и не доказывает provenance. [O1/F1 ledger](../references/o1-f1-execution.md): runtime 12/12 и browser 7+2/9 подтвердили scoped fixtures, whole-wave Senior PASS. Controlled error/409 fixtures не выданы за успешные production writes.

## Каталог операций

Таблица ниже сгенерирована из вложенного OpenAPI-снимка.

| Группа | Метод | Маршрут | Назначение | Ответы |
|---|---|---|---|---|
| health | GET | `/health` | Health | 200 |
| wells | GET | `/api/v1/wells` | List wells | 200,422 |
| wells | POST | `/api/v1/wells` | Create one well manually | 201,422 |
| wells | POST | `/api/v1/wells/import` | Bulk-import wells from an Excel file (NGDU, WELL_NUMBER, ARMITS_ID, UNITEK_ID, CEX) | 200,422 |
| wells | POST | `/api/v1/wells/bulk` | Bulk-create wells from JSON (used by the import preview after editing) | 200,422 |
| wells | GET | `/api/v1/wells/export` | Export all wells to Excel (.xlsx) | 200,422 |
| wells | GET | `/api/v1/wells/{well_id}` | Get a well by id | 200,422 |
| wells | PATCH | `/api/v1/wells/{well_id}` | Update / deactivate a well | 200,422 |
| wells | DELETE | `/api/v1/wells/{well_id}` | Delete a well | 204,422 |
| wells | GET | `/api/v1/wells/{well_id}/predictions` | List recent predictions for a well | 200,422 |
| wells | GET | `/api/v1/wells/{well_id}/predictions/export` | Export a well's predictions to Excel (.xlsx) | 200,422 |
| wells | GET | `/api/v1/wells/{well_id}/complications` | List active complications for a well | 200,422 |
| wells | GET | `/api/v1/wells/{well_id}/classification` | Classify a well by its complications | 200,422 |
| incidents | GET | `/api/v1/wells/{well_id}/incidents` | List failure cases of a well | 200,422 |
| incidents | POST | `/api/v1/wells/{well_id}/incidents` | Add a failure case to a well | 201,422 |
| incidents | DELETE | `/api/v1/incidents/{incident_id}` | Delete a failure case | 204,422 |
| incidents | GET | `/api/v1/incidents/stats` | Aggregate failure stats (by type, by month) for the dashboard | 200,422 |
| departments | GET | `/api/v1/departments` | List departments | 200,422 |
| departments | POST | `/api/v1/departments` | Create a department | 201,422 |
| departments | PUT | `/api/v1/departments/{dep_id}` | Rename a department | 200,422 |
| departments | DELETE | `/api/v1/departments/{dep_id}` | Delete a department | 204,422 |
| ngdu | GET | `/api/v1/ngdu` | List NGDU | 200,422 |
| ngdu | POST | `/api/v1/ngdu` | Create an NGDU | 201,422 |
| ngdu | PUT | `/api/v1/ngdu/{ngdu_id}` | Rename an NGDU | 200,422 |
| ngdu | DELETE | `/api/v1/ngdu/{ngdu_id}` | Delete an NGDU | 204,422 |
| analytics | GET | `/api/v1/analytics/status` | Metabase embedding status | 200,422 |
| analytics | GET | `/api/v1/analytics/dashboards/{dashboard_id}/embed` | Signed iframe URL for an embeddable dashboard | 200,422 |
| run-archive | GET | `/api/v1/runs` | List recent pipeline events | 200,422 |
| run-archive | GET | `/api/v1/runs/export` | Export pipeline events to Excel (.xlsx) | 200,422 |
| run-archive | GET | `/api/v1/runs/{dag_id}/{run_id}` | List events for a specific DAG run | 200,422 |
| parameters | GET | `/api/v1/parameters` | Parameter dictionary | 200,422 |
| audit | GET | `/api/v1/audit` | Recent user actions (last week) | 200,422 |
| ml | POST | `/api/v1/ml/wells/refresh` | Ml Refresh | 200,422 |
| ml | GET | `/api/v1/ml/runs` | Ml Runs | 200,422 |
| ml | PATCH | `/api/v1/ml/alerts/{alert_id}` | Ml Alert Status | 200,422 |
| ml | GET | `/api/v1/ml/wells` | Ml Wells | 200,422 |
| ml | GET | `/api/v1/ml/wells/by-unitek/{unitek_id}` | Ml Well By Unitek | 200,422 |
| ml | GET | `/api/v1/ml/wells/{well_id}/explain` | Ml Explain | 200,422 |
| ml | GET | `/api/v1/ml/wells/{well_id}/history` | Ml Well History | 200,422 |
| ml | GET | `/api/v1/ml/health` | Ml Health | 200,422 |
| ml | GET | `/api/v1/ml/metrics` | Ml Metrics | 200,422 |
| ml | POST | `/api/v1/ml/upload` | Ml Upload | 200,422 |
| telemetry | GET | `/api/v1/integrations` | Список источников | 200,422,503 |
| telemetry | POST | `/api/v1/integrations` | Создать черновик | 200,422,503 |
| telemetry | POST | `/api/v1/integrations/check` | Проверить источник | 200,422,503 |
| telemetry | GET | `/api/v1/integrations/logs` | Журнал сбора | 200,422,503 |
| telemetry | PUT | `/api/v1/integrations/{source_id}` | Сохранить настройки и версию профиля | 200,422,503 |
| telemetry | PATCH | `/api/v1/integrations/{source_id}/status` | Запустить или приостановить | 200,422,503 |
| telemetry | POST | `/api/v1/integrations/{source_id}/collect` | Собрать сейчас | 200,422,503 |
| telemetry | POST | `/api/v1/integrations/{source_id}/ingest` | Принять JSON источника | 200,422,503 |
| telemetry | GET | `/api/v1/telemetry` | Измерения за период | 200,422,503 |
| telemetry | POST | `/api/v1/telemetry` | Подтвердить импорт | 200,422,503 |
| telemetry | POST | `/api/v1/telemetry/score` | Проверить период моделью | 200,422,503 |
| Общее | GET | `/api/v1/telemetry/bounds` | Границы доступной истории телеметрии | 200 |
| Pulse wells | GET | `/api/v1/wells/{well_id}/telemetry` | Телеметрия по внутреннему ID Pulse | 200 |
| Pulse wells | GET | `/api/v1/wells/{well_id}/telemetry/bounds` | Границы телеметрии по ID Pulse | 200 |
| telemetry | POST | `/api/v1/wells/{well_id}/telemetry/score` | Расчёт модели по ID Pulse | 200,422,503 |
| telemetry | POST | `/api/v1/telemetry/import` | Импорт измерений с ID Pulse в points[].well_id | 200,422,503 |
| ml | GET | `/api/v1/wells/{well_id}/risk` | Результат модели по внутреннему ID Pulse | 200,422 |
| ml | GET | `/api/v1/wells/{well_id}/risk/history` | Результат модели по внутреннему ID Pulse | 200,422 |
| telemetry | GET | `/api/v1/wells/{well_id}/checks` | История ручных проверок скважины по ID Pulse | 200,404 |
| telemetry | POST | `/api/v1/wells/{well_id}/checks` | Выполнить и сохранить ручную проверку | 200,409,422,503 |
| telemetry | GET | `/api/v1/wells/{well_id}/checks/{check_id}` | Открыть сохранённый результат без повторного расчёта | 200,404 |
| reviews | GET | `/api/v1/wells/{well_id}/checks/{check_id}/review` | Состояние обработки и история изменений | 200,404 |
| reviews | PUT | `/api/v1/wells/{well_id}/checks/{check_id}/review` | Сохранить ответственного, статус, комментарий и заключение | 200,404,409,422 |
| reviews | GET | `/api/v1/ml/alerts/{alert_id}/review` | Состояние обработки и история изменений | 200,404 |
| reviews | PUT | `/api/v1/ml/alerts/{alert_id}/review` | Сохранить ответственного, статус, комментарий и заключение | 200,404,409,422 |
| presentation | GET | `/api/v1/presentation/summary` | Сводка ручных проверок показательного фонда | 200,503 |
| presentation | GET | `/api/v1/presentation/scenario` | Конфигурация сценария Pulse Data | 200,503 |
| access | GET | `/api/v1/auth/me` | Режим входа и текущий пользователь (без сеанса principal=null) | 200,401,403,409,422,429,503 |
| access | POST | `/api/v1/auth/login` | Локальный вход; выдаёт защищённый cookie сеанса | 200,401,403,409,422,429,503 |
| access | POST | `/api/v1/auth/logout` | Завершить текущий сеанс | 204,401,403,409,422,429,503 |
| access | GET | `/api/v1/auth/users` | Учётные записи (администратор) | 200,401,403,409,422,429,503 |
| access | POST | `/api/v1/auth/users` | Создать учётную запись (администратор) | 201,401,403,409,422,429,503 |
| access | PUT | `/api/v1/auth/users/{user_id}` | Изменить учётную запись и отозвать сеансы (администратор) | 200,401,403,409,422,429,503 |
| wells | POST | `/api/v1/wells/{well_id}/archive` | Архивировать скважину | 200,409,403,422 |
| wells | POST | `/api/v1/wells/{well_id}/restore` | Восстановить скважину | 200,409,403,422 |
| wells | GET | `/api/v1/wells/{well_id}/history` | История карточки в разрешённой области доступа | 200 |

## Дополнение PREP: идентичность и typed evidence

Cohort routes принимают ID Pulse и выбранный сохранённый run, а не research model-ID. `profile/cohort_version` принадлежат попытке уже в `running`/`error`; unknown старых записей не заполняется текущими значениями. Typed provenance/decision_rule и raw/watch/high evidence добавлены в SDK без смены прежних номеров полей. Точная матрица legacy_score_v0 / legacy_upload_v0 / causal_v1 и guard unsupported contexts — в [RPC compatibility](../references/model-rpc-compatibility.md).

`alert_id/alert_status` у outcome — nullable read overlay конкретной строки выбранного run; не выдумывается `new` при отсутствии alert. Его изменение не меняет immutable facts/digest. Связь explanation peak/latest и value_source сохраняется до UI. Полный output snapshot не обещает наличие immutable входных bytes; provenance остаётся partial.
