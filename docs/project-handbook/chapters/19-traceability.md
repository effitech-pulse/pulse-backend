# Связь требований макетов кода и проверок

Q1/F2: implemented FileJobs/private projection/review kind `file_check` →
[протокол](../references/q1-file-jobs.md)/[C4](../diagrams/q1-file-jobs.drawio) →
[exact source/binary/runtime proof](../references/q1-runtime.md).
DB/SW + narrow execution_busy source APPROVED тем же Senior; actual runtime/browser
и финальная публикация требуют своих свидетельств. Historical Q0-редакция
113mappings/637public sources относится только к Q0; текущие Q1 publication counts
формирует final authored→generated sync, без переноса прежних чисел.


Q0 связь: [architecture](../references/q0-architecture.md)/[diagram](../diagrams/q0-artifact-pinning.drawio) → source C272f230/Me5cc61b/B2024896 + combined execution afae2c4 (P-SR04) → [RUN-2 runtime proof](../references/q0-runtime.md). P-SR01–03 CLOSED; source/runtime/reproducer ACCEPTED назначенным Senior; final docs/publication acceptance PASS. Proof содержит13PASS и frozen identities/reproducer; historical STOP/FAIL сохранены. В историческом Q0 public async/job/outbox отсутствовали; current Q1 описан выше.
## O2 · implementation/runtime verified; публикационный gate, 20.10.2026

| Требование / владелец | Связь и состояние |
|---|---|
| O2-1…4 / tech-lead + координатор | [Execution](../references/o2-q0-execution.md) → [runtime evidence](../references/o2-runtime.md) → [runbook](../references/observability-o2.md); source/runtime Senior PASS, W1/P2 CLOSED,13phase rollback ID/SHA preserved; final docs/publication PASS |
| Pins/budgets/C4 / architect-analyst | [Архитектура](../references/o2-architecture.md), [редактируемая C4](../diagrams/o2-observability.drawio); local non-HA, никакого production capacity/lossless claim |
| Следующая очередь / tech-lead + architect-analyst | [Roadmap](../references/functional-roadmap.md), [Q0 architecture](../references/q0-architecture.md); после O2 — Q0→Q1/F2, затем прежние зависимости, не installed broker |
| Новый продуктовый блок / tech-lead + architect-analyst | [Proposal](../references/well-operations-proposal.md), [review](../references/well-operations-data-review.md): loading/export Excel запрошены, implementation не назначена; scientific not_assessed |

Авторизованные рамки пилота: PREP PG55439 отдельно от lane test PG55432; Grafana bridge прямо разрешён пользователем с возможным egress, Collector/Loki/Tempo/Prometheus остаются internal, бизнес-сети не менялись. Root runtime evidence завершено, отдельный финальный gate handbook пройден; source inventory public working tree не является commit/push и может учитывать чужие untracked inputs.

| Требование | Макет / описание | Код | Статус и проверка |
|---|---|---|---|
| Общая навигация и обзор | Figma 3:52 | AppLayout, Dashboard, ThemeProvider | Частичный перенос; browser QA Light/Dark |
| Риски и шесть вкладок | Figma F1 696:328/1766; исходный3:53 сохранён | WellDetailPage, ExplainBars, ml API | F1 software accepted; task/whole-wave Senior, browser 7+2/9 и scoped visual/mobile-account PASS |
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
| RabbitMQ / Kafka / Kubernetes | Q1 isolated RabbitMQ реализован; Kafka/Kubernetes — варианты | Default-off synthetic file jobs; штатный PREP не переключён | Q1 proof отдельно; production/HA не приняты |

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
| PREP-03 | Immutable cohort 100, membership∩RBAC, no admin bypass, no global reference writes | [Architecture](../references/readiness-architecture.md), migrations9/11, guards/integration evidence |
| PREP-04 | Local access и nullable cohort UI, peak/latest/value_source, responsive/keyboard/account | Architecture§§13–15; source Senior и browser по отдельным датированным запускам |
| PREP-05 | Bundle-only Upload, общий typed evaluator, explicit legacy/unsupported policies | [RPC matrix](../references/model-rpc-compatibility.md), model-free fixtures; не научная приёмка |
| PREP-06 | Полный immutable output, partial provenance, mutable alert overlay; `running`/`error` identity11 | Migrations10/11, restart/digest/rollback/re-review в ledger; immutable входы не сохраняются |
| PREP-07 | Независимый протокол, validation и evidence без обучения | [Acceptance](../references/independent-acceptance.md), [незаполненный пример](../examples/readiness-protocol.example.json); scientific not_assessed/user-deferred |

Локальные raw evidence logs/скриншоты и private cohort bindings не копируются в публичный комплект. Первичные документы и реестр определяют владельцев; генерация/sync/build/ZIP выполняются координатором после freeze. Финальные cloud jobs не подразумеваются наличием workflow.

## Программная приёмка O1/F1 · 20.10.2026

| Задача / владелец | Реализация и зависимости | Проверка / принятая граница |
|---|---|---|
| O1 / backend-middle через architect-analyst | Go/Python observability modules, реальные HTTP/gRPC/PG spans и safe JSON; Nginx — Go writer, Compose — координатор | ACCEPTED: ML 110/Data 60, Go check/build/real PG 68.351s, Linux builds/pipcheck и runtime 12/12. Source и whole-wave Senior PASS. Нет runtime exporter/stores/брокера |
| F1 / designer, architect-analyst согласование | Шесть вкладок, восемь Figma nodes, URL/history и прежние forms/client header UI | ACCEPTED: tests 37/build, browser 7/7 + mobile controls 2/2/pageErrors 0, scoped visual/mobile-account и whole-wave Senior PASS |
| Приёмка / tech-lead + координатор | O1-01–05/F1-01–05/GATE-01–02 закрыты по fixed source и evidence | [Execution ledger](../references/o1-f1-execution.md), [O1 contract](../references/observability-o1.md), [runtime](../references/o1-f1-runtime.md). PREP refs исторические; provenance partial |

Четыре новые payload refs приведены в ledger; local commit не означает push/remote availability. Handbook publication sync/build/validate/external source check/ZIP выполняет координатор после authored freeze. Успешный live ML scoring/persist, forced rollover и полный assembled parent-ID graph не заявлены. F2/F3/F4/O2/Q/брокер/jobs вне этой приёмки; дальнейшее продолжение по поручению 20.10 и roadmap передаётся отдельно после publication gate. Scientific not_assessed.
