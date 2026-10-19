> Снимок документа 20.10.2026. Источник: `pulse-backend/docs/configuration.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# Конфигурация

Проверено по исходникам 19.10.2026: config.Load (исходник `pulse-backend/internal/config/config.go`; в пакет не включён), bootstrap (исходник `pulse-backend/internal/bootstrap/application.go`; в пакет не включён) и пример окружения (исходник `pulse-backend/configs/app.env.example`; в пакет не включён). Backend читает `.env` в рабочем каталоге; уже заданные переменные процесса имеют приоритет. Таблица перечисляет все ключи примера. Default означает отсутствие переменной, а не пустую строку: явно заданное пустое значение не заменяется default и для чисел/boolean обычно даёт ошибку.

| Параметр | Default / пример или локальный Compose | Профиль, проверка и область доверия |
|---|---|---|
| `HTTP_ADDR` | `127.0.0.1:8000`; Compose `:8000` | Оба профиля. Bind проверяет HTTP listener; Compose публикует только loopback |
| `DATABASE_URL` | `postgresql://esp:esp@localhost:5433/esp_app` (default разработки); пример заменяет пароль на `CHANGE_ME` | Оба. Секрет операционной БД; разбор/подключение в pgx. Legacy DSN-префикс `postgresql+psycopg2://` преобразуется в `postgresql://` |
| `DB_POOL_SIZE` | `5` | Оба. Целое 2–1000; соединение прогона удерживает advisory lock |
| `API_V1_PREFIX` | `/api/v1` | Оба. Удаляется завершающий `/`; отдельного валидатора маршрута нет |
| `AUTH_MODE` | `legacy` в коде; `local` в примере и Compose | Только `legacy` или `local`. Переключение не создаёт администратора/миграции автоматически |
| `APP_SCOPE` | `cohort` в коде/примере/Compose | Только `cohort` или `research`. Cohort ограничивает и admin; research выбирается явно |
| `COHORT_VERSION` | Пусто в коде; пример `presentation-v1` | Обязательное непустое значение при cohort; membership должен быть сохранён через bin/cohort. Не задано/не найдено — fail-closed без research fallback |
| `AUTH_COOKIE_SECURE` | `true` в коде/примере; false в loopback Compose | ParseBool при старте; cookie персональных сеансов local. Для HTTPS оставить true; false допустим только для loopback HTTP разработки |
| `API_KEY` | Пусто | Только legacy: непустой требует точного HTTP `X-API-Key` для `/api/v1/*`; пустой даёт локальный общий сеанс. Local использует отдельную авторизацию, этот ключ её не заменяет |
| `CORS_ORIGINS` | `http://localhost:3000,http://localhost:5173`; Compose перечисляет 8080/5174 | Оба. Разделение по запятой, trim, пустые элементы удаляются; точное совпадение origin. Local дополнительно проверяет origin изменяющих запросов; это не список исходящих хостов |
| `PARAM_DICT_PATH` | `parameters_dict.json` | Оба. Путь к внешнему справочнику; отсутствие файла даёт пустой список |
| `ML_SERVICE_TARGET` | `127.0.0.1:50051`; Compose `ml:50051` | Оба. gRPC target без `http://`; ошибка транспорта выявляется при RPC. Сам по себе target не включает TLS |
| `ML_SERVICE_API_KEY` | Пусто в коде; пример `CHANGE_ME`, Compose не задаёт | Независим от HTTP-профиля. Backend передаёт `x-ml-api-key`; непустой ключ ML-процесса требует точного совпадения во всех RPC, включая Health |
| `ML_SERVICE_TIMEOUT_S` | `300` | Оба. Положительное конечное число секунд, результат не меньше 1 нс; deadline каждого RPC |
| `ML_AUTORUN_ENABLED` | `true` в коде; **false** в примере/локальном Compose | Оба. ParseBool; автоматический прогон модели, отдельно от сбора телеметрии |
| `ML_AUTORUN_HOUR` | `5` | Оба. Целое 0–23 в `TZ` |
| `TZ` | `Europe/Moscow` | Оба. time.LoadLocation; расписание и naive timestamps БД |
| `METABASE_SITE_URL` | Пусто | Оба. Адрес Metabase; удаляется завершающий `/`, отдельного URL-валидатора в Config.Load нет |
| `METABASE_EMBED_SECRET` | Пусто | Оба. Секрет HS256 подписанных iframe URL; не включать в клиентскую конфигурацию/логи |
| `METABASE_DASHBOARDS` | `[]` | Оба. JSON-массив `{id,name}`; ошибка JSON отвергается при старте, null нормализуется в пустой массив |
| `METABASE_TOKEN_TTL_S` | `3600` | Оба. Целое ≥1, срок жизни подписанного URL |
| `INTEGRATION_ALLOWED_HOSTS` | Пусто в коде; пример `127.0.0.1,localhost`, Compose добавляет внутренние имена | Оба. Читается bootstrap, не Config.Load. Точные hostname без схемы/порта, регистр не важен; разрешённость проверяется адаптером при обращении к внешнему источнику и Pulse Data. Пустой список не разрешает произвольный хост |
| `INTEGRATION_SECRET_KEY` | Пусто | Оба. Отдельный постоянный ключ AES-GCM: ровно 32 байта после base64-декодирования. Проверяется при Seal/Open непустого секрета, а не при старте. Пустой допускает источники без сохраняемого секрета; не позволяет сохранить непустые credentials |
| `TELEMETRY_DATA_API_URL` | Пусто в коде; пример `http://127.0.0.1:8092`, Compose `http://data:8092` | Оба. Bootstrap передаёт в Archive; http/https и allowed host проверяются при запросе, redirects запрещены. URL внутри контейнера должен адресовать Pulse Data, не loopback backend. Это чтение архива, не адрес источника коллектора |

## Профили доступа

`legacy` сохраняет общий API-key/локальный сеанс с административными правами. `local` включает персональные сеансы и роли; сначала оператор явно применяет миграции и создаёт первого администратора через `bin/adminuser` с паролем из защищённого stdin, затем меняет профиль и перезапускает API. Порядок и ограничения: персональный доступ (исходник `pulse-backend/docs/telemetry-integrations.md`; в пакет не включён). PREP согласовал включение local: оба Compose-файла теперь задают local/cohort. Это не мигрирует уже работающий экземпляр. Для compose.local требуется PULSE_COHORT_VERSION; для изолированного compose.prep — PREP_COHORT_VERSION (default presentation-v1). Backup/migration/bootstrap/capture обязательны до запуска; [порядок PREP](readiness-runtime.md).

HTTP `API_KEY`, внутренний `ML_SERVICE_API_KEY`, ключ шифрования интеграций и пароли БД — разные секреты. Наличие одного не включает другой. Plain-text gRPC ограничивается loopback/защищённой внутренней сетью. Как согласованно задать внутренний ключ и проверить probe без его вывода: [эксплуатация](backend-operations.md#проверка-ml-и-внутренний-ключ).

Некорректные числа, boolean, timezone и JSON, перечисленные в Config.Load, отвергаются при старте; адреса интеграций/ключ AES проверяются при использовании. CORS не является аутентификацией. Секреты, пароли и живые `.env` в Git и журналы не включаются.

## Граница cohort

Версия фиксирует ровно100 зарегистрированных Pulse ID независимо от active/archive. Изменение lifecycle не меняет membership. Видимый набор пересекается с principal scope; чужие ID, статистика, сохранённые runs и Excel не обходят границу. Глобальные записи НГДУ/цехов в cohort-профиле запрещены403 даже admin: справочник может быть общим с research. Настройка профиля не удаляет исторические данные.
