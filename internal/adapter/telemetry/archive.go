package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// Archive читает Pulse Data; не имеет доступа к БД и не выполняет генерацию.
type Archive struct {
	BaseURL      string
	AllowedHosts []string
}

func (a Archive) get(ctx context.Context, path string, query url.Values, out any) error {
	if a.BaseURL == "" {
		return nil
	}
	u, err := AllowedURL(strings.TrimRight(a.BaseURL, "/")+path, a.AllowedHosts)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("HTTP address required")
	}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return domain.NewError(domain.Unavailable, "Pulse Data недоступен")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return domain.NewError(domain.Unavailable, "Pulse Data не вернул телеметрию")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
	if err != nil {
		return err
	}
	if len(raw) > 8*1024*1024 {
		return domain.NewError(domain.Invalid, "Сократите период телеметрии")
	}
	return json.Unmarshal(raw, out)
}
func (a Archive) Bounds(ctx context.Context, well string) (domain.TelemetryBounds, error) {
	var out domain.TelemetryBounds
	err := a.get(ctx, "/v1/bounds", url.Values{"well_id": {well}}, &out)
	return out, err
}
func (a Archive) Read(ctx context.Context, well string, start, end time.Time) (domain.TelemetryResult, error) {
	out := domain.TelemetryResult{Points: []domain.Measurement{}}
	err := a.get(ctx, "/v1/telemetry", url.Values{"well_id": {well}, "start": {start.UTC().Format(time.RFC3339Nano)}, "end": {end.UTC().Format(time.RFC3339Nano)}}, &out)
	return out, err
}

// Scenario возвращает конфигурацию генератора отдельно от телеметрии модели.
func (a Archive) Scenario(ctx context.Context) (domain.PresentationScenario, error) {
	if a.BaseURL == "" {
		return domain.PresentationScenario{}, domain.NewError(domain.Unavailable, "Pulse Data не настроен")
	}
	var out domain.PresentationScenario
	err := a.get(ctx, "/v1/scenario", nil, &out)
	return out, err
}
