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

type DatabaseReader interface {
	ReadExternal(context.Context, domain.TelemetrySource, string) ([]map[string]any, error)
}
type Connector struct {
	AllowedHosts []string
	Databases    DatabaseReader
}

func AllowedURL(raw string, hosts []string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("invalid source address")
	}
	allowed := false
	for _, host := range hosts {
		if strings.EqualFold(strings.TrimSpace(host), u.Hostname()) {
			allowed = true
		}
	}
	if !allowed {
		return nil, domain.NewError(domain.Invalid, "Хост источника не включён в INTEGRATION_ALLOWED_HOSTS")
	}
	if u.User != nil {
		if _, has := u.User.Password(); has {
			return nil, domain.NewError(domain.Invalid, "Пароль указывается отдельно от адреса")
		}
	}
	return u, nil
}
func (c Connector) Fetch(ctx context.Context, s domain.TelemetrySource, secret string) ([]map[string]any, error) {
	u, err := AllowedURL(s.Endpoint, c.AllowedHosts)
	if err != nil {
		return nil, err
	}
	if s.Kind != "json_pull" {
		return c.Databases.ReadExternal(ctx, s, secret)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("HTTP address required")
	}
	if u.User != nil {
		return nil, fmt.Errorf("JSON auth belongs in secret field")
	}
	q := u.Query()
	since := time.Now().Add(-24 * time.Hour)
	if s.LastMeasured != nil {
		since = s.LastMeasured.Add(-time.Minute)
	}
	q.Set("since", since.UTC().Format(time.RFC3339Nano))
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("source unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("source returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 8*1024*1024 {
		return nil, fmt.Errorf("source response exceeds 8 MiB")
	}
	var data any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err = dec.Decode(&data); err != nil {
		return nil, fmt.Errorf("invalid JSON response")
	}
	for _, key := range strings.Split(s.RecordsPath, ".") {
		if key == "" {
			continue
		}
		m, ok := data.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("records path is not an object")
		}
		data = m[key]
	}
	array, ok := data.([]any)
	if !ok {
		return nil, fmt.Errorf("records path must contain an array")
	}
	if len(array) > 10000 {
		return nil, fmt.Errorf("source batch exceeds 10000 records")
	}
	out := make([]map[string]any, 0, len(array))
	for _, v := range array {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("record must be an object")
		}
		out = append(out, m)
	}
	return out, nil
}
