# Суточный тестовый дебит

`DAILY_FLOW_MODE=off` по умолчанию. `synthetic` допустим только при
`AUTH_MODE=local`, `APP_SCOPE=cohort`, `COHORT_VERSION=presentation-v1`.
`real`, иные значения и несовместимые opt-in настройки отвергаются на старте.
Новые роли, grants, migrations, источники Data и вызовы ML отсутствуют.

Режим обслуживает существующие 100 скважин через explicit embedded профиль
`internal/adapter/dailyflow/profiles/prep100-v1.json`. Startup и каждый read/export
проверяют read-only cohort identity и все 100 captured bindings. Текущая well
из каталога также должна совпадать с binding; источник не подменяется при ошибке.
Обычная телеметрия и её Excel остаются отдельными сценариями.

Профиль `prep100-daily-v1` фиксирован на 2026-07-27…2026-10-24, включая обе
границы: 90 закрытых дней Москвы. До 2026-10-25 00:00 Europe/Moscow профиль не
активируется. Он не сдвигается с текущей датой. 100×90 строк вычисляются без БД.
Изменение дат, explicit bindings, seed, recipe или результата требует новой версии,
согласованных fixtures и обновления golden digests. Нельзя обновлять golden для
маскировки неожиданного изменения при прежней версии.

## Recipe v1

Seed `pulse-wo-g-20261025`, recipe `daily-liquid-synthetic-v1`, canonicalization
`daily-flow-canonical-v1`. Для explicit ordinal `o` и ISO date `d`:
`h=SHA256(seed+"|"+decimal(o)+"|"+d)`, `j=uint16-big-endian(h[0:2])%101−50`.
Все следующие числа — целые сотые м³/сут. `E=10000+floor(o/5)*200+j`, `A=E+j`.
Ordinal задаётся manifest, разрешения произвольных ID арифметикой нет.

| Scenario | Изменение A/E по индексу дня i=0…89 |
| --- | --- |
| steady | без изменения |
| decline | A=E−45×i |
| zero_actual | A=0 при i%11=0 либо i=89 |
| gaps | i%10=0: actual null; 1: expected null; 2: оба null, present; 3: оба null, missing_day; при i=89 actual null |
| zero_expected | при i%3=0 либо i=89 E=0; в эти даты при чётном i A=0 |

Первые пять IDs 1001…1005 показывают по одному семейству. Каждое содержит 20
explicit участников. Значения ограничены 0…300; валидный ноль сохраняется.
Тестовый ориентир не является прогнозом ML, а сценарий — диагнозом.

Manifest SHA-256 `e4663d57442bacdf49dc207e4db965e77ac76b7fbced18a7e25fa94ee2789418`.
Dataset SHA-256 `4124916953c1344c001ab123b18cfc4b0cbc5efde8f7eaf7b024e0b367ac0b83`.
Dataset canonical JSON — struct в provider: canonicalization, typed profile,
ascending member IDs и ordered 90 source rows; integer hundredths, explicit null
и record_state. Manifest digest — exact UTF-8 bytes embedded файла.
Оба digests проверяются на старте. Profile getter и Read возвращают независимые
копии. Ни глобального PRNG, ни зависимости от порядка запросов нет.

## API и Excel

- `GET /api/v1/wells/{well_id}/daily-flow`: необязательные парные
  `start_date`, `end_date`, необязательный `dataset_version`. Default —
  2026-09-25…2026-10-24. Интервал включительный, 1…90 дат без silent clipping.
- `GET /api/v1/wells/{well_id}/daily-flow/export`: обязательные
  `start_date`, `end_date`, `dataset_version`, `view_id` именно показанного view.

ID — положительная decimal int64 строка, в JSON тоже строка. Неизвестные,
повторённые (включая encoded duplicate), пустые параметры и одна дата →422.
Прочие source/seed/cohort параметры пользователь не задаёт.

Обе операции используют существующее `telemetry.read` и текущую авторизацию
well. Export повторяет проверку session/epoch и scope, digest не даёт прав.
Off read после auth даёт `{enabled:false,reason:"disabled"}`, export —503.
Ошибки сохраняют code/message/detail и X-Request-ID: 401 session, 403 permission,
404 object scope, 409 `daily_flow_view_changed`, 422 query,
429 `daily_flow_busy`, 503 `daily_flow_unavailable` либо `daily_flow_disabled`.
При 409 требуется явное обновление экрана, latest fallback отсутствует.

Единственный normalized DailyFlowView содержит каждую дату, даже отсутствующую.
Null не становится нулём; last day всегда end_date. Нулевой expected даёт delta,
но null percentage. `delta=A−E`; сотые процентных пунктов вычисляются целочисленно
`round_half_away_from_zero((A−E)×10000/E)`, затем переводятся в JSON число делением
на100. В промежуточной формуле float division отсутствует.
Coverage считает calendar/recorded/actual/expected/paired/comparable_percent;
нули включены в paired, expected=0 исключён только из comparable_percent.
Сумма/среднее rate не вычисляются.

View identity — SHA-256 canonical JSON с canonicalization/formula
`actual-minus-expected-v1`, exact view metadata, coverage и ordered исходными
integer rows. Поле view_id в хэшируемом view пусто; actor/request time отсутствуют.
Workbook получает тот же view. Лист «Суточный дебит»: literal text date,
числовые actual/expected/delta/percentage, record_state/comparison_status.
Null — пустая ячейка; ноль — число0. Percentage −10 хранится числом−10, формат
0.00, без Excel процентного умножения. Лист «Источник» содержит только одну
well ID как text, source, query, coverage, версии/digests и available dates;
external IDs/чужие well bindings не экспортируются. Формулы отсутствуют.

Общий service deadline5s включает ACL/cohort, генерацию, нормализацию и encoding;
HTTP cancellation передаётся провайдеру/БД/writer. Export admission2, без очереди;
третий запрос получает429. JSON≤128KiB, XLSX≤256KiB; bytes формируются до headers,
при превышении/ошибке503 без attachment. Оба ответа `private, no-store`.

## Обновление и rollback

Для новой версии согласуйте explicit immutable profile и обновите recipe/golden,
тесты/fixtures, OpenAPI и UI через координатора. Production QA проверяет текущие
source/runtime identities отдельно от source unit tests. Изменения не требуют
миграции/seed/research данных. Включение существующего тестового портала выполняет
координатор после назначенного независимого review и runtime gates.
Rollback: `DAILY_FLOW_MODE=off` либо предыдущие проверенные BE/FE images.
DB cleanup не нужен; текущие cohort/RBAC/source settings сохраняются.
