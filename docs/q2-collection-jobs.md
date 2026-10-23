# Q2-C2 · конечный сбор синтетической телеметрии

Редакция23.10.2026; работа начата22.10.2026. C2/1 — отдельный default-off backend-модуль. Он принимает
один source/well binding и конечный минутный период, сохраняет один проверенный
HTTP capture, затем атомарно записывает весь набор measurements и receipt покрытия.
Стадии source, review, module, actual runtime и публикации разделены в
[датированном свидетельстве](q2-collection-runtime-2026-10-22.md).
[Редактируемый C4](q2-collection-jobs.drawio) имеет две страницы.

Область: только локальный controlled synthetic fixture. Рабочий сборщик, портал,
PREP, real source enable и научная приёмка не переключаются. C2 не вызывает ML,
не создаёт прогнозы, риск, алерты или cohort snapshots. Исторические
[Q2-C1](q2-cohort-jobs.md) и [Q1](q1-file-jobs.md) сохраняют свои протоколы и evidence.

## Источники поведения

- [Типы/коды/статус](../internal/domain/collection_jobs.go),
  [canonicalization/manifest/период](../internal/domain/collection_identity.go),
  [строгий capture parser](../internal/domain/collection_capture.go).
- [HTTP DTO](../internal/transport/httpapi/collection_jobs.go),
  [маршруты](../internal/transport/httpapi/routes.go), [OpenAPI](../api/openapi.json).
- [PG store/acceptance](../internal/repository/postgres/collection_jobs.go),
  [authority/ownership](../internal/repository/postgres/collection_job_auth.go),
  [переходы/Apply](../internal/repository/postgres/collection_job_transitions.go),
  [dispatch/reconcile](../internal/repository/postgres/collection_job_publish.go).
- [Shared measurement writer](../internal/repository/postgres/telemetry_writer.go),
  [legacy source fencing](../internal/repository/postgres/telemetry.go),
  [schema15](../internal/migrations/collection_jobs.sql).
- [Worker](../internal/service/collection_job_worker.go),
  [dispatcher](../internal/service/collection_job_dispatcher.go),
  [HTTP source](../internal/adapter/telemetry/collection.go),
  [AMQP](../internal/adapter/jobqueue/collection.go),
  [bootstrap](../internal/bootstrap/collection_jobs.go).

## HTTP

Пути ниже используют default prefix `/api/v1`; `API_V1_PREFIX` может его изменить.
Это `/collection-jobs`, без сегмента `/ml`. Все три маршрута требуют текущий
персональный local session с role=admin. Дополнительно проверяются текущий owner,
allowlist и explicit fund/НГДУ/цех scope; admin не обходит scope. Изменяющим запросам
нужен разрешённый Origin. Legacy API key не заменяет сеанс. Ответы имеют
`Cache-Control: no-store`; raw/canonical bytes и download route отсутствуют.

| Метод/путь | Тело | Успех |
| --- | --- | --- |
| POST `/api/v1/collection-jobs` | `replay_key`, `fixture_id`, `source_id`, `start`, `end`; ≤4096 bytes | 202, `Location` и persisted status; replay тоже 202 |
| GET `/api/v1/collection-jobs/{job_id}` | Без тела; job ID = 32 lowercase hex | 200 current persisted status |
| POST `/api/v1/collection-jobs/{job_id}/cancel` | `{"revision":1}`; ≤128 bytes, revision 1..9007199254740991 | 200 status |

DTO принимает ровно известные поля, один JSON object; duplicate keys и второй
JSON document отвергаются. Пример submit (selectors должны существовать в manifest):

```json
{"replay_key":"collection_demo_0001","fixture_id":"dense-v1","source_id":"source-a","start":"2026-10-19T00:00:00Z","end":"2026-10-19T00:01:00Z"}
```

Replay namespace — `(actor_account_id,replay_key)`; key содержит 16–64 символа
`[A-Za-z0-9_-]`, в БД хранится digest. Source/fixture selectors имеют 1–64 таких
символа. Эквивалентный UTC период/source/fixture возвращает прежний job, включая
terminal; bytes upstream при этом не перечитываются. Иное намерение под тем же
ключом даёт `409 replay_conflict`. Чужой owner и missing ID получают одинаковый
`404 collection_job_missing`.

| HTTP | Safe message |
| --- | --- |
| 400 | `invalid_collection_request`, `invalid_period`, `fixture_unavailable`, `source_not_allowed` |
| 401 | `session_required` |
| 403 | `collection_access_denied` |
| 404 | `collection_job_missing` |
| 409 | `replay_conflict`, `source_busy`, `revision_conflict` |
| 429 | `collection_capacity` |
| 503 | `collection_jobs_disabled`, `collection_store_unavailable` |

Используется существующий JSON error envelope; adapter/SQL/URL/error body не
выдаются. Persisted `error_code` отдельно описывает source timeout/unavailable,
invalid capture/input limit/input conflict, measurement conflict/unverifiable
existing/capture corrupt, revoke/source/binding change, attempts/dispatch exhaustion,
deadline или cancel. `stale_lease` — локальный отказ старому owner, не новый terminal reason.

Status раздельно показывает job/revision/attempt/deadline, requested и original
period, capture absent/ready с byte/row/null/marker/setpoint counts, write checkpoint /
inserted / identical / rejected и coverage none/complete/complete_empty. Source
availability — unknown/dense/explicit_empty/unavailable/invalid/conflict — не
равна current usability. Наблюдаемый transient source failure сохраняет unavailable
до retry; потеря worker сама по себе не доказывает недоступность источника.

## Намерение, период и capture

Acceptance одной PG transaction сохраняет immutable intent, owner/replay digest,
source/profile/config/mapping/binding, canonical/schema versions, original и UTC
период, guard, source hold и outbox до 202. **Source bytes в этот момент ещё не
зафиксированы.** Вход — aware RFC3339Nano с нулевыми seconds/nanoseconds,
`start < end`, 1–1440 минут, `[start,end)`, start ≥ fixed Horizon−31d и end ≤ Horizon.
Нет floor/ceil, clamp, moving now, since=last_measured или расширения периода.

Worker делает GET `<fixture_origin>/v1/collection-capture` с query protocol,
fixture_id, frozen external well_id, UTC start/end. В actual stand origin
`http://fixture:8080`; adapter tests допускают explicit loopback. Arbitrary host,
credentials, proxy, redirect и compression запрещены. Connect ≤2s, всё чтение ≤10s.
Нужны HTTP200 и application/json, пустой Content-Encoding; bounded max+1 read
действует независимо от Content-Length. 429/502/503/504 и transport timeout/failure
могут повторяться; deterministic protocol failures не повторяются.

Envelope содержит ровно protocol, fixture_id, well_id, start, end, data_revision,
complete, empty, expected_slots, records. Каждый record имеет well_id, timestamp,
values. Protocol=`collection-fixture-v1`, complete=true, binding/period — exact
intent match, data_revision=`[A-Za-z0-9_-]{1,64}`. Dense fixture требует каждую минуту
периода и expected_slots=число минут. Порядок rows не важен; exact duplicates
схлопываются. Missing minute, пустой dense ответ, unknown/duplicate JSON field,
truncated/extra document отвергают весь capture до записи measurements.

Только заранее объявленный empty descriptor принимает empty=true,
expected_slots=0, records=[]. Это receipt конкретной revision без наблюдений.
Dense row с nullable sensor values остаётся наблюдением; она не равна отсутствию
строки и не превращается в нулевое измерение. Это controlled dense контракт,
не утверждение о полноте real deadband потока или API Pulse Data `/v1/stream`.

До committed capture повторный Fetch после сбоя может получить другие bytes и
data_revision. После его commit worker всегда читает PG capture и проверяет hashes;
Fetch больше не вызывается даже после исчезновения fixture, restart или неизвестного
исхода business commit. Capture immutable, pages=1; pagination/export не реализованы.

## Каналы и значения

Manifest содержит явные aliases/displaynames и mappings; префикс
`^[34]x[0-9A-Fa-f]{4} ` снимается до lookup. Backend не загружает модель.
Допустимы 13 базовых measured aliases: F_out, Ia/Ib/Ic, Ped_load, CosF, R_izol,
Motor_winding_temperature, Pump_intake_pressure_kgs, Annulus_pressure,
Vibration_X/Y/Z. Descriptor может добавить не более трёх из Flowrate for current day,
Flowrate for previously day, energy_Input_energy_reactive_total_MW/kW. Всего ≤16
measured channels и ≤4 setpoint channels, ≤20 raw fields/row. Нужны все declared
raw mapped fields; явный JSON null допустим, отсутствующий field — ошибка.

R_izol=9999 и указанные reactive-energy markers 9999/999.9 → null **до** scale,
offset и bounds. Дополнительные markers задаются trusted descriptor/mapping.
Валидные нули частоты/тока/дебита сохраняются, −0 нормализуется в 0.
`Mapping.IsSet OR Channel.IsSet OR canonical set_*` исключает уставки из измерений.
Finite float64 и transformed value в technical bounds (absolute ≤1e12) обязательны;
malformed/nonfinite/overflow/out-of-range отвергают capture целиком. Нет ffill/bfill,
агрегации, угадывания каналов или заполнения missing slots. Сходящиеся aliases
обязаны дать равные canonical values/null, иначе `input_conflict`.

## Глобальный ключ, collision и покрытие

PK measurements остаётся `(well_id,measured_at)` для всей wide row. Новое nullable
`collection_provenance` связывает source/profile/config, fixture/data_revision,
binding/mapping/schema/canonical versions; не job ID, период или порядок raw rows.
Старым данным не придумывается provenance.

| Существующая row | Новый write | Результат |
| --- | --- | --- |
| Отсутствует | C2 | Insert с полной provenance |
| Payload, quality и вся provenance равны | C2 | Identical no-op; допустим overlap той же revision |
| Есть NULL provenance, даже values равны | C2 | `unverifiable_existing`, whole-job rollback |
| Values/null/key set/quality или provenance отличаются | C2 | `measurement_conflict`, whole-job rollback |
| Unprotected | Legacy unprotected | Прежний whole-row DO NOTHING / first-wins |
| Protected C2 row | Legacy Collect/Ingest/Import, даже equal values | 409 `telemetry_measurement_conflict`, whole-batch rollback |

Writer использует INSERT ON CONFLICT и читает победившую committed row FOR SHARE
в той же transaction; preflight query недостаточен. Correction, channel merge и
promotion legacy rows отсутствуют. Legacy bulk path использует bounded batches,
сохраняя whole-input atomicity и protected collision checks.

В одной owned physical-session transaction фиксируются sorted measurement writes,
one checkpoint, one coverage receipt, safe collection log, terminal success,
inbox disposition, superseded outbox и освобождение source hold. При конфликте
всё откатывается; отдельный действующий fenced owner сохраняет failure. Accepted
counts до checkpoint равны нулю; business conflict rejected=unique rows. После
неизвестного COMMIT status/reconcile проверяют persisted checkpoint.

Coverage относится к `(job,source,binding,data_revision,start,end)`, end exclusive.
MaximumMeasuredAt — отдельное nullable наблюдение, не доказательство полноты.
**C2 не продвигает legacy source.last_measured** и не использует его для запроса.
Это не global contiguous watermark и не обещание отсутствия будущих late data.
Новый explicit interval может быть раньше legacy hint; новая late revision не
пропускается автоматически, но collision с прежней revision честно конфликтует.
Capture-ready cancelled job сохраняет input, accepted=0, coverage=none; уже
committed success остаётся success при позднем cancel.

## Ownership, authority и восстановление

Job lifecycle: pending_dispatch → running → succeeded; transient failure →
retry_wait → pending_dispatch. Terminal: succeeded, failed, cancelled, revoked,
expired, quarantined. До 3 attempts, backoff 5/15s, deadline 300s, attempt 60s,
lease 30s / renew 5s; до 8 dispatch generations. Manual retry/redrive route нет.

Claim владеет одной dedicated PG connection до Close. Load/Save/Apply/Renew/Finish
исполняются на ней с serial gate; pool fallback после physical loss запрещён.
Token SHA/fence/generation/PID/DB lease/deadline/current state проверяются внутри
write transaction и при final transition. Guard → account → well → source → job →
sorted measurements — общий порядок locks; lock timeout 2s, ordinary operation 5s,
Apply 10s. Потеря physical session откатывает transaction; живой pooled connection
не возвращает старому owner право записи.

Current account revision/scope, active well identity/location/fund, source
profile/config/mapping/binding/running status, guard/run/schema и source hold
перепроверяются до I/O и внутри принимающей transaction. Revoke/profile change
во время Fetch не даёт позднему ответу сохранить capture или terminal write.
Достоверный current authority denial завершает revoked; stale owner ничего не
финализирует. SQL unavailable не маскируется как revoke.

Cancel owner-only с current rights и revision. Уже terminal возвращает status;
иначе cancel под теми же row locks увеличивает fence и освобождает hold. Победил
Apply commit — сохраняется success; победил cancel — новых measurements нет.

Rabbit — wakeup; PostgreSQL — work truth. Persistent mandatory publish с confirm
и return check; unknown publish не считается delivered. Inbox outcome фиксируется
до ACK. Uncertain DB/commit закрывает consumer channel; reconcile каждые 5s
находит due jobs из PG и восстанавливает outbox. Terminal deliveries не оживляют
работу. Messages содержат только version/kind/event_id/job_id/generation, без payload,
URLs, actor, token или credentials. Safe logs используют opaque ID/reason/counters.

Consume invocation имеет конечный budget8 reconnect attempts/backoff. Исчерпание
может завершить worker fail-closed; этот opt-in proof требует внешнего restart
оператором/оркестратором. Бесконечный autoreconnect и same-OS-process survival не
обещаются. Новый процесс получает новую dedicated session/fence после настоящего
lease expiry/reconcile; pool fallback старого owner, ACK shortcut и reset durable
retry budget запрещены. A06 проверяет combined exact PG-session termination внутри
Apply + guarded worker restart; actual ACT14 A06 PASS в этой combined области.

В A07 proof source-start observation≤40s помещается в исходный300s case; каждый
fault сохраняет старый процесс в реальном≥7s late-I/O окне, проверяет capture/effects0,
потом guarded stop/restore/new-process restart. SaveSource возвращает draft; helper
явно восстанавливает intended status отдельным PATCH. Own temporary exact-source
running-config trigger атомарно сохраняет future next_poll при PATCH и удаляется
в finally; это fixture infrastructure, не production migration/trigger/API change.
Proof bootstrap автоматически выдаёт own management-user только C2 namespace
read/write с configure^$ для terminal replay/queue get; safe faults ограничены
32basename/function/line frames и direct HTTP100..599, без payload/source/locals.
A10 drain использует два bound LIKE patterns. App/API/schema/source authority
не менялись; isolated diagnostic A07–A10 PASS не заменяют fresh full A01–A11.

Test supervisor R22 использует fixed capture_cutoff, exact current ID/latest
running-epoch genuine counter и stable lifecycle/ledger до публикации. Earliest3s
guards действуют на всех deferral/success/prewrite paths; late/incomplete candidate
не публикуется. Original1s/fresh3s/proof/caps сохранены. ACT13 A11 genuine FAIL остаётся
историей; fresh ACT14 all11/resource/ML0/teardown VERIFIED, publication PASS. Optional
saved-prefix audit — административная диагностика, не новый product prerequisite.

## Схема, budgets и откат

Schema15 additive поверх Q1 schema13 / C1 schema14 на PostgreSQL16: source writer
fence/token/profile; nullable measurement provenance; collection_job_guard, job,
source_hold, capture, checkpoint, coverage, outbox, inbox, action. Capture/checkpoint/
coverage имеют PK job_id; replay unique по actor/digest. SQL CHECK дополняют parse
limits, immutable triggers защищают intent/capture. Migration выполняется явно
`bin/migrate`, startup ничего не мигрирует и не seed-ит. Даже mode off новый API
проверяет schema15; manifest/guard/broker для authorized status read не нужны.

| Граница | C2/1 |
| --- | --- |
| Manifest | ≤65536 bytes, ≤2 accounts, ≤2 bindings, ≤4 fixtures |
| Unit/input | 1 source/well/response, 1–1440 minutes; ≤2MiB raw и canonical; ≤2880 raw /1440 unique rows |
| Admission | ≤2 nonterminal /32 total accepted jobs/run; quota не сбрасывается retry |
| Worker/PG | ≤2 workers, 1 inflight/process, pool ≤8/process, publisher 1 inflight |
| Queue | pulse.collection.v1 / .ready / .dlx / .quarantine; quorum1; каждая ≤8 messages/8192 bytes; message ≤1024 bytes |
| Queue time | ready TTL300000ms, quarantine TTL3600000ms; delivery-limit5, reject-publish, prefetch1 |
| Supervisor | 1s sampling; actual suite ≤45min / case ≤5min; shutdown ≤10s |
| RSS stop | API128MiB, worker256MiB each, fixture container64MiB, PG384MiB, broker384MiB; aggregate1536MiB |
| Host helpers | Дополнительный guard64MiB **на каждый host process**; все helpers/children/transient peaks входят в общий aggregate1536MiB |
| PG/disk stop | database256MiB, PGDATA768MiB, WAL delta256MiB, physical pg_wal512MiB, temp delta16MiB; own files2GiB |
| Other files | fixture16MiB, transient32MiB, logs64MiB, evidence64MiB; own stand ≤24h, явный teardown |

Это заранее заданные acceptance ceilings, не измеренная capacity. До actual suite
уточнено:64MiB — PER host process (actual driver, legacy-probe, supervisor, forwarder,
Docker CLI children), не сумма helpers. Aggregate включает их все и transient peaks;
fixture-container64MiB и исходные G6 caps сохранены.
Test harness monitor использует exact-owned local Engine adapter с bounded metadata,
background1s samples/fresh3s; hash verification streaming≤64KiB. Optional worker2
после A05 удаляется только по observed stop → fresh sampler ACK → complete logs →
guarded exact-ID removal → Engine absence → immutable current-run receipt. Это
resource retirement, не job state; required roles исключать нельзя. До удаления
каждого worker/API сохраняются full logs, final scan охватывает current∪retired IDs
и continuous/full outputs. Sole host transport — fixed stdio без redundant Docker PG/Rabbit publications.
Own readonly90 Rabbit collector config до boot сохраняется при restart; safe
bootstrap --user rabbitmq и strict observed ready/quarantine integer gauges
исключают ложное zero при missing metadata. A04 наблюдает unacked1 до KILL.
Source fixes/78mock tests не являются actual acceptance. Неизвестный/отсутствующий metric,
required-role gap или неполный период sampling блокируют resource gate.
[Конфигурация](configuration.md#изолированный-q2-c2-synthetic-профиль),
[операционный drain/rollback](operations.md#q2-c2-collection-recovery-и-откат),
[фактические результаты](q2-collection-runtime-2026-10-22.md).
Mode off на новом executable сохраняет protected writer semantics и status read.
Старый executable не знает C2 fencing: до его запуска нужен drain **всех** старых
и новых telemetry writers/API/schedulers, отсутствие leases/connections и read-only
sources. После unrestricted re-enable старого binary protection не гарантируется.
Schema15/rows/capture/checkpoints/coverage при rollback сохраняются; down/drop нет.
