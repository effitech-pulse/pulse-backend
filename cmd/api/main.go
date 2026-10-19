package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/bootstrap"
	"github.com/effitech-pulse/pulse-backend/internal/config"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	if err := run(); err != nil {
		slog.Error("API stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	application, err := bootstrap.New(ctx, cfg)
	if err != nil {
		return err
	}
	defer application.Close()
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           application.HTTP.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      cfg.MLTimeout + 30*time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()
	go application.ML.Schedule(ctx)
	go application.HTTP.Integrations.Schedule(ctx)
	slog.Info("Go API started", "address", cfg.Addr, "ml_target", cfg.MLTarget)
	select {
	case err = <-errCh:
		cancel()
	case <-ctx.Done():
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.MLTimeout+30*time.Second)
	defer shutdownCancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	application.ML.Wait()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return shutdownErr
}
