# Источники документации и публикация

Редакция22.10.2026, после Q0 и реализации Q1/F2. Владелец комплекта — tech-lead. Этот автономный handbook является датированным снимком рабочего дерева семи pulse-репозиториев. Актуальный код/контракты принадлежат исходным модулям; целевые решения отделяются от фактического поведения. Наличие контрольных сумм подтверждает целостность снимка, а не истинность научных выводов или работу production runtime.

## Владение и обновление

| Область | Авторитетный источник в workspace | Владелец / триггер обновления |
|---|---|---|
| HTTP API | pulse-backend/api/openapi.json | architect-analyst + backend: любое изменение маршрута/DTO/прав; синхронизировать текст главы 08 отдельно от копии JSON |
| gRPC/SDK | pulse-contracts/proto, buf-конфиги | architect-analyst: изменение схемы/совместимости/версии; сохранение копии не объявляет релиз SDK |
| Операционные SQL | pulse-backend/internal/migrations | architect-analyst: принятая миграция; копирование DDL не выполняет миграцию |
| Исследовательские PG/CH SQL | pulse-data/deploy | architect-analyst: изменение исходного DDL |
| Дизайн/тема/макеты | pulse-frontend/src/theme и design; Figma file_key km4tIbp3LnMTRrV6yi6Qho | designer с согласованием архитектора: изменение handoff/токенов/реестра/ресурсов; live Figma читается отдельно по задаче |
| Проверки и gates | pulse-backend/docs/verification-matrix.md | tech-lead: Makefile/package scripts/CI/runtime/SKIP; разовый PASS не становится обещанием CI |
| Семантика модели/RPC | pulse-ml-service/docs/model-rpc-compatibility.md | architect-analyst: feature context/decision rule/provenance; предложения не объявляются реализованными |
| Научные ограничения | pulse-model/docs/causal_baseline.md и EXPERIMENTS.md | architect-analyst + tech-lead: новый датированный эксперимент; генератор и старые group-CV не подменяют temporal-приёмку |
| O2 диагностика/пилот | pulse-backend/docs/observability-o2.md, o2-runtime-2026-10-20.md; o2-architecture-2026-10-20.md и o2-observability.drawio | Координатор владеет runbook/runtime evidence; architect-analyst — pins/budgets/C4; tech-lead — execution/главы/публикация. Source review, runtime proof и финальная приёмка отмечаются отдельно |
| Q0 и предложение паспорт/Excel/помощник | pulse-backend/docs/q0-architecture-2026-10-20.md, q0-runtime-2026-10-21.md, well-operations-assistant-proposal-2026-10-20.md, well-operations-data-review-2026-10-20.md | Q0 synthetic internal acceptance принята; паспорт/Excel/помощник остаётся предложением. tech-lead + architect-analyst поддерживают отдельные факты и открытые gates |
| Q1/F2 synthetic jobs | pulse-backend/docs/q1-file-jobs.md, q1-runtime-2026-10-21.md, q1-file-jobs.drawio, internal/migrations/file_jobs.sql | architect-planner владеет протоколом/схемой/C4; documentation-analyst и координатор — runtime facts/границы. Source review, actual fault/browser proof и whole acceptance отмечаются отдельно; default-off и real-file disabled сохраняются |
| Рабочие инструкции модулей | README/AGENTS каждого репозитория | tech-lead: граница модуля, команды, утверждённое решение или статус |
| Основные главы 01–19, схемы сценариев | Markdown/diagrams внутри этого handbook | tech-lead; архитектурные схемы сверяет architect-analyst; правятся содержательно после изменения источников |

Дата публикационного снимка задаётся явно полем `snapshot_date` (ISO) реестра. Sync и check используют сохранённую дату, а не текущие часы; смена даты — явное действие при новой редакции.

Точная связь source→snapshot, владелец и SHA256 обоих содержимых находятся в [реестре копий](inventory/snapshot-sources.json). У Markdown-копии ссылки переписываются внутрь комплекта; непоставляемый исходник сохраняется как текстовый путь для поиска в workspace и [source inventory](inventory/source-files.csv). Копия не становится отдельным источником требований.

Основные дополнительные материалы: [матрица проверок](references/verification-matrix.md), [модели/RPC](references/model-rpc-compatibility.md), [frontend handoff](references/frontend-handoff.md), [дизайн](references/frontend-design.md), [причинный бейзлайн](references/causal-baseline.md). Исходные аудит и журнал исполнения лежат в pulse-backend/docs вне автономного комплекта; README исходных репозиториев ведут к ним. Доказательства аудита сохраняют старые path:line; журнал отдельно фиксирует исправления и приёмку.

## Два вида проверки

**Автономная проверка** не требует workspace, сети, модели или БД. Из распакованного handbook:

```sh
python3 tools/validate.py
```

Она проверяет локальные ссылки/HTML-якоря, OpenAPI refs, draw.io, контрольные суммы, SHA256 локальных snapshots и состав ZIP. Ссылка на файл вне package root недопустима. По автономному PASS нельзя заключать, что исходники не изменились после создания снимка.

**Сверка с исходниками** выполняется отдельно при наличии семи checkout. Передать корень repository-split явно:

```sh
python3 tools/sync_sources.py --workspace /path/to/repository-split --check
```

Она сравнивает источники, локализованные копии и inventory, выявляя изменённые, новые и удалённые публичные исходники. `--check` ничего не записывает; отсутствие checkout или несовпадение — ошибка, не допустимый SKIP публикационного gate. Готовый ZIP остаётся автономным и не запускает эту проверку автоматически.

Регрессионные проверки инструмента с временными fixture-копиями выполняются из полного workspace:

```sh
python3 tools/test_sync_sources.py -v
```

Они проверяют source/new-file/snapshot drift, восстановление sync, отсутствующие/устаревшие checksums, различие bytes/file-set ZIP, path traversal и автономный validate распакованного пакета после удаления fixture workspace. Исходные репозитории, Git index, данные и runtime этими тестами не меняются. Git-метаданные временного fixture подставляются отдельно; чтение реального Git при финальном sync проверяется публикационным проходом.

## Публикационный проход

1. Принять изменения соответствующих модулей, зафиксировать результаты в журнале и закрыть review. Tech-lead сверяет основные главы с фактами, включая текущие профили запуска и явно обозначенные цели. Не менять продуктовую реализацию ради синхронизации документации.
2. Выполнить `python3 tools/sync_sources.py --workspace /path/to/repository-split --sync`. Команда обновляет только mapped snapshots, source inventory/метаданные и таблицу версий главы 18. Основные главы и статусы вручную оцениваются отдельно; sync не выводит их из кода.
3. Выполнить `python3 tools/build.py`, `python3 tools/validate.py`, затем внешнюю `--check`. Проверить поиск/навигацию HTML и перенесённые темы/диаграммы по изменённой области. Новые результаты занести в журнал; после записи журнала повторить sync/build/validate/check, чтобы source inventory учитывал финальное содержимое журнала.
4. При передаче архива дополнительно проверить validate.py в отдельной копии без соседних репозиториев. Этот проход создаёт файлы документации, но не публикует release/PR/сайт.

## Исключения и отсутствие цикла хешей

Публикационный снимок соответствует **public working tree**, а не только HEAD/индексу Git. Inventory автоматически учитывает допустимые публичные untracked документы, включая отдельные assistant/concept предложения других владельцев; их наличие/hash не означает принятую реализацию, принадлежность автору публикации или включение в его commit. Mapped snapshots назначаются реестром отдельно с владельцем; SDD/private runtime evidence в комплект не копируются. В итоговом отчёте различаются authored changes, generated snapshots/inventory и чужие public inputs. Внешний `--check` подтверждает соответствие этой базе, не чистоту Git или remote publication.

Source inventory содержит публичные текстовые исходники/конфигурации разрешённых расширений и Makefile/Dockerfile. Каталоги данных, окружений, Git, node_modules, build outputs, кэши, исторический archive/reference, секреты `.env*`/ключи/credentials и symlinks исключены. В индекс не входят бинарные веса, реальные выгрузки и сам project-handbook. Отдельные публичные SVG/PNG входят только в явно назначенные snapshots реестра, а не в общий текстовый source inventory. Наличие секретного/data-пути в реестре отклоняется. Полный preflight источников и targets проверяет каждый компонент на symlink и повторно проверяет resolved области до чтения mapped source или записи копий; некорректный реестр не создаёт частичных результатов. Политика индексации также пропускает приватные каталоги .aws/.ssh и не следует symlinks. Временные SDD-заметки в `.superpowers/` исключены из обхода и запрещены как explicit registry source/target; долговечные requirements/evidence живут в модульных docs. Это исключение не скрывает произвольные dot-каталоги.

Исключение самого handbook из source inventory устраняет самоссылку. Локальные checksums охватывают комплект, кроме checksums и ZIP; ZIP содержит подготовленные файлы и checksums, но не самого себя. Изменения опубликованных копий всегда выполняются через источник/реестр, а не ручной правкой generated копии. Для нового вида документа/расширения сначала определить безопасное правило включения и владельца.

## Архивные task-источники

В реестре `source_kind: archived_task` обозначает шесть опубликованных исторических документов с originals в необязательном `tasks/pulse-backend/docs/` вне checkout. Записи без этого поля имеют обязательный вид `repository` по умолчанию. Архив задач не включается в публичный source inventory.

Доступный original проходит обычные check/sync и обновление обеих SHA. При его отсутствии check/sync сохраняют bytes снимка и обе SHA: обязательны существующий snapshot, валидные source/snapshot SHA256 metadata и совпадение фактической SHA снимка. Неполный или повреждённый fallback блокирует весь sync до записи. Это подтверждает целостность сохранённого снимка, но не соответствие недоступному original. Изменение snapshot_date не перерисовывает retained snapshot.

Обновление: скорректировать ссылки доступных originals, выполнить sync, затем build и validate. Полный workspace source-check требует семь checkout, но допускает отсутствие только архивных originals; автономный validate проверяет поставленный пакет без workspace и tasks.
