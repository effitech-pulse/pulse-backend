# Сборка запуск и сопровождение

## Проверенный PREP и обычный local

Основная инструкция: [изолированный runtime](../references/readiness-runtime.md). PREP использует отдельную восстановленную app-БД pulse_prep и loopback UI8088/API8018, без рабочих research сетей. Порядок: подготовить секреты без вывода значений → поднять PG/ML/Data → восстановить согласованный backup в отдельную БД → явно применить миграции → capture existing presentation-v1 → bootstrap первого администратора → запустить backend/frontend. Успешный Health не заменяет каждый следующий шаг.

Обычный compose.local уже имеет local/cohort defaults и требует явно заданную PULSE_COHORT_VERSION. До его запуска обязательны backup/migration/bootstrap/capture; прежние containers8080 этим дополнением не перезапускались. Используйте исходный deployment/playbook, а не команду up поверх неподготовленной рабочей БД. Миграции9/10/11 добавляют membership, immutable output и раннюю identity попытки; down-миграций нет. DDL в этом комплекте — справочная копия, не команда применения.

Остановка без удаления данных — compose stop выбранного проекта. Не выполнять down -v над рабочими grant volumes. Secrets, account JSON, dump и закрытые входы не публикуются; compose config для публичного evidence — только --quiet. Backup/retention/RPO/RTO промышленной среды остаются отдельным решением.

## Нативная разработка

Backend: `make build`, `./bin/api` из pulse-backend, настроив DATABASE_URL и ML_SERVICE_TARGET. Локальный go.work связывает backend/contracts; для независимой сборки нужен доступ к фиксированной версии contracts.

Frontend: `npm ci`, `npm run dev -- --port 5174` из pulse-frontend; при необходимости `VITE_API_PROXY_TARGET=http://127.0.0.1:8000`. Готовая сборка — npm run build и dist. Для тестов нужен современный Node согласно разделу конфигурации.

Python: существующая общая repository-split/.venv не пересоздаётся. Для новой проверки/установки использовать отдельную чистую среду и согласованные wheels с hashes по runtime guide; editable workspace не подменяет clean-install evidence. ML запускается `python -m pulse_ml.server`, Data — `python -m utils.telemetry_emulator.server` с явными путями профилей/coverage/scenario. Не создавайте независимые копии данных в каждом проекте.

Лендинг: из pulse-landing `python3 build.py`; результат dist/index.html. Демо внутри лендинга не вызывает модель и не отправляет заявки/письма. Его демонстрационные статусы не являются результатами рабочего API.

## Персональный вход

Сначала применить миграцию доступа, затем CLI adminuser с `-login`/`-name` и паролем через защищённый stdin. После bootstrap оператор явно включает AUTH_MODE=local, выбирает политику cookie и проверяет разрешённый origin. В новых Compose уже local/cohort defaults; старые запущенные процессы не меняются от редактирования файла. Для Secure=false допустим только loopback HTTP, для TLS — Secure=true. Не храните секрет bootstrap в готовом примере команды.

## Проверки готовности

| Проверка | Что подтверждает | Чего не подтверждает |
|---|---|---|
| GET /health через Nginx | HTTP маршрут и соединение backend с PG | Качество модели и свежесть всех источников |
| Health ML RPC | Доступность gRPC | Возможность загрузить конкретный набор весов |
| Data /health | HTTP процесс | Наличие истории для любой скважины |
| Источник running + актуальный last_measured | Сбор и продвижение watermark | Отсутствие всех пропусков |
| Сохранённая ручная проверка | Цепочка Data → API → ML → PG | Промышленная прогнозная приёмка |

## Диагностика

| Симптом | Проверить |
|---|---|
| 502 через UI после замены backend | Жив ли backend, upstream DNS Nginx; пересоздать frontend |
| Источник недоступен в Docker | data:8092 вместо 127.0.0.1:8092; allowlist, secret, статус |
| Данные не обновляются | Отдельно last_received и last_measured, next_poll/lease, журнал цикла |
| Пустой архив | ID Pulse ↔ внешний ID ↔ coverage map, bounds и выбранный период |
| ML 503 | gRPC target/key, exact bundle/read-only mount и constraints; PG/cache/manifest нужны только research пути, не bundle-only Upload |
| 409 при сохранении | Обновить состояние, показать конфликт, не терять черновик |
| Нет входа | AUTH_MODE, bootstrap, origin, cookie Secure при HTTP, срок сеанса |
| Нет описаний параметров | Формат map canonical → labels; large.json нельзя монтировать напрямую как такой map |

## Backup и восстановление

Операционная БД, исследовательская PG, ClickHouse, модели/manifest и ключ шифрования источников имеют отдельный жизненный цикл. Перед миграцией выполняется согласованный backup; восстановление проверяется в изолированной среде. Нельзя считать сохранение одного docker image резервной копией данных. Политика retention, RPO/RTO и внешнего хранения backup ещё требует утверждения.

Смена комплекта модели не удаляет прошлые manual_check: они остаются привязаны к прежнему отпечатку. Не подключать research_rejected кандидата. Для отката приложения выбирается совместимая версия кода и схемы; автоматических down-миграций нет.


## Уточнение доступа и native-проверки — 19.10.2026

[Эксплуатационная инструкция backend](../references/backend-operations.md) разделяет HTTP 401/403, gRPC UNAUTHENTICATED, совместимый PATCH тревоги и review-процесс. Непустой ML_SERVICE_API_KEY проверяется также для Health; штатные Docker/Compose probes вызывают `python -m pulse_ml.healthcheck`, который получает ключ из окружения процесса и не выводит его/response details. Текущие profiles и секреты документационной очередью не переключались. Health подтверждает liveness/доступ, а не готовность model/cache/PG.

[Runtime guide](../references/runtime-installation.md) содержит фактически проверенный native-профиль, установку wheels, hashes/constraints, pip check и границы artifact-dependent тестов. Новая PREP-проверка отдельно зафиксировала clean native ML78/0skip и Linux ML78/Data32 PASS; это разные окружения, ни одно не является научной ML-приёмкой. Последние локальные свидетельства и пропуски отражены в [матрице проверок](../references/verification-matrix.md); внешний CI не запускался по одному факту добавления gates.

## Хранение и проверка попыток

Migration11 применена повторно на isolated БД с сохранением прежних значений и snapshot digests. Новые `running`/`error` строки получают profile/cohort_version при Begin до чтения участников; незавершённое не маскируется пустым done. Старые строки остаются unknown. После restart выбранный сохранённый output читается без повторного ML. Позднее изменение alert review отображается отдельным overlay выбранного run и не меняет его digest. Точные команды/логи и итог final run относятся к [PREP ledger](../references/readiness-ledger.md); handbook не запускает этот runtime заново.
