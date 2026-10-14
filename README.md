# Пульс скважины — Go backend

Весь прикладной backend перенесён на Go: HTTP API, PostgreSQL, скважины, справочники, инциденты, чтение прогнозов и осложнений, классификация, Excel, архив событий, аудит, Metabase JWT, управление ML-прогонами и обратная связь. Python и ML-библиотеки для запуска backend не нужны. Модель, признаки, SHAP, дрейф и обучение остаются в отдельных Python-проектах; backend обращается к ML-сервису по gRPC.

```sh
cp .env.example .env
# Настройте PostgreSQL и ML_SERVICE_TARGET / ML_SERVICE_API_KEY.
go mod download
go run ./cmd/migrate
make build
./bin/api
```

Требуется Go 1.24+; локально проверен Go 1.27.2. HTTP: `127.0.0.1:8000`, gRPC ML: `127.0.0.1:50051`. Ключ межсервисного доступа совпадает с ML-сервисом и не передаётся frontend. Публичные `/api/v1/*`, JSON поля и Excel-форматы сохранены. `/openapi.json` содержит контракт прежнего API. При недоступности модели ML-запросы возвращают 503, CRUD продолжает работать. `DB_POOL_SIZE` должен быть >=2: один connection резервируется блокировкой прогона. Локальная зона naive timestamps и расписания задаётся `TZ` (Europe/Moscow по умолчанию).

## База данных

`cmd/migrate` создаёт PostgreSQL-схему новой БД из снимка прежних Alembic миграций 0001–0012. Для существующей БД принимает только `alembic_version=0012_ml_run_drift`, не пересоздаёт таблицы и фиксирует принятие схемы в `backend_schema_migration`. Старую ревизию или БД без Alembic version отвергает; автоматические destructive изменения отсутствуют. Миграции выполняются отдельной командой, не при запуске HTTP API. Исторический Python-код и миграции сохранены вне активного проекта в `../reference/pulse-backend-python`; исходный grant также сохранён.

## ML и задания

`internal/mlpb` — готовый Go-клиент контракта `pulse.ml.v1`, схема находится в `proto/ml.proto`; канонический источник — `../contracts/ml/v1/ml.proto`. Каждый RPC имеет deadline `ML_SERVICE_TIMEOUT_S`. Файлы до 50 MiB передаются по 1 MiB. Optional, int64, false и валидные нули сохраняются при преобразовании в публичный JSON. Транспорт по умолчанию без TLS для loopback/защищённой внутренней сети.

Ручные и ежедневные прогоны защищены PostgreSQL advisory lock между экземплярами API. Список тревог, перенос статусов и завершение прогона фиксируются одной транзакцией. Низкий текущий риск и плохое качество данных не отменяют ярус, присвоенный ML. Shutdown дожидается принятых фоновых прогонов; следующий запуск помечает незавершённые после аварии записи как error. Скоринг не повторяется автоматически при timeout: Python-вычисление может завершиться после ухода клиента. Долговечной очереди заданий пока нет. Расписание включается `ML_AUTORUN_ENABLED=true` и использует `ML_AUTORUN_HOUR`.

## Проверки

```sh
go test -race ./...
go vet ./...
# Полные проверки CRUD, миграций, Excel и блокировок в отдельной тестовой БД:
TEST_DATABASE_URL='postgres://postgres:TEST_PASSWORD@localhost:5432/pulse_test?sslmode=disable' go test -race ./...
```

Интеграционные тесты создают уникальные временные схемы и удаляют их после проверки. Без TEST_DATABASE_URL они явно пропускаются. CI использует отдельный postgres:16 service. Сквозная проверка Go HTTP → Python gRPC → PostgreSQL: после `make build` запустите из окружения с зависимостями ML и httpx/psycopg2 `TEST_DATABASE_URL=... python ../scripts/verify_go_backend.py`. Она использует синтетический контекст, проверяет все ML RPC, потоковый файл 5 MiB, int64/null, ярусы, сохранение прогонов, обратную связь и аудит; реальные веса не нужны.

Для Docker: `docker build -t pulse-backend .`; образ содержит `/app/api` и `/app/migrate`. Миграция: `docker run --rm --env-file .env --entrypoint /app/migrate pulse-backend`; адреса PostgreSQL/ML должны быть доступны из контейнера. При запуске API задайте `HTTP_ADDR=:8000` для публикации порта.

Генерация bindings из repository-split: `pip install grpcio-tools==1.78.0`, `go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11`, `go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.0`, затем `python scripts/generate_ml_rpc.py`. Go plugins должны быть в PATH или переданы через `--go-bin`. Генерация обновляет Python сервер и Go клиент из одной схемы; runtime не требует compiler. Seed исследовательского манифеста остаётся в `pulse-data/scripts/seed_app_*.py`.
