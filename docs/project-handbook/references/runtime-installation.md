> Снимок документа 23.10.2026. Источник: `pulse-ml-service/docs/runtime-installation.md`; связь и владелец — `inventory/snapshot-sources.json`. Ссылки локализованы для автономного пакета.

# Native runtime и выпуск

Проверено 19.10.2026. Этот документ устанавливает исполняемый профиль сервиса,
а не качество модели. Контракты RPC и ограничения causal/legacy:
[матрица совместимости](model-rpc-compatibility.md). Обучение выполняется в pulse-model.

## Поддержанный для проверки профиль

CPython **3.13**, native wheels для конкретной ОС/архитектуры и девять версий из
[runtime-constraints.txt](../contracts/runtime-constraints.txt): numpy 2.5.1, pandas 2.3.3,
scipy 1.18.0, scikit-learn 1.9.0, catboost 1.2.10, lifelines 0.30.3,
joblib 1.5.3, grpcio 1.78.0, protobuf 6.33.6. `requires-python>=3.11` — нижняя
граница metadata, не свидетельство наличия всех этих wheels для Python 3.11.

Dockerfile использует Python3.13-slim/Linux и libgomp1; constraints применяются с первой установки project dependencies (`pip install -c ... model contracts service`), затем pip check. GitHub/GitLab CI также применяют constraints при первоначальной установке. PREP проверил отдельно чистую native macOS/arm64 среду и fresh Linux images; это разные свидетельства. Исторический native snapshot очереди A ниже сохранён с датированной областью.
Зависимости/версии ради документации не обновляются.

В общем split workspace `.venv` ссылается на `../.venv`; не пересоздавайте её и
не применяйте к ней install-команды для smoke. Наличие установленных пакетов
не является чистой установкой. Не используйте `--system-site-packages` или
`PYTHONPATH` общей среды.

## Подготовка wheels

Все команды ниже запускаются из pulse-ml-service и работают с отдельной средой:

```sh
PULSE_NATIVE_ROOT=$(mktemp -d /private/tmp/pulse-native-XXXXXX)
python3.13 -m venv "$PULSE_NATIVE_ROOT/venv"
PULSE_NATIVE_PY="$PULSE_NATIVE_ROOT/venv/bin/python"
PULSE_WHEELHOUSE="$PULSE_NATIVE_ROOT/wheelhouse"
mkdir -p "$PULSE_WHEELHOUSE" "$PULSE_NATIVE_ROOT/sources"
```

Подготовьте **копии согласованных исходников** model/contracts/service в
`$PULSE_NATIVE_ROOT/sources/{pulse-model,pulse-contracts,pulse-ml-service}`.
Не собирайте в чужом рабочем дереве: setuptools создаёт build/egg-info.
Для release копируйте/экспортируйте проверенные commit/tag, а не неявный HEAD;
для working-tree smoke сохраняйте hash каждого исходного файла. Исключайте
`.env`, `.git`, `.venv`, данные, веса, cache, build/dist и `__pycache__`.
Model нужны pyproject.toml + esp + scripts, contracts — pyproject.toml + gen/python,
service — pyproject.toml + pulse_ml. Model не зависит от pulse-data.

Сохраните перед сборкой commit каждого модуля, статус working tree и manifest
`путь → SHA256`; одного номера версии 0.1.0 недостаточно. После сборки сохраните
SHA256 **каждого** wheel, полные download/build/install logs и freeze. Частные
project wheels получайте только из согласованных исходников/артефактов выпуска.

При неполном offline wheelhouse разрешена отдельная сетевая подготовка:

```sh
"$PULSE_NATIVE_PY" -m pip download --only-binary=:all: --dest "$PULSE_WHEELHOUSE" \
  -r runtime-constraints.txt setuptools==84.0.0 wheel==0.45.1 pytest==9.1.1
"$PULSE_NATIVE_PY" -m pip install --no-index --find-links "$PULSE_WHEELHOUSE" \
  setuptools==84.0.0 wheel==0.45.1
for PULSE_SOURCE in pulse-model pulse-contracts pulse-ml-service; do
  "$PULSE_NATIVE_PY" -m pip wheel --no-index --no-build-isolation --no-deps \
    --wheel-dir "$PULSE_WHEELHOUSE" "$PULSE_NATIVE_ROOT/sources/$PULSE_SOURCE"
done
"$PULSE_NATIVE_PY" -m pip download --only-binary=:all: --dest "$PULSE_WHEELHOUSE" \
  -c runtime-constraints.txt \
  "$PULSE_WHEELHOUSE/effitech_esp_model-0.1.0-py3-none-any.whl" \
  "$PULSE_WHEELHOUSE/effitech_pulse_contracts-0.1.1-py3-none-any.whl" \
  "$PULSE_WHEELHOUSE/effitech_ml_service-0.1.0-py3-none-any.whl"
```

Последняя команда получает транзитивные зависимости явных project wheels;
не подменяет частные проекты пакетами с публичного индекса. Нет wheel нужной
платформы/версии — остановите этот профиль и сохраните ошибку, не меняйте pins
и не объявляйте установку успешной. Девять constraints не являются полным
transitive lock: повтор той же среды требует сохранённых wheels/hashes/freeze.

## Offline установка и smoke

```sh
"$PULSE_NATIVE_PY" -m pip install --no-index --find-links "$PULSE_WHEELHOUSE" \
  -c runtime-constraints.txt \
  effitech-esp-model==0.1.0 effitech-pulse-contracts==0.1.1 effitech-ml-service==0.1.0
"$PULSE_NATIVE_PY" -m pip install --no-index --find-links "$PULSE_WHEELHOUSE" \
  -r runtime-constraints.txt pytest==9.1.1
"$PULSE_NATIVE_PY" -m pip check
"$PULSE_NATIVE_PY" -m pip freeze > "$PULSE_NATIVE_ROOT/freeze.txt"
```

Второй install нужен: constraints ограничивает выбор, но сам по себе не
устанавливает все девять pins. Сверьте их через importlib.metadata. Импортируйте
`pulse_ml.server`, generated `pulse.ml.v1.ml_pb2/ml_pb2_grpc`, CatBoost, numpy,
pandas, scipy, sklearn и lifelines из **установленных** wheels. Проверяйте `__file__`
и `sys.path`: ни исходный checkout, ни общая `.venv` не должны подменять установку.

Скопируйте только tests сервиса в `$PULSE_NATIVE_ROOT/tests`, задайте там pytest.ini
с `testpaths=tests`, перейдите в `$PULSE_NATIVE_ROOT` и запустите:

```sh
env -u PYTHONPATH -u PYTHONHOME "$PULSE_NATIVE_PY" -m pytest \
  -q
```

Health/auth/CLI работают с ephemeral loopback gRPC, остальные вычислительные сценарии — с изолированными fixtures. Тест production feature-label набора требует exact artifacts: для PREP они были переданы отдельно read-only и полный suite дал78/0skip. Без них честно фиксируется конкретный SKIP, не общий model PASS.
Не копируйте data/.env в исходники или публичный wheelhouse и не запускайте продуктивный Score ради smoke. Разрешённые точные artifacts для проверки signatures передаются закрытым read-only mount.
Проверка с настоящими model/cache/manifest/research PG — отдельный opt-in;
отсутствие таких входов означает **not verified**, а не модельный PASS.

Probe `python -m pulse_ml.healthcheck` проверяет только listener/доступ/`status=ok`.
Он не читает `.env`, получает ключ из env процесса, закрывает канал, timeout 3 с;
ключ допускает только printable ASCII (0x20–0x7e) без обрезания пробелов;
Unicode/управляющие символы отклоняются без ключа и traceback;
его настройка описана в [README](ml-service-readme.md#liveness-и-внутренний-ключ).

## Историческое свидетельство очереди A — 19.10.2026

Чистая установка из собственных wheels прошла на **CPython 3.13.5, macOS 15.3.1,
arm64** в `/private/tmp/pulse-doc-queue-a-20261019/native/venv` с
`include-system-site-packages=false`, без PYTHONPATH общей среды. Все девять pins
совпали, imports загружены из этой venv, `pip check` — без нарушенных зависимостей.
Model-free suite: **57 passed, 1 deselected, 13 warnings** (существующие NumPy
DeprecationWarning); Health CLI valid/missing/wrong/no-key, status, deadline,
unavailable и закрытие channel включены. Боевые Score/Upload: **not verified**.
Первая попытка download в sandbox не разрешила DNS; разрешённая сетевая
подготовка завершилась успешно, offline install уже не обращался к индексу.

Это **локальный working-tree snapshot**, не опубликованный release. Commit
родителя дерева и hash сохранённых исходных файлов различаются по назначению:

| Проект / версия metadata | HEAD до очереди / snapshot исходников SHA256 | Wheel SHA256 |
|---|---|---|
| model 0.1.0 | `e544722535d7266b7560a61affc6fbdb08915e6d` / `c726d7ed41012ba95ed4aa50b1777a3529ac4edfe157f0bb3066cc9a84fdce2c` | `a93522a566daa3ddb3225a83dbe762a08b3b7dc132cc211ff18b5a6999a50b60` |
| contracts 0.1.1 | `f5d51b6883702de913bed2f470637af420f61a94` / `52dd4570cff76f2b7d66e3fa3d548dfd9ff6060b5903d683dbb0797898856d19` | `922506657b530d9f6a61e627b2f1f8c1d04fd475e06331c931a4ea904f4fee26` |
| service 0.1.0 | `b6c5d2b8c4fba6774cf8de8be91a29628c7dbceb` / `e14bc4481ed486c62816af0df9959841e5296b9af700292d5b2a2216879bc96c` | `214b4bc37550e42d8e00d6f064282bc488ec73a1d80eae1c7cd2192246ccb122` |

Snapshot hash = SHA256 UTF-8 JSON (`sort_keys=True`, стандартные разделители)
от отображения относительных путей в SHA256 файлов **до** wheel build.
Полные source-manifest.json, SHA256SUMS, freeze, smoke.json, pytest.xml и логи
хранятся в `/private/tmp/pulse-doc-queue-a-20261019/native/`; это свидетельство
конкретной локальной проверки, не обещание общедоступного release wheelhouse.

После исправления валидации metadata DOC-03 пересобран только service wheel.
Его же версия 0.1.0 установлена явным `pip install --no-index --force-reinstall --no-deps`
по пути нового wheel, чтобы прежняя установка той же версии не сохранилась.
Model/contracts wheels и девять pins не менялись. SHA256 установленного
`pulse_ml/healthcheck.py`, payload этого wheel и текущего исходника совпал:
`3ddc4359cde9a05f072f2d1be8229dfe3cb6067f94747d5ee3c75ac4fa998caa`.
Подтверждение: native/probe-installation.json. Финальные CLI-регрессии включают
Unicode, newline, CR, DEL и допустимый ASCII-ключ с пробелами. Freeze теперь
содержит прямой file-URL нового service wheel с hash; остальные строки зависимостей
не изменились. SHA256 финального freeze.txt:
`b9faa90fd1902e61387e5593a5a60fc30a7105c33ccbde56aee8e1108fb40f95`.

## Выпуск и приватные зависимости

Для выпуска отдельно фиксируются принятые Git commit/tag и hashes wheels всех
трёх проектов. PREP CI pins: model `f5c2a3de031ff4794194a219f048c9bf8c7c4f45`, contracts `07213c430318affd140f55dea391cdc36220d93f`; одинаковая версия
package не доказывает побайтовое совпадение с рабочим деревом или совместимость
с causal-кандидатом. Локальный snapshot выше нельзя переименовывать в релизный.

GitHub требует read-only secrets **MODEL_DEPLOY_KEY** и **CONTRACTS_DEPLOY_KEY**
для отдельных checkout; credentials в образ не передаются. GitLab использует
`MODEL_REF`, `CONTRACTS_REF`, `CONTRACTS_REPOSITORY` (URL без токена), runner Git
credentials и разрешение модели принимать `CI_JOB_TOKEN` сервиса. Значения
секретов не приводятся и не записываются в source manifest/logs.

## PREP: актуальное свидетельство 19.10.2026

В `/private/tmp/pulse-prep-20261019/native/` сохранены source-manifest.json, wheel-manifest.json, freeze и build/install/test logs. Независимый Senior сверил model103/contracts4/ML12 исходников и50 wheel hashes; pip check exit0. Полная чистая native suite:78tests,0errors/0failures/0skipped,21.869s по JUnit; включая production feature-label check. Это заменяет прежнее ограничение57+1deselected для текущего PREP source state, не стирая историю очереди A.

Отдельно fresh Linux ML image:78tests PASS; Data image:32tests PASS, constrained install/pip check. Bundle-only Upload в isolated model-only mount не требует research cache/manifest/PG. Cold evidence получен после отдельного restart ML без конкурирующих вычислений; обычный клиентский harness пишет first/repeat, которые сами по себе не означают cold/warm. Детали [runtime playbook](readiness-runtime.md).

P0 source/config/CI review ACCEPTED на21файле; окончательный staged-source gate, остальные локальные commits и межмодульный `pulse-backend/docs/readiness-compatibility.json` ожидаются у координатора. Нельзя вписать собственный backend commit hash в хешируемый им же документ. Точные refs существуют локально; их наличие на private remote/cloud job не проверялось. Новый release/push не выполнялся. Полный transitive replay среды требует сохранённых wheels/hashes/freeze, а не только девяти pins.
