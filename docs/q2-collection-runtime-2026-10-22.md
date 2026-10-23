# Q2-C2 · runtime и воспроизведение, начало22.10.2026

Текущий статус23.10.2026. Имя файла сохраняет дату начала работы; времена событий
и окончательной приёмки отражают фактическую дату. Исторические C1 claims не меняются.

Этот документ отделяет зафиксированный source contract от фактической приёмки.
[Протокол](q2-collection-jobs.md), [C4](q2-collection-jobs.drawio),
[конфигурация](configuration.md#изолированный-q2-c2-synthetic-профиль),
[drain/rollback](operations.md#q2-c2-collection-recovery-и-откат).

## Source/module/actual статусы23.10.2026; публикация отдельно

| Gate | Статус | Что подтверждено |
| --- | --- | --- |
| Core source | FROZEN | 47 source/test/config/API paths поверх BASE `7fc37f6e737c252b37d6deef644ff444ba6f89a1`, branch codex/q2-collection |
| Core Senior | SOURCE APPROVED | C2-REV-01/02/03 CLOSED тем же Senior; full module условие выполнено |
| Full module gates | PASS | module-v2: make check17.398s / isolated make integration421.538s monotonic (package419.575s) / make build7.129s; все exit0 |
| Harness/reproducer source review | SOURCE APPROVED | Тот же Senior,SamplerSuccessDeadline30paths, H01–H20 CLOSED;78 Python source tests PASS,11build02 binary hashes проверены независимо |
| Actual A01–A11 | VERIFIED / ALL11 PASS | Один свежий FULL ACT14 `r8e1b5066a1e3`,592.801s full span; source-bound resource/ML0/canary/teardown проверены координатором |
| Docs/publication | PASS | Same Senior FINALDOC23 SOURCE APPROVED/D01–03 CLOSED; root sync/build/validate/source-check и freshly extracted standalone package PASS |
| Scientific | not_assessed | Горизонт 7–14 суток и качество прогноза C2 не измеряет |

Core files aggregate SHA256:
`cc149ad3ff38b4beb510df46aa963e80ef0de8847b8ac7c4d5774dd56aa5ffef`.
Это hash согласованного набора47paths с individual file hashes, не Git commit
или binary SHA. Core47 hashes неизменны после fixes. Latest approved SamplerSuccessDeadline30file harness freeze
SHA256 `9a9e8b2b5155807ac2ebbbeee1186bbc7cf3f95ccb3469733286710a0aa3a392`,
source tar SHA256 `aae7c3725876645f089807d627dc02fcb3d6bb4c14f9a2b519e73286d362c2fe`.
Build02 manifest SHA256
`e1dbd2ae53a864f8e45bc34b6bd68ea2404f97b77151bd2a676e61ce496a8d7d`
связывает11 native/Linux binaries; Senior проверил каждый hash. Python-only monitor/lifecycle/log corrections
не меняли47core+10Go helper sources и11binaries (57 compiled source paths). Source approval и module PASS не
являются actual process/resource приёмкой.
Полный ACT14 после этого завершён и принят отдельно по evidence ниже.
Tested source77paths (core47+harness30 с прежним README) локально зафиксирован commit
`1a824a63c068f828a9d4dfa663b8036cd426e191`. Offline binary VCS stamp остаётся BASE7fc dirty:
commit выполнен после build; exact content binding57compiled source/11binaries
подтверждён без повторной сборки. Post-run README обновлён только как docs-only delta;
его новые bytes не объявляются частью прежнего30file runtime freeze. Executable29
и core47 сохраняют проверенные hashes. SDK остаётся exact1d0b.../candidate0.1.2UNRELEASED.


## Сохранённые ограничения setup

Первый own setup stand01 остановлен supervisor: cumulative WAL delta
271501264 bytes превысила исходный ceiling 268435456 bytes. Это сохранённый FAIL
setup/module lane после накопленных targeted/historical checks, не успешная
capacity-проверка A01–A11. Счётчик не сбрасывается и cap не повышается задним числом.

Для полной исторической module regression выделена отдельная own MODULE lane
с заранее объявленными тестовыми ceilings: PG512MiB, Rabbit384MiB,
aggregate1536MiB; database512MiB, PGDATA2GiB, WAL delta1GiB,
physical pg_wal512MiB, temp16MiB, own files4GiB, ≤10min/stage.
Эти увеличенные test-only budgets не доказывают actual capacity C2.
Recorded failed/diagnostic stands сохраняют свои receipts и exact retirement;
исходные8 working identities не менялись. DIAGNOSTIC11 A07–A09 и DIAGNOSTIC12
nonempty A10 проверили оставшиеся пути, но не A11/fullspan acceptance.
Stand13 exact retirement выполнен после A11 FAIL. Следующий свежий полный ACT14
завершил все11 случаев PASS; actual acceptance ниже не переписывает прежний FAIL.
Actual fresh stand обязан использовать исходные
[C2 ceilings](q2-collection-jobs.md#схема-budgets-и-откат).

API host loopback порт — 8094: 8092 занят существующим процессом Pulse Data,
который сохраняется. Fixture host8093, PG55442, AMQP5675, management15675 — только
при свободном порте. Внутренний fixture origin остаётся `http://fixture:8080`.
Единственное host loopback exposure — bounded fixed stdio forwarder к exact own
сервисам: PG5432, Rabbit5672, API8000, fixture8080, management15672;≤16connections,
≤32KiB frames. Три redundant Docker `-p` PG/Rabbit удалены: одновременный bind с
живым forwarder давал address_in_use при Rabbit restart. Docker publications не
используются; internal egress-denied network, five-port preflight и caps сохранены.
Это test infrastructure, не arbitrary proxy и не permission на рабочие процессы.

До actual suite зафиксировано resource уточнение: дополнительный64MiB guard
действует **на каждый host process**, а не на сумму helpers. Original fixture
container64MiB и aggregate1536MiB не изменены. Aggregate учитывает actual.py,
legacy-probe, supervisor, forwarder, Docker CLI children и transient peaks.
Validator обязан отвергать UNKNOWN/missing/stale metrics, отсутствующие required
roles и gaps full-span supervision; source negative controls проверены, actual
измерений это пока не даёт.

### История попыток и source refinements

| Попытка | Сохранённый исход | Следующий source-only шаг |
| --- | --- | --- |
| ACT02 | Pre-actual UNKNOWN: monitor startup/attachment deadline | Narrow exact-own local Docker Engine monitor adapter; counters1s/fresh3s сохранены |
| ACT03 | Pre-actual resource FAIL: whole-file hashing превысил host RAM guard | Streaming SHA256 blocks≤65536 bytes; binary verification не ослаблена |
| ACT04 | Prepare PASS; A01 UNKNOWN при planned physical stop/monitor restart | Observed stop→running и реальное rearm в прежние3s, immutable failure receipt |
| ACT05 | A01 primary FAIL при Rabbit restart: redundant Docker publication/address_in_use; secondary fail-closed UNKNOWN | Удалены3 redundant Docker `-p`; sole fixed stdio bridge |
| ACT06 | A01–A03 PASS; A04 FAIL до fault: management metadata без gauges; secondary UNKNOWN сохранён | Own readonly90 Rabbit config, safe CLI UID, strict observed gauge readiness |
| ACT07 | A01–A05 PASS; A06 FAIL после PG-session termination, finite worker Exit1/OOMfalse, затем observer OperationalError/InterfaceError и secondary UNKNOWN | До нового run объявлен combined PG-session fault + guarded process restart, без same-process survival claim |
| ACT08 | A01–A06 PASS; A07 UNKNOWN: source-start observation8s истекло до нового claim, full suite не принят | Observation40s в прежнем300s case, независимый guarded stop/restore/restart каждого authority variant |
| DIAGNOSTIC09 | Account/source late-I/O subcases PASS; третий binding submit400 до source GET, A07 не принят целиком | Source save возвращает draft; intended state восстанавливается явным existing status PATCH |
| DIAGNOSTIC10 | A07 PASS, A08 terminal replay HTTP401, дальнейшие cases не приняты | Existing own management-user получил только exact C2vhost read/write, configure^$ |
| DIAGNOSTIC11 | A07/A08/A09 PASS; A10 SQL-formatting FAIL0.503s | Two drain LIKE clauses исправлены на bound params; application SQL не менялся |
| DIAGNOSTIC12 | Nonempty control PASS0.990s, затем A10 PASS5.060s; no A11/fullclaim | Accepted data сохранены через modeoff/drain/old readonly API |
| FULL ACT13 `rc5ee6b9a33dc` | A01–A10 PASS; A11 FAIL до teardown: одна incoherent API sampling row | Raw FAIL сохранён, exact retirement; prospective supervisor coherence/earliest deadline fix |
| FULL ACT14 `r8e1b5066a1e3` | ALL11 PASS, canonical A11/resource/ML0/canary/exact teardown verified | Final SamplerSuccessDeadline30/H20, original caps/proof/compiled sources unchanged; publication PASS |

Partial ACT06–08 и DIAGNOSTIC09/10 результаты не объединяются в fullsuite PASS.
Даже diagnostic4PASS не заменяет свежий полный A01–A11/full-span resource proof. Recorded stand retirement не является утверждением о полной очистке Docker
environment. Ошибки не превращаются в PASS после source fixes. Нельзя обновлять stop timestamps,
сбрасывать counters или исключать helpers ради caps. Source tests78PASS проверяют
mocked boundaries и meaningful negative controls, не actual capacity.

### Изолированные диагностические proofs, не full-suite приёмка

DIAGNOSTIC11 run `rf6ed2993be66` и DIAGNOSTIC12 run `r2779f3167b63` имеют разные
исходные состояния/source snapshots. Их PASS не суммируются с ACT08 или друг с
другом в официальный all-A01–A11 результат. Диагностики не дают full acceptance. Canonical ACT14 A11/resources/canary/ML0/teardown
позднее PASS одним fresh run; publication PASS отдельно.

| Run/case | Результат / elapsed | Содержательное наблюдение |
| --- | --- | --- |
| DIAGNOSTIC11 A07 | PASS29.517s | Account/source/binding revoked, capture0/effects0; реальные late-I/O окна7.072/7.063/7.001s; cancel terminal и late cancel сохраняет success |
| DIAGNOSTIC11 A08 | PASS94.197s | Тот же terminal event реально ignored/ACKed, ready/unacked0, source Fetch delta0; retry attempts3/backoffs5.980/16.002s, exhaustion3, permanent invalid1, worker+broker recovery |
| DIAGNOSTIC11 A09 | PASS65.465s | Genuine legacy expiry60.068s; stale Commit/Fail и hold claim отвергнуты; global protected Import/other-source409, unattributed conflict, credentials rejected до capture |
| DIAGNOSTIC12 nonempty control | PASS0.990s | До A10 есть3measurements/2captures/2checkpoints/2coverage/2logs; rollback preservation не проверяется на пустой БД |
| DIAGNOSTIC12 A10 | PASS5.060s | Guard admission off, nonterminal cancel, writers drained/active owned TX0, modeoff GET200/POST503, immutable BASE7fc old API со всеми source paused и PG readonly; durable accepted data unchanged |

Safe receipt SHA256 (целостность конкретных proofs, не общий acceptance):

- A07 `1a4b4b255b6eccc247a8373e6ffa5e739a0315f742a4869d364d7c49ce8deaa9`.
- A08 `abd53cadf28fe49fe1afee350897ceb444a93c731f586586564a14c29e9248fe`.
- A09 `3f2030ea66e2da6c372a61e39598ecf01e14eff42b6c9aca4884d41bb44f9c1d`.
- Nonempty control `32c02db88509d4af29203b836b67f61fd5a8be06708e92a6704faaeb58b8ee9d`.
- A10 `af542178ba694d91123146a4e9f96580aad179f9a332d06e7898c1d3b5ee8633`.

### FULL ACT13 failure и prospective sampler fix

FULL ACT13 завершил A01–A10 PASS, но canonical A11 отверг одну sample row без API
current/peak/ID после observed running того же ID. Это **whole-run FAIL**, не memory
breach claim. Исходная incomplete row/FAIL не удаляется, не дополняется и не
переклассифицируется. A11 failure SHA256
`fdbb2b5e55a67b249a22c0b16adc117f5b644aa1874744da5bc2f009027619b5`;
exact recorded retirement SHA256
`cc9c9dd55052e68586288987b0d5f43b9f8057054aa885ae17a1119f85c54170`.
Recorded own current/historical resources сняты, working8/ports сохранены;
неатрибутированный volume не назван удалённым, total environment cleanup не заявлен.

Новый [supervisor](../test/runtime/collection/supervisor.py) публикует только coherent
candidate: один fixed capture_cutoff связывает lifecycle/ledger, exact current ID,
frozen metrics/roles и sample.at. Genuine counter обязан принадлежать latest observed
running epoch; old same-ID counter не переиспользуется даже при age<3s. Mutation
до publication invalidates/retries candidate; событие после fixed cutoff относится
к следующей row. Нет invented timestamps/values/peaks или posthoc repair.

Earliest applicable3s guard учитывает last publication, latest genuine counter либо
attachment и waiting-for-first-current-epoch running. Он действует перед selector,
после processing, на **всех** deferral paths и непосредственно before output.write
после serialization/final coherence. Missing first counter bounded defers с горячими
остальными streams, затем UNKNOWN; slow metric/prewrite work не публикует late
successful row после crossing deadline. Original1s cadence/3s freshness/gap/startup,
proof.py, caps и57compiled sources не менялись. Source controls78PASS и frozen same
Senior H20 closure не исправляют ACT13. Следующий freshACT14 принят по отдельным
реальным receipts, не по source controls.

Optional administrative read-only saved-prefix audit после case помогает обнаружить
проблему раньше, но не фильтрует canonical A11 rows, не заменяет A11 и не является
standalone product prerequisite. Raw ACT13 prefix по строгому прежнему proof
по-прежнему FAIL. Следующий ACT14 принял все11 случаев одним run, без отбрасывания
canonical rows.

#### Management authority, safe diagnostics и drain SQL

[broker.py](../test/runtime/collection/broker.py) автоматически выдаёт existing own
bootstrap management-user права только в `pulse-q2c2-synthetic`: configure=`^$`,
read/write=`^pulse\.collection\.v1(\..*)?$`. Это позволяет terminal event publish и
queue get для proof, не открывая Q1/v2/foreign resources или global tags/permissions.
Runtime application credentials/grants прежние. Ручной широкий grant не требуется.

Safe run_case receipts сохраняют отдельно operation/snapshot fault origin: не более
32innermost frames, каждое поле только basename/function/positive line; direct
HTTP status только type int100..599, иначеnull. Source/locals/error message/URL/
header/body не экспортируются; original outcome, after:null и clock cleanup прежние.
Два A10 drain predicates используют `LIKE %s` с bound `collection-runtime-%`,
сохраняя observer exclusion/active transaction check и затем all-connection count.
Это исправление driver binding в harness, не product migration/API/query refactor.

#### Authority late I/O и восстановление source status

A07 account/source/binding variants ждут реальный source GET до40s внутри исходного
case≤300s. До authority mutation prearm-ится только exact own worker. Старый процесс
не убивается намеренно до настоящего monotonic late-I/O окна≥7s от observed GET;
затем проверяются persisted revoked, capture0, whole requested PK/checkpoint/coverage0.
Только после этого guarded physical stop → authority/session restore → same-ID/new
process restart для следующего независимого fault. Cancel semantics прежние; finite8
reconnect/durable budgets, sampling/caps/fences не меняются.

Существующий source Save **всегда** возвращает draft. Test helper требует200/dict/draft
после PUT; intended running/paused восстанавливает существующим status PATCH с200/
observed exact status. Requested draft остаётся draft. PATCH также вызывает SaveSource
и сбрасывает next_poll=now(), поэтому одного предварительного seed недостаточно.

Чтобы C2 proof не запускал unrelated legacy Schedule, running activation в own test
schema окружена временным BEFORE UPDATE OF config trigger: только exact trusted
source-a/source-b и NEW.status=running меняет **только** NEW.next_poll на +1day
атомарно с PATCH. Trigger установлен до PATCH и удаляется в finally до return;
cleanup failure — failure. Other IDs/paused/draft/direct UPDATE next_poll не затронуты;
поздний intentional due-update A09 остаётся реальным. Ни permanent product trigger,
ни migration/global pause/API/job/provenance/fence change не добавлены. Rollback-only
real-PG guard proof показал неизменные source/job hashes; это не full API acceptance.
Тот же helper восстанавливает running для A09 push/restore/credential сценариев,
чтобы draft rejection не выдавался за проверку credential guard.

#### Rabbit metrics readiness

[harness.py](../test/runtime/collection/harness.py) создаёт перед boot собственный
`90-collection-test-metrics.conf` с единственной строкой
`management_agent.disable_metrics_collector = false` и readonly single-file mount
в `/etc/rabbitmq/conf.d/90-collection-test-metrics.conf` exact own Rabbit. Config
сохраняется при restart; cached image/рабочие env/mounts не меняются. Env-only
override оказался неэффективен и не используется.
[broker.py](../test/runtime/collection/broker.py) запускает ping/plugins/rabbitmqctl
через `docker exec --user rabbitmq`, требует `{ok,false}` до readiness publication.
Это предотвращает root-cookie race на first boot.

Ready/quarantine gauges должны наблюдаться как четыре nonnegative **type int**
поля: messages, messages_ready, messages_unacknowledged, consumers;
messages=ready+unacked. Missing/bool/float/string/null/inconsistent значения не нули,
а readiness failure. Runtime допускает только fixed queue endpoints и bounded10s
ожидание; prepare требует обе очереди до actual boundary. A04 ждёт observed
unacked=1 **до** KILL; no-consumer/redelivery/counter controls сохраняются.
Реальный isolated readonly-config probe подтвердил gauges после first boot и
restart, но не является full actual acceptance. Original G6/case budgets прежние.

#### A06: PG session loss и явный restart процесса

В [case_a06](../test/runtime/collection/actual.py) заранее объявлен **combined fault**:
termination exact claimed PG backend внутри Apply и guarded TERM/restart worker1.
Harness держит Apply barrier, связывает blocked PID с job physical_backend_pid/fence,
сохраняет capture hash и source GET counter, prearm-ит expected-stop только exact own
worker1 до `pg_terminate_backend`. Если worker ещё running, TERM физически
останавливает его; racing TERM error допустим лишь после exact stopped/OOMfalse
proof. Никакие другие ошибки не маскируются.

Пока worker физически stopped и barrier удержан, проверяются весь requested PK set
и global values без новых effects, capture1/тот же hash, checkpoint0/coverage0.
Restart запускает **новый процесс в том же exact container ID**. Barrier остаётся до
настоящего30s lease expiry/reconcile и нового fence>old/new physical PG PID≠terminated
внутри Apply. Только затем barrier release и сравнение полного capture/provenance,
одного checkpoint и no-refetch/hash equality. Это source protocol контроль,
реальный combined recovery затем PASS в ACT14 A06; старый ACT07 FAIL остаётся историей.

[Consume](../internal/adapter/jobqueue/collection.go) имеет конечный lifetime budget
8reconnect attempts/backoff. После его исчерпания worker может fail-closed завершиться;
в opt-in proof оператор/оркестратор должен запустить новый процесс. Выживание того же
OS worker и бесконечный autoreconnect/in-process recovery не гарантируются. Restart
не восстанавливает старую lease/session, не reset-ит durable attempts, не делает
pool fallback/ACK shortcut и не меняет оригинальные30s lease/300s case budgets.

#### Monitor, lifecycle и log evidence

[own_engine.py](../test/runtime/collection/own_engine.py) использует stdlib AF_UNIX
и текущий local Docker context: exact recorded64hex ID/run-label/name/state,
fixed exact-ID-filtered metadata≤1MiB, только `/fixture monitor <allowlisted-role>`,
без Env/stdin/privilege/arbitrary endpoint. Background attachment не блокирует1s
sampling; fresh counter/attach deadline3s и full-span coverage обязательны.
Потеря monitor вне наблюдаемой fault/restart последовательности даёт UNKNOWN.

[build.py](../test/runtime/collection/build.py) проверяет source/binary digest
потоком≤64KiB; сравнение каждого SHA и исходные RAM ceilings сохранены.

После A05 optional worker2 можно перевести в terminal resource lifecycle `retired`
только после физически наблюдаемого stop, свежего supervisor ACK об удалении его
monitor/counters, сохранения complete logs, guarded exact-ID/run-label removal,
exact Engine absence и immutable safe removal receipt текущего run/того же ID.
Это resource event, **не** API job state и не исключение для живого stopped worker.
Required PG/Rabbit/fixture/API/worker1 этим правилом не исключаются. Обычный planned
stop остаётся bounded≤300s; historical/wrong-ID/missing/unproved receipt отвергается.

[actual.py](../test/runtime/collection/actual.py) до каждого worker2/API removal
экспортирует complete stdout/stderr; перед finish — current logs. Экспорт≤32768-byte
blocks, private0600/O_EXCL, deadline20s, original total logs64MiB, child wait4 peak
учтён. Manifest охватывает exact current∪retired container IDs с run/role/retired flag,
path/bytes/SHA/complete; missing/extra/changed/out-of-owned-dir input блокирует scan.
Scanner проверяет continuous **и** full logs обоих множеств плюс все объявленные
HTTP status/errors, queue и safe DB/export surfaces. Partial export не считается
complete. Canary/raw match content не попадает в report/exception.

## Свежий полный ACT14 · ALL11 PASS

Run `r8e1b5066a1e3`,23.10.2026, один source-bound stand с исходными G6 caps.
Координатор независимо сверил11receipts, source/core/harness/binaries, full-span
resource validator, повторный canary scan и post-teardown. Это internal synthetic
runtime acceptance, не production capacity/SLA, real source enable или ML science.

| Case | Actual наблюдение | Результат / wall |
| --- | --- | --- |
| A01 | Durable acceptance до client response; API/broker restart,1job/1outbox/reconcile | PASS / 5.815s |
| A02 | Intent immutable; pre-capture revision может смениться, post-capture fixture loss без refetch | PASS / 74.653s |
| A03 | Valid combined2MiB/2880raw/1440unique, body+1/raw2881 rejected; explicit-empty receipt | PASS / 3.421s |
| A04 | Held client ACK, same-event genuine redelivery/ignored, one accepted effect; rollback до checkpoint | PASS / 52.947s |
| A05 | SIGSTOP36.142s, real lease takeover fence1→4/attempt2, stale late effects0 | PASS / 39.407s |
| A06 | Combined inside-Apply PID134 loss + same-container/new-process restart, fence1→3/PID140, partial0/no Fetch | PASS / 39.767s |
| A07 | Account/source/binding revoked/capture0/effects0, real7.070/7.068/7.022s late I/O; cancel ordering | PASS / 29.625s |
| A08 | Same terminal event ignored/no Fetch; attempts3/backoff5.994/15.987s/exhaustion3/invalid1; worker+broker recovery | PASS / 94.434s |
| A09 | Real legacy expiry60.229s, stale Commit/Fail rejected; Collect/global Import/other-source409, unattributed/credential guards | PASS / 64.973s |
| A10 | All writers drained/TX0, new modeoff read/POST503, old BASE readonly/all sources paused, durable data preserved | PASS / 5.836s |
| A11 | Canonical full-span resources/canary/ML0/current∪retired logs/exact teardown/working8/free5 PASS | PASS / canonical final |

A01 означает «до доставки ответа клиенту»; upstream API уже мог записать response.
A03 фактические combined max/body+1/raw-row+1 controls отделены от остальных границ,
проверенных frozen unit/schema suite. A06 — combined fault/restart нового процесса,
не same-OS survival. Exactly-once source Fetch до committed capture не обещается.

### Resource, privacy и teardown evidence

Full span592.800758s,569 relevant samples из596 total;897tracked host-process/wait4
records. Reconstructed full-span aggregate peak **917348352B** и maximum sampled
aggregate **956289024B** — разные показатели; оба ниже1536MiB. Их нельзя смешивать
с суммой отдельных process/container peaks. Host max process50184192B ниже64MiB.

| Метрика | Actual bytes | Что измерено |
| --- | --- | --- |
| API / worker1 / worker2 sampled current peak | 14704640 /42094592 /11362304 | Max по samples, не cgroup lifetime peak |
| Fixture / PG / Rabbit sampled current peak | 20901888 /136355840 /215703552 | Original per-role caps сохранены |
| API / worker1 / worker2 cgroup peak | 14729216 /64053248 /12353536 | Max exact current∪retired IDs каждой роли |
| Fixture / PG / Rabbit cgroup peak | 21577728 /152686592 /222113792 | Genuine kernel peak, без reset |
| Database / PGDATA | 13138967 /70352879 | Own cluster |
| WAL delta / physical pg_wal | 5691768 /33554432 | Cumulative original counters |
| Temp delta / transient files | 0 /0 | Измеренные zero, не missing |
| Own stand / logs / evidence | 527143165 /1610326 /3802437 | Supervisor sampled maxima |

Counted forbidden ML methods=0. Canary matches=0 в2012647B/226declared surfaces;
complete current∪retired logs охватывают8exact container IDs. Exact owned containers/
volumes/network/images завершены/удалены по recorded ledger; working8unchanged,
all5ports free. Unattributed parked probe volume untouched; total environment cleanup
не заявляется. Accepted synthetic DB удалена вместе с own stand после safe evidence
export; rollback сохранность проверялась **до** teardown.

Source-bound safe final evidence SHA256:
`7b44ad0229903a72c121a15a567124602ea919852a6b54bab6e4f980c282dbf7`.
Canonical A11 final SHA256:
`3e9f736b294e02df59081e736217aaeeea49431a911b519598a6365d037f1733`.
Post-teardown SHA256:
`ef7fbdf69430606c801691af3c737440adc54d896a49f7ae21188b44345b7b3a`.
Raw private URLs/credentials/canary/payloads не включаются в этот документ.

Эта публикация фактов использует сохранённые receipts и independent root verification;
DOC не запускал повторно live stand. Docs-only shell/readiness guards ниже проверены
semantic external stubs/syntax, весь напечатанный operator recipe после этих docs
исправлений повторно целиком не выполнялся. Handbook sync/build/standalone ZIP gate
PASS по отдельным root receipts; code commit или runtime PASS сами по себе его не подменяют.

## Публикация23.10.2026 · PASS

Same Senior FINALDOC23 SOURCE APPROVED, D01–D03 CLOSED. Отдельные root gates
sync/build/validate/source-check PASS; freshly unpacked package validated без соседних
репозиториев, tasks и вложенного ZIP. Проверены125mapped snapshots/781public source
files,37HTML sections,32editable draw.io pages,676local links,306OpenAPI refs и
164checksums; ZIP bytes согласованы с папкой. Это проверка автономного комплекта,
не production rollout, cloud CI/release или SDK publication.

Архив **первого** успешного прохода сохранён отдельно:2924101B,
SHA256 `cb753490d7c20c5a7d41073dd804439647849e34b619f93f179d333e8c7edfae`.
Эта SHA описывает сохранённый первый архив; после фактического status refresh root
повторяет sync/build/validate/source-check/unpack и фиксирует SHA новой редакции
в отдельном publication receipt. Встраивания self-hash текущего ZIP в его sources нет.
Локальный docs commit оформляется отдельно от accepted runtime/source commit.

## Воспроизведение из standalone checkout

Ниже автономный operator bootstrap по source-owned runner interface. ACT14 full run
прошёл с этими scripts/CLI; post-run docs добавили fail-closed shell и bounded own
infrastructure readiness guards. Они проверены external stubs, без whole live rerun
напечатанного recipe. Source-bound actual results выше относятся к exact ACT14 freeze.
Все инструменты поставлены в [test/runtime/collection/](../test/runtime/collection/README.md),
внешняя task-папка и приватный baseline не требуются. Нужны macOS/arm64, Python3.11+
с уже установленным psycopg2, Go1.25 и offline module cache, локальный Docker context
с exact cached images, exact reviewed offline contracts SDK source module и Git object
`7fc37f6e737c252b37d6deef644ff444ba6f89a1` для immutable old API. Отсутствующий SDK,
image/cache или base object — prerequisite failure; harness не скачивает зависимости.
SDK должен соответствовать exact reviewed contracts source
`1d0b2b62d2cb73d18ab69738431089edf69621f8`, candidate `v0.1.2` **UNRELEASED**.
Released tag/cached released SDK не предполагается и не считается эквивалентным
только по version string. Соседний checkout допустим, но source может находиться
в другом локальном SDK checkout; нужен exact revision и чистое source tree. Для
module directory без Git требуется заранее проверенный source manifest от этого
exact revision, иначе prerequisite identity не доказана.

### Пути оператора и frozen build

Из корня backend задайте собственные абсолютные пути. BIN_DIR до build не существует;
RUN_ROOT новый и находится вне checkout. Во всех отдельных terminals нужны те же env.

```sh
set -eu
export REPO="$PWD"
export GO_BIN=/absolute/path/to/go
export PYTHON_BIN=/absolute/path/to/python-with-psycopg2
export CONTRACTS_DIR=/absolute/path/to/exact-reviewed/pulse-contracts-checkout
export RUN_ROOT=/absolute/new/own/runtime-run
export BIN_DIR="$RUN_ROOT/binaries"
export STAND_DIR="$RUN_ROOT/stand"
test ! -e "$RUN_ROOT"
mkdir -p "$STAND_DIR"
# Для SDK checkout: exact reviewed source, не предположение о released tag.
test "$(git -C "$CONTRACTS_DIR" rev-parse HEAD)" = 1d0b2b62d2cb73d18ab69738431089edf69621f8
test -z "$(git -C "$CONTRACTS_DIR" status --porcelain --untracked-files=all)"
```

До создания stand снимите собственный baseline всех текущих рабочих container IDs; предыдущий run защищал8. Имена/ID предыдущей задачи не требуются; cached images повторно проверяет preflight.

```sh
set -eu
"$PYTHON_BIN" - <<'PYCODE'
import json,os,subprocess
from pathlib import Path
root=Path(os.environ['RUN_ROOT'])
lines=subprocess.run(['docker','ps','--no-trunc','--format','{{.Names}} {{.ID}} {{.Status}}'],check=True,capture_output=True,text=True).stdout.splitlines()
(root/'baseline.json').write_text(json.dumps({'working_containers':lines},indent=2)+'\n')
PYCODE
```

Создайте source freeze из самого checkout; development untracked implementation files также включаются. Build сверяет bytes до/после compilation. Hash не заменяет independent source approval; SDK revision фиксируется отдельно.

```sh
set -eu
"$PYTHON_BIN" - <<'PYCODE'
import os,json,hashlib,subprocess
from pathlib import Path
repo=Path(os.environ['REPO']);root=Path(os.environ['RUN_ROOT'])
paths=subprocess.run(['rg','--files','-g','*.go','-g','*.sql','-g','go.mod','-g','go.sum','-g','test/runtime/collection/*.py','-g','test/runtime/collection/Dockerfile'],cwd=repo,check=True,capture_output=True,text=True).stdout.splitlines()
files={name:hashlib.sha256((repo/name).read_bytes()).hexdigest() for name in sorted(paths)}
(root/'source-freeze.json').write_text(json.dumps({'status':'FROZEN','base':'7fc37f6e737c252b37d6deef644ff444ba6f89a1','files':files},indent=2)+'\n')
PYCODE
"$PYTHON_BIN" test/runtime/collection/build.py --repo "$REPO" --output "$BIN_DIR" --go "$GO_BIN" --contracts "$CONTRACTS_DIR" --freeze "$RUN_ROOT/source-freeze.json"
```

Build использует CGO0/Darwin+Linux arm64/timetzdata/trimpath, GOPROXY/GOSUMDB=off и readonly modules; old API собирается из собственного immutable Git archive/workspace. До runtime authorization нужен успешный build и проверенные source/SDK/binary-freeze bindings.

```sh
set -eu
"$PYTHON_BIN" test/runtime/collection/harness.py preflight --state-dir "$STAND_DIR" --baseline "$RUN_ROOT/baseline.json"
"$PYTHON_BIN" test/runtime/collection/harness.py provision --state-dir "$STAND_DIR" --baseline "$RUN_ROOT/baseline.json" --allow-isolated-mutation
"$PYTHON_BIN" test/runtime/collection/manifest.py --state-dir "$STAND_DIR"
"$PYTHON_BIN" - <<'PYCODE'
import os,json,sys
from pathlib import Path
sys.path.insert(0,'test/runtime/collection');import harness
p=Path(os.environ['STAND_DIR']);b=Path(os.environ['BIN_DIR']);s=json.loads((p/'state.json').read_text())
for role in ('postgres','rabbit'):
    harness.inspect_owned(s,s[role+'_id'])
    harness.docker('cp',str(b/'linux-fixture'),s[role+'_id']+':/fixture')
s['helper_copies_bytes']=2*(b/'linux-fixture').stat().st_size
(p/'state.json').write_text(json.dumps(s,indent=2)+'\n')
PYCODE
```

Provision асинхронен. **До supervisor и первого broker.py bootstrap** дождитесь
read-only readiness обоих exact-owned containers. Проверка ниже имеет monotonic60s
readiness deadline, polls≤0.5s и child timeouts≤min(3s,remaining); bounded child cleanup
выполняется source-owned Process wrapper. Она не запускает services, не меняет
профили/метрики и не повторяет mutating add_vhost/add_user. Helper/children учитываются
через processes tracking; raw stdout/stderr probes не выводятся. Actual span и его
G6/case limits ещё не начались, monitor attachment/freshness3s остаются прежними.
Rabbit probe ждёт `check_running`, то есть запуска RabbitMQ application;
`ping` подтверждает лишь Erlang runtime/authentication и недостаточен для cold boot.
Семантика — [официальная RabbitMQ4.3 manual](https://www.rabbitmq.com/docs/4.3/man/rabbitmq-diagnostics.8).
Исходный broker.py bootstrap не меняется и вызывается один раз после этой готовности.


```sh
set -eu
if ! "$PYTHON_BIN" - <<'PYCODE'
import json,os,re,subprocess,sys,time
from pathlib import Path
sys.path.insert(0,'test/runtime/collection')
import harness,processes

def wait_owned_infrastructure(directory):
    state=json.loads((directory/'state.json').read_text())
    deadline=time.monotonic()+60
    def readonly(args):
        remaining=deadline-time.monotonic()
        if remaining<=0:raise TimeoutError('owned infrastructure readiness deadline')
        try:
            return processes.run(args,capture_output=True,text=True,timeout=min(3,remaining))
        except subprocess.TimeoutExpired:
            return None
    while time.monotonic()<deadline:
        ready=True
        for role in ('postgres','rabbit'):
            identity=state[role+'_id']
            if not re.fullmatch(r'[0-9a-f]{64}',identity):raise ValueError('invalid owned readiness ID')
            inspected=readonly(['docker','inspect',identity])
            if inspected is None or inspected.returncode!=0:
                ready=False;continue
            records=json.loads(inspected.stdout)
            if not isinstance(records,list) or len(records)!=1:raise ValueError('owned readiness metadata mismatch')
            record=records[0]
            harness.owned_id(record,state['run_id'],state['containers'])
            if record['Id']!=identity or record['Name']!='/'+state['project']+'-'+role:
                raise ValueError('owned readiness identity mismatch')
            if not record['State']['Running']:
                ready=False;continue
            args=(['docker','exec',identity,'pg_isready','-U','collection_test','-d','collection_test']
                  if role=='postgres' else
                  ['docker','exec','--user','rabbitmq',identity,'rabbitmq-diagnostics','-q','check_running'])
            probe=readonly(args)
            if probe is None or probe.returncode!=0:ready=False
        if ready and time.monotonic()<=deadline:return
        remaining=deadline-time.monotonic()
        if remaining<=0:break
        time.sleep(min(.5,remaining))
    raise TimeoutError('owned infrastructure readiness deadline')

directory=Path(os.environ['STAND_DIR'])
processes.track_self(directory,'infra-readiness')
try:
    wait_owned_infrastructure(directory)
except Exception:
    raise SystemExit('owned PG/Rabbit readiness FAILED; preserve state and run guarded teardown') from None
finally:
    processes.self_peak('infra-readiness')
print('owned PG/Rabbit readiness READY')
PYCODE
then
  "$PYTHON_BIN" test/runtime/collection/harness.py teardown --state-dir "$STAND_DIR" --allow-isolated-mutation
  exit 1
fi
```

Продолжать можно только после `owned PG/Rabbit readiness READY`. Timeout/foreign-ID/
ownership mismatch вызывает guarded exact-owned teardown и nonzero exit до broker
mutations; state/receipts сохраняются. Не повторяйте provision или mutating broker
bootstrap на частично созданном stand. `broker.py` вызывается ровно один раз после
readiness; его ошибка также требует preserve+guarded teardown+fresh stand, не retry
add_vhost/add_user. Readiness helper сам выполняет только inspect/pg_isready/check_running;
cleanup — отдельная failure branch shell-команды выше.

Запустите supervised processes в отдельных terminals, сохраняя outputs и accounting wrappers. Только loopback55442/5675/8094/8093/15675 и собственная internal network; существующий Data8092 сохраняется.

```sh
set -eu
"$PYTHON_BIN" test/runtime/collection/supervisor.py --state-dir "$STAND_DIR" --allow-isolated-mutation
"$PYTHON_BIN" test/runtime/collection/forward.py --state-dir "$STAND_DIR"
"$PYTHON_BIN" test/runtime/collection/broker.py --state-dir "$STAND_DIR" --allow-isolated-mutation
```

Provision уже создал own readonly90 collector config до Rabbit boot; redundant Docker
`-p` отсутствуют, host exposure только forward.py. Broker включает поставленный
rabbitmq_management через `--user rabbitmq`, проверяет collector `{ok,false}` и
создаёт own restricted vhosts/users. Install/restart отсутствует. До prepare нужны
READY broker/supervisor receipts; prepare дополнительно проверяет обе queue gauges.
Все current helpers автоматические: readonly90 config/collector readiness, exact
C2 management permissions, source activation futurepoll guard/finally cleanup,
safe fault frames и bound drain queries уже внутри поставленного harness. Не нужны
ручные task-specific SQL files, permission grants, source snapshots из tasks или
редактирование job/lease/retry counters.

На macOS до prepare запустите bounded tracked caffeinate wrapper в отдельном terminal. Его собственный/wait4 child peak учитываются в aggregate; настройки OS не меняются. Stop file создаётся после per-case PASS до finish либо при retirement failed stand.

```sh
set -eu
"$PYTHON_BIN" - <<'PYCODE'
import os,sys,time,signal
from pathlib import Path
sys.path.insert(0,'test/runtime/collection');import processes
p=Path(os.environ['STAND_DIR']);processes.track_self(p,'suite-wrapper');running=True
def stop(_sig,_frame):
    global running
    running=False
signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGINT,stop)
child=processes.Process(['/usr/bin/caffeinate','-dimsu','-t','2820'],role='caffeinate')
try:
    deadline=time.monotonic()+2820
    while running and time.monotonic()<deadline and not (p/'caffeinate-stop-request').exists() and child.poll() is None:time.sleep(.2)
finally:child.terminate();child.wait(3)
PYCODE
```

После source/binary review и explicit actual authorization привяжите существующее решение к новому run ID и exact binary manifest. Поле authority ниже заменяется реальной записью authorization; создание JSON не является independent approval.

```sh
set -eu
"$PYTHON_BIN" - <<'PYCODE'
import os,json,hashlib
from pathlib import Path
p=Path(os.environ['STAND_DIR']);b=Path(os.environ['BIN_DIR']);s=json.loads((p/'state.json').read_text())
gate={'actual_authorized':True,'run_id':s['run_id'],'binary_freeze_sha256':hashlib.sha256((b/'binary-freeze.json').read_bytes()).hexdigest(),'authority':'REPLACE with recorded source/binary/actual authorization reference'}
(p/'coordinator-gate.json').write_text(json.dumps(gate,indent=2)+'\n')
PYCODE
"$PYTHON_BIN" test/runtime/collection/actual.py prepare --state-dir "$STAND_DIR" --binaries "$BIN_DIR" --coordinator-gate "$STAND_DIR/coordinator-gate.json" --allow-isolated-mutation
```

Prepare создаёт own FROMscratch images offline, явно migrate/seed, проверяет valid startup и fresh monitors до actual-started. UNKNOWN/FAIL навсегда taint-ит stand: сохранить receipts, exact teardown, новая попытка без reset/reuse/cap change. Нужен новый binary output либо per-run copy approved11 binaries+identical manifest; старые image contexts сохраняются с failed attempt.

```sh
set -eu
(
  set -e
  for CASE in A01 A02 A03 A04 A05 A06 A07 A08 A09 A10; do
    "$PYTHON_BIN" test/runtime/collection/actual.py case --case "$CASE" --state-dir "$STAND_DIR" --binaries "$BIN_DIR" --coordinator-gate "$STAND_DIR/coordinator-gate.json" --allow-isolated-mutation
  done
  touch "$STAND_DIR/caffeinate-stop-request"
  "$PYTHON_BIN" test/runtime/collection/actual.py finish --state-dir "$STAND_DIR" --binaries "$BIN_DIR" --coordinator-gate "$STAND_DIR/coordinator-gate.json" --allow-isolated-mutation
)
```

Finish запускается только после10per-case PASS: declared surfaces scan, full-span/resources/canary/queue validation, затем exact own teardown. Tracked wrapper завершается до finish для wait4 peak evidence; RAM не исключается из aggregate. При failure — guarded harness teardown с сохранением истории:

```sh
set -eu
"$PYTHON_BIN" test/runtime/collection/harness.py teardown --state-dir "$STAND_DIR" --allow-isolated-mutation
```

Не применять docker prune и не управлять protected baseline resources. Во время
prepare используются local/research, explicit guard, ML_AUTORUN_ENABLED=false,
future source next_poll и counted forbidden-ML stub. Три обязательных module
команды `make check`, `TEST_DATABASE_URL=<own disposable PG> make integration`,
`make build` уже PASS на core-v2; ACT14 runtime acceptance VERIFIED, publication PASS.
