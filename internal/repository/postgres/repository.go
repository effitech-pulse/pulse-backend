// Package postgres реализует хранилище предметной области через pgx.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type db interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

type Repository struct {
	pool                  *pgxpool.Pool
	legacyResearchUnbound bool
	fileJobPolicy         *domain.FileJobPolicy
	cohortJobPolicy       *domain.CohortJobPolicy
	cohortClaims          *cohortClaimRegistry
	collectionJobPolicy   *domain.CollectionPolicy
	collectionClaims      *collectionClaimRegistry
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, cohortClaims: &cohortClaimRegistry{claims: make(map[string]*cohortClaim)}}
}

func Open(ctx context.Context, c config.Config) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(c.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database config: %w", err)
	}
	cfg.MaxConns = c.PoolSize
	if c.Location != nil {
		cfg.ConnConfig.RuntimeParams["timezone"] = c.Location.String()
	}
	return pgxpool.NewWithConfig(ctx, cfg)
}

func (r *Repository) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}

func translate(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return &domain.Error{Kind: domain.Duplicate, Message: "Запись с такими идентификаторами уже существует", Cause: err}
		case "23503":
			return &domain.Error{Kind: domain.Invalid, Message: "Связанная запись не найдена или используется", Cause: err}
		}
	}
	return err
}

// Компилируем вложенные Sqlizer целиком перед нумерацией PostgreSQL-параметров.
var sqlBuilder = sq.StatementBuilder

func build(query sq.Sqlizer) (string, []any, error) {
	sql, args, err := query.ToSql()
	if err != nil {
		return "", nil, fmt.Errorf("build query: %w", err)
	}
	sql, err = sq.Dollar.ReplacePlaceholders(sql)
	return sql, args, err
}

func execute(ctx context.Context, db db, query sq.Sqlizer) (pgconn.CommandTag, error) {
	sql, args, err := build(query)
	if err != nil {
		return pgconn.CommandTag{}, err
	}
	tag, err := db.Exec(ctx, sql, args...)
	return tag, translate(err)
}

// JSON PostgreSQL используется только внутри адаптера; за границу выходят структуры.
func readRows[T any](ctx context.Context, db db, query sq.Sqlizer) ([]T, error) {
	sql, args, err := build(query)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := make([]T, 0)
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var value T
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, fmt.Errorf("decode database row: %w", err)
		}
		out = append(out, value)
	}
	return out, translate(rows.Err())
}

func list[T any](ctx context.Context, db db, query sq.SelectBuilder) ([]T, error) {
	return readRows[T](ctx, db, sqlBuilder.Select("row_to_json(q)").FromSelect(query, "q"))
}

func one[T any](ctx context.Context, db db, query sq.SelectBuilder) (T, error) {
	rows, err := list[T](ctx, db, query)
	var zero T
	if err != nil {
		return zero, err
	}
	if len(rows) == 0 {
		return zero, domain.NewError(domain.Missing, "Запись не найдена")
	}
	return rows[0], nil
}

func mutate[T any](ctx context.Context, db db, query sq.Sqlizer) (T, error) {
	rows, err := readRows[T](ctx, db, sq.Expr("WITH changed AS (?) SELECT row_to_json(changed) FROM changed", query))
	var zero T
	if err != nil {
		return zero, err
	}
	if len(rows) == 0 {
		return zero, domain.NewError(domain.Missing, "Запись не найдена")
	}
	return rows[0], nil
}

func remove(ctx context.Context, db db, query sq.DeleteBuilder) error {
	tag, err := execute(ctx, db, query)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.NewError(domain.Missing, "Запись не найдена")
	}
	return nil
}
