> Снимок документа 23.10.2026. Источник: `pulse-backend/docs/q2-runtime-2026-10-22.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# Q2-C1 · runtime evidence и воспроизведение · 22.10.2026

Статус этой редакции: **SOURCE APPROVED**, все8findings CLOSED тем же Senior;
bounded owned-runtime WAIT correction и latest module gates PASS. **Все3actual
cases PASS**, измеренные ресурсы и teardown подтверждены: technical runtime gates
**VERIFIED в internal synthetic-only scope**. Проверки публикации комплекта
учитываются отдельно от runtime evidence. Failed attempts сохранены ниже. Scientific acceptance `user-deferred/not_assessed`, real
enable запрещён. Дополнения вносятся по фактическому evidence соответствующего
frozen запуска.

[Протокол](q2-cohort-jobs.md), [C4 source](../diagrams/q2-cohort-jobs.drawio),
[настройки](backend-configuration.md#изолированный-q2-c1-synthetic-профиль) и
[эксплуатация](backend-operations.md#q2-c1-synthetic-worker--recovery) описывают исходники,
а не переключение действующего портала/PREP. Collection не входит в Q2-C1.

## Независимые gates

| Gate | Текущий статус этой редакции |
|---|---|
| G2 strict source-kind capability | Ранее отдельно APPROVED: backend cohort entrypoint, ML allowlist uploaded_file/cohort_snapshot, contracts capability docs; wire/SDK не менялись |
| Q2 DB/domain + service/worker/HTTP/bootstrap/harness source | APPROVED тем же Senior,все8findings CLOSED; bounded owned gate correction Q2-DB-03 APPROVED. Unchanged G2/SW/harness approvals переиспользуются |
| Whole-module check/integration/build | Latest source: PASS17.843s /194.411s monotonic (387.485s wall) /6.063s, exit0 каждого |
| Actual HTTP/API/worker/Rabbit/installed ML |3PASS: max/restart/mixed/rollback232.61s, physical270.70s, capacity1.51s; runtime VERIFIED в internal synthetic-only scope |
| Fault/resource/rights/capacity/rollback | Physical old-fence0 checkpoints/execution_busy, valid-control oversize reject, bounded resource evidence и rollback OFF сохранность PASS; teardown owned resources завершён |
| Documentation | Протокол/config/operations/editable C4 описывают принятую technical source/runtime область; standalone publication проверяется по bytes конкретной редакции комплекта |
| Научная/production/real-private приёмка | not_assessed / не разрешены этим proof |

## Identity запуска

Для actual evidence сохраняют source base и полный per-file frozen manifest,
Go/Python/platform/dependency identities, SHA256 фактических API и cohort-worker
executables, installed service/model/contracts wheels и loaded module paths,
exact pin/bundle/policy/runtime hashes, trusted descriptor/fixture/input hashes,
test source и test-only instrumentation SHA. Название ветки или package0.1.2 не
восстанавливает незакоммиченный diff и не заменяет эти identities.

Core53paths сохранены локальным commit `677cf337393b500c9173333a249c337135ff33b1`;
coordinator подтвердил53/53frozen file hashes MATCH. Фактические binaries собраны
**до commit** из base `c02b61df43cb099eb005919f2aa170c3041d4ae5` + exact reviewed tree,
с `vcs.modified=true`. Бинарнику не приписывается будущий docs HEAD или иной rebuild.
G2 local
commits: backend `c02b61df43cb099eb005919f2aa170c3041d4ae5`, ML service
`f5e08f529117280055559fb7c96cc072a0d20bc9`, contracts
`1d0b2b62d2cb73d18ab69738431089edf69621f8`. Это source checkpoints;
они не объявляют remote availability. Ниже binding latest bounded-wait rebuild,
на котором все3actual cases PASS; runtime VERIFIED в internal synthetic-only scope.
Historical failed attempts ниже сохраняют свои identities и не подменяются новым
binding. Автономность конкретной публикационной редакции проверяется отдельно.

| Identity | SHA256 (hex, кроме явно указанных pin fields) |
|---|---|
| Actual `bin/api` | `1a50a473bc07fe0035cdacc287859ebfd5241fba52971c5c64bc2f52a257c45f` |
| Actual `bin/cohort-worker` | `5abfb5760a651361ad2ff65268ebe62eb6eb77ecea8168239930aa3a753764f2` |
| Full25file DB source patch after owned-runtime correction | `c8c039efc637fc362b05430d6f61e99ef36f7a89369e73394c711bb1a73830ee` |
| Owned-runtime correction3file patch | `6b2c55cd889131abba08cca973d89e3de6828ba27398ab54de5567951531c40c` |
| SW source correction patch | `7bf71d79bf14d5fab8ce02d83a3a1902ae757a035375fca66304e529efdc6f68` |
| Product process test source | `02dd9b0c2ca4fa58110b047974b827ca9dd61544c8f6d65cc5a54c0ec0deeb71` |
| Installed ML service0.1.0 wheel | `43e042941da1fd705d5d859c6c2b0c701ae5d97e4bd2668a591db512d5618966` |
| Trusted synthetic helper | `e317aac7bc3643ee94ea71c3ec2e1a067fad822ac817ab965b98175feb9c0d24` |
| Test-only ML launcher instrumentation | `85109ded8768823081c45fa38f95fa240b1733a1f0df64d6443a126bd4c2d82e` |

Pinned model — `q0_synthetic`, `upload_pin_v1`, context `legacy_v0`; strict cohort
source identity формируется при admission. Исторический uploaded_file fixture
template не переименовывается в cohort.

| Pin field | Exact value |
|---|---|
| Bundle | `sha256:e13e9f857bef78e5b0b57e3643eb6ce27071e090008ac40cf5e623db780b3ce8` |
| Source manifest | `sha256:9caa75ba2dc5257376028ae9240801657de2bcdfe2d13b906019eea2bc481f88` |
| Runtime manifest | `sha256:4157e5dea11361e66441ede44d4811e8ebf7df1d924efd4ac1b2651df1aa79f0` |
| Feature schema | `sha256:59fff74c23c8f550653db468f1f574170f9669e3e955979b02d860ef9e67b0ea` |
| Feature recipe | `sha256:05f7b620a7ea17d5d3de0c21cba157a3e31e2b3238bbe873027284e4e5a80d31` |
| Rule | `sha256:4966850b6c0023070edd66451b7699d47e87f8e59710040d306b88310d4353a4` |

Числовой package version не заменяет exact wheel/source identity. Изолированный
Q2 installed-module overlay не менял shared environment/PREP. Recorded source
review и hashes проверены независимо; docs role сервисы/тесты повторно не запускала.

Capture recipe SHA принадлежит actual API, а actual worker SHA — отдельный binding.
При recovery worker использует сохранённые PG inputs и current trusted descriptor
metadata. Отсутствие original fixture bytes после202 не повод для recapture или
смены pin. Новая сборка API требует descriptor с её capture SHA для новых accepts.

## Сохранённая история проверок

| Этап | Фактический результат и граница |
|---|---|
| Initial actual startup | FAIL до admission: private Q2 AMQP URL содержал `?heartbeat=10`, запрещённый strict config.0jobs/0computes. Исправлены только private URLs; heartbeat adapter10s и product source не менялись |
| Oversize harness review | Q2-PROC-01 CLOSED: отрицательный startup теперь обязательно предваряется actual valid enabled-catalogue readiness control, чтобы unrelated config failure не давал false PASS |
| Следующий max actual attempt, all100 phase | Фаза завершилась:100scored,100actual fake computes,5accepted checkpoints до crash,0recomputed accepted,1snapshot/run,0norm alerts; API restart/source deletion/worker recovery проверены. Это частичный результат failed full test |
| Тот же attempt, mixed phase | FAIL: job immediately revoked из-за сравнения UTC frozen ArchivedAt и Europe/Moscow PG timestamp как JSON strings при одном абсолютном моменте. Full test не принят |
| Lifecycle correction | Q2-DB-02 CLOSED тем же Senior: nullable `time.Time.Equal`; реальный drift/NULL/lifecycle/rights checks сохранены. Recorded real-PG4new+5existing cases PASS9.935s; independent pure7cases/race PASS1.231s |
| После lifecycle correction | Его mandatory gates PASS: check17.667s/integration142.770s/build6.010s. Они относятся к предыдущему snapshot |
| Следующий max actual attempt | FAIL exit1,192.085s runner /189.848s Go package: при двух workers и100ms API polling restart не завершил100, state=retry_wait, completed44. Evidence attempt сохранён; full inference/mixed/physical/capacity/resource gates этим не приняты |
| Owned-runtime gate correction | Q2-DB-03 CLOSED, все8findings CLOSED: WAIT только already-owned Next/Renew/Checkpoint/Finish. Controlled PG RED4operations→GREEN18.741s, expiry GREEN49.136s; independent helper/context race PASS1.255s. Actual PG holder каждого старого warning не установлен |
| Initial post-owned-wait module integration | Exit2 после3096.297s wall: host suspend; failed attempt/log сохранены. Он не считается latest successful gate и не скрыт новым запуском |
| Latest module gates | check17.843s PASS; integration194.411s monotonic/387.485s wall PASS; build6.063s PASS на latest frozen source. Q1 broker/old immutable regression включены; Q1 actual ML process SKIP, Q2 actual отдельно |
| Physical initial setup | Sandbox отказал localhost до workload;0compute. Failed attempt сохранён, тот же разрешённый test затем PASS; product source не менялся |
| Latest max/restart/mixed/rollback | PASS232.61s test /233.506s Go package /235.769s runner; отдельные physical/capacity/resource/teardown gates также PASS, details ниже |

Идентификаторы частичной all100 фазы: job `1ec0fabe9f91dcc32e4e5cba4893a7a3`, run1,
fixture `sha256:04301a4a259052af9a3f8121e9dc283c5066cd00a0951d5e431edacec401f48f`.
Это прежний failed-full-test attempt, а не итог нового candidate. Его module
logs SHA256: check `5d0593e46644b3e47d66d5c8a074b6a94635647e6da584e9467456b3c5130854`,
integration `17651ea2cc313522f797062150d2774ef0bda6715a06c4151f60b7aa44a1098a`,
build `bd1ae42dc629d8628ffafa1232a0f8dba06b4bcc360f29dd2d0506b8d6e53883`.
Evidence хранится отдельно от продуктовых inputs; старые неуспешные outputs не
перезаписываются новым запуском. Предшествующий retry_wait failed attempt сохранён
отдельно от earlier timezone failure. Механизм short TRY contention→busy error→healthy
claim Close→lease-expiry recovery воспроизведён focused probes; attribution конкретного
actual PG holder не выдумывается по100ms polling. Status read не берёт capacity gate.
Его binary binding: API `901f5a6b5faa047a5d2d0eab2bc3fdbb24fbd330a714c8bd064c38b1e19cc980`,
worker `7fd1f8d86a995f7044852730fd538ad26f9d33f4dd6aa9dd7ba83fdf21443405`.
Эта таблица не объявляет итоговую runtime-приёмку.

Принятая correction сохраняет все operation/job/RPC limits: Next/Renew/Checkpoint
total5s включают serializer+TX+wait, Finish total10s с gate query≤min(remaining,5s).
После gate wait заново проверяются authority/state/time/lease/fence/token/physical
ownership. Admission/legacy/new claim/cancel/outbox/reconcile TRY неизменны;
commit/rollback освобождает gate до RPC/AMQP. [Точный протокол](q2-cohort-jobs.md#принятое-уточнение-owned-runtime-gate)
описан в approved source. Не повышали лимиты, не убирали второго worker и не
замедляли100ms polling ради latest rerun PASS.

## Подтверждённый max/restart/mixed/rollback

`TestCohortJobActualHTTPWorkerRestart`, latest source/binaries выше: exit0,
test232.61s / package233.506s / runner235.769s. API restart, immutable source-file
deletion recovery, worker crash после5accepted checkpoints, replacement workers и
duplicate hints завершились одним all100 snapshot/run,0norm alerts и0recomputed
accepted checkpoints. Actual fake starts101: uncommitted compute повторился один
раз, exactly-once compute не заявляется.

| Фаза | Фактический итог |
|---|---|
| All100 | job `8b0e0b63d753977bef3c757d84e32bd6`, run1;100scored,1snapshot,101actual fake starts,5accepted before crash,0recomputed accepted |
| All100 fixture | `sha256:523615a4ded05b629d2a265e302581714589799bb23c9789aeae478ed3445ec0` |
| Mixed100 | job `74ec499bb35f3096c0786ec838032c29`, run2;95scored/1archived/1inactive/2insufficient/1error,96actual fake starts; old published snapshot preserved while pending |
| Mixed fixture | `sha256:7901f76d5bb379ce5987af7ad9f927105c29d2d625c7e9b81061f777be1dde78` |
| Rollback OFF | Admission off,consumers stopped,actual computes added0; retained2jobs/196inputs/200checkpoints/2snapshots с прежними digests |

Latest recorded evidence SHA256:

| Record | SHA256 |
|---|---|
| Runner result | `1daa19dcf368a35ea28531f9940a48a90f8fe930873099d157a415fc7f4efe6d` |
| Actual process log | `c5421651719f9766b339d4967953270ee12f57928ece703a79d25c342a04081a` |
| All100 proof | `0495db7e633dc5802ba86405c7416ea4a5ce9dc6a7560303e90e40c2800c8536` |
| Mixed proof | `36e222694a2ab5ccf219b7300f754204256e7f4e979ba323a8e90ea25b26393f` |
| Rollback proof | `45d20522f8afee6b1465ad3986bd2643b056fbe18b358508cfed7c77dd47d379` |
| Latest check log | `0229ad6f6aaca5d1c05ed1128c6dd345c168063c6d21c421a9aa07f6630acdb7` |
| Latest integration log | `5e504f425accbc271cc2eea0eb033a8a0a7eed6b8269b8c34777c4032381f877` |
| Latest build log | `bd1ae42dc629d8628ffafa1232a0f8dba06b4bcc360f29dd2d0506b8d6e53883` |

Это подтверждённый actual max/restart/mixed/rollback scope. Остальные два actual
cases и измерения ниже закрывают согласованный internal synthetic runtime gate;
публикационный контроль комплекта учитывается отдельно. Сервисы/DB/source
doc writer повторно не запускал.

## Physical stale, capacity и границы доказательства

| Actual case | Test / Go package / runner | Результат |
|---|---|---|
| `TestCohortJobActualPhysicalStale` |270.70s /271.230s /273.256s |PASS; genuine lease age66.373961917s, persisted execution_busy=true, old fence checkpoints0; затем100scored/1snapshot, mixed95/1/1/2/1, rollback OFF PASS |
| `TestCohortJobActualMaxCatalogueReject` |1.51s /2.363s /4.364s |PASS; actual valid enabled startup control, затем только size change:25208700B>25165824B,0jobs/0compute |

Physical job `cd7af548848dd0eeaa90483ff0a7cd9b`, old owned PG backend6661/fence1;
mixed job `930c3007c6577f55926d9ca953d4abf1`. Только принадлежащая claim session
терминирована; natural lease45s не подменялся SQL clock rewrites. Оба full100
восстановления завершились101actual fake starts и0recomputed accepted checkpoints.
В physical scenario accepted checkpoint до барьера0; это не повтор первого crash
window с5accepted checkpoints. Non-scored mixed Result=null и old published snapshot
до final сохранены. Physical rollback сохранил2jobs/196inputs/200checkpoints/2snapshots,
admission off/consumers stopped и0новых computes.

Client RPC deadline10s истёк во время pause: successful late RPC **не заявляется**.
Rollback checkpoint/final transaction при потере physical session отдельно доказан
real-PG integration; процессный barrier его не заменяет. Q1 tail в resource harness —
отдельный direct uploaded_file RPC на принятом Q1 runtime, а не новый Q1 durable-job
rerun; original predict завершается после75s barrier, client deadline10s сохранён.

Oversize fixture252087B/member,100members/25208700B; cap24MiB=25165824B.
Control manifest SHA `sha256:2b39a2d37b8564a16eb0c699eea2ac44f3bfe68833be8be0775ba69d7b94d7be`.
Rejection boundary — API catalogue startup до admission. Это следующий размерный
профиль, не ровно один byte сверх cap; unrelated startup error не считается PASS.

| Evidence | SHA256 |
|---|---|
| Physical runner result | `815cf4abf424a25cefdf2309b2a2b45e15e7f908ec3231ab4759592e40e0720b` |
| Physical process log | `42e74963338a0ce9ca812ca71939c18f0b31aa7db3fb26edcf21f0bb16894cff` |
| Physical stale proof | `002ac9f6ce9f14cfbcb39e26f1d7057c462d96643835d740d19220f6b5885c67` |
| Capacity runner result | `a611d0ef715c698c5899f92fc1dd065f053324aa31f35b21c9ffa88f1bba235a` |
| Capacity process log | `f0190fbe7db53ea2a002d5241b8e902a362b86ba17ad6e0b5e25578fc09578fc` |
| Capacity reject proof | `51bcd11b7544089da1023a5d0348f193d03fb3bcf13a072fc846d580997f259b` |

## Измеренные ресурсы и завершение стенда

Все перечисленные ceilings выдержаны в согласованном isolated synthetic proof.
Это observed sampled RSS, не абсолютный instantaneous OS high-water и не production
SLA.6846RSS samples с nominal interval0.1s;314PG/resource samples. PG/WAL counters
имеют database/global scope, max_schema_bytes — максимум временной схемы;
значения не изображаются точным per-job attributable consumption.

| Метрика | Фактически | Ceiling / ограничение |
|---|---|---|
| API RSS |250871808B =239.25MiB |512MiB |
| Cohort-worker RSS |45711360B =43.59MiB |512MiB |
| Q2 ML RSS |167788544B =160.02MiB |1536MiB |
| Q1 ML tail RSS |108462080B =103.44MiB |Resource-only direct RPC |
| Simultaneous Q2ML+Q1tail same-sample peak |259801088B =247.77MiB |3072MiB; не сумма независимых peaks |
| Combined peak во время Q1 compute tail |187367424B =178.69MiB |658overlap samples; ML physical peak compute1 |
| Max PG schema bytes |8249344B =7.87MiB |768MiB/schema |
| Database baseline→peak |47135767B→56220695B |Database scope; schema metric отдельно |
| WAL delta |77051088B =73.48MiB |1GiB/full100 budget; measurement scope выше |
| PG temp_bytes delta |0B |0B |
| Fixture/bundle disk peak |1173489B |128MiB; logical frozen input max25008700B при cap25165824B |
| Ready queue |Ready peak1/unacked peak1 |Actual quorum args max-length16/max-length-bytes16384 |
| Quarantine queue |Ready peak1/unacked peak0 |Те же actual16messages/16KiB args |

Rabbit CLI вернул **пустые byte gauges**: measured byte peak unavailable, не0.
Configured byte cap подтверждён фактическими quorum arguments; отсутствие gauges
не превращается в нулевую нагрузку. Изолированные ML TMPDIR `q2-temp` и
`q1-tail-temp` проверены пустыми. Backend temporary payload files не создаются по
source invariant memory/PG; **global backend /tmp measurement не выполнялся**.

Secret canary:56files проверены, leak_found=false, private environment исключён.
Q2/Q1-tail ML supervisors joined с exit0, sampler joined, bounded caffeinate
остановлен. После evidence capture teardown проверил ownership/labels и удалил
только2созданных test containers и2volumes. Все8существующих рабочих containers
сохранили IDs; unrelated mutations=false. Teardown record SHA256
`48d7f315e7d68291590d758e26f309c9f336e8c760356a63c51c3a773e6e9074`.
Это cleanup disposable proof стенда; C1 product GC отсутствует, retained operational
inputs/checkpoints не разрешено удалять как обычный rollback.

Техническая runtime-приёмка **internal synthetic-only PASS/VERIFIED**. Scientific
acceptance остаётся user-deferred/not_assessed, real/production enable запрещён;
remote publication и cloud CI/release не выводятся из этого runtime результата;
автономная публикация проверяется для bytes конкретной редакции комплекта.

## Воспроизведение из standalone checkout

Нужны Go1.25+, PostgreSQL16 с **отдельной disposable test DB**, RabbitMQ4.3.6 с
изолированным vhost `pulse-q2-synthetic`, отдельный supervised Q2 ML process из
проверенных установленных wheels с pinned synthetic fake. Подключите reviewed
contracts checkout обычным локальным Go workspace по README. Не используйте PREP
или operational database в TEST_DATABASE_URL.

Нужно **эксклюзивное окно proof в пределах одной PostgreSQL database**, даже если
тесты используют разные schemas: advisory locks74019321/74019323 общие для schemas.
Не запускайте параллельно mutating integration/ревью probes или другой process
proof на этой БД. Read-only sampling выполняется отдельно и не выдаётся за workload.

Из корня backend:

```sh
make check
make integration # TEST_DATABASE_URL уже задан в private process environment
make build
```

Integration создаёт временные схемы; обычный прогон делает SKIP actual-process
теста без explicit opt-in. Такой SKIP не подтверждает actual inference/recovery.
Schema migration —14, PostgreSQL server major —16. API/worker не мигрируют и не
создают synthetic guard самостоятельно; process test подготавливает собственные
100 wells/cohort/admin/guard в своей временной схеме.

Перед opt-in оператор задаёт следующие значения в **private process environment**;
в документацию/логи не помещают их секретные значения:

| Переменная test harness | Значение |
|---|---|
| `TEST_DATABASE_URL` | Отдельная PG test DB; test управляет только своей схемой |
| `Q2_API_BINARY`, `Q2_WORKER_BINARY` | Absolute paths проверенных `bin/api`, `bin/cohort-worker` |
| `Q2_PIN_PATH`, `Q2_CANONICAL_PATH` | Private regular files: typed `domain.ExecutionPin` JSON и trusted canonical seven-channel CSV |
| `Q2_ML_TARGET`, `Q2_ML_API_KEY` | Dedicated Q2 listener и согласованный внутренний ключ |
| `Q2_ML_COMPUTES_PATH` | Private bounded JSONL compute-start counter, создаваемый supervised test fake |
| `Q2_PRODUCER_AMQP_URL`, `Q2_WORKER_AMQP_URL` | Отдельные scoped producer/consumer URLs vhost pulse-q2-synthetic, без query/fragment; `?heartbeat=10` запрещён, adapter задаёт10s сам |
| `Q2_MAX_INPUT` | `1` для max-size valid full100 и oversize catalogue profile; не product env |
| `Q2_ML_CONTROL_DIR` | Private barrier directory для отдельного physical-stale scenario и соответствующего supervised test-only ML control |
| `Q2_SANDBOX_ROOT` | Absolute существующий private parent временного каталога proof |
| `Q2_HTTP_ADDR` | Свободный loopback HTTP address |

Убедитесь, что loopback59997/59998 свободны: harness использует их как намеренно
недоступные producer/legacy endpoints. `Q2_ML_COMPUTES_PATH` должен отражать реальные
вызовы original synthetic model; файл-заглушка не заменяет inference. Допустимая
test-only instrumentation оборачивает только `FixedModel.predict_proba`, всегда
вызывает original, пишет конечное число events и добавляет bounded задержку около
0.1s для crash window. Fit, подмена installed ML code и fabricated results не
используются. Instrumentation раскрывают и хешируют отдельно от installed wheel.

Точная команда materialized test harness:

```sh
Q2_PROCESS_PROOF=1 go test ./test/integration \
  -run '^TestCohortJobActualHTTPWorkerRestart$' -count=1 -v -timeout=8m
```

Для max valid profile та же команда получает `Q2_MAX_INPUT=1`. Другие actual cases
запускаются последовательно в том же exclusive window с новым evidence output и
согласованным supervisor/barrier environment:

```sh
Q2_PROCESS_PROOF=1 go test ./test/integration \
  -run '^TestCohortJobActualPhysicalStale$' -count=1 -v -timeout=8m
Q2_PROCESS_PROOF=1 Q2_MAX_INPUT=1 go test ./test/integration \
  -run '^TestCohortJobActualMaxCatalogueReject$' -count=1 -v -timeout=8m
```

Physical case требует `Q2_ML_CONTROL_DIR`;10s RPC deadline сохраняется даже при
server barrier. Он не доказывает successful late RPC. Transaction fencing отдельно
доказывается dedicated-claim-connection PG tests. Oversize profile требует сначала
actual successful valid startup control, затем только size-related rejection без
jobs/compute. Это следующий размерный профиль, не ровно один дополнительный byte.

Тест расположен в
test/integration/cohort_job_process_test.go (исходник `pulse-backend/test/integration/cohort_job_process_test.go`; в пакет не включён),
build tags не нужны. Его internal context6min; внешний supervisor ограничивает
жизнь child processes и останавливает только принадлежащие ему PID/process groups.
Команда требует уже подготовленных переменных таблицы и supervised ML/Rabbit/PG;
их имена `Q2_*` относятся к тесту, приложение читает `COHORT_JOB_*`.
Standalone воспроизведение не требует каталога внешних задач или credential files
другого checkout. Секретные environment/log/descriptor/counter artifacts храните
private; retained public evidence содержит только безопасные aggregate facts/hashes.

## Что проверяет actual process test

All100 scenario: durable202 при недоступном producer, freeze100 до compute,
API restart и тот же idempotent job/run; worker SIGKILL после≥5 accepted checkpoints,
restart с пропуском accepted checkpoints;100 scored с cohort_snapshot provenance,
один snapshot/run, zero alerts для pinned norm fake, cancel succeeded→409 actual
job. Допустимы100–102 compute starts: uncommitted compute может повторяться.

Mixed100 scenario:95 scored,1 archived,1 inactive,2 insufficient и1 error;
100 exact member IDs, non-scored result=null. Один insufficient получается через
actual ML по stopped F_out/Ia=0, второй — frozen marker. Source-unavailable marker
не маскируется нормой. До второго final commit read-side сохраняет прежний
published run. Это assertions harness; подтверждённый latest outcome приведён выше отдельно от
полного согласованного internal synthetic gate, без научной приёмки.

## Матрица обязательных доказательств

| Проверка | Подтверждённый scope и оставшаяся evidence-граница |
|---|---|
| Freeze/replay | All100 bytes/markers и identities до202; source modification/deletion после acceptance, worker resume без source re-read |
| Crash/recovery/fence | Kill API/worker, durable checkpoint continuity; physical session loss, late worker и unknown commit не дают accepted stale effect |
| Atomicity | Fault до/после final commit, один coherent snapshot/alerts/run/job/inbox; прежние snapshots JSON/digest сохранены |
| Authority/privacy | Loss role/scope/lifecycle/guard revokes whole job; full100 admin allowlist; restricted/by-run views без global Execution/payload/actor/token/secret |
| Coexistence | Q1/legacy races через common gate, unchanged same-key/terminal Q1 behavior; dedicated Q2 ML execution_busy и old physical tail |
| Retry/queue | Exact transient whitelist; finite attempts/backoff/generations; uncertain confirm, duplicate/oversized/malformed event, quarantine/cancel/expiry |
| Enforced budgets | Input/checkpoint/snapshot/storage/admission cap+1 reject без partial effects; retained terminal reservations сохраняются |
| Resource measurement | API/worker RSS≤512MiB each; Q2 ML≤1536MiB; Q1-tail+Q2≤3072MiB; PG growth≤768MiB/schema, WAL≤1GiB/full100, temp delta0, backend temp files0; fixture/bundle disk≤128MiB; queue≤16messages/16KiB each |
| Rollback | Stop admission, cancel/drain, остановить workers, сохранить PG inputs/checkpoints/results/schema14; old binary только после terminal/drain всех Q2 intents |

Для measurement сохраняют baseline и workload отдельно, wall time, sampling method,
actual peaks/deltas и ограничения платформы. DB cap — growth measurement, а не
равенство declared reservations. Source gate, зелёный Go suite и успешные два
process сценария по отдельности не закрывают всю runtime матрицу. Пределы не
повышают ради PASS. Новые source/binary/pin identities требуют нового binding
соответствующего evidence; исторический Q1 proof не переписывается.
