> Снимок документа 22.10.2026. Источник: `pulse-backend/docs/q0-runtime-2026-10-21.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# Q0: synthetic runtime proof и воспроизведение · 21.10.2026

**Implementation/runtime VERIFIED: RUN-2, 13/13 PASS.** Source/runtime/reproducer ACCEPTED по назначенному Senior; Q0 synthetic technical proof VERIFIED. Назначенный Senior одобрил docs/publication; координатор принял Q0 в synthetic internal scope 21.10.2026. Этот документ фиксирует проверенное поведение внутренних stored-file primitives; [архитектура](q0-architecture.md) задаёт identity, bounds и совместимость.

В принятой Q0-редакции public saved-file/async HTTP routes, job/outbox/worker и automatic recovery не реализованы. На 21.10 Q1 source реализует их отдельным default-off synthetic профилем; [текущий Q1 proof](q1-runtime.md) имеет собственные source/runtime/browser gates. Старые sync Upload/period-check сохраняют свои контракты. Schema12 проверена в disposable synthetic schemas; включение на actual PREP не заявляется. SDK0.1.2 — local UNRELEASED candidate; remote fetch/release/cloud CI OPEN. Scientific acceptance — user-deferred/not_assessed. Real-private enable закрыт до согласования TTL/max lifetime, encryption, operator access/audit, quotas/disk/WAL, retention и backup/restore.

## Принятая документация и автономный комплект

Назначенный Senior одобрил source/runtime/reproducer и документацию; координатор закрыл внутреннюю synthetic приёмку Q0 21.10.2026. Историческая принятая Q0-редакция [handbook](../README.md): 113 mappings / 637 public sources, 33 HTML sections, 501 local links / 176 OpenAPI refs / 26 draw.io pages / 152 checksums. Выполнены sync, build, validate и source-check; 24 unit-теста инструмента синхронизации и проверка отдельно распакованного ZIP прошли. Статус относится к локальному автономному комплекту; cold install, SDK release/remote CI, real-private enable, production и scientific acceptance сохраняют ограничения выше.

## Зафиксированные identities

| Компонент | Identity RUN-2 |
|---|---|
| Backend execution source | `afae2c4db4189cc3361a3cbd09deee4e3d1eb55b` |
| Backend tree | `2b7b2240fd817a3285e1f78505ae0237c87cb9ba` |
| Process test SHA256 | `270ee75bb1f30182daa683b40474c17a35e638a870fa8f17c3a8e9ad850f1e2f` |
| Frozen test binary | SHA256 `5049ca2543dd28b8bd1f29d70aa7b07391a4c8e317f24db9cce0cf3189215656`, **50032754B** |
| Compiler / platform | Go **1.25.12**, darwin/arm64; no race/coverage/profiler |
| Contracts source | `272f23027e4a3ba636305b931f6ab83e5fca8166`, SDK0.1.2 UNRELEASED |
| ML source | `e5cc61b3c282eb6abb446c81e1e800e7b7ad2c8a` |
| Model source | `f5c2a3de031ff4794194a219f048c9bf8c7c4f45` |
| Contracts / ML wheel SHA256 | `09ba4f0974c5db723aa3a3fdab26901bb4d5fbfdbfee18b4f63a664ea202cb79` / `079c42153b611d53d5bf2669730bc6e8c605405ef0b780e71118ccab1eac8760` |
| Installed ML source manifest SHA256 | `4fb1355605f6a633a39dd6ed74424c4b9a7710c31510449009a7fc34c4948db6` |
| Installed runtime manifest SHA256 | `24012967768e4dc98536eb85a8878f017a33409c46daa53f87f617ffa66bc0f9` |
| Synthetic bundle SHA256 | `e13e9f857bef78e5b0b57e3643eb6ce27071e090008ac40cf5e623db780b3ce8` |

Binary SHA/length и build info записаны **до первого invocation**, SHA повторно подтверждён после cleanup; source CLEAN до/после. Одно compiled binary прошло resource→replay→faults без reset накопленных counters/evidence. Смена Go1.25.0 исторического probe на1.25.12 RUN-2 отражена actual recipe metadata. Повторная сборка после docs/retention commits не называется тем же historical binary.

Registry содержит trusted test-only `q0_synthetic.FixedModel` (45B pickle, fixed0.1), meta/drift/bundle manifest. Исполняются actual transport, features и decision code, без fit, real weights или оценки качества. [Reproducer](q0-reproducer.md) сохраняет генератор, launcher, synthetic registry, schemas, manifest, pin, source/wheel lineage и checksums. Установленные source/runtime manifests должны совпасть с retained pin: одинаковая package version не достаточна.

Retained reproducer SOURCE APPROVED:20files/60231B, inventory SHA256 `629a40eaad95a67131e8838fc13e510d8066256dc1306000f5ec1cc962835bbb`; compiler-version remark R-SR01 CLOSED. Назначенный Senior: RUNTIME APPROVED, composite AC1–4 PASS synthetic. Эти решения принимают source/runtime/reproducer; docs/publication ACCEPTED 21.10.2026.

## Результаты и границы измерений

| Frozen binary command | Cases | Monotonic wall | Exit |
|---|---:|---:|---:|
| `-test.run=^TestQ0StoredFileResourceCaps$ -test.v -test.timeout=480s` |8/8 PASS |21.228s |0 |
| `-test.run=^TestQ0StoredFileProcessReplay$ -test.v -test.timeout=300s` |1/1 PASS |7.882s |0 |
| `-test.run=^TestQ0StoredFileProcessFaults$ -test.v -test.timeout=180s` |4/4 PASS |7.233s |0 |

| Resource case | Проверенное поведение | OS high-water B | Sampled process-tree RSS B | Sampled temp B |
|---|---|---:|---:|---:|
| W2 |128 columns, XLSX spill |201965568 |197410816 |10704345 |
| W2columns |129 columns → `columns_limit` |59899904 |60620800 |0 |
| W3raw |20971521B → `raw_limit` |91586560 |0 |0 |
| W3rows |100001 rows → `rows_limit` |194609152 |164970496 |0 |
| W3 |20MiB /100000 rows /64 mappings, prepare+accept |449609728 |429572096 |0 |
| W4 |amplified canonical>50MiB → `canonical_limit` |360005632 |360333312 |0 |
| W5 |byte quota /race /metadata |279527424 |280576000 |0 |
| W5count |16 accepted inputs,17th rejected |31178752 |32047104 |0 |

Budgets не изменены: Go1GiB, ML2GiB, combined owned parser+ML temp128MiB;100ms sampling/watchdog,40s resource child; gateway/ML5min, actual adapter RPC30s. Peak ML OS123404288B/sample122880000B. Sampling и OS high-water — разные измерения: короткий W3raw пропущен sampler, поэтому sampled0 не означает нулевую память. `/usr/bin/time -l` bytes — Darwin-specific; Linux flags/units этим proof не покрыты. Wall UTC заметно менялся относительно monotonic elapsed; причина не установлена. Latency/overlap берутся из monotonic clocks.

W3 canonical14900842B, SHA256 `c02a8730b23da93a7bae858e0af9104f05a149ff0ef16304d3b6d91ac0e7f925`. W4 отказал до admission: inputs/payloads/results/charged bytes0, feature calls0. Emitted boundary byte count не записан, точное превышение не выдумывается.

W5 сохранил7 inputs/payloads, charged251237896B. Per-account byte rejection, global exact-quota race и duplicate без повторного списания прошли. Race:1 success +1 `Limited/file_input_quota`; безопасное admission field — `rate_limited`. Два metadata readers дали по15 успешных samples:30success,0errors, payload-select delta0. Actual call intervals каждого reader пересекли admission дважды: overlaps `[2,2]`; искусственные blocking callbacks здесь не использовались. Baseline p50/p95/max4.387/5.062/5.512ms; concurrent success-only4.999/12.946/19.678ms. W5count:16inputs, charged1488944B, metadata errors0/payload-select delta0. Это конкретный synthetic workload, не production latency SLA.

W1 прошёл реальный session→Go→PostgreSQL→отдельный ML process, два SIGKILL после durable input/result commits, restart Go+ML, удаление исходного CSV, explicit replay и session retry. Immutable result SHA256 `4b985be747514b0109e28974d4ec02cb7ad3696474583bd08d4b2b20c086aadd` сохранился; norm и insufficient различаются. Итоговые counters: features2, predict2, feature_result2 (по60windows), started4, legacy_loader0/default_loader0; replay не добавил features. Проверены current actor/scope, session, int64, marker/valid zero controls. Гарантия — один immutable stored result для принятого input; exactly-once compute не заявляется.

Fault cases: missing→`input_unavailable`, corrupt→`input_corrupt`, policy→`policy_mismatch`, bundle→`bundle_unavailable`. Каждый отказал с feature_delta0/result_count0; warm unavailable bundle в W1 также не вошёл в features. Никакого downgrade на legacy/default loader.

## PostgreSQL, cleanup и retained evidence

RUN-2 использовал отдельный PostgreSQL16.15 Debian/aarch64 на одноразовом loopback55479, tmpfs PGDATA1GiB, memory1GiB; image SHA256 `ca0bd484cb98bf4b24eb1010e73fb3fcbd6714d240fbc1a10eea5b7dbecb641d`. Ни live DB, ни копия production data не использованы. Existing PREP8088/8018/55439 и прежний testPG55432 сохранены.

Before/after/barrier snapshots отдельно фиксируют logical bytes, relation sizes, TOAST heap/index и total_including_toast_indexes. Total уже включает TOAST/index; повторное сложение неверно. Наблюдавшиеся W3 logical35891128B/WAL936408B; W5 logical251237896B/WAL6542552B; idle200ms в W1 дал1176B WAL. WAL cluster-wide с background noise, не чистая causal цена запроса.

Cleanup: owned processes0, schemas0, parser/ML temp0, source CLEAN, binary SHA unchanged. Docker stop exit0; immediate snapshot ещё видел asynchronous `--rm` deletion, final read-only recheck подтвердил отсутствие контейнера. Lease55479 RELEASED. Чужие processes/schema/temp не очищались.

Finite inventory RUN-2:157files, SHA256 inventory `e5d569abe0931847a21259cf0aae64d05bfcece5190b20f0e7f1210c8ead6f44`. В scope — весь frozen run-2 кроме самого inventory: pre-invocation/final binary SHA,18copied fixtures, private prepared fixture, native logs/process/DB/barrier/outcome/cleanup evidence и machine summary. Raw local evidence не является частью публичной поставки; safe identities/aggregate здесь и retained reproducer позволяют проверить границы результата без payload, секретов или private host paths.

История не заменяется новым PASS: первоначальные W3/W4 STOP дали OS1148174336/1145864192B при budget1073741824B. Memory correction2024896 убрала два overlapping bulk map graphs только в saved normalization; legacy semantics сохранены и parity проверена. RERUN1 затем FAIL на private test fixture roundtrip; P-SR04 исправил test envelope с payload/limits, сохранив public JSON privacy. Исходный binary provenance gap остаётся историческим ограничением. Актуальные числа выше — только RUN-2 afae2c4.

## Воспроизводимые команды и условия остановки

Полные установки, synthetic registry trust, accepted-wheel SHA, installed-pin comparison, Go workspace и cleanup — [reproducer runbook](q0-reproducer.md). `requirements-accepted.txt` — installed version inventory, не полный wheel lock. Cold external installation **UNTESTED**: равные wheels/versions могут дать иной RECORD/direct_url/source digest. Несовпадение actual installed pin останавливает proof; требуется новая проверенная identity/proof, retained pin не редактируют ради совпадения.

Generator требует CPython3.13.5/zlib1.2.12 и новый output directory, фиксирует ZIP metadata/DEFLATE level6. Проверена byte parity11/11:8datasets+2schemas+manifest, и2 unit selftests. Исходные generation versions не были записаны: совпадение доказано регенерацией, а не предположением об истории. Это конкретная воспроизводимость, не переносимость на произвольные Python/zlib/OS.

Задайте `BACKEND`, `CONTRACTS`, `Q0_ROOT`, `Q0_WORK`, `Q0_PYTHON`, `Q0_PG_CONTAINER`, `Q0_PG_PORT` как новые operator-owned пути/ресурсы; настройте отдельный GOWORK по reproducer README. `TEST_DATABASE_URL` должен указывать только disposable PG16, готовую по `pg_isready`. Не используйте рабочую/PREP БД и не запускайте `make migrate` для harness: он создаёт собственные `q0_p_*` schemas. `PYTHONPATH` должен содержать только retained test helper; accepted isolated installed ML environment — отдельно от shared workspace `.venv`.

```sh
cd "$BACKEND"
python3 -m unittest discover -s test/q0_process -p 'test_*.py' -v
"$Q0_PYTHON" test/q0_process/generate.py "$Q0_ROOT"
# Проверить actual installed pin против generated pin.json по reproducer README.
# До первого invocation записать source HEAD/tree/status, modules и compiler metadata.
git rev-parse HEAD HEAD^{tree} > "$Q0_ROOT/evidence/source-ref-before.txt"
git status --porcelain > "$Q0_ROOT/evidence/source-status-before.txt"
go version > "$Q0_ROOT/evidence/go-version-before.txt"
go env GOOS GOARCH GOWORK GOFLAGS > "$Q0_ROOT/evidence/go-env-before.txt"
go list -m -json all > "$Q0_ROOT/evidence/go-modules-before.json"
shasum -a 256 internal/transport/httpapi/file_inputs_process_test.go \
  > "$Q0_ROOT/evidence/process-source-before.sha256"
GOTOOLCHAIN=local go test -c -o "$Q0_ROOT/httpapi.test" ./internal/transport/httpapi
shasum -a 256 "$Q0_ROOT/httpapi.test" > "$Q0_ROOT/evidence/binary-before.sha256"
wc -c < "$Q0_ROOT/httpapi.test" > "$Q0_ROOT/evidence/binary-before.bytes"
go version -m "$Q0_ROOT/httpapi.test" > "$Q0_ROOT/evidence/binary-buildinfo-before.txt"
unset Q0_CHILD Q0_READY Q0_SCHEMA Q0_ML_ADDR Q0_TEST_PIN_FAULT Q0_W5_FORCE_CONTROL
export Q0_PROCESS_RUN=1
export Q0_ROOT Q0_PYTHON
export PYTHONPATH="$BACKEND/test/q0_process"
cd "$BACKEND/internal/transport/httpapi"
"$Q0_ROOT/httpapi.test" -test.run '^TestQ0StoredFileResourceCaps$' -test.v -test.timeout=480s
# Только после8/8 PASS:
"$Q0_ROOT/httpapi.test" -test.run '^TestQ0StoredFileProcessReplay$' -test.v -test.timeout=300s
# Только после replay PASS, тот же binary/root:
"$Q0_ROOT/httpapi.test" -test.run '^TestQ0StoredFileProcessFaults$' -test.v -test.timeout=180s
```

Каждую команду выполняют последовательно с проверкой exit code; failure прекращает зависимые шаги. Inherited Q0/ML/OTEL/PYTHON fault/config overrides очищают по reproducer README. Resource tests требуют Darwin `/usr/bin/time -l`; отсутствие platform/runtime/dependencies — OPEN/SKIP, не PASS. Не меняйте caps/workloads/timeouts/GC ради приёмки. Перед завершением сверяют source/binary/fixture hashes, outcome, counters, actual metadata intervals/errors, temp/own schemas/processes, затем останавливают только свой контейнер и подтверждают его отсутствие. Evidence фиксируют finite SHA/length inventory после cleanup. Production, scientific и финальная публикационная приёмка не следуют автоматически из запуска этих команд.
