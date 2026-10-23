> Снимок документа 23.10.2026. Источник: `tasks/pulse-backend/docs/o1-f1-execution-2026-10-20.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# Исполнение O1 ∥ F1 — 20.10.2026

**Пользователь одобрил реализацию O1 и F1 параллельно через команду, с Senior-ревью и проверками перед приёмкой.** Дополнительного одобрения технических шагов внутри этой области не требуется. Текущий статус: **O1/F1 приняты как программное изменение в согласованной области**. Три source Senior и итоговый whole-wave Senior: Spec/Standards PASS, findings 0; 71/71 reviewed hashes MATCH. Runtime 12/12, browser 7+2/9 и scoped visual PASS. Приёмка учитывает явно перечисленные ниже границы evidence; научный gate not_assessed. Публикационная синхронизация handbook остаётся отдельным следующим шагом координатора.

Основания: [общий roadmap](functional-roadmap.md), O1 контракт архитектора (исходник `pulse-backend/docs/observability-messaging-proposal-2026-10-20.md`; в пакет не включён), принятые [PREP](readiness-ledger.md)/[payload refs](../inventory/readiness-compatibility.json). Этот журнал связывает требования, владельцев, fixed diff и доказательства; фактический baseline новой фазы и toolchain фиксирует координатор. Три существующих planning-артефакта сохраняются, чужие изменения/удаления не откатываются.

## Разрешённая область и владельцы

| Поток | Владелец реализации / согласования | Область |
|---|---|---|
| O1 | backend-middle, назначенный architect-analyst; дизайнер — только frontend error/correlation UI по контракту | Общая JSON schema/санитизация Go API и Python ML/Data, HTTP/gRPC spans/context, JSON Nginx access и bounded local logs; необходимые SDK pins/tests/config/docs в соответствующих модулях |
| F1 | designer с обязательным согласованием architect-analyst | Шесть вкладок карточки, существующие controls/data/state, совместимые ссылки/навигация, Figma3:53 и необходимые state variants, DESIGN/HANDOFF/сценарии |
| Контракты/владение | architect-analyst → координатор | До записи закрепляет точные interfaces и одного writer каждого файла; особенно frontend api/client/error UI, nginx.conf, SDK/packaging/Compose. Роль в таблице не разрешает двум исполнителям писать один файл |
| Независимая проверка | backend-senior | Frozen diff/manifest, standards/spec/privacy/recovery regressions и scoped повторная проверка исправлений |
| Приёмка | tech-lead + координатор | Проверяемые пользовательские outcomes, документы, результаты проверок/ограничения; ledger обновляется после evidence |

**Вне этой реализации:** O2 Collector/Loki/Tempo/Prometheus/Grafana и централизованное хранение, Q0/Q1/RabbitMQ/Kafka/jobs/input store, F2 новый файл/saved check, F3 журнал, F4 read-state, Q2 перенос scheduler/bulk, новые модели/обучение/promotion, SSO/удалённый TLS/HA/Kubernetes. Не переносим предположения об их готовности в O1/F1. Model/contracts остаются библиотеками; для них не создаются новые runtime-сервисы.

## Binding O1: общий контекст без изменения бизнес-результата

1. Один JSON record на событие. Точные общие ключи: `timestamp` (UTC RFC3339), `severity_text` (`DEBUG/INFO/WARN/ERROR`), `event_name`, `service.name`, `service.version`, `service.instance.id`, `deployment.environment.name`; при активном контексте — **http_request_id/trace_id/span_id**. Optional typed fields: `duration_ms`, `http.request.method`, статический `http.route`, `http.response.status_code`, `rpc.method`, `rpc.grpc.status_code`, статический `error_code`, разрешённый `operation_digest`. Dynamic message/error/stack не обязательны и не сериализуются; допустим только фиксированный safe message catalog. Record≤8192 UTF-8 bytes, resource≤128, event/error code≤64 safe ASCII, прочие разрешённые строки≤256; optional attrs удаляются с пересборкой валидного JSON. Debug выключен. Nginx access JSON использует `duration_s` в секундах, не генерирует trace/span IDs. Native request error stream отключён (`error_log /dev/null crit`); launcher/config errors до загрузки конфигурации остаются native, а не обещанным JSON.
2. **http_request_id — конкретная HTTP-попытка**, возвращается только безопасным response header `X-Request-ID`. UI переносит его в локальный optional `ApiError.http_request_id` и отдельную строку «Код обращения»; error body/DTO ради O1 не расширяется. Existing DTO **request_id — стабильный бизнес-ключ идемпотентности** Checks: не переименовывается, не заменяется случайным HTTP ID, при retry операции сохраняется. Для логов — отдельная разрешённая operation reference/digest вместо неконтролируемого raw key. Trace/correlation не дают прав и не доказывают provenance.
3. HTTP `X-Request-ID` / gRPC `x-request-id`: единственное входное значение `^[A-Za-z0-9_-]{16,64}$`, иначе новые случайные 32 lowercase hex. Nginx всегда генерирует свой ID, пересылает API, скрывает upstream duplicate и возвращает свой ID `always`; direct Go/Data валидируют независимо. CORS разрешает `X-Request-ID,traceparent` и открывает браузеру response `X-Request-ID` с прежней origin/credentials policy. `traceparent` — одно значение≤512 bytes без control chars, официальная W3C-валидация. W3C propagation/реальные SDK spans на HTTP API, исходящем Data HTTP и gRPC ML/Python paths. Некорректный/слишком длинный/untrusted context заменяется безопасным новым; исходный мусор не попадает в лог. Baggage/tracestate по умолчанию не распространяются без явного allowlist. Sample flag клиента не определяет права/budget. Headers/metadata передаются адресно, auth metadata не логируется целиком.
4. Реальный period check: API→scope→PG telemetry→canonical CSV→ML Upload RPC→result persist. Data HTTP span появляется **только при реальном вызове** Archive/ReadApp с binding/BaseURL; либо отдельно проверяется collection Go→Data/source HTTP→PG. Не рисовать Data span для PG-only пути. Detached bulk/collection сохраняют связь с инициатором без привязки жизненного цикла к HTTP cancellation; scheduler имеет собственный operation context. Алгоритмы scoring/collection/snapshot и текущие deadlines не меняются.
5. Source-level allowlist/redaction: нет password/cookie/token/Authorization/x-ml-api-key/DSN credentials, raw query strings/SQL params, filenames/paths/file bytes, telemetry values, реальных external well IDs/номеров, свободных review comments и private exception text. SQL spans — безопасная операция/template без bind values; gRPC — method/status без payload/chunks. Public errors остаются безопасными, correlation header/JSON extension не ломает существующие plain/text или JSON response contracts.
6. Core OTel API/trace SDK **1.38.0**: реальный provider на старте executable, локальный sampler 100%, limits attributes 32/events 16/links 8/string 256. Runtime без сетевого exporter, batch queue и retention list; in-memory exporter существует только в тестах. Go и Python скрывают контекст/санитизацию в небольшом observability module каждого executable, без зависимости Model→Data. Python pins API/SDK 1.38.0 + SDK semconv 0.59b0; существующий numerical constraints-профиль требует Python≥3.12 из-за NumPy 2.5.1, O1 не заявляет устранение этого ограничения при declared Python≥3.11. O1 не требует внешнего Collector: actual spans проверяются test exporter и контекстом логов. Не устанавливаются stores/брокер и не обещается центральный поиск. Formatter/failing sink не ломают login/check/persist; сетевого exporter в O1 нет. Root-owned isolated Compose задаёт Docker `local`, `max-size=10m`, `max-file=3` на runtime-container. Фактическая конфигурация/rotation проверяется координатором; direct CLI stdout сам не ротируется, durability/losslessness и отсутствие OS-blocking не обещаются. Нет публичного endpoint выгрузки приватных логов. Результат, null/0/false/int64, scope, idempotency и historical saved responses сохраняются.

## Binding F1: карточка существующего результата

Порядок labels: **Риски → Телеметрия → Проверки → Отказы → История → Сведения**. Сведения отделяются от риска; контекст скважины/ID Pulse остаётся доступным. Все существующие данные, формы/действия, review/incident/lifecycle semantics сохраняются. «История» в F1 — существующая история карточки; это не реализация общего предметного журнала F3. Текущие сведения паспорта не называются историческими данными выбранного ML run.

Canonical keys закреплены: `overview`, `telemetry`, `checks`, `failures`, `history`, новый `details`. Отсутствующий/неизвестный key выбирает Риски без потери URL; **старый overview открывает Риски**, старые failures/history/checks/telemetry links работают; новый label не означает удаление старого URL. `run_id`, `check` и посторонние поддержанные query-параметры не теряются при смене вкладки. `?tab=checks&check=<id>` открывает тот же сохранённый check; выбранный run не заменяется latest. Ошибка/нет доступа/нет snapshot не запускает скрытый fallback или новый расчёт.

Табы/Back/Forward воспроизводят выбранные вкладку, run/check; reload/direct URL дают тот же результат. Null/insufficient/archived/inactive не становятся нулём/нормой и не показывают выдуманный график/SHAP. Известные peak/latest/value_source сохраняются. Admin не обходит cohort, viewer не получает новые write права; logout/новый аккаунт не наследует старые данные.

Figma источник — [карточка3:53](https://www.figma.com/design/km4tIbp3LnMTRrV6yi6Qho?node-id=3-53). Исходные Light `3:53` и Dark `136:470` сохранены. Designer передал восемь versioned F1 nodes: Риски `696:328`/`696:1766`, Сведения `696:528`/`696:2040`, мобильная строка 390 `696:713`/`696:2299`, отдельный support ID `696:733`/`696:2319` (Light/Dark). DESIGN/HANDOFF фиксируют соответствие существующим loading/error/empty/forbidden/null components; макеты не служат источником данных. Их existence/source review не заменяет assembled browser/visual QA. Live Figma не меняет tech-lead; вне F1 frames не перерабатываются.

## Матрица программной приёмки

**O1-01–05, F1-01–05, GATE-01–02 — ACCEPTED в доказанной области ниже.** Приняты требования к изменённому программному поведению, совместимости и локальной диагностике; это не обещание всех возможных end-to-end или промышленных сценариев. Детальные исходные bindings выше не изменены. Ограничения не заменены словом PASS.

| ID | Проверяемый исход | Владелец проверки | Полученное evidence и граница приёмки |
|---|---|---|---|
| O1-01 | Единообразный JSON Go/ML/Data, Nginx access и отдельная граница error stream | O1 исполнители; Senior | ACCEPTED: реальные SDK/logging и schema/size/privacy tests; captured runtime JSON, UTC/identity и Docker local 10m/3 config подтверждены. Forced rollover не выполнялся |
| O1-02 | Контекст реальных HTTP/gRPC/PG операций, отсутствие фиктивного Data leg | O1 исполнители + координатор; Senior | ACCEPTED: SDK exporter/listener tests подтверждают parent/link IDs и lifecycle; real PG integration подтверждает read/save/link. Assembled Go→ML invalid CSV422 и Go→Data unsaved connection check 503 подтверждают фактические вызовы/common trace+attempt ID без записи. Полный assembled parent-ID graph, successful mapped ingest и live successful ML→PG одним прогоном не заявлены |
| O1-03 | Safe context и неизменный business request_id | O1 исполнители; Senior | ACCEPTED: missing/invalid/duplicate/oversize/control fixtures, CORS/auth/errors/direct ingress, Nginx override; real PG idempotency/replay и browser controlled409 retry сохраняют бизнес-ключ. grpc C-core boundary описана ниже |
| O1-04 | Source allowlist не сериализует секреты/данные | O1 исполнители; Senior | ACCEPTED: canary tests всех error/log/span seams и runtime canary absence в public error/captured logs; safe third-party fallback и panic/sink tests. Это не глобальный аудит всех исторических public error messages |
| O1-05 | No-network SDK и bounded diagnostics независимы от бизнес-результата | O1 исполнители + координатор | ACCEPTED: SDK без exporter/queue/retention, failing sink/formatter и detached context/link/deadline tests, login/runtime и PG persistence regressions PASS. Подтверждена Docker LogConfig, не stress/lossless/durable stdout |
| F1-01 | Шесть вкладок, сведения отделены, controls/data сохранены | designer + архитектор | ACCEPTED: восемь versioned Figma nodes, source tests/Senior, scoped screenshot composition/state visual PASS. Полное pixel parity всей оболочки вне F1 не заявлено |
| F1-02 | Legacy/default/unknown tabs, run/check/query/hash/history/reload | designer + координатор; Senior | ACCEPTED: actual API browser saved 30/32/check, Back/Forward/reload и extras сохранены; новых ML writes 0, hidden recompute/latest fallback нет |
| F1-03 | Честные null/lifecycle/error/forbidden и peak/latest/value_source | designer + координатор; Senior | ACCEPTED: unit fixtures, actual archived/inactive saved run 32 и controlled error fixtures; отсутствие выдуманных graph/SHAP/нулевого риска. Новый backend outcome ради теста не создавался |
| F1-04 | Роли, logout/cache, существующие actions/draft/idempotency | designer + координатор | ACCEPTED: actual viewer/restricted/logout, mobile technologist/viewer controls probe 2/2; controlled409 сохраняет draft/ключ без успешной записи. Backend write guards/persist — real PG integration со scorer seam, не новая production mutation |
| F1-05 | Light/Dark 1440/390, mounted resize/keyboard/focus/local scroll/account | designer + координатор | ACCEPTED: root browser 7/7 и extra controls 2/2, pageErrors 0; visual/mobile-account PASS, screenshot geometry. Полный WCAG/новые 1280/1024 прогоны не заявлены |
| GATE-01 | Модульные/PG проверки, pins/build/install/Linux соответствуют diff | Владельцы модулей + координатор | ACCEPTED: Go make check/race/vet/build, isolated PG integration 68.351s; F1 tests 37/build на Docker Node 22; ML 110/Data 60 без SKIP, wheels installed вне source cwd и pipcheck; четыре Linux image builds и ML/Data Linux pipcheck. Proto/contracts/ML алгоритмы не менялись |
| GATE-02 | Независимое review и согласованная документация/границы | backend-senior → TL/координатор | ACCEPTED: Go34/Python12/F1eight source reviews и whole-wave 71/71 MATCH, Spec/Standards PASS, findings 0; visual 8/8 MATCH. Три новых registry mappings приняты Senior; финальные authored docs закрыты этим журналом. Sync/HTML/ZIP публикация ещё не заявлены |

## Итоговые свидетельства и пределы

Первичные отчёты/логи/fixed manifests находятся у координатора в `/private/tmp/pulse-o1-f1-20261020/`; приватные raw records/credentials и временные отчёты не включаются в автономный handbook. Долговечные инструкции: [O1 contract/runbook](observability-o1.md), [O1/F1 runtime](o1-f1-runtime.md), frontend `DESIGN.md`/`design/HANDOFF.md`, ML/Data README. [PREP payload manifest](../inventory/readiness-compatibility.json) остаётся историческим PREP snapshot и не подменяется новой реализацией. Immutable output сохраняется; provenance остаётся partial, поскольку immutable input bytes/replay не добавлены.

| Источник evidence | Подтверждено |
|---|---|
| Go/Python/F1 handoffs и task Senior reports | Source freeze34/12/8, три Spec/Standards PASS; Go independent race probes sinkpanic/remote sampling PASS; Python C-core boundary независимо воспроизведена |
| `final-senior-report.md` + addendum | Whole-wave fixed snapshot 71/71 MATCH, Spec/Standards PASS, findings 0; три registry mappings reviewed. Дополнение приняло новые actual Go→Data context/error checks без source change |
| `runtime-smoke-public.json` | **12/12 PASS**, actual isolated8088/8018 без response mocks. Nginx override/auth401, login, direct Go valid/invalid context, historical saved read, actual Go→ML Upload 422/no-persist, Data200/501 и реальный Go→Data unsaved connection check 503. Обе цепочки имеют common trace/attempt IDs; canary отсутствует в captured logs и public error |
| `runtime-images-logging.json` | Exact four image IDs; все четыре runtime-container `local`, `max-size=10m`, `max-file=3`. Frontend/ML/Data Dockerhealthy; backend без Docker Healthcheck, HTTPhealth 200. Это config inspection, не forced rollover/load/durability |
| `f1-browser/results.json` | **7/7 PASS**, fail 0/pageErrors 0: saved 30/32/check, шесть tabs/URL/history/reload, Light/Dark 1440→390→1440, null archived/inactive, роли/logout, локальный scroll/focus. Header/network и409 fixtures явно controlled; успешных writes 0 |
| `f1-visual-qa-report.md` и mobile artifacts | Scoped Figma composition/state visual PASS,8/8 source hashes MATCH; account keyboard/logout/relogin и narrow geometry PASS. Дополнительный controls probe **2/2 PASS**, pageErrors 0: technologist write control, viewer/no admin link, mobile nav keyboard/inert |
| `tl-handbook-tools-final.log` | 16/16 PASS: actual source index scratch exclusion и registry rejection до partial write, плюс прежние fixture regressions. Production sync/build не заменены fixture tests |

HTTP span заканчивается с ответом; detached check/bulk создаёт связанный operation root с прежним lifecycle/principal/deadline, без удержания Gin context. Upload CLIENT заканчивается после CloseAndRecv/terminal error; Python SERVER охватывает consumption/result, без span на chunk. Cancellation не обещает прерывание уже выполняющегося pandas/CatBoost. PG spans — реальные store calls, не driver-level SQL trace.

Runtime Go→Data проверен через POST `/integrations/check` с **несохранённым** json_pull endpoint `http://data:8092/health`: health object не является records, поэтому сохранён штатный503; source save/import не выполнялись. Это доказывает реальный context/error path, но не успешный ingest или полный assembled parent-ID graph. Последний подтверждается по компонентам SDK exporter/listener tests. Successful live ML scoring/persist одним assembled прогоном не выполнялся; real PG persistence/idempotency проверена с test scorer seam. Эти границы приняты для O1/F1 и не превращены в научную приёмку.

## Передача и DoD

| Этап | Итог / владелец следующего шага |
|---|---|
| Одобрение и назначение | Получено O1 ∥ F1; architecture interfaces/sole-writer ownership закреплены до реализации. Нового одобрения внутри области не требовалось |
| Implementation / проверки | Три frozen source handoffs и четыре Linux images; изолированные UI/API/ML/Data заменены без рабочего8080, новых миграций и successful product writes |
| Task reviews / whole-wave / visual | **PASS**, findings 0; целевые interface/privacy/lifecycle/roles и visual/mobile gaps закрыты указанным evidence, implementation source неизменен |
| Software DoD O1/F1 | **ACCEPTED**: O1-01–05/F1-01–05/GATE-01–02 выполнены в явно принятой доказанной области |
| Документация и публикация | Авторские ledger/handbook закрыты; **следующий обязательный gate root** — actual sync → build → validate → external source check → extracted ZIP validate, затем final source/docs/registry hashes. До него HTML/ZIP/source-sync PASS не заявлены |
| Отложенное | Scientific **not_assessed / user-deferred**; O2/Q/broker/jobs/F2/F3/F4 и остальные очереди не входят в эту программную приёмку. Следующее исполнение разрешено отдельной передачей пользователя ниже; SSO/TLS/HA/Kubernetes/SLA и scientific readiness не предполагаются |

## Использованные скиллы

- **to-spec** — `/Users/daniilkoreskov/.codex/skills/to-spec/SKILL.md`: одобренные O1/F1 переведены в bindings и observable outcomes; отделены correlation HTTP attempt от business idempotency, фактический Data leg от фиктивного span, существующая история от F3. Scope исключает незаказанные stores/broker/jobs.
- **to-tickets** — `/Users/daniilkoreskov/.codex/skills/to-tickets/SKILL.md`: O1/F1 независимы; shared frontend/Nginx files имеют предварительный ownership gate, matrix назначает владельца и evidence для каждого исхода. Публикация задач наружу не выполняется по выбранному модульному ledger.
- **writing-plans** — `/Users/daniilkoreskov/PycharmProjects/grant/.agents/skills/writing-plans/SKILL.md`: определены небольшие проверяемые gates, совместимость и negative/review focus до кода. Детальные interfaces/files/RED-GREEN steps назначает архитектор исполнителям; уже данное пользовательское одобрение сохранено, повторный выбор execution mode не запрашивается.


### Навыки сверки и закрытия техлида

- **doc-coauthoring** — `/Users/daniilkoreskov/.codex/skills/doc-coauthoring/SKILL.md`: surgical edits существующих глав и читательская сверка historical/current/target; промежуточные OPEN заменены финальными scoped outcomes. Влияние: шесть вкладок/точная schema и новые доказательства связаны с требованиями без перепланирования.
- **test-driven-development** — `/Users/daniilkoreskov/PycharmProjects/grant/.agents/skills/test-driven-development/SKILL.md` и `writing-good-tests.md`: два fixture RED до `.superpowers` filter, затем полная suite 16 GREEN. Влияние: временный SDDscratch исключён из public inventory и explicit registry до partial writes.
- **verification-before-completion** — `/Users/daniilkoreskov/PycharmProjects/grant/.agents/skills/verification-before-completion/SKILL.md`: прочитаны финальные Senior/runtime/browser/visual outputs, hashes и ссылки; component/runtime/controlled/scientific/publication limits сохранены отдельно. Влияние: software acceptance не выдана за scientific, forced rollover или уже готовую HTML/ZIP публикацию.

## Решения координатора и их цена

Это перенос четырёх execution rulings из временного SDD progress в долговечный ledger; исходные bindings выше не меняются.

| Решение | Основание | Цена / граница при ошибке |
|---|---|---|
| Существующие split checkouts, отдельные codex/o1-f1 branches, baselines/hashes и sole-writer ownership | Native managed worktree tool указывает на исторический grant root, который не содержит актуальные семь модулей | Возможные несвязанные конкурентные правки потребуется согласовать по сохранённым hashes; текущие файлы не отбрасываются |
| Разрешён единственный service import internal/observability для detached operation context | Согласованная архитектура требует safe seed/link без Gin Context и новых adapters | При ошибке seam потребуется service-owned safe context abstraction; остальная слоистость не ослаблена |
| grpc1.78 C-core repeated traceparent boundary принят на доступной приложению границе | C-core нормализует raw duplicates до Python hooks; приложение валидирует видимые значения, Go отправляет одно поле; custom HTTP2 replacement вне задачи | Diagnostic parent может быть выбран из нормализованного duplicate вместо отклонения. Trace не auth/provenance/budget, erased wire multiplicity не обещана обнаруженной |
| Go SDK 1.38 resource env isolation — только startup clear/restore OTEL_RESOURCE_ATTRIBUTES/OTEL_SERVICE_NAME под mutex | SDK WithResource объединяет arbitrary resource.Environment без публичного bypass; source allowlist обязательна. Safe failure/canary/restoration tests и Senior PASS | Будущий независимый параллельный SDK initializer мог бы увидеть временно очищенные env. Поэтому операция до config/bootstrap/goroutines/serving; mutex не разрешает runtime reinit |

Диагностические ошибки harness сохранены как evidence, а не product defects: sandbox bind/Chrome launch проверялись отдельно; cohort Health намеренно local, проверку actual RPC перенесли на invalid Upload 422; Go→Data ожидание500 исправлено на штатный503 только в QA assertion; mobile exact-name selector не учитывал `plus`, исправлен на содержательный selector и дополнен реальным viewer probe. Implementation source не менялся ради этих fixtures.

### Использованные координатором скиллы

Ниже действия root, переданные в execution record; это не утверждение, что техлид повторно выполнял координационные этапы.

- **using-git-worktrees** — `/Users/daniilkoreskov/PycharmProjects/grant/.agents/skills/using-git-worktrees/SKILL.md`: проверен checkout/worktree контекст, выбраны актуальные split branches с baseline hashes вместо исторического корня. Влияние: изоляция и review inputs соответствуют реальному продукту.
- **subagent-driven-development** — `/Users/daniilkoreskov/PycharmProjects/grant/.agents/skills/subagent-driven-development/SKILL.md`: три независимых реализации после exact interface/ownership handoff, task reviews и whole-wave gate. Влияние: nginx/client/Compose/docs writers не конфликтовали, готовность отдельных частей не закрывала общую волну.
- **systematic-debugging** — `/Users/daniilkoreskov/PycharmProjects/grant/.agents/skills/systematic-debugging/SKILL.md`: sandbox failures и неверные Health/HTTP503 fixtures проверены одной гипотезой по реальному пути. Влияние: изменены QA assumptions, а не бизнес-поведение или frozen source.
- **requesting-code-review** — `/Users/daniilkoreskov/PycharmProjects/grant/.agents/skills/requesting-code-review/SKILL.md`: переданы fixed diffs/bases/manifests, task source reviews, финальный 71-file whole-wave и отдельные registry/runtime deltas. Влияние: независимые Spec/Standards verdicts и cannot-verify limits связаны с точными исходниками.
- **verification-before-completion** — `/Users/daniilkoreskov/PycharmProjects/grant/.agents/skills/verification-before-completion/SKILL.md`: свежие native/Linux/PG outputs, runtime 12 и source hashes проверены до closure; publication остаётся отдельным gate. Влияние: PASS не переносится на неисполненные live successful scoring/rollover/scientific сценарии.
- **webapp-testing** — `/Users/daniilkoreskov/.codex/skills/webapp-testing/SKILL.md`: actual isolated browser/API reads, semantic navigation/geometry/pageErrors и явно controlled409/header seams. Влияние: saved snapshot/state/roles проверены на новой сборке без product writes/ML recompute; designer добавил mobile gates отдельно.

## Локально сохранённые O1/F1 payload refs

Координатор сохранил reviewed implementation paths после staged secret/path/whitespace audits. Реализация не менялась относительно reviewed hashes; это локальные commits без push. Backend получит последующий docs-only descendant публикации: генерируемый source-manifest отражает базу снимка, не самоссылочный hash финального документа. PREP refs выше сохранены историческими.

| Репозиторий | Source payload ref |
|---|---|
| pulse-backend | `b6c03e07babe9369b8dc23162c20cb9005e0880a` |
| pulse-frontend | `2c6521a27e4989bf2e58750d7f19ec97bea936d9` |
| pulse-ml-service | `22b34c3854d4d3a9c9d00bfa5864d5b8ed1ac0bf` |
| pulse-data | `a1dc418ba69b5dd21c336ad1db4bad36870d050b` |

## Последующее поручение пользователя · 20.10.2026

«После того, как закончишь, продолжай по плану» разрешает координатору после publication gate этой волны перейти к следующей очереди [roadmap](functional-roadmap.md): O2/Q0 gates, затем Q1/F2 и далее по существующим зависимостям. Новая очередь оформляется отдельной TL/architect передачей; в текущие O1/F1 bindings/code/DoD не включена. Generic повторное разрешение продолжать не требуется; внешняя доставка, реальные file TTL и production assumptions не выводятся из этой фразы. Scientific остаётся not_assessed по ранее явному решению пользователя.
