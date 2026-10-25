package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"os"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

func main() {
	if err := run(); err != nil {
		slog.Error("notification activation failed", "error_code", "operation_failed")
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := postgres.Open(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	r := postgres.New(pool)
	if err = r.ValidateNotifications(ctx); err != nil {
		return err
	}
	var token [16]byte
	if _, err = rand.Read(token[:]); err != nil {
		return err
	}
	activation, err := r.ActivateNotifications(ctx, hex.EncodeToString(token[:]))
	if err != nil {
		return err
	}
	slog.Info("notification capture active", "activation_id", activation.ID, "activated_at", activation.ActivatedAt)
	return nil
}
