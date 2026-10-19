# Связь требований макетов кода и проверок

| Требование | Макет / описание | Код | Статус и проверка |
|---|---|---|---|
| Общая навигация и обзор | Figma 3:52 | AppLayout, Dashboard, ThemeProvider | Частичный перенос; browser QA Light/Dark |
| Риски скважины | Figma 3:53 | WellDetailPage, ExplainBars, ml API | Есть; новый порядок вкладок ещё требуется |
| Несколько каналов | Figma 363:536, 363:784 | TelemetryWorkspace/TelemetryChart | Есть; нули/пропуски и относительная база тестируются |
| Сравнение A/B | Figma 363:1023 | TelemetryWorkspace, lib/telemetry | Есть; короткий период не растягивается |
| Таблица измерений | Figma 363:1291 | TelemetryWorkspace | Есть; качество и пропуски |
| CSV/XLSX/Parquet | Figma 363:1579, 363:1826 | ImportTelemetry, telemetryFiles, integrations service | Есть; единая точка рабочего сценария ещё требуется |
| Источники и mapping | Figma 337:4169, 337:4585 | IntegrationsPage, Connector, MapTelemetry | Есть; роли/allowlist/гигиена |
| Интервалы сбора | Figma 337:4754 | Integrations.Schedule, ClaimSource/CommitTelemetry | Есть; актуальный watermark защищён регрессией |
| ID приложения | Бизнес-решение | well_identity.sql, Source.well_identity, PulseTelemetryID | Есть; внешние 0/null и повтор импорта проверены |
| Сохранённая проверка | Форма/результат проверки | Checks, manual_check, model_identity | Есть; request_id/период/версия/хеш |
| Решение технолога | Figma 19:1595, 19:1641 | Reviews, case_review/review_event, ReviewPanel | Есть; серверный actor и конфликт версии |
| Роли/вход | Figma 42:74 | Access, auth tables, AuthProvider | Local/cohort принят в isolated PREP; исходный8080 не переключался |
| Фильтры и сортировка | Figma 44:421 | FundFilters, WellFilter, export | Fixed membership100 включён; scope/filter подмножества и Excel проверены |
| Архив/восстановление | Figma 44:921, 44:998 | well_lifecycle, WellHistoryPanel, WellsPage | Есть; атомарность/история/конфликты проверены |
| Предметный журнал | Figma 19:1694, 19:1754 | Сейчас AuditPage/action_log + отдельные истории | Частично; требуется единая лента событий |
| Новые тревоги/ошибки сбора | Figma 33:1004/1178 | WorkspaceEvents | Только текущая очередь; доставка/прочтение не реализованы |
| Почтовые рассылки | Figma 43:939–1693 | Старые DDL не равны реализации | Не завершено |
| Статистика | Figma 44:176 | IncidentStats, Dashboard; опциональный Metabase | Частично; полная эксплуатационная статистика не завершена |
| Единый Pulse Connector /100 | Последнее решение пользователя | Текущий fund_type operational/presentation | PREP cohort/capture/guards/Upload/output реализованы; naming/прочие UX задачи не подменяют технический статус |
| SSO | Целевая архитектура | auth_identity, service identity adapter | Основа есть; OIDC не подключён |
| RabbitMQ/Kafka/Kubernetes | Архитектурные варианты | Нет подключённой подсистемы | План, не текущий runtime |

Номера Figma — идентификаторы узлов в общем файле, прямые ссылки перечислены в каталоге. Таблица связывает области кода; она не заменяет проверку полного пользовательского сценария и визуальную приёмку каждого состояния.


## Документационная очередь A — 19.10.2026

Источник требований — датированный audit pulse-backend/docs/documentation-audit-2026-10-19.md; приёмка и результаты — отдельный execution log pulse-backend/docs/documentation-queue-a-execution-2026-10-19.md вне этого комплекта. Пользователь одобрил DOC-01–DOC-07; они приняты. Последующий PREP — отдельное согласованное поручение, не повтор той очереди и не выполнение всей продуктовой очереди B. Исторические path:line аудита сохраняются, актуальное устранение связывается с ID DOC в журнале.

| Задача | Источник / исправляемая область | Свидетельство в этом комплекте |
|---|---|---|
| DOC-01 | Немаркированные заявления acceptance/ROADMAP/презентации и генераторов | Причинная глава 10; source inventory фиксирует обновлённые исходники; научная приёмка не объявлена |
| DOC-02 | Модульные AGENTS, архивные фазы, frontend DESIGN/HANDOFF и Figma | [Frontend handoff](../references/frontend-handoff.md), [дизайн](../references/frontend-design.md), source inventory |
| DOC-03 | Local/legacy/ML-key, конфигурация/статусы и probe | Главы 12/14/15 и [ML README](../references/ml-service-readme.md); тесты доступа отражаются в матрице |
| DOC-04 | Семантика RPC/версий и provenance | [Спецификация моделей/RPC](../references/model-rpc-compatibility.md): current и target разделены |
| DOC-05 | Разовая проверка против реально включённого CI gate | [Матрица проверок](../references/verification-matrix.md); cloud CI не подменяется локальным PASS |
| DOC-06 | Лимиты/fallback/native/release | [ML README](../references/ml-service-readme.md), runtime constraints/source inventory |
| DOC-07 | Владельцы, snapshots, source drift и автономная публикация | [Source policy](../SOURCE_POLICY.md), snapshot registry; build/validate и отдельный workspace check |

Таблица описывает связи, а не объявляет каждый DoD выполненным: окончательный статус и границы проверки находятся в execution log. [Политика публикации](../SOURCE_POLICY.md) объясняет, как сопоставить source paths и автономные копии.

## Дополнение PREP · 20.10.2026

| Требование | Реализация / review seam | Автономное свидетельство |
|---|---|---|
| PREP-01 | Семь exact source refs, локальное сохранение без push | [Compatibility manifest](../inventory/readiness-compatibility.json), source inventory; локальный commit не доказывает cloud/release |
| PREP-02 | Native/Linux/PG/runtime и после-review browser | [Ledger](../references/readiness-ledger.md), [playbook](../references/readiness-runtime.md), глава16;22/22 bounded browser PASS |
| PREP-03 | Immutable cohort100, membership∩RBAC, no admin bypass, no global reference writes | [Architecture](../references/readiness-architecture.md), migrations9/11, guards/integration evidence |
| PREP-04 | Local access и nullable cohort UI, peak/latest/value_source, responsive/keyboard/account | Architecture§§13–15; source Senior и browser по отдельным датированным запускам |
| PREP-05 | Bundle-only Upload, общий typed evaluator, explicit legacy/unsupported policies | [RPC matrix](../references/model-rpc-compatibility.md), model-free fixtures; не научная приёмка |
| PREP-06 | Полный immutable output, partial provenance, mutable alert overlay; `running`/`error` identity11 | Migrations10/11, restart/digest/rollback/re-review в ledger; immutable входы не сохраняются |
| PREP-07 | Независимый протокол, validation и evidence без обучения | [Acceptance](../references/independent-acceptance.md), [незаполненный пример](../examples/readiness-protocol.example.json); scientific not_assessed/user-deferred |

Локальные raw evidence logs/скриншоты и private cohort bindings не копируются в публичный комплект. Первичные документы и реестр определяют владельцев; генерация/sync/build/ZIP выполняются координатором после freeze. Финальные cloud jobs не подразумеваются наличием workflow.
