# Пульс скважины — API

FastAPI, миграции и операционная БД. Библиотека модели устанавливается отдельным пакетом; исходники `esp` сюда не копируются.

Для разработки расположите проекты model и Effitech рядом (в подготовленном каталоге они называются pulse-model и pulse-backend):

```sh
python3 -m venv .venv
.venv/bin/python -m pip install -e ../pulse-model
.venv/bin/python -m pip install -e '.[test]'
cp .env.example .env
# Настройте DATABASE_URL, PG_* и ML_DATA_ROOT в .env.
.venv/bin/python -m alembic upgrade head
.venv/bin/python -m uvicorn src.main:app --reload --port 8000
```

Тесты: `.venv/bin/python -m pytest`. Для релиза установите wheel `effitech-esp-model` версии 0.1.0. ML_DATA_ROOT содержит models/stage1_binary_v1, cache/features_v3 и manifest/grant_manifest_1000.xlsx. PostgreSQL esp_app хранит приложение; PostgreSQL esp хранит исследовательские справочники. Артефакты модели и данные не включены в Git. Seed-скрипты в scripts/ ожидают локальный data/manifest — подготовьте его отдельно.
