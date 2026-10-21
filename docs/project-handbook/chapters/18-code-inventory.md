# Инвентаризация исходников и версий

Редакция22.10.2026 индексирует **public working tree** семи репозиториев после принятого Q0 и реализации Q1/F2. SHA и наличие файла фиксируют bytes; статус приёмки отдельно указан в [разделе2](02-status.md) и [Q1 runtime proof](../references/q1-runtime.md). Mapped snapshots с владельцами перечислены в [реестре](../inventory/snapshot-sources.json); private SDD/runtime files исключены. PREP/O1/O2 refs ниже исторические. Текущие sync/source check и автономная проверка пакета PASS:117 mappings,680 source records,35 HTML-разделов,28 draw.io pages; это целостность публикации, не whole-Q1 runtime acceptance.

Файловая инвентаризация: [CSV с SHA256](../inventory/source-files.csv), [репозитории и HEAD](../inventory/source-manifest.json). В перечень включены исходники и публичные конфигурации; исключены данные, окружения, Git metadata, бинарники, секреты и этот комплект документации.

Исторические PREP runtime payload семи репозиториев сохранены локальными коммитами и связаны [compatibility manifest](../inventory/readiness-compatibility.json). Четыре новые reviewed O1/F1 payload refs backend/frontend/ML/Data сохранены отдельно в [execution ledger](../references/o1-f1-execution.md); PREP refs ими не заменены. HEAD в генерируемой таблице — база снимка исходников; последующий commit публикации содержит сам комплект. Dirty entries фиксируются в момент sync и могут включать проверенную реализацию и документы, ещё не сохранённые локальным commit; точные bytes перечислены в CSV. Обновление чисел/HEAD выполняет final sync; push и remote availability этим не подтверждаются.

| Репозиторий | HEAD | Изменённые/новые записи Git | Репозиторий |
|---|---|---|---|
| pulse-model | `bf5326d65578` | 0 | [GitHub](https://github.com/effitech-pulse/pulse-model) |
| pulse-data | `e38a978a1c25` | 0 | [GitHub](https://github.com/effitech-pulse/pulse-data) |
| pulse-ml-service | `47a9eeccea3f` | 0 | [GitHub](https://github.com/effitech-pulse/pulse-ml-service) |
| pulse-contracts | `f871f8109a53` | 0 | [GitHub](https://github.com/effitech-pulse/pulse-contracts) |
| pulse-backend | `968142c1eebc` | 68 | [GitHub](https://github.com/effitech-pulse/pulse-backend) |
| pulse-frontend | `771187e27051` | 0 | [GitHub](https://github.com/effitech-pulse/pulse-frontend) |
| pulse-landing | `e555b333003f` | 0 | [GitHub](https://github.com/effitech-pulse/pulse-landing) |

## Каталоги исполнения

### pulse-model

- `esp/`: `config/`, `evaluation/`, `features/`, `io/`, `labeling/`, `models/`, `monitoring/`, `params/`, `pipeline/`, `rules/`, `synth/`, `validation/`
- `scripts/`
- `tests/`: `evaluation/`, `features/`, `fixtures/`, `labeling/`, `models/`, `monitoring/`, `pipeline/`, `research/`, `rules/`, `synth/`, `validation/`

### pulse-data

- `scripts/`
- `utils/`: `loaders/`, `parsers/`, `telemetry_emulator/`
- `tests/`: `fixtures/`, `loaders/`, `parsers/`
- `deploy/`: `clickhouse/`, `postgres/`

### pulse-ml-service

- `pulse_ml/`
- `tests/`

### pulse-contracts

- `scripts/`
- `proto/`: `pulse/`
- `gen/`: `go/`, `python/`
- `tests/`: `go/`

### pulse-backend

- `scripts/`
- `cmd/`: `adminuser/`, `api/`, `cohort/`, `migrate/`, `worker/`
- `internal/`: `adapter/`, `bootstrap/`, `config/`, `domain/`, `migrations/`, `observability/`, `repository/`, `service/`, `transport/`
- `test/`: `architecture/`, `integration/`
- `deployments/`: `local/`

### pulse-frontend

- `src/`: `api/`, `auth/`, `components/`, `hooks/`, `lib/`, `pages/`, `theme/`
- `tests/`
- `design/`: `brand-concepts/`, `brand-kit/`, `dark-theme/`, `integrations/`, `v3/`, `v4/`

### pulse-landing

- `design/`
- `assets/`

## Просмотр конкретного файла

Откройте соответствующий соседний репозиторий и путь из CSV. GitHub-ссылка ведёт в репозиторий; наличие локального reviewed commit на private remote не проверено. Значение SHA256 в CSV относится к локальному содержимому файла при сборке комплекта. Сам CSV не содержит тел файлов или значений секретов.

## Совместимость дополнения PREP

[Manifest семи репозиториев](../inventory/readiness-compatibility.json) фиксирует согласованные source refs и границы локального сохранения. Версии package0.1.0/0.1.1 сами по себе недостаточны; 119 public source hashes и 50 wheel hashes сохранены в [native manifest](../inventory/readiness-native-artifacts.json); runtime evidence связан через PREP ledger. Эта авторская часть не подменяет генерируемую таблицу HEAD: её сверяет final sync. Remote availability/push/cloud run не заявлены.
