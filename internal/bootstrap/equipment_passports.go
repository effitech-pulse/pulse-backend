package bootstrap

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/spreadsheet"
	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func buildEquipmentPassports(ctx context.Context, c config.Config, store *postgres.Repository, scope service.FundScope) (*service.EquipmentPassports, func(), error) {
	if err := store.ValidateEquipmentPassports(ctx); err != nil {
		return nil, nil, err
	}
	passports, err := service.NewEquipmentPassports(store, spreadsheet.PassportCodec{}, service.PassportOptions{Scope: scope, AppScope: c.AppScope, CohortVersion: c.CohortVersion, Location: c.Location})
	if err != nil {
		return nil, nil, err
	}
	closeGC, err := startPassportPurge(ctx, store.PurgeExpiredPassportPreviews, 5*time.Minute)
	if err != nil {
		return nil, nil, err
	}
	return passports, closeGC, nil
}

// startPassportPurge fails startup if expired payloads cannot be purged. Close cancels
// an in-flight bounded purge and waits before the application closes its DB pool.
func startPassportPurge(parent context.Context, purge func(context.Context) error, interval time.Duration) (func(), error) {
	ctx, cancel := context.WithCancel(parent)
	if err := purge(ctx); err != nil {
		cancel()
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := purge(ctx); err != nil && ctx.Err() == nil {
					slog.ErrorContext(ctx, "equipment passport preview purge failed", "error_code", "equipment_passport_purge_failed")
				}
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { cancel(); <-done }) }, nil
}
