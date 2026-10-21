> Снимок документа 22.10.2026. Источник: `pulse-model/docs/independent-acceptance.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# Независимая приёмка: офлайн-проверка готовности

Дата: 19.10.2026. PREP-07. Пользователь подтвердил: новых независимых данных пока нет, подключение будет отдельным шагом. **Научная приёмка отложена. Технический валидатор доступен сейчас.** Он не обучает модель, не читает БД, не продвигает веса и не изменяет существующий `eval_honest.py`.

`validated_metadata` означает только согласованность предоставленных JSON/CSV. Это не доказательство истинности сведений, происхождения телеметрии, отсутствия всех утечек, достаточной статистической мощности или промышленного качества. Валидатор не измеряет новые метрики и не сравнивает результаты с критериями promotion: последнее требует независимого научного разбора. Mapping/holdout/fit_roles — декларации владельца, а не техническая изоляция test.

## Внешние предпосылки

- Непрерывное наблюдение одновременно контрольного и отказного фонда во всех четырёх частях; даты отказов/ремонтов, конец наблюдения и время доступности сведений. Отсутствие зарегистрированного отказа само по себе не доказывает норму.
- Версионированное соответствие скважин физическим месторождениям. НГДУ/ЦДНГ не заменяют physical oilfield holdout; research row ID, Unitek ID и Pulse ID различаются.
- Новый независимый закрытый test с владельцем и версией. Повторный запуск просмотренного архива не создаёт независимость.
- Согласованные владельцем стоимость FP/FN, минимальный своевременный recall, допустимые эпизоды ложных тревог на 100 наблюдаемых скважино-дней. Пример намеренно не задаёт численные продуктовые критерии.
- Разметка классов с неопределённостью. #2/#8/#9 объединены в `deposits_2_8_9`; неизвестная аномалия выделена. Класс с support=0 получает `not_measurable` и recall=null.

[Причинный бейзлайн](causal-baseline.md) и эксперименты (исходник `pulse-model/EXPERIMENTS.md`; в пакет не включён) сохраняют ограничения архива. Снимок 14.10.2026:168242 окна,975 скважин,9 NGDU proxy groups; в train/calibration нет контрольных скважин без отказа в manifest. Test содержит604 скважины,34 с положительными окнами. Три сида повторяют когорту. 570 остальных в бинарной оценке отличаются от565 manifest-no-failure:5 имеют отказ вне положительного test-интервала. Метрики7–30 не подтверждают отдельную цель7–14.

## Входной контракт readiness_v1

[Пример JSON](../examples/readiness-protocol.example.json) содержит незаполненные обязательные сведения и должен вернуть blocked. Публичная синтетическая согласованная fixture: `tests/evaluation/test_readiness.py::sample`. Она доказывает механизм проверки, а не качество модели.

Входы доверенные и локальные. Pickle не принимается. Лимиты:protocol64 KiB,CSV64 MiB и1000000 строк,reports16 MiB и10000 записей; seeds/rotations до100 значений каждое. Закрытые CSV не публикуются.

Protocol фиксирует:

- `schema_version=readiness_v1`,`context_version=causal_v1`,`dataset_sha256` — SHA256 точных байтов переданного CSV, а не каталога/pickle; `horizon_days`14 либо30,`lead_days=7`.
- Три возрастающих `cutoffs`,`window_days>=2`,`embargo_days>=3`,положительный `lookback_days`,уникальные неотрицательные `seeds`/`rotations`. Числа конечные. Даты без зоны трактуются как UTC; предпочтительны ISO8601 UTC.
- `mapping.type=physical_oilfield`,версию; декларации `fit_roles` (train/calibration/selection),`holdout` (independent/sealed=true,previously_reviewed=false,custodian/version); точные `availability` из примера.
- `label_as_of` для четырёх ролей: знания train/calibration/selection не позже следующего cutoff; test as-of — время доступности итоговой разметки.
- `criteria`:owner/version,положительные FP/FN costs,recall[0,1],неотрицательный alarm budget. Числа задаёт владелец.
- `metric_definitions`:заранее объявленные правила объединения тревог в эпизоды,знаменателя наблюдаемого времени,uncertainty; `missing_class=not_measurable`.

CSV — подготовленные окна оценки, не телеметрия:

| Поле | Смысл |
|---|---|
| rotation,role | rotation из protocol;role=train/calibration/selection/test |
| well_id,oilfield | Закрытый стабильный ID и физическое месторождение; известные NGDU/CDNG proxy префиксы отклоняются |
| window_end | Конец окна UTC |
| y | 0/1;positive требует отказ через lead…horizon включительно |
| population | control/failure;раннее отрицательное окно failure не заменяет control |
| failure_at | Дата отказа для failure,пусто для control |
| observed_until | Подтверждённый конец наблюдения;для y=0 не раньше window_end+horizon |
| input_available_at | Самое позднее время доступности использованного входа;не позже window_end |
| label_available_at | Доступность разметки;не позже label_as_of роли;для negative не раньше полного горизонта,для positive не раньше отказа |

Каждый rotation содержит четыре роли и все использованные группы. Назначение сверяется с существующим `ProtocolConfig/split_roles`, включая purge=horizon+window и embargo; произвольное назначение не поддержано. Небезопасные окна не отбрасываются молча. В каждой роли нужны оба класса окон и обе популяции control/failure. Скважина не меняет группу; повтор `(rotation,well_id,window_end)` запрещён. Признаки и фактическое месторождение валидатор не восстанавливает.

Одна стабильная `well_id` имеет одну `population` и одну `failure_at` (либо её отсутствие) во всех окнах и rotations. Раннее отрицательное окно отказной скважины сохраняет population=failure и ту же дату события: оно не становится отдельной контрольной скважиной. Несколько ремонтных эпизодов под одной well_id этот контракт не поддерживает; для них нужен отдельно согласованный episode contract. Пустая failure_at у control допустима, непустая нераспознаваемая дата блокирует gate (`failure_at_invalid`) и не превращается в отсутствие события. Конфликты возвращают `well_population_conflict`/`well_failure_at_conflict` без значений или ID.

Reports JSON:ровно одна запись на каждую объявленную пару `(seed,rotation)`. Ошибочные,дублированные,отсутствующие и лишние результаты блокируют gate. Запись содержит `dataset_sha256`,`context_version`,`label_min_lead_days`,`protocol` с теми же cutoffs/horizon/window/embargo/lookback и `metrics`:

- finite `timely_recall`,`f1_macro`[0,1];неотрицательный `false_alarm_episodes`,положительный `observed_well_days`;
- `confusion_matrix`:квадратная матрица неотрицательных целых counts,порядок norm,1,deposits_2_8_9,3,4,5,6,7,10,11,12,13,unknown;
- `classes`:все эти ключи,целый support равен сумме строки матрицы;при support>0 status=measured,recall[0,1];при0 status=not_measurable,recall=null;
- `groups`:ровно test-группы rotation;каждая содержит support>0,timely_recall[0,1];
- `uncertainty`:непустой method,lower/upper[0,1],lower<=upper. Bootstrap валидатор не выполняет.

Это новая проверочная форма, не существующий runtime wire-контракт или формат honest summary. Старый бинарный report не получает недостающие классовые метрики заполнением нулями. Future report adapter/расчёт назначается отдельно; отсутствие классов остаётся not_measurable. Наличие ключей не доказывает правильность метрик.

## Проверка без обучения

Из pulse-model:

```sh
.venv/bin/python -m scripts.validate_readiness \
  --protocol /private/path/protocol.json \
  --windows /private/path/prepared-windows.csv \
  --reports /private/path/reports.json \
  --out /private/path/readiness-new.json
```

CLI создаёт только новый файл exclusive create. Код0 — validated_metadata;код2 — blocked/недоступный output. Отчёт содержит status,scientific_acceptance=not_assessed,reason codes и counts; ID/строки входа/тексты ошибок reports не копируются. Повреждённый ввод→input_invalid,изменённый CSV→dataset_digest_mismatch. Архивировать закрытые inputs должен вызывающий модуль.

## Будущий диагностический повтор: НЕ ЗАПУСКАЛСЯ

Команды ниже обучают модели на прежнем архиве, требуют отдельной исследовательской задачи и не закрывают независимую приёмку. `--save-candidate` отсутствует; разные dataset/out защищены проверкой отсутствия. CLI `eval_honest` пока не принимает новый physical mapping,availability manifest и новые cutoffs; это ограничение здесь не исправлено.

7–14:purge16,embargo3,lookback решения14:

```sh
set -eu
prep_dataset='data/cache/dataset_causal_7_14_prep07_20261019.pkl'
prep_out='data/reports/honest_7_14_prep07_20261019'
test ! -e "$prep_dataset"
test ! -e "$prep_out"
.venv/bin/python -m scripts.eval_honest \
  --cache-dir data/cache/features_v3 \
  --manifest data/manifest/grant_manifest_1000.xlsx \
  --dataset "$prep_dataset" --out "$prep_out" --rebuild \
  --horizon 14 --lead-days 7 --iterations 400 --seeds 0 1 2 --rotations 5
```

7–30:purge32,embargo3,lookback решения14:

```sh
set -eu
prep_dataset='data/cache/dataset_causal_7_30_prep07_20261019.pkl'
prep_out='data/reports/honest_7_30_prep07_20261019'
test ! -e "$prep_dataset"
test ! -e "$prep_out"
.venv/bin/python -m scripts.eval_honest \
  --cache-dir data/cache/features_v3 \
  --manifest data/manifest/grant_manifest_1000.xlsx \
  --dataset "$prep_dataset" --out "$prep_out" --rebuild \
  --horizon 30 --lead-days 7 --iterations 400 --seeds 0 1 2 --rotations 5
```

Старые cutoffs:01.01/01.03/01.05.2026. Поддержку нового горизонта считать отдельно,не переносить604/34 из7–30 в7–14. `eval_honest.py:111` возвращает exit0 при хотя бы одном успешном результате:обязательны все15 уникальных результатов без error,проверки состава частей и skipped cache files. Это обнаруженное ограничение,а не гарантия исходного оценщика.
