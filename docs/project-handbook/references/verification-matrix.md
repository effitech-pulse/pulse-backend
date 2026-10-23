> Снимок документа 23.10.2026. Источник: `pulse-backend/docs/verification-matrix.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# Проверки Pulse и обязательные gates — DOC-05

## Q2-C2 · collection,23.10.2026

[Протокол](q2-collection-jobs.md), [C4](../diagrams/q2-collection-jobs.drawio),
[source/actual/publication](q2-collection-runtime.md).
Core47/SamplerSuccessDeadline30 SOURCE APPROVED,core3+H01–H20 CLOSED;
78Python controls и module-v2 PASS. Fresh ACT14 `r8e1b5066a1e3` ALL11 PASS,
source-bound full-span resources/canary/ML0/teardown VERIFIED. Publication PASS.

| Gate | Подтверждение / граница |
| --- | --- |
| Whole backend | make check17.398s, isolated integration421.538s monotonic(pkg419.575s), build7.129s PASS |
| Protocol/PG | Exact replay/intent/capture/coverage, bounded parser+markers/null/zero/setpoints, global provenance collisions, authority/fence/session/legacy regressions |
| Actual14 | A01–A10 и canonical A11 PASS одним fresh run; per-case5.815/74.653/3.421/52.947/39.407/39.767/29.625/94.434/64.973/5.836s |
| Resources |592.801s/569relevant из596samples/897host+wait4; reconstructed aggregate917348352B и sampled956289024B — разные метрики, оба<1536MiB; source-owned helper peaks учтены |
| Privacy/teardown | MLmethods0,canary0/2012647B/226surfaces/8current∪retiredlogIDs; exact own teardown/working8same/free5; parkedunattributedvolume untouched |
| Publication | Docs-only README handover/D01–03 shell/readiness semantic controls, local links/XML/syntax; root sync/build/validate/sourcecheck/standalone ZIP PASS |

Stand01 WALFAIL, failed/partialACT02–13 и isolateddiagnostics сохранены отдельно.
ACT13 A01–10PASS/A11FAIL не исправлен задним числом; ACT14 имеет собственный fullproof.
A03 actualcombined2MiB/2880raw/1440unique/body+1/rows+1 отделён от остальных unit/schema
границ. A06 combined PG-session loss+same-container/new-process restart не sameOS
survival; finite8 reconnect не unlimiteddaemon guarantee. Рецепт после docs-only
D01/D02 guards проверен externalstubs/syntax, wholelive operator rerun не заявляется.
Current code commit `1a824a63c068f828a9d4dfa663b8036cd426e191` content-binds77testedfiles;
old binary VCS stamp BASE7fcdirty сохраняется, postrunREADME prose separate.
Defaultoff/syntheticonly, PREP/UI/real/production/SLA/scientific не приняты.

## Q2-C1 · source и actual runtime gates, 22.10.2026

Отдельный default-off synthetic cohort путь описан в [протоколе](q2-cohort-jobs.md)
и [постоянном proof/runbook](q2-runtime.md). Source APPROVED тем же Senior,
все8findings CLOSED, включая nullable lifecycle и owned-runtime gate WAIT correction.
Latest `make check`17.843s, integration194.411s monotonic (387.485s wall), build6.063s
PASS на latest source; failed host-suspend integration сохранён отдельно.
Все3actual cases PASS: max232.61s/physical270.70s/capacity1.51s;
measured resources и teardown подтверждены. Technical runtime VERIFIED в
internal synthetic-only scope; publication checks относятся к отдельной редакции.
Unchanged G2/SW/harness approvals переиспользованы; новые broad core/Q1 reviews не
заявляются. Все прежние URL/timezone/retry_wait failed attempts сохранены в proof.
Scientific not_assessed, real enable запрещён.

| Gate | Проверка и граница |
|---|---|
| Whole backend | `make check`; отдельный `TEST_DATABASE_URL` + `make integration`; `make build` (включая cohort-worker). Source/PG tests не заменяют actual Rabbit/ML process proof |
| Admission/identity | Все100 frozen before202, same key replay/changed conflict, strict7-channel C1, bytes/hash/pin/as_of, cap+1 rollback, schema13→14 сохранность historical JSON/digests |
| Ownership/atomicity | Real PG dedicated claim-session loss во время checkpoint/final TX, stale fence, unknown commit, один all100 snapshot+alerts+run/job/inbox, accepted checkpoints не переписываются |
| Rights/privacy/coexistence | Current full100 allowlisted admin; lifecycle/scope revokes whole job, restricted/by-run Execution cleared, Q1 same-key/terminal compatibility, narrow Q1/legacy/Q2 capacity races |
| Actual processes | `Q2_PROCESS_PROOF=1 go test ./test/integration -run '^TestCohortJobActualHTTPWorkerRestart$' -count=1 -v -timeout=8m`; [полная подготовка environment](q2-runtime.md#воспроизведение-из-standalone-checkout). Без opt-in — honest SKIP, не PASS |
| Full runtime matrix | Max/restart/mixed/rollback PASS:100scored,accepted recompute0,mixed95/1/1/2/1,retained2jobs/196inputs/200checkpoints/2snapshots. Physical270.70s, genuine lease66.37396s/execution_busy=true/oldfence0CP, capacity1.51s с actual valid-control, resource/teardown proof PASS; Q1tail resource-only directRPC |
| Resources | Observed sampled RSS API239.25MiB/worker43.59/Q2ML160.02/Q1tail103.44, same-sample combined247.77; PGschema7.87MiB/WAL73.48MiB/tempdelta0, fixture/bundle1173489B. Rabbit configured16messages/16KiB args проверены, bytepeak unavailable. ML TMPDIR empty; backend temp source invariant, global/tmp не измерялся. Все бюджеты и caveats в proof |
| Docs/publication | Editable draw.io XML/links/OpenAPI route refs и standalone handbook validation; product docs не зависят от внешней task-папки. Publication evidence фиксируется после сборки |

PostgreSQL server16 / app schema14, exact API capture SHA и отдельный worker SHA,
source/wheel/pin identities связываются с каждым actual run. Q1 численные budgets и
исторический PASS не являются Q2 capacity evidence. Caps не повышают ради PASS;
GC/redrive API/real enable и scientific acceptance не входят в C1.

## Q1/F2 · source и bounded runtime evidence, 21–22.10.2026

[Q1 proof/runbook](q1-runtime.md) фиксирует exact source/binary identities,
обязательные commands, API-only resource proof и отдельную physical fault matrix.
Source и обязательные runtime gates VERIFIED в synthetic-only области. Source
DB/SW и narrow execution_busy fix APPROVED тем же Senior; post-busy full check /
isolated integration / build, scoped adapter/service race и vet PASS.
Старый physical stale-zombie FAIL сохранён; отдельно actual post-fix22.10 PASS
проверен тем же Senior, Q1-RUNTIME-01 CLOSED:3attempts/один accepted result,
late-worker state unchanged5.36s. Actual frontend scope принят назначенным
frontend-reviewer и отделён от controlled QA; UI worker restart/full25/прочие
ограничения этим новым backend probe не закрываются. Финальная приёмка
координатором и новая closure-публикация отдельно; real-private, production/SLA
и scientific acceptance не заявлены.


Q0 RUN-2 ·21.10.2026: [постоянный proof/runbook](q0-runtime.md) фиксирует source/binary/pin identity и **13/13 synthetic runtime PASS**: resource8, replay1, strict faults4. Test names: `TestQ0StoredFileResourceCaps`, `TestQ0StoredFileProcessReplay`, `TestQ0StoredFileProcessFaults` в `internal/transport/httpapi/file_inputs_process_test.go`; generator/launcher — `test/q0_process/`. Без `Q0_PROCESS_RUN=1`, отдельной PG, accepted Python/pin или Darwin measurement profile process SKIP/отсутствие evidence не является acceptance. Историческая Q0 source/runtime/reproducer и final docs/publication ACCEPTED; cold install UNTESTED, remote SDK/CI OPEN, scientific user-deferred/not_assessed. Прежние STOP/RERUN1 FAIL сохранены отдельно от актуального RUN-2.
Срез 19.10.2026. Владелец матрицы — tech-lead; владельцы команд — сопровождающие соответствующих модулей. Команды выполняются из корня указанного репозитория. Эта матрица различает требуемую проверку, настроенный CI и фактически полученное свидетельство. Наличие workflow не означает, что он уже исполнялся на текущем diff.

## Обязательные проверки

| Модуль и окружение | Проверка перед приёмкой | Настроенный CI | Ручная часть и SKIP |
|---|---|---|---|
| Backend: Go ≥1.25 по go.mod, SDK contracts 0.1.2 local UNRELEASED (исторический PREP0.1.1 отдельно), отдельная PostgreSQL 16 | `make check`; `TEST_DATABASE_URL=... make integration`; `make build`. `make fmt` исправляет код, для контроля форматирования используется проверка gofmt из make check | GitHub: gofmt, race tests, vet, сборка api/migrate; тот же job использует отдельный сервис PG. GitLab: race tests/vet с PG. Make build дополнительно проверяет adminuser/cohort/worker/cohort-worker/collection-worker | Без тестовой PG integration не принят, даже если прямой go test вывел SKIP. Рабочую БД esp_app не использовать. Проверки реального источника, браузерных прав и восстановления выделяются отдельно |
| Frontend: Node 22 (для тестов минимум 22.18), package-lock | `npm ci`; `npm test`; `npm run build` (TypeScript + Vite) | GitHub и GitLab: install → **npm test → build**; ошибка теста останавливает job, ignore-failure не задан | Для этих тестов модель/БД не нужны: отсутствие модели не основание для SKIP. Визуальная сверка с Figma, клавиатура, обе темы и браузерные сценарии проверяются отдельно |
| Data: Python3.13 в CI, model exact source SHA из PREP manifest; workspace `.venv` общий | `.venv/bin/python -m pytest`; `.venv/bin/python -m pip wheel --no-deps . -w dist` | GitHub: установка pinned model, test, build/twine check. Отдельного GitLab workflow в текущем репозитории нет | Офлайн-регрессия парсера не требует UNITEK/ARMITS credentials. Живой сбор — отдельная разрешённая проверка; не смешивать его пропуск с пропуском офлайн-тестов |
| Model: Python 3.13 в CI, test/deep-learning extras для объявленного набора | `.venv/bin/python -m pytest`; `.venv/bin/python -m pip wheel --no-deps . -w dist` | GitHub: pytest с JUnit, build/twine check. GitLab: pytest и wheel | Если необязательный extra отсутствует, конкретный SKIP и причина фиксируются. Успех unit suite не подтверждает ML-качество; причинная оценка eval_honest.py и закрытый test — отдельная ML-приёмка. Обучение не запускается ради этого документа |
| ML service: Python 3.13 в CI, совместимые model/contracts; runtime profile по отдельной инструкции | `.venv/bin/python -m pytest`; `.venv/bin/python -m pip wheel --no-deps . -w dist`; отдельный smoke установки и Health | GitHub: pinned model/contracts, pytest с JUnit, build/twine check. GitLab: установка зависимостей из заданных refs и pytest. CI/Docker применяют runtime constraints при первоначальной установке model/contracts/service; затем pip check | Artifact-dependent SKIP допустим только как «проверка с моделью не выполнена». Model-free gRPC/probe тесты обязательны. Без exact model/cache/manifest/PG не заявлять успешные Score/Upload или промышленную готовность |
| Contracts: Go ≥1.24 по go.mod, Python 3.13 в CI, Buf/plugins по Makefile | `make lint`; `make breaking BASELINE='.git#ref=<base-sha>'`; `make test`. Генерация/check выполняются в отдельном чистом checkout | GitHub: make tools/check, Go/Python tests, wheel. Breaking относительно PR-base выполняется **только для pull_request**; push/tag/manual автоматически его не проверяют | Для прочих запусков breaking выполняется отдельно с зафиксированным base-sha до приёмки изменения контракта. `make check` перегенерирует gen и проверяет diff; не запускать поверх чужих изменений ради документационного аудита. Нет baseline/Buf/доступа к SDK — gate не пройден; SKIP не заменяет breaking-проверку |
| Landing: Python 3.13 в CI; runtime-зависимости приложения не нужны | `python3 build.py`; проверить dist/index.html и отсутствие необработанных placeholder | GitHub: build, проверка HTML/встроенного SVG и artifact. GitLab: build и artifact | Визуальный QA desktop/mobile и reduced motion отдельно; сборка не проверяет отправку писем, ML или интеграции — лендинг их не выполняет |

Источники команд и CI: [backend Makefile](../inventory/pulse-backend--Makefile), [frontend package.json](../inventory/pulse-frontend--package.json), [model Makefile](../inventory/pulse-model--Makefile), [data Makefile](../inventory/pulse-data--Makefile), [ML README](ml-service-readme.md), [contracts Makefile](../inventory/pulse-contracts--Makefile), landing build.py (исходник `pulse-landing/build.py`; в пакет не включён). Для CI источником является `.github/workflows/ci.yml` и, где существует, `.gitlab-ci.yml` каждого модуля.

## Свидетельства текущей очереди A

| Проверка | Результат | Граница свидетельства |
|---|---|---|
| Frontend `npm test`, 19.10.2026, текущий рабочий diff | PASS, 11 тестов, 0 SKIP | Локальный прогон координатора. Cloud CI после изменения workflow здесь не запускался |
| Frontend `npm run build`, 19.10.2026 | PASS, TypeScript/Vite; предупреждение о размере vendor chunks | Сборка локальная, не публикация сайта. Предупреждение не является ошибкой сборки |
| Model suite DOC-01, 19.10.2026 | PASS, 295 тестов по отчёту исполнителя; финальный acceptance regression повторён координатором: 1 PASS | Suite проверяет реализацию; научная приёмка этим не проведена. Существующие deprecation warnings отражены в отчёте |
| ML probe DOC-03, повторная проверка 19.10.2026 | PASS: 31 targeted/native RPC/CLI tests независимо senior; 4 malformed-key CLI случая дополнительно проверены координатором | Printable ASCII без trim; корректный ключ с пробелами проходит настоящий gRPC. R-DOC03 CLOSED; установленный probe hash совпал, scoped review APPROVE. Profiles не переключались; loopback RPC не доказывают Docker image build |
| Native runtime DOC-06, повторная проверка нового service wheel 19.10.2026 | PASS по отчёту исполнителя: clean venv **57 passed / 1 production-artifact test deselected**, shared suite **58 passed**; 9 pins/pip check/isolation, wheels/source hashes сохранены | Новый service wheel после printable-key/no-echo fix. Wheel SHA256 214b4bc37550e42d8e00d6f064282bc488ec73a1d80eae1c7cd2192246ccb122; source tree e14bc4481ed486c62816af0df9959841e5296b9af700292d5b2a2216879bc96c. Final source/wheel/freeze hash verdict senior APPROVE; 11/11 handoff source hashes дополнительно сверены tech-lead. macOS/arm64 CPython3.13; Score/Upload/PG и Linux/Docker здесь не подтверждены |
| Документационные tools DOC-07, 19.10.2026 | PASS: 14 fixture regressions, включая source/parent/target alias на dummy private paths и no-partial-writes; независимый core review APPROVE, R-DOC07-01 CLOSED; финальная generated-приёмка APPROVE после DOC-03/06 | Источники/новый файл/копия drift, explicit snapshot date, missing/stale checksums, ZIP bytes/file-set/traversal, extracted package без workspace. Общее финальное review очереди A завершено и принято; это историческое свидетельство, не новый PREP publication gate |
| Прочие прогоны очереди A | Актуальные результаты и ограничения записываются в журнал исполнения (локальный архив задач: `../tasks/pulse-backend/docs/documentation-queue-a-execution-2026-10-19.md`) | Отсутствие свежего результата означает «не проверено в этом проходе», а не PASS |

Исторические свидетельства из [главы качества handbook](../chapters/16-quality.md) остаются датированными записями прежнего прохода. Они не подтверждают новый diff или весь набор семи модулей автоматически.

## Правило приёмки и публикации

В результате проверки записывают ID задачи, базу/состав diff, runtime, команду, дату, exit code и counts passed/failed/skipped с причинами. Для внешнего CI сохраняют ссылку на конкретный job; если он не запускался, так и указывают. JUnit, локальный лог или подписанный результат сопровождают проверяемый diff, а не только имя ветки. Ветку с незакоммиченными изменениями нельзя восстановить одним HEAD.

Обязательная проверка с ошибкой или пропущенной зависимостью остаётся открытой. Допустимый SKIP относится к конкретному опциональному сценарию и не расширяется на весь gate. Нельзя подменять отсутствие результатов зелёной общей надписью. Перед изменением protobuf/SDK/миграций нужны соответствующие контрактные и integration проверки; очередь A таких изменений не выполняет.

Обновление этой матрицы требуется при изменении Makefile, package scripts, CI, runtime profile или условия SKIP. DOC-07 связывает её с README и автономным handbook. Согласованный аудит (локальный архив задач: `../tasks/pulse-backend/docs/documentation-audit-2026-10-19.md`) сохраняет исходные находки, журнал — результаты их устранения.

## PREP — текущие свидетельства, отдельные от очереди A

Статус документа: финальная техническая сверка20.10.2026. Нижеследующие результаты прочитаны техлидом в отчётах исполнителей/независимого Senior; это не повтор suite техлидом. PREP-01–07 технически приняты, scientific acceptance user-deferred/not_assessed; финальная публикация handbook выделена отдельным gate в [PREP ledger](readiness-ledger.md).

| Gate / команда | Полученное свидетельство | Предел / оставшийся gate |
|---|---|---|
| Contracts `make lint`, `make breaking BASELINE='.git#ref=f5d51b6'`, official generate, `make test` | P1 Senior clean; wire additive, повтор generate без drift, Go/Python presence/null/int64 checks PASS | Exact source contracts07213c430318affd140f55dea391cdc36220d93f; package0.1.1 не новый release |
| ML `../.venv/bin/python -m pytest -q` | 78PASS; отдельный clean native wheels JUnit78/0skip, pip check; fresh Linux image78PASS6.95s | Не новая precision/recall; bundle-only Upload software gate отдельно от research Score |
| Model `python -m pytest`; P7 `tests/evaluation/test_readiness.py` | Full355PASS по логу исполнителя; Senior targeted60PASS, оба замечания CLOSED | Валидатор метаданных не проверяет raw truth/физическое месторождение; scientific acceptance user-deferred/not_assessed |
| Data Linux fresh constrained image | 32testsPASS0.62s, install/pip check | Synthetic emulator не контрольная ML когорта |
| Backend `make check`, `make build`, `TEST_DATABASE_URL=<isolated pulse_test> make integration` | Fix1 frozen38files: check/build всех4CLI PASS; полная race integration67.610sPASS | Senior F1–F3 CLOSED:10targetedPG/race18.271s+lease1.989sPASS,38/38hashes; старые70.506/68.932s не переносятся на новый diff |
| Frontend Node22 `npm test`, `npm run build` | Первые3Senior findings CLOSED;29testsPASS; bounded responsive followup30tests/buildPASS | Responsive Senior sourcePASS14aggregate/6fix hashes и6/6SSR tests; реальный browser GREEN22/22,fail0/pageErrors0,14/14source hashes на rebuilt image |
| P0 config/CI source profile | Senior ACCEPTED21files; native model103/contracts4/ML12 hashes и50wheel hashes сверены | Backend Senior staged-source223files ACCEPTED; все7payload commits в compatibility manifest, frozen backend38точны; docs-only потомок отдельно |
| Реальный isolated8088/8018 | API→Data→ML, auth/scope/Excel/logout/Origin и сохранённый snapshot после restart PASS по координатору; run30/31=100scored, run32=98scored/1archived/1inactive | Рабочий8080 не менялся. Lifecycle fixture восстановлен, browser GREEN22/22; after11run33 done100scored с правильной identity, snapshots30/32 неизменны, restricted0 |
| Cloud GitHub/GitLab | Конфиги exact source refs+constraints проверены локально | Cloud jobs/push не выполнялись; remote наличие local commits не заявлено |

Go/ML/Data consumer CI фиксирует contracts07213c430318affd140f55dea391cdc36220d93f и model f5c2a3de031ff4794194a219f048c9bf8c7c4f45 там, где применимо. GitLab fetch точного SHA с checkout detached и проверкой HEAD; `clone --branch <SHA>` не используется. Конфигурация не делает локальный commit доступным remote. [Финальный compatibility manifest](../inventory/readiness-compatibility.json) фиксирует все7reviewed payload commits и image IDs. Backend ref — runtime payload99b7fa3d; финальный README/docs-only потомок не меняет этот runtime ref и не создаёт hash-самоссылку.

Migration11 runtime: первое/повторное применение exit0; прежние значения5таблиц и immutable snapshot digests сохранены по coordinator migration11-evidence.json. Все4финальных images подняты с health wait. Lifecycle fixture восстановлен через API revisions; after11run33 done/profilecohort/versionpresentation-v1:100members/100scored,0error; historical30/32 unchanged, restricted0.

`runtime-image-binding.json`:4actual image IDs связаны с source manifests; семь численных ML/Data versions совпадают, pip check обоих PASS. Backend image имеет no_container_healthcheck: HTTP Health200 проверен отдельно, поэтому общий Health не выводится из Docker healthy.

Техническая приёмка PREP завершена; [native artifact manifest](../inventory/readiness-native-artifacts.json) сохраняет119source/50wheel hashes. Остался отдельный публикационный gate: final handbook sync/build/check/validate/extractedZIP и docs-only commit координатора. Научный/промышленный/cloud gate этим не закрывается.
