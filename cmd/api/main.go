package main

import (
	"context"
	"effitech/backend/internal/app"
	"errors"

	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"
)

func main() {
	if err := run(); err != nil {
		slog.Error("API stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	config, err := app.LoadConfig()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	pool, err := app.OpenPool(ctx, config)
	if err != nil {
		return err
	}
	defer pool.Close()
	ml, err := app.NewGRPCClient(config)
	if err != nil {
		return err
	}
	defer ml.Close()
	api := app.New(config, pool, ml)
	srv := &http.Server{Addr: config.Addr, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: config.MLTimeout + 30*time.Second, IdleTimeout: 120 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	go api.Schedule(ctx)
	slog.Info("Go API started", "address", config.Addr, "ml_target", config.MLTarget)
	select {
	case err = <-errCh:
		cancel()
	case <-ctx.Done():
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), config.MLTimeout+30*time.Second)
	defer shutdownCancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	api.Wait()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return shutdownErr
}
