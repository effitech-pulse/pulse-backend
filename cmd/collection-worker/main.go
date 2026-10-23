package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/effitech-pulse/pulse-backend/internal/bootstrap"
	"github.com/effitech-pulse/pulse-backend/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c, e := config.Load()
	if e != nil {
		slog.Error("collection worker configuration rejected", "error_code", "configuration_invalid")
		os.Exit(1)
	}
	if e = bootstrap.RunCollectionJobWorker(ctx, c); e != nil {
		slog.Error("collection worker stopped", "error_code", "worker_unavailable")
		os.Exit(1)
	}
}
