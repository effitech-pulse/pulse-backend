// Package migrations управляет явным принятием схемы PostgreSQL.
package migrations

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var initialSchema string

//go:embed integrations.sql
var integrationSchema string

//go:embed well_identity.sql
var wellIdentitySchema string

//go:embed manual_checks.sql
var manualChecksSchema string

//go:embed reviews.sql
var reviewsSchema string

//go:embed fund_type.sql
var fundTypeSchema string

//go:embed access.sql
var accessSchema string

//go:embed well_lifecycle.sql
var wellLifecycleSchema string

//go:embed cohort.sql
var cohortSchema string

//go:embed ml_snapshot.sql
var snapshotSchema string

//go:embed run_scope.sql
var runScopeSchema string

//go:embed file_inputs.sql
var fileInputsSchema string

// CurrentVersion соответствует последней additive migration.
const CurrentVersion = 12

// Apply создаёт схему новой БД или принимает БД на проверенном Alembic head.
func Apply(ctx context.Context, pool *pgxpool.Pool) error {
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
	const recordMigration = `
		CREATE TABLE IF NOT EXISTS backend_schema_migration (
			version integer PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		);
		INSERT INTO backend_schema_migration(version)
		VALUES (1) ON CONFLICT DO NOTHING`
	if _, err = tx.Exec(ctx, recordMigration); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, integrationSchema); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, wellIdentitySchema); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, manualChecksSchema); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, reviewsSchema); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, fundTypeSchema); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, accessSchema); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, wellLifecycleSchema); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, cohortSchema); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, snapshotSchema); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, runScopeSchema); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, fileInputsSchema); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
