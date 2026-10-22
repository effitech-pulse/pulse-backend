> Снимок документа 22.10.2026. Источник: `pulse-backend/docs/q1-runtime-2026-10-21.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# Q1/F2 · synthetic runtime proof и сопровождение · 21–22.10.2026

Q1 source реализован и принят назначенным Senior в узкой synthetic-only области.
`FILE_CHECK_MODE=off` по умолчанию; `FILE_CHECK_ALLOW_REAL=false` обязательно.
Разрешены только server-owned manifest/accounts/well/cohort/guard/parse spec/pin
в выделенной test schema. Actual PREP schema13 не активирована; рабочий портал
этим proof не переключён. [Протокол/HTTP](q1-file-jobs.md),
[конфигурация](backend-configuration.md), [эксплуатация](backend-operations.md),
[редактируемая C4](../diagrams/q1-file-jobs.drawio).

Актуальный статус на22.10.2026: source и обязательные runtime gates **VERIFIED**
в synthetic-only области. Source DB/SW и narrow execution_busy fix **APPROVED**;
API-only resource proof и четыре исходных recovery случая **PASS**. Исходный
physical stale-zombie **FAIL** сохранён как исторический результат. Отдельный
post-fix stale probe22.10 **PASS / VERIFIED**, Q1-RUNTIME-01 **CLOSED** тем же Senior;
новая binary generation и границы доказательства описаны ниже.
Actual frontend/API QA: **10functional +9layout/keyboard +1copy PASS**, pageErrors0;
назначенный frontend-reviewer независимо проверил evidence и принял этот scope. Post-busy full
`make check` / isolated `make integration` / `make build` **PASS**.
Назначенный Senior закрыл все source/runtime findings. Финальная приёмка
координатором и публикация новой closure-редакции выполняются отдельно; прежняя
publication approval относится к прежним artifact bytes. Ниже результаты разных
binary generations явно разделены.

## Зафиксированные identities

| Binding | Точная identity / область |
|---|---|
| Accepted Q0 backend baseline | `eb28ef81f1748082989a97db79d933ed16b76c6f` |
| Q1 local implementation commits | backend `968142c1eebc721d1f21e18554f0dd3c4bbd9736`; frontend `771187e27051018e32d7a518bd24fc4893928447`; compute/pin bytes unchanged |
| Contracts / ML / model compute source | `272f23027e4a3ba636305b931f6ab83e5fca8166` / `e5cc61b3c282eb6abb446c81e1e800e7b7ad2c8a` / `f5c2a3de031ff4794194a219f048c9bf8c7c4f45` |
| SDK | 0.1.2 local **UNRELEASED**; exact installed wheels/runtime из [Q0 proof](q0-runtime.md), новый proto Q1 не добавляет |
| Исходный combined source manifest | SHA256 `611e78a67f5afd2f479b67d1bfb93fd4bc0edbc84cfcec751072da64198718f7`;55 paths, до busy fix; включает датированные docs, не является hash всего текущего checkout |
| Исходный API binary | SHA256 `dd46dc52f615469d8867019f9e86f1ef9ac07deb010ef92b4ed67440815e1457`,50594274B |
| Исходный worker binary | SHA256 `18fdd026ad7171e2a3fab4c6426ea198c30cc27910558a43c09e92e19f3bcb36`,40755234B |
| Post-busy source manifest | SHA256 `9b94a44cdb1eeca412c1c5bee7bb05feac4fc77a0ccdbfb372f4c3d2e8ba3882`;51 non-authored file hashes MATCH; authored docs имеют отдельный freeze |
| Post-busy paired API binary | SHA256 `56c2232788a443e6e5ab19337dec241d22cb7f41f804f19fb54eb637b92e7dcb` |
| Post-busy paired worker binary | SHA256 `1eff07d81019b85b33c34f6b3f604e95f60770b09d9b4ca375185ce17730087c`; rebuild PASS, physical/browser verdict отдельно |
| Busy delta |3 adapter files, patch SHA256 `b7bd40b1abceb46612682136c2540699aae0a30c7ff297dad0ec76430c68068d`; source APPROVED; новые paired binaries указаны выше |
| Busy source after hashes | `pinned.go`: `eb0569c63a83942b6ddd21bcce61312467defc83ff33379255c1e37b07dfe0b7`; `file_job_scorer.go`: `f794807fbce8915498932088cc2c5707b8fbc2407a1b11763cb49f6fae0aacd9`; test: `333484ee196cb6bdd21ad2198a37a1188d27c19beadd51882bd77ff9faf495c5` |
| Synthetic pin / immutable fixture manifest | SHA256 `4eb7fb6881560a5e5767fca967f7212cbf00cab5b7d8f6e48c43054e323fc734` / `83eacc7f954454eae12bd4e83b56522b5e0adaa51ed14a9ee18fb7441f029b20` |
| Synthetic params schema | SHA256 `fe7dc93f5236c2080670db48a14c41a3b2ba382f612ca334cdd182ae778cef35`; обычный schema.json для accepted W2/W3, amplification не применялась |
| Broker / client | RabbitMQ4.3.6 image digest `sha256:3e5cc23235b0eb850f604e8ace10d9a9b6d05be3e36730e53522ee9ead707a9e` / amqp091-go1.15.0 |

Go1.25.12, darwin/arm64; отдельные PG16 на loopback55481 и Rabbit5679,
HTTP8021 и resource-only8023. PREP PG55439 и integration lane55432 — другие БД.
Pin фиксирует synthetic комплект; точность или прогнозный горизонт он не доказывает.

## Source и обязательные проверки

До busy delta: `make check` exit0 (17.067s wall), isolated
`TEST_DATABASE_URL=... make integration` exit0 (85.112s wall; package83.191s),
`make build` exit0 (5.346s). DB/SW findings закрыты тем же Senior.
После delta: `make check` exit0 (4.875s wall), isolated `make integration` exit0
(83.465s wall), `make build` exit0;51 non-authored hashes совпали с post-busy
source manifest. Integration использовал отдельный bounded vhost на том же
собственном broker (max8connections/4queues), retained UI vhost не менялся.
Optional actual API/worker binary process gate в этой integration-команде не
включён; его отдельное evidence ниже не подменяется обычным integration PASS.
Независимо Senior выполнены scoped race:14 top-level tests PASS/0SKIP,
включая actual bufconn8-case busy/limits/Q0/Q1 matrix, и scoped vet PASS.
Public Q0 `UploadPinned` error mapper сохранён; новый backend-internal Q1 entry
сохраняет safe `execution_busy` только при exact RESOURCE_EXHAUSTED reason.
Другие resource limits не превращены в retry; raw RPC message не возвращается.
Unknown PG execution read не финализируется как semantic failure и не ack-ается.

Frontend source/fixwave APPROVED отдельным назначенным reviewer:90unit PASS/build
PASS,11focused+7related+2session controlled browser assertions PASS, pageErrors0.
Это intercepted transport, отдельно от следующего actual API evidence.
После actual copy fix итоговые92unit и build PASS; тот же reviewer принял узкую
3-file delta SHA256 `d56ea9afe7d77181599180a6cb63fa1a175687eea1ed08869331f0ac24c0ae65`.
Обычная отмена не показывает ложное предложение повтора; неизвестные reason codes
не выводятся пользователю. Targeted existing-cancelled-job browser assertion PASS,
новый ML job для этого не создавался.

На post-busy paired binaries actual frontend/API browser дал **10 assertions PASS**,
pageErrors0 по четырём JSON evidence исполнителя (submit/reopen/cancel/review).
Дополнительно9 assertions проверяют stable layout/keyboard для Light/Dark на
1440/1280/1024/390;1 targeted copy assertion проверяет завершённую отмену.
Суммы10functional+9layout+1copy разделены; это не полный pixel/WCAG/zoom audit.
Evidence review тем же frontend-reviewer **VERIFIED / ACCEPTED** для этой области:

- Реальный POST202 → закрытие browser → новый context читает saved result/review;
  reload не отправляет compute POST.
- До готовности result409; actual UI cancel200 → cancelled, result410.
- Concurrent review409 сохраняет mounted comment; explicit server revision reload
  сохраняет draft и позволяет explicit save200.
- Logout/401 очищает private draft/result; same-account relogin читает сохранённое
  review без stale draft/storage/compute POST; anonymous result401 не содержит projection.
- Owner-history cursor проверен только на8→7items с исключением cursor item;
  full25 boundary этим не проверен.

Эти UI runs шли со здоровым worker: worker restart, ML pause и другие fault signals
в них не выполнялись. Ранний backend actual production-binary restart PASS —
отдельное свидетельство, не переименование UI runs в restart proof.
Новые live Figma состояния, полный pixel/WCAG/zoom audit не заявляются.
Actual browser other-owner/roles, running-cancel, full25-row pagination и UI worker
restart в этом проходе не выполнялись. Они не выданы за PASS; серверные guards,
state races и legacy compatibility имеют отдельные integration/controlled evidence.

## API-only resource proof

Отдельный actual production API binary SHA256
`b86a47f59f47e54d6ae35469677ef20f4379adb31cc578aa602a91ea9c64674f`;
Go-source snapshot SHA256 `3e934a6823dbf29e9b630cc85232f287ec2427c9e24c9d9822a9aa03e3e110e7`.
Это отдельная сборка admission/read proof; worker, ML RPC и broker publish в этой
нагрузке отсутствовали. Server manifest SHA256
`f9fd1c57bbf7cb0821f22641621d738e581ff94fcfc09fc1453f6a48aa908a82`.

| Реальный synthetic input | HTTP / время | Сохранено |
|---|---|---|
| XLSX128columns, raw5187051B, expanded10705559B |202 /724.583ms |745rows, canonical35109B |
| CSV raw20971520B (exact20MiB),100000rows,64mappings |202 /1272.510ms |canonical14900842B |
| Raw20971521B (cap+1) |413 /25.037ms |0new inputs/jobs |
|100001rows |422 /137.649ms |0new inputs/jobs |
| XLSX129columns |422 /100.504ms |0new inputs/jobs |

Итог:2inputs/2jobs/2pending outbox/0attempts/0results;
logical retained charge41119532B. Три отказа квоту не расходовали.
Первый отдельный harness attempt получил корректный422 из-за forbidden пустого
schema_sha256; исправлен только клиент fixture, исходное отклонение сохранено.

|10 параллельных current-session readers | Count | p95 (nearest rank) | Max | Errors |
|---|---|---|---|---|
| Auth |385 |3.634ms |9.022ms |0 |
| Metadata list |470 |33.509ms |52.289ms |0 |
| Job status |300 |24.078ms |47.870ms |0 |

1155reads/0errors; все p95≤250ms и отдельные reads<1s stop threshold.
Окно — конечные submissions плюс1s, не sustained-load/general SLA.

| Resource | Фактически | Synthetic gate |
|---|---|---|
| API OS peak RSS (Darwin Getrusage) |451444736B (430.53MiB) |≤1GiB PASS |
| API sampled RSS (100ms) |425902080B |supporting |
| API temp |10704345B; после stop0B |≤128MiB PASS |
| Own schema relation+indexes+TOAST |7782400B |≤1GiB PASS |
| Cluster WAL delta |6868376B |≤2GiB PASS |
| Host minimum free disk |75753459712B |≥2GiB PASS |
| PG/broker VM free disk, before/after |≥393484345344B |≥2GiB PASS |

WAL — concurrent cluster delta, не атрибуция только двум inputs. Сжимаемые synthetic
bytes объясняют отличие physical PG7.42MiB от logical retained39.22MiB.
Idle/shared ML/Rabbit snapshots не являются large-input inference capacity proof.
ML RSS2GiB, broker RSS512MiB/volume target512MiB и free disk2GiB остаются
операторскими stop bounds; не все теоретические caps максимизированы одновременно.
Test exit0/PASS3.82s (package4.137s), API остановлен, port закрыт/temp0.
Machine report SHA256 `0742f7bd6c33fbcc12a1354d6b93970833dc8e710221657a1e683c52a394e364`.

## Recovery и исходный stale failure

Следующие пять случаев относятся к исходным combined API/worker hashes выше;
они не переименованы в результаты нового busy fix.

| Actual случай | Полученное свидетельство |
|---|---|
| Worker отсутствует до claim |PASS: POST202/pending/attempt0/result0; новый worker →1result/1attempt/1check/0ManualCheck |
| Physical SIGKILL во время outstanding real RPC |PASS: ML физически paused, worker→ML TCP ESTABLISHED и PG running/fence1; kill/replacement → result1, generation2/attempt2/fence4 через62.216s; SQL clocks не менялись |
| Stale zombie после replacement claim |**FAIL**: expiry→retry_wait10s→generation2/attempt2/fence3; replacement получил raw gRPC8/execution_busy, прежний sanitizer потерял reason → failed/pinned_input_unavailable/result0. Исходные failed job/timeline сохранены |
| Broker outage после durable acceptance |PASS: POST202/pending/attempt0/result0 при остановленном своём broker; restart того же container+volume →1result/1attempt |
| Actual transient ML TCP outage |PASS: один real TCP reset → gRPC14/retry_wait/ml_unavailable; прозрачное forwarding к тому же ML →1result/attempt2 |

Physical barrier доказывает outstanding transport и persisted lease, но не начало
feature code до pause. Старый ML compute мог продолжиться после expiry; accepted
result защищён fence. Общий исходный inventory6jobs/5results/9attempts/9outbox/
9inbox/0ManualCheck включает initial happy и5faultjobs. ML counters5features/
5predict относятся к recorded isolated window, включая stale outstanding request.
Это не exactly-once compute. Отдельный ack-loss test — application barrier после
commit перед ack с channel-loss/redelivery, не physical kill-after-commit.

Missing/corrupt pinned-bundle probes отдельно дали failed/pinned_input_unavailable,
result0/attempt1/features0/predict0/default-loader0/legacy-loader0; исходное раннее
ошибочное error-code свидетельство сохранено после source fix, не стёрто.

## Post-fix stale recovery ·22.10.2026

**PASS / actual VERIFIED; Q1-RUNTIME-01 CLOSED назначенным Senior.** После прямого
разрешения пользователя выполнен один оставшийся synthetic probe на новом
изолированном stand с fresh job/key. Это новый результат после busy fix;
исторические4PASS/1FAIL, прежние binary bindings и измерения выше не переписаны.

| Binding нового probe | Identity |
|---|---|
| Backend source / implementation | `2aec9c5011518cf72b58441e5d392baa48beecd5` / `968142c1eebc721d1f21e18554f0dd3c4bbd9736` |
| Fresh build | exit0,5.827s;51 non-authored source blobs совпали с принятой post-busy source manifest |
| API binary | SHA256 `79a8554817b3e12df652d03e1be4c7d11338cf2bac6667baaed7061c8760e7a3`,50594754B |
| Worker binary | SHA256 `edc2f3ab7cf41526fbff6e7e6ec58e00b37d7f5ea2efe7b75ae1ee19db10ef9e`,40755730B |
| Compute / fixture binding | Все7trusted ML fixture files, synthetic bundle/registry/pin/launcher совпали с прежними bytes; code body не менялся, новые Go binary hashes учитывают VCS metadata после commits |
| Final evidence manifest | SHA256 `4b06ab3064210cd53d4786f6ce79c06ef15a9609ed6d994b39e7a768dc194bc0`;15allowlisted artifacts size/hash независимо проверены Senior |
| Actual result / monotonic timeline | SHA256 `353e7b3458145ff64e61e5d1bb11af21983c12bb661ce6f6b52364d55540dc43` / `ca4b81dcc7959c40dc53e64a68e3c6ecef1b6bbafb1a31851313740b22939f27` |

Actual driver exit0,100.185s по monotonic,22.10.2026 08:19:59.194–08:21:39.377UTC.
Real ML feature entry08:20:01.184649UTC предшествовал физической паузе ML
08:20:01.195374UTC; старый worker был paused/replaced во время настоящего RPC.
Это более узкий фактически наблюдённый feature barrier нового probe, отдельно
от transport-only barrier исходной matrix.

| Attempt | Generation / attempt fence | Persisted outcome | Started → ended UTC |
|---|---|---|---|
|1|1 /1|lease_expired|08:20:00.438113 →08:20:46.538517|
|2|2 /3|retry / execution_busy|08:20:57.555451 →08:20:58.184230|
|3|3 /5|succeeded|08:21:32.547558 →08:21:33.643820|

Реальный45s lease истёк через46.100404s от claim. Persisted eligibility ровно
+10s после lease expiry и+30s после execution_busy retry; следующие claims
произошли после eligibility. SQL clock, budgets, retry count и caps не изменялись.

Победивший commit: succeeded/generation3/attempt_count3/final fence6,
result_count1 и один `q1_<jobID>` check, lease/next_attempt/error NULL. Независимый
READ ONLY PG запрос Senior подтвердил inbox3completed, outbox3superseded
(publish_attempts1 каждый; pending/leased0), свободный runtime slot и ManualCheck0.
Обычный owner HTTP reader получил сохранённый non-insufficient result.

Старый worker возобновлён после winner commit; через5.36s полный
state/check/fence/result_count оставался идентичен winner. Повторное независимое
PG чтение подтвердило ту же строку и единственный результат. Это finite
late-worker observation, а не гарантия ровно одного compute: за proof window
features2/feature_result2/predict2, whole fresh event file3each включает отдельный
bootstrap happy run. Legacy/default loader events отсутствуют. At-least-once
compute и один accepted effect сохранены; exactly-once compute не заявляется.

Новый probe закрывает только оставшуюся physical stale boundary. Он не расширяет
ранее принятый UI scope до UI worker restart/других ролей/full25 pagination,
не добавляет physical kill-after-commit, live Figma, реальные данные, production
или scientific acceptance. Прежний runtime/source/resource review не повторялся.

## Воспроизведение и rollback

Из корня backend, с exact reviewed contracts checkout через отдельный GOWORK:

```sh
make check
TEST_DATABASE_URL="$Q1_ISOLATED_TEST_DATABASE_URL" make integration
make build
go test -race -count=1 ./internal/adapter/grpcml ./internal/service -run 'TestFileJob|TestPinned'
go vet ./internal/adapter/grpcml ./internal/service ./internal/bootstrap
```

Для actual process proof требуется оператором подготовленная exclusive synthetic
PG, scoped broker users/URLs, accepted fixture directory/pin и работающий strict
synthetic ML. Test использует только w1.csv или w1-insufficient.csv и сам создаёт
unique schema, complete cohort, manifest и guard; порт8021 должен быть свободен.
Секреты задаются защищённым окружением без вывода значений. Установите
`TEST_DATABASE_URL`, `Q1_PRODUCER_AMQP_URL`, `Q1_WORKER_AMQP_URL`, `Q1_PIN_PATH`,
`Q1_FIXTURE_DIR`, `Q1_ML_TARGET`; pin/schema/fixtures должны совпасть с identities
выше. Затем из backend checkout:

```sh
Q1_API_BINARY="$PWD/bin/api" Q1_WORKER_BINARY="$PWD/bin/worker" \
  go test -count=1 -timeout=10m -v ./test/integration \
  -run '^TestFileJobActualHTTPWorkerRestart$'
```

Без explicit binaries/dependencies тест может SKIP: это не runtime acceptance.
Встроенные actual broker tests дополнительно требуют обе scoped URLs; DB-only
integration PASS не доказывает их запуск. Max CSV/XLSX resource и physical
SIGSTOP/SIGKILL/TCP-fault measurements — отдельные операторские probes, обычная
команда unit/integration их не воспроизводит. Этот compact runbook сохраняет
проверяемые boundaries, но не обещает cold-install без accepted synthetic artifacts.

Остановите admission; завершите bounded worker/dispatcher/reconciler и сохраните
PG/Rabbit volumes, inputs и pending намерения. Не выполняйте schema downgrade,
`down -v` или очистку retained inputs для rollback. Перед сигналами проверьте
точную ownership PID/PGID, после pause всегда SIGCONT в cleanup; чужие ML/portal
процессы не затрагиваются. Native `make build` включает worker; текущий Dockerfile
его не упаковывает, worker image/deployment этим proof не принят.

Cleanup первого stand после проверок21–22.10, до нового stale probe: собственные
test API/worker/ML/Vite штатно завершены; изолированные PG/Rabbit containers и их
volumes удалены, test listener ports закрыты; рабочие восемь containers сохранили
прежние identities. Этот исторический teardown относится к первому stand.
Новый stand probe22.10 очищен отдельно после независимой runtime verification:
API/worker/ML и parent supervisors завершились exit0; exact current/old test
processes отсутствуют, ports8021/65273/55481/5679 закрыты. Собственные2containers
PG/Rabbit и2volumes удалены; non-system schemas перед удалением PG0; все8рабочих
containers сохранили identities. Все15frozen evidence hashes после cleanup
по-прежнему MATCH. Это normal teardown нового stand, не новый capacity benchmark.

Cold install UNTESTED; release/tag/checksum/remote fetch/cloud CI OPEN;
real-private TTL/encryption/operator access/retention/backup policy OPEN.
Научная приёмка user-deferred/not_assessed: новые precision/recall, горизонт7–14сут,
обучение, promotion, production/HA/SLA/RPO/RTO здесь не подтверждены.
