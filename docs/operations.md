# Эксплуатация


Q0 synthetic process/resource проверки описаны в [runbook/proof](q0-runtime-2026-10-21.md); их запускают на отдельной disposable PostgreSQL, не на PREP. Schema12 — версия исходников, не свидетельство миграции actual PREP. Новые accepts требуют parser v2; DDL читает historical v1/v2, без backfill. Для rollback оставляют additive tables и immutable registry, отключая новых callers; сохранённые входы не пересчитывают через mutable legacy Upload. Это историческая Q0 область. Q1 job/worker/recovery реализованы в отдельном default-off synthetic профиле; [датированный proof](q1-runtime-2026-10-21.md) фиксирует actual outcomes и ограничения. Real-private TTL/encryption/access/backup policies остаются gate.
## Миграции

```sh
make migrate
```

- Новая БД: принимается baseline прежних миграций 0001–0012 и применяются последующие Go-миграции до текущей версии15 (C2 collection/capture/coverage поверх C1 schema14, Q1 schema13 и Q0 schema12). PostgreSQL server16 и app schema15 — разные версии.
- Существующий Alembic baseline: требуется `alembic_version=0012_ml_run_drift`; принятие фиксируется в `backend_schema_migration`. Существующая Go-БД обновляется последовательными аддитивными миграциями; cohort9, snapshot10, run profile/cohort identity11, additive file inputs/payload/budget/result12, jobs/outbox/inbox/guard/leases13, cohort jobs/inputs/checkpoints14, collection jobs/capture/coverage и source fencing15. Migration13/14/15 не создают synthetic guard и не включают feature; actual PREP не мигрирован этими проверками. Таблицы и прежние данные сохраняются.
- Другая ревизия или отсутствующий Alembic marker: команда завершается ошибкой. Сначала приведите БД к поддерживаемой ревизии прежним мигратором.

Приложение не выполняет миграции при HTTP-старте. Перед изменением нужен backup и проверка на отдельной восстановленной БД. Версия11 не backfill-ит profile/cohort_version старых ml_run: NULL означает исторически неизвестную identity. Не удаляйте nullable-поля/таблицы ради rollback приложения. Исходники прежнего монолита — только локальный archive.

## Docker

Контракты передаются как отдельный build context. Текущий source использует candidate v0.1.2 UNRELEASED; Q2 G2 contracts source checkpoint `1d0b2b62d2cb73d18ab69738431089edf69621f8` содержит capability/docs delta без изменения wire/generated SDK. Исторический Q0 binding — `272f23027e4a3ba636305b931f6ab83e5fca8166`. Для воспроизведения PREP требуется прежний contracts `07213c430318affd140f55dea391cdc36220d93f` и соответствующий PREP consumer source: package0.1.1 само по себе их не различает. Не смешивайте эти профили; используйте проверенные соседние checkout:

```sh
# Из pulse-backend, с соседним pulse-contracts:
docker build --build-context contracts=../pulse-contracts \
  -f build/package/Dockerfile -t pulse-backend .
```

Backend не хранит копию SDK. В runtime-образ попадают только Go-бинарники; credentials доступа к приватному репозиторию не передаются в образ. В CI checkout контракта выполняется перед сборкой через read-only deploy key.

Образ содержит `/app/api`, `/app/migrate`, `/app/adminuser`, `/app/cohort`. Q1 `bin/worker` и Q2 `bin/cohort-worker` собираются нативной командой `make build`; текущий Dockerfile workers не упаковывает, готовый worker image этой документацией не заявляется. Настройте адреса БД и ML, доступные из контейнера, затем:

```sh
docker run --rm --env-file .env --entrypoint /app/migrate pulse-backend
docker run --rm --env-file .env -e HTTP_ADDR=:8000 -p 8000:8000 pulse-backend
```

## Задания и остановка

Ручной запуск: `POST /api/v1/ml/wells/refresh`. Повторный запуск во время действующего lock возвращает 409. История: `GET /api/v1/ml/runs`. Совместимый PATCH статуса тревоги принимает `new`, `acknowledged`, `false_positive`.

Предметная обработка через review — отдельная модель: `status=new|in_progress|closed`,
а при `closed` обязателен `conclusion=confirmed|not_confirmed|inconclusive` и комментарий.
Для `in_progress`/`closed` нужен ответственный, для изменения — ключ запроса и revision.
Review записывает историю и отображает состояние обратно в совместимое поле тревоги:
`new` → `new`, `in_progress` → `acknowledged`, `closed/not_confirmed` → `false_positive`,
`closed/confirmed` → `confirmed`, `closed/inconclusive` → `inconclusive`.
Поэтому чтение статуса может вернуть пять значений; расширять старый PATCH до пяти нельзя.
Статус обработки, заключение технолога и вычисленный ML-ярус — разные поля.
Источники: [reviews.go](../internal/service/reviews.go), [руководство](telemetry-integrations.md).

Для legacy runs SIGINT/SIGTERM прекращает приём запросов и дожидается принятых прогонов. После аварии PostgreSQL освобождает lock; следующий legacy прогон помечает его незавершённые записи как `error`. Q2-owned runs исключены из legacy cleanup/Fail и восстанавливаются собственным durable protocol.

Timeout gRPC не прерывает уже выполняющуюся функцию pandas/CatBoost автоматически. Legacy Score не повторяется backend автоматически. Q1/Q2 имеют отдельные opt-in durable protocols ниже; Q2 использует выделенный ML target с одним pinned physical execution, конечными retry и persisted checkpoints.

## Диагностика

| Симптом | Проверка |
|---|---|
| `/health`: `database=error` | DATABASE_URL, доступность PostgreSQL и размер пула |
| HTTP 401 в legacy | При непустом `API_KEY` проверить точный `X-API-Key`; пустой ключ использует общий локальный сеанс |
| HTTP 401 в local | Проверить персональный вход, действительность `pulse_session`, Secure cookie/HTTPS и отсутствие истечения сеанса; общий API-key не заменяет вход |
| HTTP 403 в local | Проверить роль/разрешение действия и origin изменяющего запроса. Доступ вне scope может маскироваться как 404; это не ошибка ключа ML |
| ML HTTP 503 | ML listener, deadline и согласованность внутреннего ключа; gRPC UNAUTHENTICATED отличается от HTTP 401/403 |
| Прогон `error` | Поле `error` в `/api/v1/ml/runs` и логи обоих сервисов |
| Нет аналитики | Metabase URL, secret и whitelist дашбордов |

## Проверка ML и внутренний ключ

Профили HTTP и gRPC независимы. `AUTH_MODE=legacy` остаётся code default; текущие Compose задают `local` и cohort.
До запуска нужны явные миграции/capture/bootstrap первого администратора,
с точными CORS origins и подходящей Secure cookie. См. [все настройки и defaults](configuration.md)
и [порядок персонального входа](telemetry-integrations.md#этап-4--персональный-доступ-и-будущий-sso).
Исходный работающий pulse-local8080 не переключался в PREP; file defaults и фактически запущенный экземпляр различаются.

В ML пустой/отсутствующий `ML_SERVICE_API_KEY` отключает проверку metadata. Непустой
ключ требует точного `x-ml-api-key` во всех RPC, включая Health. Если оператор включает
ключ, он должен **явно** задать одно значение в backend и в окружении ML-процесса;
Docker probe наследует окружение ML. Обычный compose.local сам не добавляет ключ и не
переносит переменные host shell в контейнер: используйте согласованный deployment
override/средство доставки секретов для обоих сервисов и перезапустите их. Проверку
выполняйте без echo, shell tracing и вывода разрешённой Compose-конфигурации с секретами.
HTTP `API_KEY`/`X-API-Key` этим ключом не являются.

Из установленного окружения ML или внутри его контейнера:

```sh
python -m pulse_ml.healthcheck
# Для другого согласованного loopback-порта:
python -m pulse_ml.healthcheck --target 127.0.0.1:50052
```

Probe обращается к `127.0.0.1:50051`, timeout 3 с, проверяет `status=ok`, закрывает
канал и возвращает 0 только при успехе. При непустом ключе читает его из env процесса,
не из `.env` и не из argv. При auth/transport/deadline/неверном status код ненулевой;
вывод содержит только безопасную категорию ошибки, без ключа/деталей RPC. Ключ для metadata должен состоять из printable ASCII (0x20–0x7e); Unicode/управляющие символы отклоняются без traceback, допустимые пробелы не обрезаются.
Docker/compose.local используют interval15с; isolated compose.prep —10с. Timeout5с и start_period30с; default probe RPC timeout3с.
`0.0.0.0` — bind сервера, не адрес клиента. Если сервер слушает другой адрес/порт,
endpoint probe меняется согласованно. Health подтверждает listener/доступ, но не
загрузку модели, наличие cache/manifest/PG или качество Score. Автопрогон в локальном
Compose остаётся выключенным. Plain-text ML listener не публикуется наружу;
используйте loopback/защищённую внутреннюю сеть.

## Сквозная проверка

`make check`, `make build` и `TEST_DATABASE_URL=... make integration` выполняются из pulse-backend. Integration использует отдельную PostgreSQL и временные схемы, а не рабочую esp_app. Тесты RPC/nullable/int64/экспорта и fixture ML не подтверждают научное качество.

Для реального контролируемого app→Data→ML пути используйте [изолированный PREP runtime](readiness-runtime.md): отдельная PG-копия, loopback8088/8018, model-only ML mount, local auth, membership100 и сохранённый full snapshot. Действующий pulse-local8080 при этой проверке не изменяется.

## Локальное приложение целиком в Docker

Файл `deployments/compose.local.yaml` запускает frontend (Nginx), Go backend, Python ML и Pulse Data. Использует уже существующие `grant-postgres` и `grant-clickhouse`, сети `postgres_default` и `clickhouse_default`, а также соседние split-репозитории и `workspace-data`. Существующие базы и их volumes не пересоздаются. Это конфигурация текущего локального рабочего места.

Перед первым использованием новых local/cohort defaults явно выполните backup/migration/bootstrap/capture, задайте `PULSE_COHORT_VERSION` и проверьте private env. Команды, требующие существующих рабочих БД, не являются безопасным smoke. Для нового проверочного запуска предпочтителен [изолированный профиль](readiness-runtime.md).

После подготовленного rollout (из pulse-backend):

```sh
docker compose -f deployments/compose.local.yaml config --quiet
docker compose -f deployments/compose.local.yaml build
docker compose -f deployments/compose.local.yaml up -d --wait
```

Интерфейс: <http://127.0.0.1:8080/>. API доступен через `/api/v1` на том же origin; `/health` проверяет соединение backend с операционной БД. Прямой API — `127.0.0.1:8000` и `127.0.0.1:8002`; порт 8000 сохраняет совместимость с Vite на 5174. Перед запуском остановите прежний нативный backend, чтобы освободить порт и не запускать второй сборщик.

Адрес Pulse Data из Docker-backend — `http://data:8092`, адрес JSON-источника — `http://data:8092/v1/stream`. `127.0.0.1:8092` внутри контейнера означает сам backend и не подходит. Для существующего источника адрес меняется через API/форму настройки, с сохранением ID, mapping и watermark. Модели, признаки и сценарии подключены read-only; живые `.env` в образы не копируются.

В текущем файле `AUTH_MODE=local`, `APP_SCOPE=cohort`, обязательна `PULSE_COHORT_VERSION`; автоматический ML-прогон выключен. Персональные аккаунты/bootstrap/capture не создаются автоматически. Keycloak не реализован. Порты опубликованы только на loopback; при изменении PULSE_HTTP_PORT согласуйте точный CORS origin. Уже работающий контейнер может сохранять прежний legacy-профиль до отдельного rollout.

Обычная остановка приложения (без удаления данных):

```sh
docker compose -f deployments/compose.local.yaml stop
```

Схема процессов, сетей и зависимостей: [C4 в draw.io](diagrams/pulse-c4.drawio).

## Прогоны, происхождение и исторические записи PREP

В cohort пользовательский refresh читает app-телеметрию и вызывает bundle-only Upload для каждого подходящего участника. Все100 outcomes сохраняются атомарно с завершением/алертами: scored содержит result; archived/inactive/insufficient/error — null result и причину. Это синтетический presentation фонд, не ML holdout. Research Score доступен только отдельному профилю.

С версии11 попытка получает profile/cohort_version на BeginCohort до чтения members; ранний failed/running run не исчезает из истории. Внешний failure reason обезличен. Legacy pre11 записи остаются unknown; текущий cohort им задним числом не присваивается. Статус технолога — изменяемый read-side overlay; immutable decision snapshot/digest при его изменении не переписывается.

Исторический PREP snapshot читается после ML restart без нового вычисления. Его typed provenance имеет статус partial: bundle/runtime/source/policy/output identity и hash входа не заменяют сохранённые immutable входные bytes. Нельзя обещать полный replay/as-of или промышленное качество. Q2 отдельно сохраняет immutable inputs/pin и Execution identity; прежние snapshots от этого replayable не становятся. Источники решения SHAP/окна и computed/training_median/zero_fallback не скрываются.

## Q1 synthetic worker / recovery

Сначала выбрать exclusive test schema/loopback ports и создать fixture manifest,
matching complete cohort и file_job_pilot_guard в отдельном harness. Выполнить
schema migration явно; API/worker сами не мигрируют и не seed-ят guard. Проверить
manifest/hash/schema/pin startup, затем запустить API и один `bin/worker`.
Capability/действия требуют настоящую local session; выключенная feature даёт503.
См. [Q1 protocol](q1-file-jobs.md) и [configuration](configuration.md).

Broker pin `rabbitmq:4.3.6@sha256:3e5cc23235b0eb850f604e8ace10d9a9b6d05be3e36730e53522ee9ead707a9e`
проверен как arm64 synthetic image; перед иной платформой проверьте manifest/digest.
Версию amqp091-go1.15.0 не заменять floating dependency. Main/DLQ queues quorum,
каждая≤64messages/64KiB, mainTTL20min/DLQ1h, message≤1KiB. max_message_size1024,
channel16,connection8,heartbeat10s. Worker manual ack/prefetch1; retry delay в PG.

Broker outage после202 сохраняет pending_dispatch. Запустить dispatcher после
восстановления; unknown confirm безопасно повторяет EventID. Worker crash
восстанавливает reconciler по истечению45s lease с новым fence и конечным attempt
budget. Не использовать ручной INSERT result, reset counts, latest model fallback
или републикацию payload. Quarantine — явная остановка, не бесконечная очередь.
Malformed неизвестные references discard-ятся; известные проверяются/карантинятся
через persisted mapping, без raw error/body в broker, logs или PG action.

SIGTERM останавливает новые claims, отменяет RPC и ждёт bounded operations. Для
app rollback остановить API admission, worker/dispatcher/reconciler, сохранить
PG и Rabbit volumes; старый binary обслуживает legacy records. Pending jobs
возобновляются новым worker с тем же manifest/policy. `compose down -v`, очистка
inputs и schema downgrade не являются rollback этим сценарием.

Resource stop для synthetic proof: GoRSS1GiB,MLRSS2GiB,temp128MiB,brokerRSS512MiB,
PG relation+TOAST1GiB/WALdelta2GiB,free disk<2GiB; broker dedicated volume target512MiB.
Снять evidence/остановить admission при превышении. Это measured pilot limits,
не production SLA и не обещание OS hard cap. Секреты/raw data в evidence не писать.

## Q2-C1 synthetic worker / recovery

Точный [Q2 protocol](q2-cohort-jobs.md), [configuration](configuration.md#изолированный-q2-c1-synthetic-профиль)
и [dated runtime evidence](q2-runtime-2026-10-22.md) отделяют source behavior от actual
runtime/resource gates. Q1 численные бюджеты выше не переносятся на cohort.

1. Выберите отдельную disposable PostgreSQL16 DB/test schema, свободные loopback
   ports, isolated Rabbit vhost `pulse-q2-synthetic` и dedicated Q2 ML listener.
   Остановите конкурирующих callers на этом ML target. Соберите API и cohort-worker,
   сохраните их фактические SHA256 и exact source/wheel/pin bindings.
2. Явно примените migration14 только к выбранной тестовой БД. Создайте100 operational
   synthetic wells, immutable cohort, allowlisted admin с full100 AccessScope и
   matching `cohort_job_guard`. Migration/API/worker guard не seed-ят. Для штатного
   proof это делает [opt-in process test](q2-runtime-2026-10-22.md#воспроизведение-из-standalone-checkout)
   в своей временной схеме; отдельной public seed/redrive команды нет.
3. Подготовьте private trusted descriptor и≤2fixtures/48MiB, точный C1 header из
   семи измерений, full pin и capture recipe с SHA фактически собранного **API**.
   Укажите `COHORT_JOB_*`, local/cohort и отдельный ML target. API проверяет все
   источники при загрузке каталога; пути ограничены regular root-relative files.
4. Запустите API, затем supervised `bin/cohort-worker`. Последний объединяет
   dispatcher/reconciler/consumer и требует scoped producer+consumer AMQP connections.
   Native worker image этой документацией не заявляется. Действующий Compose/портал
   автоматически не переключается.
5. Выполните submit через настоящую local session, сохраните safe job/run identity;
   после202 проверьте status через GET и старый published snapshot до final.
   Новые source bytes/as_of/pin принимаются только новым key/job.

Mutating integration/review/process proof запускайте в эксклюзивном окне одной
PostgreSQL database: разные temporary schemas не изолируют advisory74019321/74019323.
Read-only resource sampler допустим отдельно; параллельный mutating test может
получить capacity busy и испортить evidence. Source APPROVED,8findings CLOSED;
latest module gates и3actual cases PASS. Internal synthetic runtime/resource gates
и teardown VERIFIED; публикационный контроль комплекта учитывается отдельно, см. dated proof.

Rabbit topology: direct durable exchange `pulse.cohort.v1`, routing `ready`, quorum
queue `pulse.cohort.v1.ready`; DLX `pulse.cohort.v1.dlx`, routing `quarantine`, quorum
queue `pulse.cohort.v1.quarantine`. Каждая≤16messages/16KiB, ready TTL30min,
quarantine1h, ready delivery-limit5, envelope≤1024bytes, prefetch1. Existing
RabbitMQ4.3.6/amqp091-go1.15.0 pins не повышаются этим профилем. Publish mandatory+
confirm с конечными outbox attempts; PG остаётся authority при broker outage.
Q2 scoped AMQP URLs задаются без query/fragment; `?heartbeat=10` отвергается,
heartbeat10s установлен самим adapter. Не переносите Q1 URL query в Q2.

При outage после202 сохраняется pending intent. Восстановите зависимости и тот же
trusted descriptor/policy, затем новый worker продолжит missing checkpoints по
immutable PG bytes. **Original fixture inputs после202 могут быть удалены/изменены**:
worker их не читает. API restart для новых admissions снова требует валидный
каталог и capture SHA; recovery существующего job не требует API recapture.
Worker executable hash отличается от API capture SHA и записывается отдельно.

Не сбрасывайте attempts/quota, не меняйте pin/input/hash и не вставляйте result
вручную. Lease45s и current physical claim connection ограничивают ownership;
Next/renew/checkpoint/final TX идут на этой же connection. Session loss абортирует
её transaction; late worker не принимает эффект по истёкшему fence. Exact busy или
transient ML failure получают persisted10/30s backoff в пределах3claims/3member/
102total starts и30min lifetime. Unknown commit не объявляется success и не ack-ается
без durable disposition. Нет automatic latest fallback или exactly-once compute.

Для already-owned Next/Renew/Checkpoint/Finish принято bounded WAIT на том же
74019323 вместо потери healthy claim из-за короткого TRY collision;
исправление APPROVED, Q2-DB-03 CLOSED, latest module gates и max actual rerun PASS. Next/Renew/Checkpoint total5s включают local serializer,
Begin/gate/checks/writes/commit. Finish total10s не изменён; его gate query≤5s либо
remaining caller deadline. После ожидания перечитываются DB time, current rights,
state/lease/fence/token/physical ownership. Cancel/revoke/expiry во время ожидания
по-прежнему запрещают effect; context/SQL/session errors не подавляются.
Admission/legacy/new claim/cancel/outbox/reconcile сохраняют TRY. Gate освобождается
до compute/RPC/AMQP. Heartbeat10s/lease45s и caps не повышаются; при ожидании сверх
bound исследуют holder/critical section. Не уменьшайте two-worker/100ms-poll workload
ради PASS неизменённого actual rerun. Точная [owned-runtime policy](q2-cohort-jobs.md#принятое-уточнение-owned-runtime-gate)
не обещает FIFO или успех при произвольной нагрузке.

Quarantine — nonterminal reservation, redrive API отсутствует: доступны revision
cancel либо expiry. Admission active≤1global/actor; retained≤8global/4actor и
512/256MiB не освобождаются terminal status. **GC в C1 отсутствует**. Не удаляйте
accepted inputs/checkpoints при обычной остановке или teardown рабочего synthetic
стенда. Disposable process test удаляет только собственную временную схему после
остановки child processes.

При превышении measurement budgets остановите admission, сохраните safe evidence
и bounded завершите работу: API/worker≤512MiB RSS each, Q2 ML≤1536MiB,
Q1-tail+Q2 ML≤3072MiB; PG growth≤768MiB/schema, WAL≤1GiB/full100,
PG temp_bytes delta0, backend temp files0, fixture/bundle disk≤128MiB.
Бюджет — проверяемый gate, не OS hard limit; не увеличивайте его ради прохождения.

Actual proof выдержал эти ceilings: sampled API239.25MiB/worker43.59/Q2ML160.02,
same-sample combinedML247.77, PGschema7.87MiB/WAL73.48MiB/tempdelta0. Rabbit byte
gauges были пустыми:16KiB/16messages — проверенные actual args, не measured0peak.
ML TMPDIR пусты; backend temporary payload files отсутствуют по memory/PG source
invariant, global/tmp measurement не выполнялся. Подробные raw bytes, sampling и
owned teardown — в dated proof; production SLA из этих чисел не следует.

Для rollback сначала выключите admission новым binary (`COHORT_JOB_MODE=off`),
cancel/drain принятые jobs либо оставьте persisted intents с остановленным consumer.
Сохраните PG inputs/checkpoints/results, trusted bundles/policy и schema14.
**Старый binary допускается только после явного terminal/drain всех Q2 intents и
остановки cohort workers**: он не знает common Q2 capacity guard. До drain rollback
означает остановленный новый consumer с сохранённым PG. Не отправляйте accepted Q2
через legacy и не выполняйте schema downgrade/volume deletion. Новый binary
сохраняет narrow Q1/legacy guards даже при mode off; quarantine Q1 блокирует Q2,
terminal/superseded Q1 delivery получает durable disposition без нового compute.

## Q2-C2 collection recovery и откат

[Протокол](q2-collection-jobs.md), [конфигурация](configuration.md#изолированный-q2-c2-synthetic-профиль),
[ACT14 evidence](q2-collection-runtime-2026-10-22.md),
[автономный operator bootstrap](../test/runtime/collection/README.md).
Core47/SamplerSuccessDeadline30/H20 и module-v2 приняты; свежий ACT14 all11 PASS,
full-span resources/canary/ML0/exact teardown VERIFIED. Publication PASS отдельно.
Рабочие PREP/UI/сбор/real enable не переключены, scientific not_assessed.

1. Нужны exact reviewed offline SDK1d0b.../candidate0.1.2UNRELEASED, source77 commit
   `1a824a63c068f828a9d4dfa663b8036cd426e191`, cached PG16/Rabbit4.3.6 и новый own run root.
   Проверить свободные host8094/8093/55442/5675/15675; Data8092 сохраняется.
   Host exposure только fixed bounded stdio, Docker publications отсутствуют;
   internal egress-denied network и original caps сохраняются.
2. Provision асинхронен: bounded exact-owned **read-only** PG/Rabbit readiness до
   supervisor и единственного mutating broker bootstrap. Fail→preserve/guarded
   teardown/fresh stand, не повторять add_vhost/add_user. Полный copyable recipe
   включает set-eu, SDK identity/run-root guard и readiness deadline; docs controls
   проверены на stubs, новый whole operator run после handover не заявляется.
3. Явные migrations15/own guard/accounts/presentation wells/sources выполняет seed;
   local/research, ML_AUTORUN_ENABLED=false, source interval86400/next_pollfuture.
   Own readonly90 Rabbit collector config/--user rabbitmq/strict4queuegauges и
   exact C2management namespace permissions автоматические. Startup ничего не
   мигрирует/seed-ит на рабочей БД.
4. Submit durable intent до202; source bytes фиксируются позже immutable capture.
   При потере ответа повторить replay key, GET читает persisted capture/write/coverage.
   После capture commit не refetch; unknown commit определяется checkpoint.
   Finite worker reconnect8/backoffs может завершить процесс: operator/orchestrator
   restart получает новый claim/session/fence, не resurrect старую lease.
5. Authority/profile change revoke; каждый A07 fault сохраняет real≥7s late-I/O окно.
   Source PUTdraft восстанавливается intendedstatus PATCH с own temporary exact-source
   futurepoll guard/finally cleanup, не permanent production trigger/global pause.
   Не редактировать job attempts/fence/lease/quota или capture вручную.
6. Rollback: own guard accepting=false, owner cancel nonterminal/current revision,
   stop dispatch/consumers≤10s и проверить leases/owned transactions0. Новый API off
   даёт authorized GET200/POST-cancel503, сохраняя protected writer semantics.
   Данные/schema/capture/checkpoint/coverage не удаляются down migration.
7. **Старый executable** только после drain всех старых/новых telemetry writers/API/
   schedulers и отсутствия их PG connections. Все sources paused, PG readonly.
   Unrestricted old writes не имеют C2 fences; same protection не обещается.
   ACT14 A10 проверил nonempty accepted data preservation в этих точных границах.
8. До optional worker2 retirement/API replacement сохраняются complete current∪retired
   logs и exact ID/run binding; physical stop→fresh sampler ACK→guarded removal→
   Engine absence→immutable receipt. Required roles не исключаются из supervision.
   Fixed-cutoff/current-epoch/earliest3s sampler и78source controls сохраняют rawFAIL
   прежнего ACT13; новый ACT14 принят только по собственному canonical A11.
9. Export safe metadata, выполнить exact ledger teardown, recheck working IDs/ports.
   ACT14:592.801s,569relevant/596total samples,897host+wait4; reconstructed917348352B
   и sampled956289024B — разные aggregate metrics, оба ниже1536MiB; ML0/canary0,
   226surfaces/8logIDs, working8same/free5. Неатрибутированный volume не удалён;
   полного environment cleanup нет. Stand01 и prior failures остаются отдельной историей.

Точный code/binary binding, per-case durations/caveats и non-production scope — в
runtime guide. PostgreSQL16 и app schema15 различаются; C2 не продвигает legacy
last_measured. Root выполнил handbook sync/build/validate/source-check и independent extracted ZIP validation после docs review.

## Персональные уведомления · schema19

F4 source подготавливает schema19 поверх18; запуск не выполняет DDL/activation.
Для будущего maintenance обязательны backup18, explicit migration, сверка старых
IDs/results/reviews/RBAC, обновление всех publishers и drained однократная
activation через `bin/notifications-activate`. UI default-off не выключает capture.
Restore согласованного snapshot сохраняет cutoff/fact IDs/first read_at/attempts.
Rollback старого binary требует остановки всех затронутых публикаций; selective
reset read/cutoff не предусмотрен. [Полный протокол](notifications.md).
