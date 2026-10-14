package main

import (
	"context"
	"effitech/backend/internal/app"

	"log/slog"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		slog.Error("migration failed", "error", err)
		os.Exit(1)
	}
}
func run() error {
	config, err := app.LoadConfig()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := app.OpenPool(ctx, config)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err = app.Migrate(ctx, pool); err != nil {
		return err
	}
	slog.Info("database schema ready", "version", 1)
	return nil
}
