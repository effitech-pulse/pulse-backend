# Тестирование и критерии приёмки

Полная [матрица проверок и CI gates](../references/verification-matrix.md) содержит команды, окружения, допустимые SKIP и свежие свидетельства очереди A. Это локализованная копия источника pulse-backend/docs/verification-matrix.md; [политика](../SOURCE_POLICY.md) задаёт её владельца и синхронизацию. Ниже — краткая карта и исторические свидетельства прежнего прохода. Внешний CI не объявляется пройденным по наличию workflow.

## Автоматические проверки

| Модуль | Команды из репозитория | Покрываемые свойства |
|---|---|---|
| Backend | make check; TEST_DATABASE_URL=... make integration; make build | Unit/race/vet, архитектурные границы, реальная тестовая PG, сборка CLI |
| Frontend | npm run build; npm test | TypeScript/Vite, гигиена, относительное изменение, ID, импорт/экспорт CSV/XLSX/Parquet |
| Data | .venv/bin/python -m pytest | Ресемплинг, long_raw, loaders, сценарий и детерминизм API |
| Model | .venv/bin/python -m pytest | Признаки, правила, разметка, split, гигиена, causal context, модели/оценка |
| ML service | .venv/bin/python -m pytest | gRPC, upload, scoring и model identity |
| Contracts | make lint; make breaking; make test | Buf style/breaking и SDK совместимость |
| Landing | python3 build.py | Сборка статической страницы; визуальный QA отдельно |

Тесты backend используют отдельную БД и временные схемы, которые очищаются после прогона. Нельзя подставлять рабочую esp_app в TEST_DATABASE_URL. В Data офлайн-регрессии не требуют живых UNITEK/ARMITS credentials.

## Проверенные регрессии приложения

ID Pulse сохраняется при импорте; внешний 0 не превращается в null. Маркеры очищаются по каналу, нули сохраняются; is_set не попадает в измерения. CSV/XLSX/Parquet проходят обратное чтение. Сравнение не растягивает короткий период и не меняет даты.

Проверены ручные проверки и их повторные ключи; серверный автор заключения; недоступность чужой скважины; запреты viewer; конфликт optimistic revision; атомарность archive/edit; сохранение проверок/заключений при архиве; курсорная история. Отдельный регрессионный тест исключает замену актуального watermark старым JSON-снимком настроек.

Запись предыдущего рабочего прохода, до очереди A: backend прошёл make fmt/check/integration/build; frontend — production build и 11 тестов. Выполнены браузерные проверки оболочки, обзора, тем, фильтра и профиля. Через Docker проверены здоровье PG API, архив bounds реальной скважины, обновление источника и сохранение ручной проверки скважины обезличенного фонда с версией модели. Эти факты относятся к этому проходу, а не означают непрерывный мониторинг всех функций.

## Продуктовые критерии и их границы

| Сценарий | Критерий |
|---|---|
| Pulse Connector | Одно согласованное название источника, корректное описание происхождения данных |
| Фонд | Фиксированное membership100; scope/фильтры и lifecycle могут уменьшать отображаемое подмножество. Bulk сохраняет outcome каждого участника, не требует100scored; research исключён на сервере |
| Карточка | Риски открываются первыми; Telemetry второй; deep links сохранены |
| Файл | Единый сценарий выбора/предпросмотра/валидации/проверки/результата; ошибка не создаёт ложный успех |
| Журнал | Видны реальные commit-события с объектом, временем, автором и исходом; API-аудит отделён |
| RBAC | 401 без сеанса, 403 на запрещённые действия, scope на всех чтениях/экспортах, logout очищает клиентский кэш |
| Повтор/сбой | Повторный request_id не дублирует действие; сбой процесса не выдаёт незавершённое за успешное |
| Figma | Основные разрешения, обе темы, статические assets и все рабочие состояния сверены |
| Модель | Только причинная независимая оценка; генератор не присваивает риск и не служит holdout |

## Нефункциональные требования

Нужны измеренные latency/throughput, память/CPU ML, пределы импорта, деградация при недоступности БД/источника, восстановление после перезапуска, журналирование без секретов и проверенное восстановление backup. Числовой SLA, RPO/RTO и результаты нагрузочного теста не объявляются выполненными до измерения.

Актуализация документации выполняется по [source policy](../SOURCE_POLICY.md), а требования provenance/RPC разобраны в [отдельной спецификации](../references/model-rpc-compatibility.md). Typed decision/provenance и immutable cohort output реализованы PREP; complete input replay остаётся неподтверждённым.

Проверка документации: локальные ссылки, якоря, отсутствие пропавших assets, полные OpenAPI $ref, draw.io XML и endpoints связей, контрольные суммы архива. Сборка этого комплекта не переобучает модель и не мигрирует рабочую БД.

## Подтверждённые свидетельства PREP · 20.10.2026

Источник результатов — [PREP ledger](../references/readiness-ledger.md) и linked executor/Senior evidence. Здесь не повторялись runtime suites и не создавались новые научные метрики.

| Gate | Датированное свидетельство | Ограничение |
|---|---|---|
| Native / Linux | [Native artifact manifest](../inventory/readiness-native-artifacts.json): 119 public source hashes и 50 wheel hashes; clean native ML78/0skip + pip check; Linux ML78 PASS, Data32 PASS; source/wheel hashes сверены Senior | Не облачный job и не произвольная совместимость версий |
| Backend | make check/build, PG/race integration; F1–F3 CLOSED, независимые targeted/re-review и migration11 repeat сохранены | Только отдельная test/isolated PG, рабочая БД не мигрировалась |
| Frontend | Source Senior PASS; responsive regressions/build; browser22/22 PASS, fail0/pageErrors0,14/14 source hashes | Только bounded light/dark390/1440 и проверенные сценарии, не полный Figma/WCAG |
| Scope/auth | Full100/restricted0, direct ID/Excel, role mutations/origin/logout; mutable status reset и foreign reference writes покрыты независимо | Global membership не меняется под scope читателя |
| Snapshot | Полные outcomes, null non-scored, old output после ML restart; ранняя `running`/`error` identity11 | Output сохранён; exact immutable input bytes отсутствуют, provenance partial |
| Scientific gate | Readiness validator: targeted60PASS независимо Senior, full model355PASS по executor log, P7-F1/F2 CLOSED | Software validation, scientific_acceptance=not_assessed; внешние данные отложены пользователем |

Успешный exit вспомогательного browser script не принят без чтения JSON: итог22assertions проверен отдельно. Начальные harness failures сохранены и superseded финальными visible-element/geometry checks. В browser fixtures не было новых alert rows; новый реальный alert/review save не заявляется проверенным UI-проходом. Native30/30, SSR/backend и реальные auth6/6 — отдельные свидетельства своих запусков, не слагаемые одной статистики.

Финальный after11 run33 завершён: done, cohort/presentation-v1, 100 scored, 0 errors/alerts; restricted reader0, прежние snapshots30/32 не изменились. Identity фиксировалась уже в running. Точные семь payload refs и image IDs находятся в [compatibility manifest](../inventory/readiness-compatibility.json); он не пытается ссылаться на собственный docs commit. Cloud job, push и release не запускались. Отложенная научная приёмка и будущий самостоятельный ML hookup не препятствуют честной публикации технических результатов.
