package app

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var initialSchema string

type DB interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func queryJSON(ctx context.Context, db DB, sql string, args ...any) ([]map[string]any, error) {
	rows, err := db.Query(ctx, "SELECT row_to_json(q) FROM ("+sql+") q", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var row map[string]any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err = dec.Decode(&row); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
func single(ctx context.Context, db DB, sql string, args ...any) (map[string]any, error) {
	rows, err := queryJSON(ctx, db, sql, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, notFound("Запись не найдена")
	}
	return rows[0], nil
}
func dbError(err error) error {
	if err == nil {
		return nil
	}
	var api *APIError
	if errors.As(err, &api) {
		return err
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		if pg.Code == "23505" {
			return conflict("Запись с такими идентификаторами уже существует")
		}
		if pg.Code == "23503" {
			return validation("Связанная запись не найдена или используется")
		}
	}
	return err
}

// Migrate создаёт схему новой БД или принимает БД на проверенном Alembic head.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(74019322)"); err != nil {
		return err
	}
	var present bool
	if err = tx.QueryRow(ctx, "SELECT to_regclass('well') IS NOT NULL").Scan(&present); err != nil {
		return err
	}
	if present {
		var hasVersion bool
		if err = tx.QueryRow(ctx, "SELECT to_regclass('alembic_version') IS NOT NULL").Scan(&hasVersion); err != nil {
			return err
		}
		if !hasVersion {
			return fmt.Errorf("existing database has no Alembic version; refusing automatic adoption")
		}
		var version string
		if err = tx.QueryRow(ctx, "SELECT version_num FROM alembic_version").Scan(&version); err != nil {
			return err
		}
		if version != "0012_ml_run_drift" {
			return fmt.Errorf("database is at unsupported revision %s", version)
		}
	} else {
		if _, err = tx.Exec(ctx, initialSchema); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS backend_schema_migration(version integer PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now()); INSERT INTO backend_schema_migration(version) VALUES(1) ON CONFLICT DO NOTHING"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func mutateRow(ctx context.Context, db DB, sql string, args ...any) (map[string]any, error) {
	rows, err := db.Query(ctx, "WITH changed AS ("+sql+") SELECT row_to_json(changed) FROM changed", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if rows.Err() != nil {
			return nil, rows.Err()
		}
		return nil, notFound("Запись не найдена")
	}
	var raw []byte
	if err = rows.Scan(&raw); err != nil {
		return nil, err
	}
	var result map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err = dec.Decode(&result); err != nil {
		return nil, err
	}
	return result, rows.Err()
}

// OpenPool сохраняет локальную зону naive timestamps исходного API.
func OpenPool(ctx context.Context, c Config) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(c.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if c.Location != nil {
		cfg.ConnConfig.RuntimeParams["timezone"] = c.Location.String()
	}
	size, err := envInt("DB_POOL_SIZE", 5)
	if err != nil || size < 2 {
		return nil, fmt.Errorf("DB_POOL_SIZE must be >=2 for run locking")
	}
	cfg.MaxConns = int32(size)
	return pgxpool.NewWithConfig(ctx, cfg)
}
