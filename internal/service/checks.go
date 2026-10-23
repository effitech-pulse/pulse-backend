package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/observability"
)

type CheckStore interface {
	ManualChecks(context.Context, int64, string) ([]domain.ManualCheck, error)
	ManualCheck(context.Context, int64, string) (domain.ManualCheck, error)
	ManualCheckRequest(context.Context, int64, string) (domain.ManualCheck, error)
	SaveManualCheck(context.Context, domain.ManualCheck) (domain.ManualCheck, error)
}
type Checks struct {
	scope     FundScope
	store     CheckStore
	telemetry *Integrations
}

func (s *Checks) WithScope(scope FundScope) *Checks { s.scope = scope; return s }

func NewChecks(store CheckStore, telemetry *Integrations) *Checks {
	return &Checks{store: store, telemetry: telemetry}
}
func (s *Checks) List(ctx context.Context, well int64, before string) ([]domain.ManualCheck, error) {
	if err := requireDataPermission(ctx, "checks.read"); err != nil {
		return nil, err
	}
	if domain.RBACEnforced(ctx) {
		store, ok := s.store.(Store)
		if !ok {
			return nil, domain.NewError(domain.Unavailable, "Справочник не подключён")
		}
		if _, err := NewCatalog(store).WithScope(s.scope).RequireWell(ctx, "checks.read", well); err != nil {
			return nil, err
		}
	}
	if err := scopedWell(ctx, s.scope, well); err != nil {
		return nil, err
	}
	return s.store.ManualChecks(ctx, well, before)
}
func (s *Checks) Get(ctx context.Context, well int64, id string) (domain.ManualCheck, error) {
	if err := requireDataPermission(ctx, "checks.read"); err != nil {
		return domain.ManualCheck{}, err
	}
	if domain.RBACEnforced(ctx) {
		store, ok := s.store.(Store)
		if !ok {
			return domain.ManualCheck{}, domain.NewError(domain.Unavailable, "Справочник не подключён")
		}
		if _, err := NewCatalog(store).WithScope(s.scope).RequireWell(ctx, "checks.read", well); err != nil {
			return domain.ManualCheck{}, err
		}
	}
	if err := scopedWell(ctx, s.scope, well); err != nil {
		return domain.ManualCheck{}, err
	}
	return s.store.ManualCheck(ctx, well, id)
}

var checkRequestID = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,80}$`)

func checkPeriod(v domain.ManualCheck, in domain.CheckInput) (domain.ManualCheck, error) {
	if !v.Start.Equal(in.Start) || !v.End.Equal(in.End) {
		return domain.ManualCheck{}, domain.NewError(domain.Duplicate, "Этот request_id уже использован для другого периода")
	}
	return v, nil
}

// Create сохраняет результат до ответа клиенту. Повтор запроса возвращает сохранённый расчёт.
func (s *Checks) Create(ctx context.Context, well domain.Well, in domain.CheckInput) (out domain.ManualCheck, err error) {
	if domain.RBACEnforced(ctx) {
		store, ok := s.store.(Store)
		if !ok {
			return domain.ManualCheck{}, domain.NewError(domain.Unavailable, "Справочник не подключён")
		}
		current, err := NewCatalog(store).WithScope(s.scope).RequireWell(ctx, "checks.run", well.ID)
		if err != nil {
			return domain.ManualCheck{}, err
		}
		well = current
	}
	if err := requireWellValue(ctx, "checks.run", well); err != nil {
		return domain.ManualCheck{}, err
	}
	if err := scopedWell(ctx, s.scope, well.ID); err != nil {
		return domain.ManualCheck{}, err
	}
	if in.Start.Nanosecond()%1000 != 0 || in.End.Nanosecond()%1000 != 0 {
		return domain.ManualCheck{}, domain.NewError(domain.Invalid, "Точность границ периода — до микросекунды")
	}
	if !checkRequestID.MatchString(in.RequestID) || in.Start.IsZero() || !in.Start.Before(in.End) || in.End.Sub(in.Start) > 366*24*time.Hour {
		return domain.ManualCheck{}, domain.NewError(domain.Invalid, "Нужны request_id (16–80 символов) и период до 366 дней")
	}
	previous, err := s.store.ManualCheckRequest(ctx, well.ID, in.RequestID)
	if err == nil {
		return checkPeriod(previous, in)
	}
	var de *domain.Error
	if !errors.As(err, &de) || de.Kind != domain.Missing {
		return domain.ManualCheck{}, err
	}
	// Закрытие вкладки не отменяет уже запущенную ручную проверку и её запись.
	operationStart := time.Now()
	operation, span := observability.Detached(ctx, "check.execute")
	defer func() { observability.Complete(operation, span, "operation completed", operationStart, err) }()
	work, cancel := context.WithTimeout(operation, 5*time.Minute)
	defer cancel()
	if s.telemetry.scorer == nil {
		return domain.ManualCheck{}, domain.NewError(domain.Unavailable, "ML-сервис не подключён")
	}
	data, err := s.telemetry.ReadApp(work, well, in.Start, in.End)
	if err != nil {
		return domain.ManualCheck{}, err
	}
	content, err := telemetryCSV(data)
	if err != nil {
		return domain.ManualCheck{}, err
	}
	result, err := s.telemetry.scorer.Upload(work, "telemetry.csv", strings.NewReader(content))
	if err != nil {
		return domain.ManualCheck{}, err
	}
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		return domain.ManualCheck{}, err
	}
	digest := sha256.Sum256([]byte(content))
	v := domain.ManualCheck{
		ID:          hex.EncodeToString(token),
		WellID:      well.ID,
		RequestID:   in.RequestID,
		Start:       in.Start.UTC(),
		End:         in.End.UTC(),
		CreatedAt:   time.Now().UTC(),
		InputSHA256: hex.EncodeToString(digest[:]),
		PointsCount: len(data.Points),
		Resolution:  data.Resolution,
		Result:      result,
	}
	// Для записи выделяется отдельный срок, даже если лимит расчёта истёк на последнем шаге.
	save, stop := context.WithTimeout(context.WithoutCancel(work), 10*time.Second)
	defer stop()
	saved, err := s.store.SaveManualCheck(save, v)
	if err != nil {
		return domain.ManualCheck{}, err
	}
	return checkPeriod(saved, in)
}
