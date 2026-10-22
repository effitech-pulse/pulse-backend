> Снимок документа 22.10.2026. Источник: `pulse-backend/docs/q2-cohort-jobs.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# Q2-C1 · долговечный прогон cohort

Редакция исходников 22.10.2026. Реализован отдельный внутренний synthetic-only путь,
выключенный по умолчанию. Source APPROVED, все8findings CLOSED; bounded owned-runtime
WAIT исправление принято, latest module gates и все3actual cases PASS. Runtime
VERIFIED в internal synthetic-only scope, включая physical stale, capacity, measured
resources и teardown; публикационный контроль комплекта учитывается отдельно. Source review, actual runtime и публикация — отдельные gates; их статус
приведён в [датированном свидетельстве](q2-runtime.md).
Научная приёмка `not_assessed`; прогноз за 7–14 суток остаётся целью.
Collection — отдельный следующий slice. Рабочий портал, refresh, расписание,
синхронная загрузка и Q1 private saved checks этим профилем не переключаются.

## Приём и неизменяемые входы

API загружает доверенный серверный каталог synthetic fixtures. Клиент выбирает
только `fixture_id`; он не передаёт telemetry, пути, pin, actor или membership.
До ответа202 одна PostgreSQL transaction фиксирует run, job, полные100 участников,
manifest, immutable input bytes либо explicit no-compute marker, reservations и
outbox. Compute и успешный Rabbit publish для202 не нужны. Неполная подготовка не
принимается. `as_of`, версия/hash cohort, порядок100 уникальных Pulse ID,
metadata/lifecycle, capture recipe и полный execution pin заморожены.

Перед current lifecycle overlay проверяется весь trusted fixture. Archived/inactive
участник получает frozen marker; нет выдуманного входа или риска0. После admission
изменение membership/lifecycle/полномочий отзывает целый job, а не меняет его inputs.

Archive timestamps сравниваются как nullable абсолютные моменты (`time.Time.Equal`):
UTC frozen value и Europe/Moscow PG value одного instant не означают lifecycle
drift. NULL/реальное изменение времени остаются различимы; rights/active/membership
checks не ослаблены. Initial mixed false-revocation и исправление сохранены в proof.

Строгий C1 CSV имеет UTF-8 header в точном порядке:

```text
timestamp,CosF,F_out,Ia,Motor_winding_temperature,Ped_load,Pump_intake_pressure_kgs,qg
```

Это семь измерений плюс timestamp. Общий ресурсный потолок32 channels не означает
поддержку32 произвольных каналов: текущий валидатор принимает ровно эти семь.
Времена — UTC RFC3339Nano с `Z`, строго возрастающие, не позже `as_of`, в пределах
предыдущих60 суток; диапазон данных30–60 суток. Пустое измерение означает пропуск;
finite числа и валидные нули сохраняются. Требуются LF, конечный newline, отсутствие
CR/NUL и не более1KiB на поле. Bounds end — last timestamp +1ns.

Recipe — `cohort_fixture_capture_v1` / `none_supplied_canonical`: API принимает уже
канонические synthetic bytes, не выполняет новую нормализацию архива. Производитель
fixture очищает sensor markers9999/999.9 по конкретному каналу до агрегации,
сохраняет нули простоя и исключает уставки из измерений. Этот профиль не является
универсальным IRZ importer и не доказывает готовность real telemetry capture.

`CaptureRecipe.BackendBuildSHA256` — SHA256 **API executable**, фактически
загрузившего каталог. Descriptor создают после сборки API. Worker — другой binary:
при старте он читает trusted descriptor/policy/fixture permits и проверяет PG guard,
но не перечитывает исходные input files и не сравнивает capture SHA с собой.
Accepted job продолжается по immutable PG bytes после удаления/изменения исходных
fixture files. Descriptor остаётся доверенной текущей policy; его произвольная
замена не разрешена. Actual worker SHA фиксируется отдельно в runtime evidence.

## HTTP

Полный контракт — [OpenAPI](../contracts/openapi.json). Все три маршрута требуют local
session активного admin из server allowlist и explicit current AccessScope на
всех100 участников. Admin shortcut здесь не применяется. Изменяющие запросы
проверяют Origin; ответы имеют `Cache-Control: no-store`.

| Запрос | Результат |
|---|---|
| `POST /api/v1/ml/cohort-jobs`, JSON `{"fixture_id":"synthetic100"}`, `Idempotency-Key` |202 после полного freeze+PG commit; `Location=status_url`, `Retry-After: 2` |
| `GET /api/v1/ml/cohort-jobs/{job_id}` |200, persisted status/progress из PG; без ML, broker и чтения source files |
| `POST /api/v1/ml/cohort-jobs/{job_id}/cancel`, JSON `{"revision":1}` |200 с actual job; при revision/terminal conflict409; terminal conflict содержит `code` и actual `job` |

Key —16–80 символов `[A-Za-z0-9_-]`, fixture ID —1–80 таких символов. Namespace
связан с actor; commitment включает actor, fixture ID и trusted fixture SHA.
Повтор того же key/commitment возвращает тот же job/run без второй квоты.
Другой commitment под тем же key даёт409. Новые inputs/as_of/pin требуют нового
намерения. JSON body≤1024bytes, неизвестные поля и второй JSON документ запрещены.

| HTTP | Значение |
|---|---|
|400 | Malformed body/key/revision |
|401 /403 | Нет session / недопустимая роль или Origin до lookup |
|404 | Fixture/job недоступен, отсутствует либо current full scope/allowlist не разрешают job |
|409 | Изменённый commitment, устаревшая revision или terminal race |
|429 | Admission capacity; `Retry-After: 10` |
|503 | Feature выключена либо необходимая зависимость недоступна |

Status содержит opaque `id`, decimal-string `run_id`, state/revision/times,
safe nullable error, attempt/max_attempts, `completed_count`, `total_count=100`,
outcome counts, `as_of`, cohort version и URLs. Только succeeded имеет
`result_url=/api/v1/ml/runs/{run_id}/snapshot`. Нет inputs, actor, credentials,
lease/fence token, pin inventory. `redrive_available=false`; API list/retry/redrive
не предусмотрены. Опубликованный snapshot читается существующим scoped route;
restricted/empty projection очищает глобальный `execution`, full100 сохраняет его.

## Исполнение и публикация

PG — authority для intent, event/generation, lease/fence, rights, attempts,
checkpoints и ack disposition. `bin/cohort-worker` объединяет отдельные dispatcher
(1s), reconciler(5s) и consumer(prefetch1). Rabbit envelope≤1024bytes содержит
только `version=1`, `kind=cohort.ready.v1`, opaque `event_id`, `job_id`, generation.
Telemetry, paths, actor/session, payload, pin и lease token в broker не попадают.

Claim удерживает dedicated PG connection с session advisory lock74019321.
Next/renew/checkpoint/final transactions исполняются на **той же connection**;
потеря session не может оставить параллельный pooled transaction публикующим
результат. In-process token→connection registry служит только locator, не хранит
authority. DB time, live ownership, lease, fence, token, current full100 rights и
manifest/input/pin identities проверяются повторно. Heartbeat и SQL сериализованы
с bounded context; Close закрывает claim connection.

Worker последовательно обрабатывает missing checkpoints и вызывает строго
`UploadCohortPinned` с `source_kind=cohort_snapshot`, bytes/hash/fingerprint и полным
expected pin. Q0/Q1 `UploadPinned`/`UploadFileJobPinned` остаются uploaded_file-only.
Wire/SDK version не менялись; semantic capability требует Q2 ML source. Fallback
на обычный Upload, latest bundle или другой pin отсутствует.

Перед compute persisted счётчик старта увеличивается; no-compute marker не тратит
compute start. Accepted checkpoint immutable по job+well; identical digest повторно
принимается только при текущем fence. После crash accepted checkpoints пропускаются.
Compute до checkpoint commit может повториться: exactly-once compute не обещается.
Неизвестный PG/commit outcome оставляет delivery unacked до durable resolution.

Staged checkpoints показывают прогресс, но не меняют фондовый риск/ярус/alerts.
До финала читатели получают прежний опубликованный snapshot. Финализатор перечитывает
ровно100 matching persisted outcomes и одной fenced transaction публикует snapshot
format2/digest, alerts, run done, job succeeded, inbox и business action. Ошибка до
commit откатывает все эффекты; replay после неизвестного commit не создаёт второй
accepted snapshot/alert set. Cohort status transfer сохраняет Pulse ID + cohort
version и explicit reset; research transfer по Unitek остаётся отдельным.

| Outcome | Result |
|---|---|
| `scored` | Actual pinned norm/watch/alert result с provenance/decision rule |
| `archived` / `inactive` | Frozen lifecycle marker, null result |
| `insufficient` | Frozen insufficient_telemetry либо actual ML insufficient; null result, ML diagnostics/provenance сохраняются |
| `error` | Trusted synthetic_source_unavailable marker, null result |

Invalid/corrupt/missing input, identity/policy/runtime/bundle mismatch или неизвестный
verdict завершают job failure; они не становятся per-well нормой. Snapshot имеет
`execution.replay_status=immutable_inputs_pinned`; прежний `provenance_status=partial`
сохраняется. Это software replay identity, не scientific acceptance. Исторические
snapshots/JSON/digests не переписываются и не объявляются replayable.

## Состояния, retry и совместимость

`pending_dispatch → queued → running → succeeded`. Transient failure переводит в
`retry_wait`; возможны `cancel_requested`, `quarantined` и terminal
`failed|cancelled|expired|revoked`. Completed count — число persisted checkpoints,
а не количество ML calls или scored wells.

Retry whitelist: `ml_unavailable`, `ml_deadline`, exact `execution_busy` и recovery
истёкшей физической попытки. Общий ResourceExhausted не retryable. Delays10/30s;
не более3 claims,3 compute starts/member и102/job. Lease45s, heartbeat10s,
renew5s; job lifetime30min, attempt≤20min с резервом10s до deadline для final,
RPC10s, final10s. Cancel pending/queued/retry/quarantined завершается сразу;
running→cancel_requested увеличивает fence и запрещает дальнейший accepted effect,
reconciler завершает cancellation. Уже победивший final commit возвращается как
actual terminal conflict. Logout/истечение browser session само по себе job не
отменяет; потеря actor active/admin/scope/allowlist либо lifecycle/guard drift
отзывает весь job.

Common transaction gate74019323 координирует Q2, Q1 и legacy. Active Q2 исключает
новые Q1/legacy execution; accepted nonterminal Q1, включая quarantine, исключает
Q2 admission. Q1 metadata gate ждёт bounded≤5s, сохраняя concurrent same-key replay. Q2 admission,
legacy Reserve, new Q2 claim, cancel/outbox/reconcile и прочие не-owned paths
сохраняют TRY; принятая узкая owned-runtime WAIT policy описана ниже. Поздний terminal/superseded Q1 event получает durable
disposition до cross-kind capacity check. Q1 Redrive semantics сохранены.
Legacy cleanup/Fail исключают cohort-job-owned runs. Это узкая capacity integration;
существующие Q1/legacy HTTP и результаты сохраняются.

Dedicated Q2 ML target отличается от legacy target. Pinned execution в каждом ML
runtime≤1; old physical tail может вернуть execution_busy. Совместное измерение
Q1-tail+Q2 ML не является production capacity proof всех synchronous clients.
См. [редактируемый C4](../diagrams/q2-cohort-jobs.drawio).

### Принятое уточнение owned-runtime gate

После сохранённого failed actual retry_wait44/100 реализовано bounded blocking
acquisition **того же** transaction advisory gate74019323 только для Next/Renew/
Checkpoint/Finish уже владеющей claim connection. Q2-DB-03 CLOSED тем же Senior;
latest module gates и неизменённый max/restart/mixed/rollback actual rerun PASS.
Physical/capacity/resources/teardown evidence также PASS в internal synthetic-only
scope; научная и production приёмка из этого не следуют. Admission/new claim/legacy/cancel/outbox/
reconcile остаются TRY; Q1 bounded same-key rule сохраняется. Global helper не
заменяется на WAIT для всех callers.

Next/Renew/Checkpoint имеют **total5s**, включая ожидание локального serializer,
Begin, gate, повторные проверки, writes и commit. Finish сохраняет **total10s**;
только его gate query ограничена `min(remaining caller deadline,5s)`. Нового retry
loop, дополнительных5s после ошибки или увеличения job/lease/RPC budget нет.

Physical lock уже принадлежит session. После local serialization transaction
сначала получает gate, затем authority/member/job row locks; после ожидания заново
проверяются DB time, current authority, lease/fence/token/state и physical ownership.
Expired lease, cancel/revoke/session loss не превращаются в право записи. Другие
gate holders лишь TRY physical lock и не ждут owned serializer, сохраняя lock order.
Gate освобождается commit/rollback **до** compute/ML RPC/AMQP.

Короткая успешная координация не должна терять healthy claim, создавать новый
generation/attempt или лишний member start. Реальный context/SQL/session error
сохраняет rollback/error/unacked recovery; renewal error не подавляется. Heartbeat10s,
lease45s и renew total5s прежние. Strict FIFO/fairness и завершение при произвольной
нагрузке не обещаются. API status read не берёт capacity gate; polling не объявлен
его непосредственным holder без PG evidence. При превышении существующего bound
исследуется critical section/holder, а не повышаются timeout или caps.

## Ограничения C1

| Ресурс | Предел |
|---|---|
| Active jobs |1 global и1/account; quarantine nonterminal |
| Retained jobs/storage reservation |8 global/4account,512/256MiB; reservation=input+36MiB; terminal данные квоту не освобождают |
| Members/manifest | Ровно100; metadata≤4KiB/member; manifest≤512KiB |
| Canonical input |≤512KiB/member,≤24MiB/job,2–2000rows,30–60days; C1 ровно7 измерений в общем потолке32 |
| Checkpoints/snapshot |≤256KiB/member,≤16MiB суммарно; snapshot≤20MiB; truncation отсутствует |
| Catalogue/admission |≤2fixtures/48MiB; startup context30s, HTTP10s, обычная DB operation5s |
| Outbox |≤8generations,≤6publishes/generation; lease15s, delays1/2/5/10/30s; queued без claim60s может получить следующую generation |
| Rabbit | Ready/quarantine quorum≤16messages/16KiB каждая; TTL30min/1h, ready delivery-limit5; confirms+mandatory, one publish in flight |

Исчерпание outbox budget даёт quarantine; без redrive остаются cancel/expiry.
GC в C1 нет. Schema14 применяется явно на PostgreSQL16; migration не создаёт
synthetic guard и не включает feature. Snapshot run_id пока INTEGER: admission
выше2147483647 отклоняется с rollback. Live PREP migration, real-private policies,
remote release и промышленная приёмка — отдельные gates.

Измеряемые runtime gates: API/worker≤512MiB RSS каждый, Q2 ML≤1536MiB,
Q1-tail+Q2 ML≤3072MiB; PG growth≤768MiB/schema, WAL delta≤1GiB/full100,
PG temp_bytes delta0, backend temporary files0, fixture/bundle disk≤128MiB.
Это acceptance budgets для изолированного proof, не OS hard limiter и не SLA.
Изолированный measured profile принят в [датированном proof](q2-runtime.md):
API239.25MiB/worker43.59MiB/Q2ML160.02MiB, same-sample combinedML247.77MiB;
PGschema7.87MiB/WAL73.48MiB/tempdelta0. Rabbit bytepeak unavailable при проверенных
actual16messages/16KiB args, backend temp — source invariant без global/tmp sampler.
Новые production workloads этим не измерены. Порядок запуска/recovery/rollback:
[operations](backend-operations.md#q2-c1-synthetic-worker--recovery).

## Источники реализации

- Domain/CSV/identity (исходник `pulse-backend/internal/domain/cohort_job_identity.go`; в пакет не включён), typed protocol (исходник `pulse-backend/internal/domain/cohort_jobs.go`; в пакет не включён).
- Service (исходник `pulse-backend/internal/service/cohort_jobs.go`; в пакет не включён), worker (исходник `pulse-backend/internal/service/cohort_job_worker.go`; в пакет не включён), HTTP (исходник `pulse-backend/internal/transport/httpapi/cohort_jobs.go`; в пакет не включён).
- PG admission (исходник `pulse-backend/internal/repository/postgres/cohort_jobs.go`; в пакет не включён), authority (исходник `pulse-backend/internal/repository/postgres/cohort_job_auth.go`; в пакет не включён), transitions/publication (исходник `pulse-backend/internal/repository/postgres/cohort_job_transitions.go`; в пакет не включён), outbox (исходник `pulse-backend/internal/repository/postgres/cohort_job_publish.go`; в пакет не включён), [migration14](../schemas/postgres/cohort_jobs.sql).
- API catalogue/policy (исходник `pulse-backend/internal/bootstrap/cohort_jobs.go`; в пакет не включён), worker bootstrap (исходник `pulse-backend/internal/bootstrap/cohort_worker.go`; в пакет не включён), queue (исходник `pulse-backend/internal/adapter/jobqueue/cohort.go`; в пакет не включён), strict cohort adapter (исходник `pulse-backend/internal/adapter/grpcml/cohort_job_scorer.go`; в пакет не включён).
