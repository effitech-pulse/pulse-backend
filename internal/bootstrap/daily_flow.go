package bootstrap

import (
	"context"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/dailyflow"
	"github.com/effitech-pulse/pulse-backend/internal/adapter/spreadsheet"
	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func buildDailyFlow(ctx context.Context, c config.Config, catalog *service.Catalog, scope service.FundScope, now time.Time) (*service.DailyFlow, error) {
	if err := config.ValidateDailyFlow(c); err != nil {
		return nil, err
	}
	if c.DailyFlowMode == "off" || c.DailyFlowMode == "" {
		return service.NewDailyFlow(catalog, scope, nil, nil, "off"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p, err := dailyflow.New(now)
	if err != nil {
		return nil, err
	}
	if err = service.ValidateDailyFlowCohort(ctx, scope, p.Profile()); err != nil {
		return nil, err
	}
	return service.NewDailyFlow(catalog, scope, p, spreadsheet.DailyFlowWriter{}, "synthetic"), nil
}
