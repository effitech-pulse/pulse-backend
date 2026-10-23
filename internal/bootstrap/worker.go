package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/grpcml"
	"github.com/effitech-pulse/pulse-backend/internal/adapter/jobqueue"
	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

// RunFileJobWorker — отдельный bounded supervisor; не принимает browser principal.
func RunFileJobWorker(ctx context.Context, c config.Config) error {
	if c.FileCheckMode != "synthetic" || c.FileJobAMQPURL == "" || c.FileJobProducerAMQPURL == "" {
		return errors.New("file_checks_disabled")
	}
	pool, e := postgres.Open(ctx, c)
	if e != nil {
		return e
	}
	defer pool.Close()
	client, e := grpcml.New(c)
	if e != nil {
		return e
	}
	defer client.Close()
	mode := domain.RBACMode(c.RBACMode)
	if mode == "" {
		mode = domain.RBACCompat
	}
	ctx = domain.WithRBACMode(ctx, mode)
	store := postgres.New(pool)
	if err := store.ValidateRBAC(ctx, mode); err != nil {
		pool.Close()
		return err
	}
	scope, e := service.NewFundScope(ctx, store, c.CohortVersion)
	if e != nil {
		return e
	}
	_, checks, trusted, e := buildFileJobs(ctx, c, store, scope, client)
	if e != nil {
		return e
	}
	random := make([]byte, 16)
	if _, e = rand.Read(random); e != nil {
		return e
	}
	worker, e := service.NewFileJobWorker(trusted, checks, hex.EncodeToString(random))
	if e != nil {
		return e
	}
	publisher := jobqueue.NewPublisher(c.FileJobProducerAMQPURL)
	defer publisher.Close()
	dispatcher, e := service.NewFileJobDispatcher(trusted, publisher)
	if e != nil {
		return e
	}
	reconciler := service.NewFileJobReconciler(trusted)
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	loop := func(interval time.Duration, fn func(context.Context) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-run.Done():
					return
				case <-ticker.C:
					bounded, done := context.WithTimeout(run, 10*time.Second)
					err := fn(bounded)
					done()
					if err != nil && run.Err() == nil {
						slog.WarnContext(run, "file job background operation failed", "error_code", "operation_failed")
					}
				}
			}
		}()
	}
	loop(time.Second, dispatcher.DispatchOnce)
	loop(5*time.Second, reconciler.ReconcileOnce)
	e = jobqueue.Consume(run, c.FileJobAMQPURL, worker)
	cancel()
	wg.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return e
}
