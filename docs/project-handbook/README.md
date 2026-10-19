# Техническая документация Пульс скважины

Комплект для разработчиков, технологов и сопровождения: от визуальной системы и макетов Figma до кода, контрактов, данных, ML, доступа и запуска.

**Начните с [HTML-версии с поиском](index.html)** или читайте разделы Markdown ниже. [Figma](https://www.figma.com/design/km4tIbp3LnMTRrV6yi6Qho) и [C4 в draw.io](diagrams/pulse-c4.drawio) связаны с описанием текущего кода. [Сценарии в draw.io](diagrams/pulse-flows.drawio) дополняют архитектуру.

## Статус · дополнение PREP, 20.10.2026

Изолированный PREP на loopback 8088/8018 проверен с персональным входом и `APP_SCOPE=cohort`, версия `presentation-v1`: зафиксированы 100 существующих участников, scope действует на API/экспорт/расчёты без обхода membership администратором. Обычный compose.local также получил local/cohort defaults, но изменение файла не переключает ранее запущенный экземпляр 8080; его рабочие БД и контейнеры в PREP не менялись.

Backend выполняет пользовательский bulk через телеметрию приложения и bundle-only Upload. Полный output snapshot сохраняется неизменяемо; provenance остаётся partial, поскольку точные входные bytes для replay не сохраняются. Senior-замечания PREP закрыты; ограниченный browser gate — 22/22 PASS, без ошибок страницы. Это технические проверки, а не научная приёмка модели. Пользователь отложил внешние данные и подключит ML позже: scientific acceptance `not_assessed`, продвижения модели нет. SSO, общий предметный журнал, доставка уведомлений, брокер и Kubernetes остаются отдельными задачами; полный перенос Figma не объявлен.

[PREP ledger](references/readiness-ledger.md), [архитектура](references/readiness-architecture.md), [runtime playbook](references/readiness-runtime.md) и [совместимость семи репозиториев](inventory/readiness-compatibility.json) и [native artifact hashes](inventory/readiness-native-artifacts.json) фиксируют источники и границы свидетельств. [Статус](chapters/02-status.md) отделяет подтверждённое от оставшихся задач. Push, cloud job и публикация release не выполнялись; горизонт 7–14 суток остаётся целью.

## Содержание

- [Назначение и границы проекта](chapters/01-project.md)
- [Текущее состояние и согласованные изменения](chapters/02-status.md)
- [Дизайн кит и правила интерфейса](chapters/03-design.md)
- [Каталог макетов Figma](chapters/04-figma-catalog.md)
- [Архитектура и взаимодействие приложений](chapters/05-architecture.md)
- [Разделение кода по модулям](chapters/06-modules.md)
- [Данные и схемы хранения](chapters/07-data.md)
- [HTTP API и gRPC контракты](chapters/08-api.md)
- [Источники и обработка телеметрии](chapters/09-integrations.md)
- [Модель и причинная валидация](chapters/10-machine-learning.md)
- [Рабочие сценарии и жизненный цикл скважины](chapters/11-workflows.md)
- [Аутентификация RBAC и защита данных](chapters/12-security.md)
- [Журналы события и уведомления](chapters/13-events.md)
- [Конфигурация и зависимости окружения](chapters/14-configuration.md)
- [Сборка запуск и сопровождение](chapters/15-operations.md)
- [Тестирование и критерии приёмки](chapters/16-quality.md)
- [Решения и план развития](chapters/17-decisions.md)
- [Инвентаризация исходников и версий](chapters/18-code-inventory.md)
- [Связь требований макетов кода и проверок](chapters/19-traceability.md)

## Вложенные материалы

- `design/brand-kit/` — мастера, экспорт логотипа и локальная презентация фирменного стиля.
- `design/figma/` — реестры узлов/тем; каталог даёт прямые ссылки на макеты.
- `diagrams/` — редактируемые draw.io и PNG для просмотра.
- `contracts/` — OpenAPI, protobuf, Buf и runtime constraints модели.
- `schemas/` — снимки операционных миграций, исследовательской PG и ClickHouse.
- `examples/` — иллюстративные JSON-запросы с описанием ограничений.
- `inventory/` — версии репозиториев, публичные манифесты и хеши исходников.
- `references/` — автономные копии матрицы проверок, PREP ledger/архитектуры/runtime, моделей/RPC, независимого acceptance protocol, frontend handoff и причинного бейзлайна.
- `SOURCE_POLICY.md` — источники, владельцы, триггеры и порядок синхронизации.
- `tools/` — sync_sources.py для отдельной сверки workspace, пересборка HTML/ZIP и автономная валидация.
- `checksums.sha256` — контрольные суммы файлов комплекта.

Реальные данные, сопоставления скважин, веса и секреты не включены. Основная HTML-документация открывается локально без серверов приложения. Figma, GitHub и внешняя документация требуют сети; исторический прототип v3 может использовать сторонние CDN. Для актуализации следуйте [политике источников](SOURCE_POLICY.md): содержательно сверяйте главы, обновляйте mapped snapshots/source inventory через `tools/sync_sources.py --workspace <repository-split> --sync`, затем выполняйте build/validate и отдельную внешнюю сверку `--check`. Самостоятельный `python3 tools/validate.py` работает в распакованном комплекте без соседних репозиториев. [Матрица проверок/CI](references/verification-matrix.md) и [совместимость моделей/RPC](references/model-rpc-compatibility.md) различают подтверждённые факты, локальные результаты и последующие требования.
