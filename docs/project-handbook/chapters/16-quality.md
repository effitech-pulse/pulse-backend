# Тестирование и критерии приёмки

C2 ·23.10.2026: Core47/SamplerSuccessDeadline30 SOURCE APPROVED,core3+H01–H20
CLOSED,78Python/mock controls PASS. Module17.398/421.538monotonic(pkg419.575)/7.129s
PASS. FULLACT14 all11 PASS с canonical A11/resources/canary/ML0/teardown; это отдельное
actual evidence, не вывод из unit tests. [Runtime](../references/q2-collection-runtime.md),
[матрица](../references/verification-matrix.md).

Fullspan592.801s/569relevant/596total/897host+wait4; reconstructed917348352B и
sampled956289024B различаются, оба ниже1536MiB. Canary0/2012647B/226surfaces/8logIDs,
working8same/free5; parkedunknownvolume untouched. PriorACT13 rawA11FAIL и isolated
DIAGNOSTIC11/12PASS сохранены отдельно, ACT14 — одинfresh acceptedrun. Publication
PASS, scientific not_assessed. README docs-only D01/D02 fixed shell/readiness
проверены24externalstub controls/синтаксисом; новый wholeprinted operator run не заявлен.

Q2-C1 ·22.10.2026: source APPROVED, все8findings CLOSED; latest check17.843s/
integration194.411s monotonic (387.485s wall)/build6.063s PASS. Actual max/restart/
mixed/rollback PASS232.61s (runner235.769s), accepted checkpoint recompute0.
Physical270.70s и capacity1.51s actual tests также PASS; sampled resources и teardown
подтверждены, internal synthetic runtime VERIFIED. Проверки публикации комплекта учитываются отдельно; scientific/real/production не приняты. [Q2 proof/runbook](../references/q2-runtime.md)
содержит exact opt-in command и required environment для materialized
`TestCohortJobActualHTTPWorkerRestart`. Без `Q2_PROCESS_PROOF=1` actual-process тест
делает SKIP, который не доказывает inference/recovery. [Протокол](../references/q2-cohort-jobs.md)
и [C4](../diagrams/q2-cohort-jobs.drawio) описывают source behavior; научный статус
not_assessed сохраняется.

Gate требует complete100 freeze до202, checkpoint resume, dedicated physical PG
session loss во время checkpoint/final TX, отсутствие stale accepted effects,
atomic all100 publication, current full100 authority и restricted Execution privacy.
Отдельно проверяются Q1/legacy capacity races, old ML tail/exact busy, finite
retry/queue budgets, cap+1 rejects, source bytes deletion recovery и drain rollback.
All100/mixed100 happy process scenarios не заменяют fault/resource матрицу.
Измерения RSS/PG/WAL/temp/queue/disk и source/API/worker/wheel/pin bindings приводятся
только по actual evidence; Q1 числа и unit PASS не являются Q2 capacity proof.
Editable XML/links/OpenAPI refs и автономная handbook publication проверяются
отдельно. G2 APPROVED ранее; неизменённые G2/Q1 этим текстом повторно не принимаются.

Observed Q2 proof: RSS API239.25MiB/worker43.59/Q2ML160.02/Q1tail103.44, simultaneous
combined247.77MiB; PGschema7.87MiB/WAL73.48MiB/tempdelta0. Canary56files no leak;
owned test resources удалены, working8containers unchanged. Rabbit bytepeak
unavailable при actual16KiB/16message caps; ML TMPDIR empty, backend temp source
invariant без global/tmp measurement. Это internal synthetic acceptance, не SLA.

Q1 source и обязательные runtime gates VERIFIED в synthetic-only области22.10;
DB/SW и narrow busy fix APPROVED тем же Senior, actual frontend scope принят
назначенным frontend-reviewer отдельно. Post-busy full check/integration/build,
scoped transport/service race и vet PASS. API-only ресурсный proof:2accepted/3rejects,
1155reads/0errors, list p95=33.509ms, OS API RSS451444736B. Это короткий synthetic
admission/read workload без ML/broker publish; production SLA этим не заявляется.
[Q1 proof](../references/q1-runtime.md) сохраняет исходный physical stale-zombie FAIL
и отдельно фиксирует actual post-fix22.10 PASS:3attempts, generation3/final fence6,
один accepted result/check, late-worker state unchanged5.36s; Q1-RUNTIME-01 CLOSED
тем же Senior. Новая closure-публикация и финальная приёмка координатором отдельно.


Q0 RUN-2 ·21.10.2026: **13/13 synthetic runtime PASS** на одном pre-invocation-hashed binary afae2c4: resource8/replay1/fault4. W3 OS449609728B, W4 OS360005632B при budget1GiB; W5 race1success+1quota, metadata30success/0errors/payload-select0, overlaps[2,2], p9512.946ms. [Полный proof](../references/q0-runtime.md) сохраняет source/pin/RSS/PG/WAL/cleanup и исторические STOP/RERUN1FAIL. Source/runtime/reproducer ACCEPTED; final docs/publication gate PASS; холодная установка UNTESTED, CI/release OPEN, scientific user-deferred/not_assessed.
## O2 · implementation/runtime verified, публикационный gate

[Execution](../references/o2-q0-execution.md) связывает O2-1…4 с [runtime evidence](../references/o2-runtime.md). Source Go/Python/stack Senior reviews пройдены; runtime VERIFIED: W1/P2 CLOSED,150samples/151.256s/425.25MiB peak и86.75MiB headroom без swap/max/oom/restarts, три UI round-trips PASS; final rollback13phase ID/SHA+recovered failure ID PASS. Финальные docs review/publication PASS; O2 принята как локальный пилот. Реальные trace/log paths, отдельные пять20s outages, actual expiry guard и retained checks относятся к координаторскому evidence. Handbook не повторяет эти тесты и не объявляет source PASS доказательством runtime/capacity. PREP PG55439 и lane test PG55432 разделены; business identity/hash проверяются отдельно от telemetry counters. Scientific not_assessed сохранён.

Полная [матрица проверок и CI gates](../references/verification-matrix.md) содержит команды, окружения, допустимые SKIP и свежие свидетельства очереди A. Это локализованная копия источника pulse-backend/docs/verification-matrix.md; [политика](../SOURCE_POLICY.md) задаёт её владельца и синхронизацию. Ниже — краткая карта и исторические свидетельства прежнего прохода. Внешний CI не объявляется пройденным по наличию workflow.

## Автоматические проверки

| Модуль | Команды из репозитория | Покрываемые свойства |
|---|---|---|
| Backend | make check; TEST_DATABASE_URL=... make integration; make build | Unit/race/vet, архитектурные границы, реальная тестовая PG, сборка CLI |
| Frontend | npm run build; npm test | TypeScript/Vite, гигиена, относительное изменение, ID, импорт/экспорт CSV/XLSX/Parquet |
| Data | .venv/bin/python -m pytest | Ресемплинг, long_raw, loaders, сценарий и детерминизм API |
| Model | .venv/bin/python -m pytest | Признаки, правила, разметка, split, гигиена, causal context, модели/оценка |
| ML service | .venv/bin/python -m pytest | gRPC, upload, scoring и model identity |
| Contracts | make lint; make breaking; make test | Buf style/breaking и SDK совместимость |
| Landing | python3 build.py | Сборка статической страницы; визуальный QA отдельно |

Тесты backend используют отдельную БД и временные схемы, которые очищаются после прогона. Нельзя подставлять рабочую esp_app в TEST_DATABASE_URL. В Data офлайн-регрессии не требуют живых UNITEK/ARMITS credentials.

## Проверенные регрессии приложения

ID Pulse сохраняется при импорте; внешний 0 не превращается в null. Маркеры очищаются по каналу, нули сохраняются; is_set не попадает в измерения. CSV/XLSX/Parquet проходят обратное чтение. Сравнение не растягивает короткий период и не меняет даты.

Проверены ручные проверки и их повторные ключи; серверный автор заключения; недоступность чужой скважины; запреты viewer; конфликт optimistic revision; атомарность archive/edit; сохранение проверок/заключений при архиве; курсорная история. Отдельный регрессионный тест исключает замену актуального watermark старым JSON-снимком настроек.

Запись предыдущего рабочего прохода, до очереди A: backend прошёл make fmt/check/integration/build; frontend — production build и 11 тестов. Выполнены браузерные проверки оболочки, обзора, тем, фильтра и профиля. Через Docker проверены здоровье PG API, архив bounds реальной скважины, обновление источника и сохранение ручной проверки скважины обезличенного фонда с версией модели. Эти факты относятся к этому проходу, а не означают непрерывный мониторинг всех функций.

## Продуктовые критерии и их границы

| Сценарий | Критерий |
|---|---|
| Pulse Connector | Одно согласованное название источника, корректное описание происхождения данных |
| Фонд | Фиксированное membership100; scope/фильтры и lifecycle могут уменьшать отображаемое подмножество. Bulk сохраняет outcome каждого участника, не требует100scored; research исключён на сервере |
| Карточка | Риски открываются первыми; Telemetry второй; deep links сохранены |
| Файл | Единый сценарий выбора/предпросмотра/валидации/проверки/результата; ошибка не создаёт ложный успех |
| Журнал | Видны реальные commit-события с объектом, временем, автором и исходом; API-аудит отделён |
| RBAC | 401 без сеанса, 403 на запрещённые действия, scope на всех чтениях/экспортах, logout очищает клиентский кэш |
| Повтор/сбой | Повторный request_id не дублирует действие; сбой процесса не выдаёт незавершённое за успешное |
| Figma | Основные разрешения, обе темы, статические assets и все рабочие состояния сверены |
| Модель | Только причинная независимая оценка; генератор не присваивает риск и не служит holdout |

## Нефункциональные требования

Нужны измеренные latency/throughput, память/CPU ML, пределы импорта, деградация при недоступности БД/источника, восстановление после перезапуска, журналирование без секретов и проверенное восстановление backup. Числовой SLA, RPO/RTO и результаты нагрузочного теста не объявляются выполненными до измерения.

Актуализация документации выполняется по [source policy](../SOURCE_POLICY.md), а требования provenance/RPC разобраны в [отдельной спецификации](../references/model-rpc-compatibility.md). Typed decision/provenance и immutable cohort output реализованы PREP; complete input replay остаётся неподтверждённым.

Проверка документации: локальные ссылки, якоря, отсутствие пропавших assets, полные OpenAPI $ref, draw.io XML и endpoints связей, контрольные суммы архива. Сборка этого комплекта не переобучает модель и не мигрирует рабочую БД.

## Подтверждённые свидетельства PREP · 20.10.2026

Источник результатов — [PREP ledger](../references/readiness-ledger.md) и linked executor/Senior evidence. Здесь не повторялись runtime suites и не создавались новые научные метрики.

| Gate | Датированное свидетельство | Ограничение |
|---|---|---|
| Native / Linux | [Native artifact manifest](../inventory/readiness-native-artifacts.json): 119 public source hashes и 50 wheel hashes; clean native ML78/0skip + pip check; Linux ML78 PASS, Data32 PASS; source/wheel hashes сверены Senior | Не облачный job и не произвольная совместимость версий |
| Backend | make check/build, PG/race integration; F1–F3 CLOSED, независимые targeted/re-review и migration11 repeat сохранены | Только отдельная test/isolated PG, рабочая БД не мигрировалась |
| Frontend | Source Senior PASS; responsive regressions/build; browser22/22 PASS, fail0/pageErrors0,14/14 source hashes | Только bounded light/dark390/1440 и проверенные сценарии, не полный Figma/WCAG |
| Scope/auth | Full100/restricted0, direct ID/Excel, role mutations/origin/logout; mutable status reset и foreign reference writes покрыты независимо | Global membership не меняется под scope читателя |
| Snapshot | Полные outcomes, null non-scored, old output после ML restart; ранняя `running`/`error` identity11 | Output сохранён; exact immutable input bytes отсутствуют, provenance partial |
| Scientific gate | Readiness validator: targeted60PASS независимо Senior, full model355PASS по executor log, P7-F1/F2 CLOSED | Software validation, scientific_acceptance=not_assessed; внешние данные отложены пользователем |

Успешный exit вспомогательного browser script не принят без чтения JSON: итог22assertions проверен отдельно. Начальные harness failures сохранены и superseded финальными visible-element/geometry checks. В browser fixtures не было новых alert rows; новый реальный alert/review save не заявляется проверенным UI-проходом. Native30/30, SSR/backend и реальные auth6/6 — отдельные свидетельства своих запусков, не слагаемые одной статистики.

Финальный after11 run33 завершён: done, cohort/presentation-v1, 100 scored, 0 errors/alerts; restricted reader0, прежние snapshots30/32 не изменились. Identity фиксировалась уже в running. Точные семь payload refs и image IDs находятся в [compatibility manifest](../inventory/readiness-compatibility.json); он не пытается ссылаться на собственный docs commit. Cloud job, push и release не запускались. Отложенная научная приёмка и будущий самостоятельный ML hookup не препятствуют честной публикации технических результатов.
