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
	"google.golang.org/grpc/grpclog"

	"github.com/effitech-pulse/pulse-backend/internal/bootstrap"
	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/observability"
)

func main() {
	observability.Install("unknown")
	grpclog.SetLoggerV2(grpclog.NewLoggerV2(slog.NewLogLogger(slog.Default().Handler(), slog.LevelInfo).Writer(), slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn).Writer(), slog.NewLogLogger(slog.Default().Handler(), slog.LevelError).Writer()))
	gin.SetMode(gin.ReleaseMode)
	if err := run(); err != nil {
		slog.Error("API stopped", "error_code", "operation_failed")
		os.Exit(1)
	}
}

func run() error {
	runtime, err := observability.NewRuntime(context.Background(), "unknown", observability.ExportConfig{Enabled: os.Getenv("PULSE_OBSERVABILITY_ENABLED") == "true", LogDir: os.Getenv("PULSE_OBSERVABILITY_LOG_DIR")})
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = runtime.Shutdown(ctx)
	}()
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
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
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
	slog.Info("Go API started")
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
