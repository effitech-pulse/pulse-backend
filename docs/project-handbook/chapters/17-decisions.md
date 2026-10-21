# Решения и план развития


Q0 принятое source-решение: typed immutable input + actual parser v2 identity + strict pre-inference pin в отдельном UploadPinned. Exact9 parser dependencies и embedded source bytes включаются в recipe, missing BuildInfo fail-closed; OS tzdb/compiler attestation не обещаются. Memory correction убрала overlapping bulk map graphs только saved path, сохранив legacy semantics. [Архитектура](../references/q0-architecture.md), [RUN-2 measurements](../references/q0-runtime.md); final docs/publication gate PASS; Q1 job/outbox — отдельная фаза.
## Актуальное продолжение · 20.10.2026

[Roadmap](../references/functional-roadmap.md): O1/F1 приняты; O2 — implementation/runtime VERIFIED, W1/final rollback CLOSED; final docs/publication gate PASS, O2 принята как локальный пилот, затем Q0→Q1/F2 и прежние зависимости. [O2 execution](../references/o2-q0-execution.md) и [архитектура](../references/o2-architecture.md) отделяют проверяемый local pilot от production. [Q0 architecture](../references/q0-architecture.md) описывает immutable input/pinning prerequisite, не installed broker/durable consumer.

[Паспорт/эксплуатация/Excel/аналитика/помощник](../references/well-operations-proposal.md) и [архитектурная сверка](../references/well-operations-data-review.md) — отдельное рассматриваемое дополнение. Загрузка и выгрузка Excel запрошены; источник паспортов/работ неизвестен, graph qg/qg_tex независим. Рекомендация existing PG+bounded JSONB не равна установке/миграции; образ робота — brief, не нарисованный UI. Этот снимок не назначает реализацию нового блока и не меняет scientific deferred.

## Согласованная последовательность

1. PREP реализовал fixed cohort100, server scope, отдельный runtime и local access; проверенные границы — в главе02 и PREP ledger. Пользовательское имя Pulse Connector не отменяет описание происхождения данных.
2. Исправить порядок карточки, единый файловый сценарий и предметный журнал.
3. Персональная аутентификация/RBAC принята в isolated PREP; OIDC/TLS для удалённого размещения остаются отдельной задачей.
4. Перенести и проверить остальные экраны Figma на реальных рабочих сценариях.
5. Добавить надёжные фоновые задания/события через выбранный брокер.
6. Подготовить и проверить Kubernetes-развёртывание.

Документация фиксирует эти изменения, но не применяет их автоматически. Научная ML-приёмка отложена: новых внешних данных сейчас нет, пользователь подключит ML позднее. Протокол/валидатор готовы; обучение, новые метрики и promotion не выполнялись.

## Принятые архитектурные основания

| Решение | Основание / последствия |
|---|---|
| Раздельные репозитории | Независимая сборка model/data/ML/API/UI/landing/contracts |
| Go API, Python ML | Операционная транзакционность отдельно от вычислительного ядра |
| gRPC + отдельные contracts | Типизированная межсервисная граница, сохранение optional/int64 |
| ID Pulse | Внешняя система мониторинга не определяет идентичность объекта приложения |
| Ручные проверки и неизменяемый output | Полный сохранённый ответ/digest; просмотр не запускает новый расчёт. Provenance partial: hash входа не заменяет immutable input bytes/replay |
| Версии и предметная история | Конкурентные действия не перезаписывают решения молча |
| Архив вместо удаления | Проверки, измерения и заключения сохраняются |
| Model не зависит от Data | Генератор/добыча данных не управляют прогнозом |
| Причинная приёмка | Старые group-CV и красивый график генератора не подтверждают целевое качество |

## RabbitMQ и Kafka — архитектурные варианты

На текущем масштабе RabbitMQ рассматривается для заданий проверки/импорта, уведомлений и контролируемых повторов. Задание должно иметь стабильный ID, статус, попытки, timeout и идемпотентный обработчик; ack только после успешного сохранения. Outbox решает атомарную связь предметной транзакции с публикацией. Это изменение кода, а не только добавление broker container. [RabbitMQ work queues](https://www.rabbitmq.com/tutorials/tutorial-two-python).

Kafka рассматривается при отдельном телеметрическом журнале, повторном чтении и независимых потребителях. Возможный ключ — ID Pulse скважины; версия схемы, event-time и порядок внутри partition задаются контрактом. Kafka не отменяет дубликаты, поздние события, окна модели и компенсации между БД/брокером. [Apache Kafka](https://kafka.apache.org/intro/).

RabbitMQ Streams также допускает повторное чтение; выбор не сводится к абсолютному «RabbitMQ не умеет потоки». [RabbitMQ Streams](https://www.rabbitmq.com/docs/streams). Нагрузка 100 скважин сама по себе не требует двух брокеров. Выбор/бенчмарк, retention и эксплуатационная ответственность ещё не утверждены; брокеры в текущем Docker отсутствуют.

## Kubernetes — план размещения

| Элемент | Предлагаемое размещение |
|---|---|
| Frontend/Nginx | Deployment + Service, вход через Ingress |
| Backend | Deployment + Service; graceful termination и допустимые deadlines |
| ML | Отдельный Deployment с ресурсами и версионированными read-only артефактами |
| Data/collector | Управляемое число экземпляров; lease/leader policy и тесты конкурентного сбора |
| Конфигурация | ConfigMap; секреты отдельно, с учётом прав RBAC Kubernetes |
| Миграции | Явный Job до rollout, с backup и проверкой версии |
| PostgreSQL/ClickHouse | На первом этапе внешние; перенос в кластер требует отдельной storage/backup-политики |
| Модели и кэши | PVC или объектное хранилище/доставка артефакта вместо host bind mounts |

Текущие локальные Docker-сети и host paths не переносятся в Kubernetes без адаптации. Нужны readiness/liveness/startup probes, resource requests/limits, TLS, NetworkPolicy, наблюдаемость и стратегия обновления/отката. In-memory ML-кэш и долгая синхронная проверка требуют отдельного контроля при масштабировании; несколько pod не означают автоматически надёжное возобновление задания.

Kubernetes RBAC управляет доступом к API кластера; роли технолог/наблюдатель приложения остаются отдельным уровнем. Готовых принятых Kubernetes/Helm-манифестов текущая поставка не заявляет. [Deployments](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/), [StatefulSets](https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/).

## Открытые решения

Требуется определить внешний домен и TLS, среду размещения, IdP/realm и сопоставление ролей, адресатов/SMTP, retention предметных событий и телеметрии, ресурсные пределы, RPO/RTO, фактические месторождения вместо административной замены, подтверждённые эксплуатационные признаки/метки и независимую контрольную когорту модели.

## Закреплённые решения PREP

Membership100 отделён от active/archive/eligible и от пользовательского scope. Backend cohort вызывает существующий Upload вместо фильтрации research Score; ML bundle-only не требует research PG/cache. Один typed evaluator сохраняет различные legacy политики; causal Upload fail-closed до согласованного feature recipe. Immutable output и mutable review-overlay разделены. Identity11 фиксирует профиль попытки до потенциального failure; unknown старых строк не backfill. Эти решения приняты и проверены в рамках PREP, не являются заказом нового UI или повторным открытием DOC-01–07.
