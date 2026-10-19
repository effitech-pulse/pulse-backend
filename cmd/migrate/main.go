package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/migrations"
	"github.com/effitech-pulse/pulse-backend/internal/observability"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

func main() {
	observability.Install("unknown")
	if err := run(); err != nil {
		slog.Error("migration failed", "error_code", "operation_failed")
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err = migrations.Apply(ctx, pool); err != nil {
		return err
	}
	slog.Info("database schema ready", "version", migrations.CurrentVersion)
	return nil
}
