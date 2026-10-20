# O2/Q0 — требования и передача в архитектуру, 20.10.2026

Статус: **O2 — ACCEPTED (implementation/runtime/Senior/docs/publication); Q0 — архитектурная подготовка, реализация не принята**. Основание — одобренное пользователем продолжение после принятого O1/F1; [roadmap](functional-roadmap-2026-10-20.md), строки O2, Q0→Q1/F2 и решения D1–D4/O-data. O1/F1 не переоткрывается. После закрытия O2 продолжаем Q0→Q1/F2, затем прежние зависимости.

## O2: текущий evidence gate

Source Go/Python/optional stack прошёл task Senior reviews; whole-wave review выявил W1/P2 по памяти Grafana. Однострочный `GOMEMLIMIT=384MiB` при прежнем hard limit512MiB проверен:150samples/151.256s, cgroup peak425.25MiB, запас86.75MiB, swap/max-events/oom/restarts0; native Go limit402653184bytes. **W1/P2 CLOSED** независимым Senior. Три полных UI round-trips и финальный rollback прошли; финальные docs delta review и автономный publication gate PASS; O2 принята как supervised local pilot. Это локальный probe, не capacity/SLA.

| AC | Подготовленное evidence / оставшийся gate |
|---|---|
| O2-1 | Фактический detached `check.execute`: 8 spans/3 services, реальные PG/Data/ML связи и log→trace click; три UI log→trace→Related logs цикла пройдены, native responses200; подробности в [runtime](o2-runtime-2026-10-20.md) |
| O2-2 | Service/latency/error/export metrics и privacy проверки, отдельные cumulative snapshots. Actual ML dependency outage503→same key recovery200: error rpc/check counters по1, Backend export-failure counters0; это dependency failure, не model exception. Runtime metrics не являются model metadata. Exact queries — [runbook](observability-o2.md) |
| O2-3 | Все пять20s outages и actual expiry guard проверены; final rollback:13phase checks по прежним ID/SHA и recovered ML-failure ID, четыре O1 apps+PG up, пять optional services stopped. Заключительный guard fail-closed остановил пять после unobservable measurement; helper lookup absent не означает cancel RPC guarantee |
| O2-4 | Exact pins/budgets — [архитектура](o2-architecture-2026-10-20.md); auth loopback3300 проверен. W1/P2 CLOSED:425.25MiB peak/86.75MiB headroom после150samples/трёх UI cycles; budget167/200,inflight0. Контролируемая серия160samples исключила19 transitions; O2-off — producer off с частью stores, не полный O1 baseline |

Grafana-only `grafana-ui` bridge — **прямо разрешённое пользователем исключение локального пилота с возможным egress**; выключенные reporting/plugins/updates не являются сетевым запретом. Collector/Loki/Tempo/Prometheus остаются во внутренних сетях; бизнес-сети не меняются. Supervisor polling/retention не являются физической disk quota. RAM counters/offsets/очереди не гарантируют lossless/exactly-once; Collector restart может повторить diagnostics. Runbook и runtime принадлежат координатору, архитектура/C4 — architect-analyst; этот ledger связывает требования и evidence, не создаёт новый runtime proof.

## Цель и границы

Публикационный gate20.10.2026: independent final docs review APPROVED, открытых findings0; sync109mappings/588public sources, build33HTML sections, validate444local links/176OpenAPI refs/20draw.io pages/148checksums и standalone extracted ZIP PASS. Текущий regression suite фактически16/16PASS (не прежние14). PREP/O1 snapshots остаются датированными; source refs O2: backend`8b21da52ad9593ad4215bd30838dadd1cdac5f78`, ML`10df91a9e0539a7a1f36471e71de660f9d334cdb`, Data`942e0adb58c9b30849262fc44ff888d79f923e47`, frontend`c937bd6067c779092d4e0fe951f55c6421085520`. Будущий backend docs commit не включается сам в себя; local commit не означает push/release. Inventory содержит public working tree, в том числе не принадлежащие этой задаче planning inputs; авторство/реализация ими не присваиваются.

**O2:** инженер находит настоящий trace, связанные безопасные логи, latency/errors сервисов и ошибки экспорта в необязательном локальном Collector-contrib→Loki/Tempo/Prometheus/Grafana. Сохраняются принятые [O1 envelope/context/privacy](observability-o1.md), малая кардинальность и JSON stdout fallback. Для каждого сигнала выбирается один путь ingestion; Docker.sock не подключается. Runtime metrics не являются model metadata.

**Q0:** доказать возможность неизменяемого воспроизводимого входа и pinning комплекта до inference — необходимые контракты для последующего Q1/F2. Это не установка брокера и не заявление о durable consumer. По исходной дорожной карте period-check сохраняет hash, но не вход, а Upload не обеспечивает expected bundle; один hash не закрывает восстановление после crash. Архитектор должен связать каждую щель с конкретным контрактом и доказательством.

Предлагаемый первый saved-file flow: одна скважина, CSV/XLSX, raw≤20 MiB, ≤100000 строк, canonical≤50 MiB; XLSX expansion/columns также ограничены. Это предложение D1/D2, а не изменение лимитов старого sync Upload. Старый синхронный путь сохраняется. RabbitMQ рекомендован для локальных jobs; два брокера не вводятся. Q1/F2 начинается только после принятия Q0-контрактов.

## Проверяемые критерии приёмки

| ID | Результат и доказательство |
|---|---|
| O2-1 | Для реальных API→PG→ML и отдельного collection→Data/source→PG инженер открывает trace/linked roots и связанные sanitized logs; сохраняется отчёт с opaque ID и запросами поиска. Фиктивных Data spans нет. IDs не используются как metric/Loki labels; payload, credentials, cookies, DSN и private exceptions отсутствуют. |
| O2-2 | Latency, ошибки сервиса и exporter failures доступны запросами/панелью; fault injection отличает ошибку inference от ошибки диагностики. Выбранные metric names/units и безопасные dimensions описаны. |
| O2-3 | Остановка/перезапуск Collector и каждого store не меняет login/read/check/persist semantics. Нагрузка ограничена SDK/Collector queues, timeouts и storage quotas; drop/export errors видимы, stdout продолжает работать. Lossless/HA не заявляются. |
| O2-4 | Зафиксированы совместимые exact pins, конфигурация, замеры CPU/RAM/disk и workload, при котором они получены; UI доступны только loopback с аутентификацией, секреты вне Git. Численные бюджеты закрепляются до включения, без вывода production capacity. |
| Q0-1 | Synthetic proof сохраняет bounded immutable raw bytes, canonical input и snapshot mapping/timezone/parser recipe/source kind/Pulse ID; actor берётся из session. Fingerprint покрывает эту identity, list/status не читают payload. |
| Q0-2 | Один request key и тот же fingerprint возвращают тот же принятый объект/результат; другой fingerprint даёт 409. Restart/retry читает сохранённый snapshot, а не mutable/latest источник. HTTP attempt ID и operation request key различаются. |
| Q0-3 | Expected bundle/policy/recipe сохранены; доступность именно pinned артефактов проверяется **до** inference. Отсутствующий/сменившийся bundle приводит к явному отказу без fallback latest; сравнение hash после вычисления недостаточно. |
| Q0-4 | Proof включает crash/restart, отсутствующий input/bundle, превышение raw/rows/canonical/expansion/quota и повтор запроса. Server scope/роли перепроверяются; browser preview не authority. Нули, markers, уставки, Pulse/external ID и прежние sync response semantics сохранены; 30/60 дней не универсальная достаточность. |

## Зависимости и enable gates

O2 зависит от принятого O1, но не от Q1. Архитектор измеряет локальную среду: исходный discovery — Docker 12 CPU/~23 GB, свободно ~89 GiB на host; это не нагрузочная приёмка. Проверенный supervised pilot имеет конкретные pins/retention/budgets в архитектуре/runbook. Исходные O-data предложения7/3/15дней и будущий sampling не становятся разрешением unattended/production запуска.

Q0 synthetic proof допустим сейчас. Реальные частные файлы запрещено включать до решения владельца о TTL после terminal state, encryption, доступе оператора, quotas, backup/retention и max job lifetime. Активный input GC не удаляет; реальное автоудаление этим документом не разрешено. Для Q1 нужны также DB authority/outbox/idempotency/recovery; proof Q0 сам по себе их не подтверждает.

## Задачи и handoff

| ID / владелец | Область, зависимости и возвращаемый результат |
|---|---|
| O2-A / architect-analyst | После O1: выбрать pins/resources/ingestion, explicit budgets, безопасный доступ и failure matrix под O2-1…4; вернуть проект и измерения техлиду. |
| Q0-A / architect-analyst | Независимо от O2-A: определить snapshot/pinning contract, потребителей backend/ML/contracts, migrations/rollback и synthetic proof под Q0-1…4; вернуть закрытие щелей и оставшиеся gates. |
| O2-I, Q0-I / backend-middle через архитектора | Только после архитектурного handoff: реализация в назначенных владельцу файлах и проверки по AC; shared proto — через pulse-contracts. Точные paths/interfaces/test commands закрепляет архитектор до кода. |
| O2-R, Q0-R / backend-senior | Независимое ревью frozen diff и evidence исполнителя: privacy, bounded failure, immutable replay и pre-inference pinning; адресат — архитектор и техлид. |

DoD: requirement→task→diff→check, воспроизводимые команды/результаты, runbook/rollback, перечень OPEN и независимое ревью. Runtime использует изолированные UI/API8088/8018 и **PREP PostgreSQL55439**; отдельные lane integration tests используют **test PostgreSQL55432**. Эти БД не взаимозаменяемы. Исходные8080/live DB, shared venv/data symlinks не затрагиваются. SSO, SMTP/внешняя доставка, обучение/promotion, HA/Kubernetes/production и scientist assessment вне scope; последнее остаётся user-deferred/not_assessed.

## Использованные скиллы

- **to-spec** — `/Users/daniilkoreskov/.codex/skills/to-spec/SKILL.md`: синтез одобренного контекста без нового интервью; pre-inference availability и восстановление snapshot стали отдельными AC вместо обещания «сохраняем hash».
- **to-tickets** — `/Users/daniilkoreskov/.codex/skills/to-tickets/SKILL.md`: задачи получили владельцев и blocking edges; Q1/F2 отделён от Q0, O2 не стал лишним blocker Q0. По поручению задачи сохранены здесь, без внешней публикации.
- **writing-plans** — `/Users/daniilkoreskov/PycharmProjects/grant/.agents/skills/writing-plans/SKILL.md`: scope check и failure-focused self-review дали самостоятельные gates и evidence; exact file/interface/test plan оставлен архитектурному этапу, этот документ не выдаётся за implementation plan.
- **verification-before-completion** — `/Users/daniilkoreskov/PycharmProjects/grant/.agents/skills/verification-before-completion/SKILL.md`: проверены diff whitespace, локальные ссылки и покрытие AC исходным brief/roadmap; runtime checks не запускались, статус остался OPEN.
