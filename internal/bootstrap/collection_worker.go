package bootstrap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/jobqueue"
	"github.com/effitech-pulse/pulse-backend/internal/adapter/telemetry"
	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

// RunCollectionJobWorker обслуживает отдельный synthetic consumer без browser principal.
func RunCollectionJobWorker(ctx context.Context, c config.Config) error {
	if c.CollectionJobMode != "synthetic" || c.CollectionJobAMQPURL == "" || c.CollectionJobProducerAMQPURL == "" {
		return errors.New("collection_jobs_disabled")
	}
	policy, err := loadCollectionPolicy(c)
	if err != nil {
		return err
	}
	if c.AuthMode != "local" || c.AppScope != "research" || c.PoolSize > 8 {
		return errors.New("collection_jobs_disabled")
	}
	pool, err := postgres.Open(ctx, c)
	if err != nil {
		return err
	}
	defer pool.Close()
	trusted, err := postgres.New(pool).WithCollectionJobPolicy(policy)
	if err != nil {
		return err
	}
	if err = trusted.ValidateCollectionJobPolicy(ctx); err != nil {
		return err
	}
	source, err := telemetry.NewCollectionSource(policy.FixtureOrigin)
	if err != nil {
		return err
	}
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		return err
	}
	workerID := hex.EncodeToString(id)
	worker, err := service.NewCollectionJobWorker(trusted, source, workerID)
	if err != nil {
		return err
	}
	publisher := jobqueue.NewCollectionPublisher(c.CollectionJobProducerAMQPURL)
	defer publisher.Close()
	dispatcher, err := service.NewCollectionJobDispatcher(trusted, publisher, workerID)
	if err != nil {
		return err
	}
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
						slog.WarnContext(run, "collection job background operation failed", "error_code", "operation_failed")
					}
				}
			}
		}()
	}
	loop(time.Second, dispatcher.DispatchOnce)
	loop(5*time.Second, dispatcher.ReconcileOnce)
	err = jobqueue.ConsumeCollection(run, c.CollectionJobAMQPURL, worker)
	cancel()
	wg.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return err
}
