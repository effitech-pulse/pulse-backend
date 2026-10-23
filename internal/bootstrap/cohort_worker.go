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

// RunCohortJobWorker обслуживает отдельный synthetic consumer без browser principal.
func RunCohortJobWorker(ctx context.Context, c config.Config) error {
	if c.CohortJobMode != "synthetic" || c.CohortJobAMQPURL == "" || c.CohortJobProducerAMQPURL == "" || c.CohortJobMLTarget == "" || c.CohortJobMLTarget == c.MLTarget {
		return errors.New("cohort_jobs_disabled")
	}
	policy, err := loadCohortPolicy(c)
	if err != nil {
		return err
	}
	pool, err := postgres.Open(ctx, c)
	if err != nil {
		return err
	}
	defer pool.Close()
	mode := domain.RBACMode(c.RBACMode)
	if mode == "" {
		mode = domain.RBACCompat
	}
	ctx = domain.WithRBACMode(ctx, mode)
	if err = postgres.New(pool).ValidateRBAC(ctx, mode); err != nil {
		return err
	}
	trusted, err := postgres.New(pool).WithCohortJobPolicy(policy)
	if err != nil {
		return err
	}
	if err = trusted.ValidateCohortJobPolicy(ctx); err != nil {
		return err
	}
	mlConfig := c
	mlConfig.MLTarget = c.CohortJobMLTarget
	mlConfig.MLTimeout = 10 * time.Second
	client, err := grpcml.New(mlConfig)
	if err != nil {
		return err
	}
	defer client.Close()
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		return err
	}
	workerID := hex.EncodeToString(id)
	worker, err := service.NewCohortJobWorker(trusted, grpcml.CohortJobScorer{Scorer: client}, workerID)
	if err != nil {
		return err
	}
	publisher := jobqueue.NewCohortPublisher(c.CohortJobProducerAMQPURL)
	defer publisher.Close()
	dispatcher, err := service.NewCohortJobDispatcher(trusted, publisher, workerID)
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
						slog.WarnContext(run, "cohort job background operation failed", "error_code", "operation_failed")
					}
				}
			}
		}()
	}
	loop(time.Second, dispatcher.DispatchOnce)
	loop(5*time.Second, dispatcher.ReconcileOnce)
	err = jobqueue.ConsumeCohort(run, c.CohortJobAMQPURL, worker)
	cancel()
	wg.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return err
}
