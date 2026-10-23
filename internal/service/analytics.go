package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type Dashboard struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type AnalyticsStatus struct {
	Enabled    bool        `json:"enabled"`
	Dashboards []Dashboard `json:"dashboards"`
}

type EmbedURL struct {
	URL string `json:"url"`
}

type Analytics struct {
	URL        string
	Secret     string
	TTL        int64
	Dashboards []Dashboard
}

func (a Analytics) Status() AnalyticsStatus {
	return AnalyticsStatus{Enabled: a.URL != "" && a.Secret != "", Dashboards: a.Dashboards}
}

func (a Analytics) EmbedContext(ctx context.Context, id int64) (EmbedURL, error) {
	if err := requireFullApplicationScope(ctx, "analytics.read"); err != nil {
		return EmbedURL{}, err
	}
	if !a.Status().Enabled {
		return EmbedURL{}, &domain.Error{Kind: "analytics_disabled", Message: "Аналитика (Metabase) не настроена"}
	}
	allowed := false
	for _, d := range a.Dashboards {
		if d.ID == id {
			allowed = true
		}
	}
	if !allowed {
		return EmbedURL{}, domain.NewError(domain.Missing, "Дашборд не разрешён для встраивания")
	}
	payload := struct {
		Resource struct {
			Dashboard int64 `json:"dashboard"`
		} `json:"resource"`
		Params  struct{} `json:"params"`
		Expires int64    `json:"exp"`
	}{Expires: time.Now().Unix() + a.TTL}
	payload.Resource.Dashboard = id
	raw, err := json.Marshal(payload)
	if err != nil {
		return EmbedURL{}, err
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	body := header + "." + base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(a.Secret))
	_, _ = mac.Write([]byte(body))
	token := body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return EmbedURL{URL: a.URL + "/embed/dashboard/" + token + "#bordered=true&titled=true"}, nil
}
