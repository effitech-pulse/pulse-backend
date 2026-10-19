> Снимок документа 20.10.2026. Источник: `pulse-backend/docs/readiness-runtime.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# Изолированный PREP runtime

Профиль compose.prep.yaml (исходник `pulse-backend/deployments/compose.prep.yaml`; в пакет не включён) проверяет пользовательский путь с персональным входом, отдельной PostgreSQL и синтетической телеметрией. Он не подключён к рабочим сетям PostgreSQL/ClickHouse. ML получает только каталог models: manifest/cache/исследовательская БД ему не передаются. Успешный Health проверяется отдельно от Upload и фондового расчёта.

Требования и результаты приёмки ведутся в [PREP ledger](readiness-ledger.md); архитектура и ограничения — в [контракте](readiness-architecture.md). Самостоятельное подключение будущего ML-модуля не требует подменять эти ограничения новыми метриками.

## Подготовка

Команды выполняются из pulse-backend. Нужны соседние исходники model/contracts/data/frontend и существующие read-only `workspace-data/models`, `workspace-data/emulator`. Все образы собираются из согласованных исходников, а не скачиваются по старому release tag.

Создать локальный файл секретов один раз, без вывода значений:

```sh
python3 - <<'PY'
import os, secrets
fd = os.open('.env.prep', os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'w') as f:
    f.write('PREP_POSTGRES_PASSWORD=' + secrets.token_hex(24) + '\n')
    f.write('PREP_ML_API_KEY=' + secrets.token_hex(24) + '\n')
PY
docker compose --env-file .env.prep -f deployments/compose.prep.yaml config --quiet
docker compose --env-file .env.prep -f deployments/compose.prep.yaml build
docker compose --env-file .env.prep -f deployments/compose.prep.yaml up -d --wait postgres ml data
```

`.env.prep` и файлы `.env.*` исключены из Git и Docker context. Не выполнять `compose config` без `--quiet` для записи публичного evidence: разрешённая конфигурация содержит секреты. Не переносить пароль в CLI, логи или README.

## База и capture

Пустая БД не содержит пользовательского фонда. Сначала восстановить **согласованную резервную копию app-БД** в `pulse-prep-postgres-1`, user/database `pulse_prep`. Не копировать в Git dump, реальные ID или входную телеметрию. Проверить восстановление по counts/checksums исходных таблиц. Службы приложения до этого не запускаются.

```sh
docker cp /absolute/private/app.dump pulse-prep-postgres-1:/tmp/app.dump
docker exec pulse-prep-postgres-1 pg_restore -U pulse_prep -d pulse_prep \
  --no-owner --no-acl --exit-on-error /tmp/app.dump
docker compose --env-file .env.prep -f deployments/compose.prep.yaml run --rm --no-deps \
  --entrypoint /app/migrate backend
docker compose --env-file .env.prep -f deployments/compose.prep.yaml run --rm --no-deps \
  --entrypoint /app/cohort backend -version presentation-v1
```

Capture принимает только уже зарегистрированные 100 presentation-скважин. Он не создаёт недостающих и не восстанавливает архив. Неполный или неоднозначный состав — ошибка; `LIMIT 100` и fallback в research запрещены. Membership остаётся прежним при изменении active/archive. Повторное применение миграций проверяется до старта; возвращение предыдущей версии приложения не требует удаления аддитивных таблиц.

Bootstrap создаёт первого администратора, только если аккаунтов ещё нет. Пароль (15–72 байта) передать одной строкой через stdin из защищённого файла/менеджера секретов:

```sh
docker compose --env-file .env.prep -f deployments/compose.prep.yaml run --rm --no-deps -T \
  --entrypoint /app/adminuser backend -login prep.admin -name 'PREP Administrator' \
  < /absolute/private/admin-password.txt
docker compose --env-file .env.prep -f deployments/compose.prep.yaml up -d backend frontend
```

Адрес интерфейса по умолчанию: [127.0.0.1:8088](http://127.0.0.1:8088). API: loopback8018. Порты переопределяются `PREP_HTTP_PORT`/`PREP_API_PORT`/`PREP_POSTGRES_PORT`. Для HTTP loopback cookie Secure=false; при TLS нужен отдельный профиль с Secure=true. В Compose включены local auth и APP_SCOPE=cohort. У обычного `compose.local` теперь те же безопасные defaults: перед его использованием явно выполнить backup/migration/bootstrap/capture и задать PULSE_COHORT_VERSION. Изменение файла не мигрирует работающие контейнеры автоматически.

## Что проверять

- Анонимный API401; login/me/logout; недопустимый Origin403; повтор старого session cookie после logout401.
- Два аккаунта с разными scope, viewer/technologist/admin; запрет мутаций viewer; список, прямой ID, telemetry, проверки, сохранённые runs, references и Excel соблюдают одну границу. Admin также не читает внекогортные ID.
- Полный membership100 отдельно от scored/archived/inactive/insufficient/error; result=null не превращается в norm/risk0. Ограниченные пользователи получают только свои ID и счётчики.
- Реальный API→Data→ML Upload без research cache/PG. Health с ключом и отказ без ключа — отдельные проверки.
- Чтение выбранного сохранённого snapshot после refresh/restart не вызывает новое вычисление. Старое происхождение unknown; hash mutable входа даёт partial, не complete replay.
- Аддитивная миграция сохраняет counts/checksums прежних записей. Injected failure откатывает snapshot/alerts/finish вместе. Рабочая БД не используется для integration.

Для integration используется отдельный `compose.test.yaml`, TEST_DATABASE_URL и уникальные тестовые схемы. `make integration` обязан реально выполнить тесты, а не SKIP. Cloud job подтверждается только ссылкой на его запуск; локальный эквивалент не выдаётся за cloud CI.

### Измерение Upload

Клиентский smoke (исходник `pulse-backend/test/smoke/upload.py`; в пакет не включён) обозначает вызовы `first/repeat`: первый вызов клиента сам по себе не доказывает холодный процесс модели. Для cold/warm evidence дождаться завершения всех bulk/Upload, перезапустить **только isolated ML**, дождаться Health и выполнить smoke первым вычислительным запросом процесса:

```sh
docker compose --env-file .env.prep -f deployments/compose.prep.yaml restart ml
docker compose --env-file .env.prep -f deployments/compose.prep.yaml up -d --wait --wait-timeout 120 ml
docker exec -i pulse-prep-ml-1 python - < test/smoke/upload.py
```

Между restart и первым sample допустим Health; Score, Upload и фондовые refresh не запускать. В evidence сохранить время restart/идентичность контейнера и отсутствие конкурентных вычислений. Если это условие не подтверждено, сообщать только `first/repeat`, без утверждения cold/warm. Три повтора проверяют одинаковое решение на одном синтетическом файле; не измеряют precision/recall или промышленный SLA.

Не выполнять `down -v` над рабочими проектами. Для остановки PREP без удаления копии БД:

```sh
docker compose --env-file .env.prep -f deployments/compose.prep.yaml stop
```

## Результат локальной приёмки20.10.2026

[Реестр совместимости](../inventory/readiness-compatibility.json) фиксирует семь reviewed runtime commit SHA, фактические локальные image IDs, версии и границы проверок. [Native manifests](../inventory/readiness-native-artifacts.json) содержат119 публичных source hashes и50 wheel hashes свежей offline-установки. Это идентичность локальных артефактов; публикация образов/пакетов и cloud CI не выполнялись.

Изолированный стенд прошёл API→Data→ML smoke и финальный run33:100members/100scored,0ошибок; ограниченный аккаунт видит0. Тестовые archive/inactive состояния восстановлены через API с актуальными revisions. Сохранённые snapshots30/32 после этого и нового расчёта совпали; отдельная проверка run30 после ML restart также прошла. Миграция11 применена и повторена; прежние значения и digest snapshots сохранены без ретроспективного заполнения profile.

`make check`, сборка4CLI и `make integration` с настоящей отдельной PostgreSQL/race прошли. LinuxML78/Data32, nativeML78 без skips и frontend30 прошли; финальный browser gate22/22 без pageErrors. Senior закрыл подтверждённые замечания backend, ML validator и frontend; source/CI gate принят. Полный отчёт требований — [PREP ledger](readiness-ledger.md).

Независимая научная приёмка отложена пользователем: внешний архив/physical oilfield mapping/cost criteria будут подключены позднее по [протоколу](independent-acceptance.md). Новые метрики качества, обучение и продвижение модели не выполнялись. Immutable output snapshot не заменяет immutable input bytes: provenance остаётся partial. Эти результаты относятся к изолированному loopback PREP, исходный pulse-local8080 не перезапускался.
