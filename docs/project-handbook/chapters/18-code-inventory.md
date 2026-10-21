# Инвентаризация исходников и версий

Редакция O2 индексирует **public working tree**, включая допустимые untracked public planning/concept документы других владельцев. SHA/наличие input не означает принятую реализацию, авторство публикации или inclusion в commit. Mapped snapshots с владельцами перечислены отдельно в [реестре](../inventory/snapshot-sources.json); private SDD/runtime files исключены. PREP/O1 refs ниже исторические; текущий O2 implementation/runtime proof не объявлен committed/remote по обновлению inventory. Финальный sync выполнен после стабилизации source/evidence; external source check и автономная проверка пакета PASS.

Файловая инвентаризация: [CSV с SHA256](../inventory/source-files.csv), [репозитории и HEAD](../inventory/source-manifest.json). В перечень включены исходники и публичные конфигурации; исключены данные, окружения, Git metadata, бинарники, секреты и этот комплект документации.

Исторические PREP runtime payload семи репозиториев сохранены локальными коммитами и связаны [compatibility manifest](../inventory/readiness-compatibility.json). Четыре новые reviewed O1/F1 payload refs backend/frontend/ML/Data сохранены отдельно в [execution ledger](../references/o1-f1-execution.md); PREP refs ими не заменены. HEAD в генерируемой таблице — база снимка исходников; только backend получает последующий docs-only commit публикации. Dirty entries фиксируются перед публикацией и не означают, что код PREP ещё ожидает сохранения. Обновление чисел/HEAD выполняет final sync; push и remote availability этим не подтверждаются.

| Репозиторий | HEAD | Изменённые/новые записи Git | Репозиторий |
|---|---|---|---|
| pulse-model | `bf5326d65578` | 0 | [GitHub](https://github.com/effitech-pulse/pulse-model) |
| pulse-data | `e38a978a1c25` | 0 | [GitHub](https://github.com/effitech-pulse/pulse-data) |
| pulse-ml-service | `84cb3dfde631` | 0 | [GitHub](https://github.com/effitech-pulse/pulse-ml-service) |
| pulse-contracts | `f871f8109a53` | 0 | [GitHub](https://github.com/effitech-pulse/pulse-contracts) |
| pulse-backend | `f5ec7aa9fbfc` | 50 | [GitHub](https://github.com/effitech-pulse/pulse-backend) |
| pulse-frontend | `dae98f86318d` | 0 | [GitHub](https://github.com/effitech-pulse/pulse-frontend) |
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
- `cmd/`: `adminuser/`, `api/`, `cohort/`, `migrate/`
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
