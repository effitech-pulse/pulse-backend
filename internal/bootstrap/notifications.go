package bootstrap

import (
	"context"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

// buildNotifications проверяет готовность без DDL и без включения capture.
func buildNotifications(ctx context.Context, c config.Config, store *postgres.Repository, scope service.FundScope) (*service.Notifications, error) {
	if err := store.ValidateNotifications(ctx); err != nil {
		return nil, err
	}
	return service.NewNotifications(store, scope, service.NotificationsOptions{AuthMode: c.AuthMode, Mode: c.AppScope, Prefix: c.Prefix, UIEnabled: c.NotificationsUIEnabled})
}
