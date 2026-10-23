package postgres

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// insertTelemetryRows сравнивает коллизию в той же транзакции после actual INSERT.
func insertTelemetryRows(ctx context.Context, tx pgx.Tx, sourceID string, points []domain.Measurement, provenance *domain.CollectionProvenance) (inserted int, identical int, err error) {
	if provenance == nil {
		return insertLegacyTelemetryRows(ctx, tx, sourceID, points)
	}
	ordered := append([]domain.Measurement(nil), points...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].WellID != ordered[j].WellID {
			return ordered[i].WellID < ordered[j].WellID
		}
		return ordered[i].Timestamp.Before(ordered[j].Timestamp)
	})
	var attribution any
	if provenance != nil {
		attribution, err = json.Marshal(provenance)
		if err != nil {
			return
		}
	}
	for _, p := range ordered {
		var raw []byte
		raw, err = json.Marshal(p.Values)
		if err != nil {
			return
		}
		tag, e := execute(ctx, tx, sqlBuilder.Insert("telemetry_measurement").Columns("well_id", "measured_at", "values", "quality", "source_id", "collection_provenance").Values(p.WellID, p.Timestamp, raw, p.Quality, sourceID, attribution).Suffix("ON CONFLICT (well_id,measured_at) DO NOTHING"))
		if e != nil {
			return 0, 0, e
		}
		if tag.RowsAffected() == 1 {
			inserted++
			continue
		}
		var values, prov []byte
		var quality, source string
		if e = tx.QueryRow(ctx, `SELECT values,quality,source_id,collection_provenance FROM telemetry_measurement WHERE well_id=$1 AND measured_at=$2 FOR SHARE`, p.WellID, p.Timestamp).Scan(&values, &quality, &source, &prov); e != nil {
			return 0, 0, e
		}
		if provenance == nil {
			if prov != nil {
				return 0, 0, domain.NewError(domain.Duplicate, "telemetry_measurement_conflict")
			}
			identical++
			continue
		}
		if prov == nil {
			return 0, 0, domain.NewError(domain.Duplicate, "unverifiable_existing")
		}
		var existing map[string]*float64
		var old domain.CollectionProvenance
		if json.Unmarshal(values, &existing) != nil || json.Unmarshal(prov, &old) != nil {
			return 0, 0, domain.NewError(domain.Duplicate, "measurement_conflict")
		}
		if source != sourceID || quality != p.Quality || !equalJobJSON(existing, p.Values) || old != *provenance {
			return 0, 0, domain.NewError(domain.Duplicate, "measurement_conflict")
		}
		identical++
	}
	return
}

// legacyTelemetryKey совпадает с реальным PostgreSQL PK: pgx хранит timestamptz в микросекундах.
type legacyTelemetryKey struct {
	well string
	at   time.Time
}

func legacyKey(p domain.Measurement) legacyTelemetryKey {
	return legacyTelemetryKey{p.WellID, p.Timestamp.UTC().Truncate(time.Microsecond)}
}

// insertLegacyTelemetryRows ограничивает размер SQL/ответов512 строками, а не число точек Import.
// Первая входная строка каждого фактического PK остаётся победителем; затем ключи сортируются для locks.
func insertLegacyTelemetryRows(ctx context.Context, tx pgx.Tx, sourceID string, points []domain.Measurement) (inserted, identical int, err error) {
	const batchSize = 512
	unique := make([]domain.Measurement, 0, len(points))
	seen := make(map[legacyTelemetryKey]struct{}, len(points))
	for _, point := range points {
		key := legacyKey(point)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, point)
	}
	sort.Slice(unique, func(i, j int) bool {
		a, b := legacyKey(unique[i]), legacyKey(unique[j])
		if a.well != b.well {
			return a.well < b.well
		}
		return a.at.Before(b.at)
	})
	// SQL templates are built once per bounded chunk size; rebuilding the Squirrel
	// persistent value list for every chunk adds avoidable CPU under the existing deadline.
	templates := make(map[int]string, 2)
	for start := 0; start < len(unique); start += batchSize {
		end := min(start+batchSize, len(unique))
		chunk := unique[start:end]
		sql := templates[len(chunk)]
		if sql == "" {
			q := sqlBuilder.Insert("telemetry_measurement").Columns("well_id", "measured_at", "values", "quality", "source_id", "collection_provenance")
			for range chunk {
				q = q.Values(nil, nil, nil, nil, nil, nil)
			}
			var e error
			sql, _, e = build(q.Suffix("ON CONFLICT (well_id,measured_at) DO NOTHING"))
			if e != nil {
				return 0, 0, e
			}
			templates[len(chunk)] = sql
		}
		args := make([]any, 0, len(chunk)*6)
		for _, point := range chunk {
			raw, e := json.Marshal(point.Values)
			if e != nil {
				return 0, 0, e
			}
			args = append(args, point.WellID, point.Timestamp, raw, point.Quality, sourceID, nil)
		}
		tag, e := tx.Exec(ctx, sql, args...)
		if e != nil {
			return 0, 0, e
		}
		inserted += int(tag.RowsAffected())
		if tag.RowsAffected() == int64(len(chunk)) {
			continue
		}
		// Inspect only batches with an actual INSERT conflict, after concurrent writers resolved.
		// Own newly inserted rows have NULL attribution; any protected row is a conflicting existing key.
		// The bounded pipeline holds SHARE locks until the surrounding whole-batch transaction ends.
		conflicts := &pgx.Batch{}
		for _, point := range chunk {
			conflicts.Queue(`SELECT collection_provenance FROM telemetry_measurement WHERE well_id=$1 AND measured_at=$2 FOR SHARE`, point.WellID, point.Timestamp)
		}
		if conflicts.Len() == 0 {
			continue
		}
		result := tx.SendBatch(ctx, conflicts)
		for i := 0; i < conflicts.Len(); i++ {
			var attribution []byte
			if e = result.QueryRow().Scan(&attribution); e != nil {
				_ = result.Close()
				return 0, 0, e
			}
			if attribution != nil {
				_ = result.Close()
				return 0, 0, domain.NewError(domain.Duplicate, "telemetry_measurement_conflict")
			}
		}
		if e = result.Close(); e != nil {
			return 0, 0, e
		}
	}
	return inserted, len(points) - inserted, nil
}
