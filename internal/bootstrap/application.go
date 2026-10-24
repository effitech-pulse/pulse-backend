// Package bootstrap собирает зависимости; бизнес-слои не знают адаптеров.
package bootstrap

import (
	"context"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/grpcml"
	"github.com/effitech-pulse/pulse-backend/internal/adapter/parameterfile"
	"github.com/effitech-pulse/pulse-backend/internal/adapter/telemetry"
	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
	"github.com/effitech-pulse/pulse-backend/internal/transport/httpapi"
)

type Application struct {
	HTTP   *httpapi.Server
	ML     *service.ML
	pool   *pgxpool.Pool
	client *grpcml.GRPCClient
}

func New(ctx context.Context, c config.Config) (*Application, error) {
	pool, err := postgres.Open(ctx, c)
	if err != nil {
		return nil, err
	}
	client, err := grpcml.New(c)
	if err != nil {
		pool.Close()
		return nil, err
	}
	mode := domain.RBACMode(c.RBACMode)
	if mode == "" {
		mode = domain.RBACCompat
	}
	ctx = domain.WithRBACMode(ctx, mode)
	store := postgres.New(pool).WithExecutionProfile(c.AuthMode, c.AppScope)
	if err := store.ValidateRBAC(ctx, mode); err != nil {
		pool.Close()
		return nil, err
	}
	if err := store.ValidateEmployeeProfiles(ctx); err != nil {
		_ = client.Close()
		pool.Close()
		return nil, err
	}
	var scope service.FundScope
	if c.AppScope != "research" {
		scope, err = service.NewFundScope(ctx, store, c.CohortVersion)
		if err != nil {
			_ = client.Close()
			pool.Close()
			return nil, err
		}
	}
	ml := service.NewML(store, store, client, service.RunOptions{
		Profile:       c.AppScope,
		CohortVersion: c.CohortVersion,
		Timeout:       c.MLTimeout,
		AutoRun:       c.AutoRun,
		Hour:          c.AutoRunHour,
		Location:      c.Location,
	})
	dashboards := make([]service.Dashboard, 0, len(c.Dashboards))
	for _, d := range c.Dashboards {
		dashboards = append(dashboards, service.Dashboard{ID: d.ID, Name: d.Name})
	}
	archive := telemetry.Archive{BaseURL: os.Getenv("TELEMETRY_DATA_API_URL"), AllowedHosts: strings.Split(os.Getenv("INTEGRATION_ALLOWED_HOSTS"), ",")}
	server := &httpapi.Server{
		Config:       c,
		Catalog:      service.NewCatalog(store).WithScope(scope),
		ML:           ml,
		Parameters:   service.NewParameters(parameterfile.Reader{Path: c.ParameterPath}),
		Integrations: service.NewIntegrations(store, telemetry.Connector{AllowedHosts: strings.Split(os.Getenv("INTEGRATION_ALLOWED_HOSTS"), ","), Databases: postgres.ExternalTelemetry{}}, telemetry.Secrets{Key: os.Getenv("INTEGRATION_SECRET_KEY")}, client).WithArchive(archive).WithScope(scope),
		Analytics: service.Analytics{
			URL:        c.MetabaseURL,
			Secret:     c.MetabaseSecret,
			TTL:        c.TokenTTL,
			Dashboards: dashboards,
		},
	}
	server.Access, err = service.NewAccess(store, c.AuthMode == "local")
	if err != nil {
		_ = client.Close()
		pool.Close()
		return nil, err
	}
	server.Access = server.Access.WithRBACMode(mode)
	server.Events, err = service.NewEvents(store, scope, service.EventsOptions{Mode: c.AppScope, Prefix: c.Prefix})
	if err != nil {
		_ = client.Close()
		pool.Close()
		return nil, err
	}
	server.Checks = service.NewChecks(store, server.Integrations).WithScope(scope)
	server.Reviews = service.NewReviews(store).WithCatalog(store).WithScope(scope)
	server.Presentation = service.NewPresentation(store).WithSource(archive).WithScope(scope)
	if c.FileCheckMode == "synthetic" {
		var trusted *postgres.Repository
		server.FileJobs, _, trusted, err = buildFileJobs(ctx, c, store, scope, client)
		if err != nil {
			_ = client.Close()
			pool.Close()
			return nil, err
		}
		server.Reviews = service.NewReviews(trusted).WithCatalog(trusted).WithScope(scope)
	}
	if c.CohortJobMode == "synthetic" {
		server.CohortJobs, _, err = buildCohortJobs(ctx, c, store)
		if err != nil {
			_ = client.Close()
			pool.Close()
			return nil, err
		}
	}
	server.CollectionJobs, err = buildCollectionJobs(ctx, c, store)
	if err != nil {
		_ = client.Close()
		pool.Close()
		return nil, err
	}
	if scope != nil {
		ml.WithCohort(scope, server.Integrations, store)
	}
	return &Application{HTTP: server, ML: ml, pool: pool, client: client}, nil
}

func (a *Application) Close() {
	a.ML.Wait()
	_ = a.client.Close()
	a.pool.Close()
}
