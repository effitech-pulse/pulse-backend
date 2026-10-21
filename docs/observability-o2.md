# O2: локальная наблюдаемость

Статус: реализация и runtime-проверки выполнены; итог приёмки и публикации — в execution ledger. [Требования](project-handbook/references/o2-q0-execution.md), [архитектура и бюджеты](o2-architecture-2026-10-20.md), [редактируемая C4](o2-observability.drawio). Этот runbook описывает согласованный способ проверки; текущие фактические результаты — в [runtime-отчёте](project-handbook/references/o2-runtime.md).

O2 добавляет Collector, Loki, Tempo, Prometheus и Grafana к изолированному `pulse-prep`. Корреляция и безопасный JSON stdout сохраняют [O1-контракт](observability-o1.md). Трейсы и метрики идут по OTLP/gRPC; логи — только через ограниченные файлы. Метрики описывают работу сервисов, а не качество модели или действия сотрудников.

## Подготовка

Используются `deployments/compose.prep.yaml`, затем `compose.o1-f1.yaml`, затем `compose.o2.yaml`. Четыре приложения получают отдельные теги `:o2`; принятые `:o1-f1` остаются для отката. Исходный контур8080 и живые БД не используются.

Приватный env-файл содержит прежние PREP-настройки и `PULSE_GRAFANA_PASSWORD_FILE` — абсолютный путь к отдельному файлу пароля с правами0600 вне Git. Значение пароля не записывается в env. Для пилота явно задаётся `PULSE_OBSERVABILITY_ENABLED=true`; по умолчанию false. Producer logdir внутри optional overlay фиксирован `/var/log/pulse`.

Grafana доступна только на `http://127.0.0.1:3300`, пользователь `pulse-operator`. Native INI file provider читает tmpfs-копию пароля0400; краткий root bootstrap снижает права до UID472, у серверного процесса отсутствуют effective/permitted capabilities. Пароль не переносится через `GF_*__FILE` в окружение процесса. Collector, хранилища, OTLP и metrics endpoints на host не публикуются. На проверенном Docker Desktop host publish требует отдельной обычной сети `grafana-ui` только для Grafana. Пользователь явно разрешил её в локальном пилоте: Grafana может устанавливать исходящие соединения. Отключённые reporting/plugins/updates не являются сетевым запретом egress; production-решение этим исключением не согласовано.

Перед включением нужны успешные проверки pinned-конфигураций, Linux-сборок, прав томов/ротации, login и supervisor. Файлы каждого producer ограничены tmpfs16MiB; собственная ротация приложений держит current/.1/.2 по2MiB, Nginx проверяет размер раз в секунду с возможным превышением порога до следующей проверки. Лимиты пяти контейнеров и ретеншн описаны в архитектуре. Grafana использует мягкий Go memory target384MiB при прежнем hard512MiB/.5CPU; он не ограничивает RSS напрямую. Проверенный локальный UI workload и cgroup headroom приведены в runtime-отчёте.

## Управляемый пилот

Пределы одного supervised запуска: до30минут, до200 synthetic business HTTP requests, до2 одновременно. Общий budget-файл контроллера резервирует каждый запрос; latency измеряется только вокруг HTTP, отдельно от reserve/release. Это предел тестового workload, не глобальный ingress limit приложения.

```sh
python3 scripts/o2_pilot.py budget-init --budget-file <absolute-budget-json>
python3 scripts/o2_pilot.py reserve --budget-file <absolute-budget-json>
python3 scripts/o2_pilot.py release --budget-file <absolute-budget-json> --token <returned-token>
python3 scripts/o2_pilot.py status --budget-file <absolute-budget-json>
```

Budget не обнуляется повторным init. Release выполняется в finally; зависшая lease после crash автоматически не освобождается. Перед supervisor полным merged Compose выполняется `--profile observability up --no-start --no-deps otel-collector loki tempo prometheus grafana`: создаются тома для первоначального замера. Затем в отдельном процессе запускается supervisor:

```sh
python3 scripts/o2_pilot.py supervise --project pulse-prep \
  --env-file <absolute-private-env> --evidence <new-jsonl> \
  --budget-file <absolute-budget-json> --duration-seconds 1800
```

После успешного первоначального измерения координатор запускает пять optional services и приложения. Supervisor проверяет свободное место host, прирост четырёх store volumes, время и доступность budget/evidence. Замер выполняет короткоживущий helper: только четыре собственных тома read-only, network none, read-only rootfs,64MiB/0.2CPU, единственная добавленная capability DAC_READ_SEARCH. Без неё закрытые каталоги хранилищ нельзя корректно измерить.

Рост stores≥1GiB, свободное место<10GiB, истечение срока либо недоступное измерение/evidence приводят к остановке ровно пяти optional services. Результат остановки сохраняется, ошибка не называется успехом. Приложения и PostgreSQL продолжают работу; уже созданные app exporters остаются ограниченными очередями/timeout до отдельного контролируемого отката. Обычные named volumes не имеют физической disk quota; polling допускает overshoot. Ретеншн и guard не заменяют эту квоту и не разрешают unattended запуск. Проверка идёт каждые5s с накладными расходами: последовательный worst-case бюджет измерений CLI до46s, lock до0.5s и stop до25s. Deadline1800s проверяется по monotonic timer, завершение допускает эти накладные расходы; это не жёсткая wall-clock граница. При timeout создания helper завершение Docker RPC может остаться неизвестным; cleanup и отсутствие подтверждаются только на момент проверки daemon.

## Поиск и диагностика

В Grafana provisioned UIDs: `pulse-loki`, `pulse-tempo`, `pulse-prometheus`. Ссылки связывают trace с безопасными логами; detached operation может иметь отдельный root с link на HTTP trace. Request ID попытки отличается от request key бизнес-операции.

[Запросы метрик](../deployments/observability/metrics-queries.md) используют реально проверенные имена pinned-компонентов. Loki `/series` показывает индексируемые labels; stream map ответа query_range дополнительно содержит structured metadata и не является списком index labels. Trace/span/request IDs остаются полями/контекстом, а не labels метрик или индекса Loki; metric exemplars выключены.

При недоступном Collector безопасный `telemetry.snapshot` в stdout каждые10s показывает размеры очередей и накопленные потери/ошибки экспорта. После восстановления cumulative counters отправляются снова. Отсутствующая Prometheus series означает пробел наблюдения; её нельзя считать нулём. RAM counters, offsets и очереди не дают lossless/durable гарантии; crash может скрыть последние сведения. Filelog checkpoint находится в tmpfs и может потеряться при unmount/recreate; повторное чтение после восстановления дало фактические дубликаты диагностических строк. Метрики/счётчики и dedup бизнес-результатов не означают exactly-once log transport.

## Откат

Остановить только `otel-collector loki tempo prometheus grafana` полным O2 Compose, затем вернуть приложения через прежние prep/O1 overlays и теги`:o1-f1`. Это контролируемый restart приложений, не действие disk guard. Исходный Nginx снова пишет только stdout по O1. Business PostgreSQL, история проверок, входы, модельные данные и store volumes не удаляются. Общий `down -v` не используется; удаление диагностических томов требует отдельного явного действия оператора.
