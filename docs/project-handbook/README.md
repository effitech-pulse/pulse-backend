# Техническая документация Пульс скважины

Комплект для разработчиков, технологов и сопровождения: от визуальной системы и макетов Figma до кода, контрактов, данных, ML, доступа и запуска.

**Начните с [HTML-версии с поиском](index.html)** или читайте разделы Markdown ниже. [Figma](https://www.figma.com/design/km4tIbp3LnMTRrV6yi6Qho) и [C4 в draw.io](diagrams/pulse-c4.drawio) связаны с описанием текущего кода. [Сценарии в draw.io](diagrams/pulse-flows.drawio) дополняют архитектуру.

## Статус · PREP и исполнение O1/F1, 20.10.2026

Изолированный PREP на loopback 8088/8018 проверен с персональным входом и `APP_SCOPE=cohort`, версия `presentation-v1`: зафиксированы 100 существующих участников, scope действует на API/экспорт/расчёты без обхода membership администратором. Обычный compose.local также получил local/cohort defaults, но изменение файла не переключает ранее запущенный экземпляр 8080; его рабочие БД и контейнеры в PREP не менялись.

Backend выполняет пользовательский bulk через телеметрию приложения и bundle-only Upload. Полный output snapshot сохраняется неизменяемо; provenance остаётся partial, поскольку точные входные bytes для replay не сохраняются. Senior-замечания PREP закрыты; ограниченный browser gate — 22/22 PASS, без ошибок страницы. Это технические проверки, а не научная приёмка модели. Пользователь отложил внешние данные и подключит ML позже: scientific acceptance `not_assessed`, продвижения модели нет. SSO, общий предметный журнал, доставка уведомлений, брокер и Kubernetes остаются отдельными задачами; полный перенос Figma не объявлен.

[PREP ledger](references/readiness-ledger.md), [архитектура](references/readiness-architecture.md), [runtime playbook](references/readiness-runtime.md) и [совместимость семи репозиториев](inventory/readiness-compatibility.json) и [native artifact hashes](inventory/readiness-native-artifacts.json) фиксируют источники и границы свидетельств. [Статус](chapters/02-status.md) отделяет подтверждённое от оставшихся задач. Push, cloud job и публикация release не выполнялись; горизонт 7–14 суток остаётся целью.

**O1/F1 приняты как программное изменение в согласованной области.** Core OTel SDK 1.38.0/безопасные JSON logs и шесть вкладок с восемью versioned Figma nodes проверены: task и whole-wave Senior Spec/Standards PASS, findings 0; runtime 12/12 и browser 7+2/9, pageErrors 0, scoped visual PASS. HTTP код обращения отделён от бизнес `request_id`; exporter/центрального хранилища нет. [Execution ledger](references/o1-f1-execution.md), [O1 contract](references/observability-o1.md) и [runtime playbook](references/o1-f1-runtime.md) фиксируют payload refs, evidence и пределы: нет successful live ML→PG прогона, forced rollover или научной приёмки. Фактические sync/build/validate/ZIP и source check выполняет координатор после этого authored freeze; до них обновление HTML/ZIP не заявлено. PREP manifest сохранён историческим.

## Содержание

**O2 · реализация и runtime проверены, 20.10.2026.** Optional Collector/Loki/Tempo/Prometheus/Grafana прошли source Senior review; W1/P2 закрыто Senior, три UI cycles и финальный rollback прошли. Финальные docs review и автономная публикационная проверка PASS; O2 принята как локальный пилот. [Runbook](references/observability-o2.md), [runtime evidence](references/o2-runtime.md), [execution ledger](references/o2-q0-execution.md) и [архитектура/C4](references/o2-architecture.md) отделяют выполненные проверки от открытых gates. Пилот на8088/8018 использует PREP PG55439; PG55432 — отдельные integration tests. Grafana-only bridge с возможным egress прямо разрешён пользователем для локального пилота; это не сетевой deny и не production approval. Scientific not_assessed сохраняется. Новые Q0 и паспорт/Excel/помощник документы представлены как архитектура/план, не реализация. Sync/build/validate/source check и standalone ZIP validation PASS; фактический итог публикационного gate зафиксирован в execution ledger.

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
- `references/` — автономные копии матрицы проверок, PREP ledger/архитектуры/runtime, O1/F1 execution/contract/runtime, моделей/RPC, независимого acceptance protocol, frontend handoff и причинного бейзлайна.
- `SOURCE_POLICY.md` — источники, владельцы, триггеры и порядок синхронизации.
- `tools/` — sync_sources.py для отдельной сверки workspace, пересборка HTML/ZIP и автономная валидация.
- `checksums.sha256` — контрольные суммы файлов комплекта.

Реальные данные, сопоставления скважин, веса и секреты не включены. Основная HTML-документация открывается локально без серверов приложения. Figma, GitHub и внешняя документация требуют сети; исторический прототип v3 может использовать сторонние CDN. Для актуализации следуйте [политике источников](SOURCE_POLICY.md): содержательно сверяйте главы, обновляйте mapped snapshots/source inventory через `tools/sync_sources.py --workspace <repository-split> --sync`, затем выполняйте build/validate и отдельную внешнюю сверку `--check`. Самостоятельный `python3 tools/validate.py` работает в распакованном комплекте без соседних репозиториев. [Матрица проверок/CI](references/verification-matrix.md) и [совместимость моделей/RPC](references/model-rpc-compatibility.md) различают подтверждённые факты, локальные результаты и последующие требования.

Шесть исторических originals имеют в registry вид `archived_task` и могут отсутствовать вместе с внешним `tasks/`. Source-check в этом случае проверяет целостность поставленных snapshots и сохранённых SHA metadata; sync не изменяет их bytes или hashes. Остальные продуктовые originals обязательны. Автономный `validate.py` не требует ни tasks, ни соседних checkout.
