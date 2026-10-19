package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/observability"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

func main() {
	observability.Install("unknown")
	version := flag.String("version", "", "Immutable cohort version to capture")
	flag.Parse()
	if err := run(*version); err != nil {
		slog.Error("cohort capture failed", "error_code", "operation_failed")
		os.Exit(1)
	}
}
func run(version string) error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, c)
	if err != nil {
		return err
	}
	defer pool.Close()
	cohort, err := postgres.New(pool).CaptureCohort(ctx, version)
	if err == nil {
		slog.Info("cohort captured", "version", cohort.Version, "members", 100)
	}
	return err
}
