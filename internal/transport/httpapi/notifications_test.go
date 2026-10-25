package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestNotificationOpaqueSourceIDQueryRoundTrip(t *testing.T) {
	for _, id := range []string{"legacy/a", "literal%2F", "%", "?", "+", "кириллица/a%?+"} {
		q, e := parseNotificationQuery("source_id="+url.QueryEscape(id), "source_id")
		if e != nil || q.Get("source_id") != id {
			t.Fatal(q, e)
		}
	}
	for _, raw := range []string{"source_id=%QQ", "source_id=a&source_id=b", "source_id=", "source_id=a&unknown=x"} {
		if _, e := parseNotificationQuery(raw, "source_id"); e == nil {
			t.Fatal(raw)
		}
	}
}

type notificationHTTPStore struct {
	source   string
	err      error
	families []domain.NotificationFamily
}

func (f *notificationHTTPStore) NotificationCapabilities(context.Context, domain.NotificationScope) (domain.NotificationCapabilities, error) {
	return domain.NotificationCapabilities{Families: f.families}, f.err
}
func (f *notificationHTTPStore) ReadNotificationPage(context.Context, domain.NotificationQuery) (domain.NotificationRecords, error) {
	if f.err != nil {
		return domain.NotificationRecords{}, f.err
	}
	if len(f.families) == 0 {
		return domain.NotificationRecords{}, domain.NewError(domain.Forbidden, "private")
	}
	zero := 0.0
	return domain.NotificationRecords{Items: []domain.NotificationRecord{{Key: domain.NotificationKey{ID: 9007199254740993, RecordedAt: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}, Kind: domain.NotificationKindAlertCreated, Code: domain.NotificationCodeAlertCreated, Alert: &domain.NotificationAlertTarget{Well: domain.NotificationWell{ID: 9007199254740993}, RunID: 9007199254740995, AlertID: 9007199254740997, Risk: &zero}}}}, nil
}
func (f *notificationHTTPStore) CountUnreadNotifications(context.Context, domain.NotificationScope) (domain.NotificationCount, error) {
	return domain.NotificationCount{Value: 99, Capped: true}, f.err
}
func (f *notificationHTTPStore) MarkNotificationRead(_ context.Context, _ domain.NotificationScope, id int64) (domain.NotificationRead, error) {
	return domain.NotificationRead{ID: id, ReadAt: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}, f.err
}
func (f *notificationHTTPStore) ReadNotificationAlert(_ context.Context, _ domain.NotificationScope, l domain.NotificationAlertLocator) (domain.NotificationAlertContext, error) {
	return domain.NotificationAlertContext{Target: domain.NotificationAlertTarget{Well: domain.NotificationWell{ID: l.WellID}, RunID: l.RunID, AlertID: l.AlertID}, RunProfile: "research", AlertStatus: "unknown"}, f.err
}
func (f *notificationHTTPStore) ReadNotificationSource(_ context.Context, _ domain.NotificationScope, id string) (domain.NotificationSourceOverview, error) {
	f.source = id
	return domain.NotificationSourceOverview{ID: id, Name: strings.Repeat("я", 300), Kind: "arbitrary-secret-canary", Status: "other-secret-canary"}, f.err
}
func notificationHTTPRouter(t *testing.T, f *notificationHTTPStore, enabled bool) *gin.Engine {
	t.Helper()
	svc, e := service.NewNotifications(f, nil, service.NotificationsOptions{AuthMode: "local", Mode: "research", Prefix: "/api/v1", UIEnabled: enabled})
	if e != nil {
		t.Fatal(e)
	}
	s := &Server{Config: config.Config{Prefix: "/api/v1"}, Notifications: svc}
	r := gin.New()
	r.Use(s.notificationsHeaders(), func(c *gin.Context) {
		id := int64(41)
		p := domain.Principal{AccountID: &id, Authenticated: true, Mechanism: "session", Authorization: &domain.AuthorizationSnapshot{AccountID: id, Active: true, Epoch: 1, Permissions: []domain.Permission{"wells.read", "risks.read", "alerts.read", "integrations.read"}}}
		c.Request = c.Request.WithContext(domain.WithPrincipal(c.Request.Context(), p))
		c.Next()
	})
	r.GET("/api/v1/notification-targets/source", s.wrap(s.notificationSource))
	r.GET("/api/v1/notifications", s.wrap(s.listNotifications))
	r.GET("/api/v1/notifications/capabilities", s.wrap(s.notificationCapabilities))
	r.GET("/api/v1/notifications/unread-count", s.wrap(s.notificationUnreadCount))
	r.PUT("/api/v1/notifications/:notification_id/read", s.wrap(s.markNotificationRead))
	r.GET("/api/v1/wells/:well_id/runs/:run_id/alerts/:alert_id", s.wrap(s.notificationAlert))
	return r
}
func TestNotificationRoutesPrivateDTOAndCapabilities(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := &notificationHTTPStore{families: []domain.NotificationFamily{domain.NotificationFamilyAlerts}}
	r := notificationHTTPRouter(t, f, true)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{{"GET", "/api/v1/notifications", "", 200}, {"GET", "/api/v1/notifications?limit=01", "", 422}, {"GET", "/api/v1/notifications?state=", "", 422}, {"GET", "/api/v1/notifications?unknown=1", "", 422}, {"GET", "/api/v1/notifications?state=all&state=all", "", 422}, {"PUT", "/api/v1/notifications/nf1_1/read", "{}", 422}, {"PUT", "/api/v1/notifications/nf1_1/read?account_id=1", "", 422}, {"PUT", "/api/v1/notifications/nf1_1/read", "", 200}, {"GET", "/api/v1/wells/9007199254740993/runs/9007199254740995/alerts/9007199254740997", "", 200}, {"GET", "/api/v1/wells/01/runs/1/alerts/1", "", 422}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
		if tc.path == "/api/v1/notifications" {
			for _, piece := range []string{`"id":"9007199254740993"`, `"run_id":"9007199254740995"`, `"alert_id":"9007199254740997"`, `"risk":0`, `"active":false`, `"read_at":null`, `"window_end":null`} {
				if !strings.Contains(w.Body.String(), piece) {
					t.Fatal(piece, w.Body.String())
				}
			}
		}
	}
	for _, enabled := range []bool{true, false} {
		f.families = nil
		r := notificationHTTPRouter(t, f, enabled)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/notifications/capabilities", nil))
		reason := "no_families"
		status := 403
		if !enabled {
			reason = "feature_off"
			status = 503
		}
		if w.Code != 200 || !strings.Contains(w.Body.String(), reason) {
			t.Fatal(w.Code, w.Body.String())
		}
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/notifications", nil))
		if w.Code != status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	s := &Server{Config: config.Config{Prefix: "/api/v1", APIKey: "required"}}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/notifications", nil))
	if w.Code != 401 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "Требуется вход.") {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestNotificationSafeSourceCanary(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(old)
	f := &notificationHTTPStore{}
	r := notificationHTTPRouter(t, f, true)
	for _, id := range []string{"legacy/a", "literal%2F", "%", "?", "+", "кириллица/a%?+"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/notification-targets/source?source_id="+url.QueryEscape(id), nil))
		var v notificationSourceDTO
		if json.Unmarshal(w.Body.Bytes(), &v) != nil || w.Code != 200 || f.source != id || v.ID != id || len([]rune(v.Name)) != 256 || strings.Contains(w.Body.String(), "canary") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, err := range []error{domain.NewError(domain.Missing, "secret-canary"), errors.New("secret-canary")} {
		f.err = err
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/notification-targets/source?source_id=hidden", nil))
		if (w.Code != 404 && w.Code != 503) || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "canary") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if strings.Contains(logs.String(), "canary") {
		t.Fatal(logs.String())
	}
	for _, raw := range []string{"source_id=%QQ", "source_id=a&source_id=b", "source_id=", "unknown=1"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/notification-targets/source?"+raw, nil))
		if w.Code != 422 {
			t.Fatal(raw, w.Code)
		}
	}
}
func TestNotificationJSONBudget(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	if e := notificationResponse(c, strings.Repeat("x", 256*1024)); e == nil || w.Body.Len() != 0 {
		t.Fatal("partial oversize response")
	}
	if _, e := notificationSource(domain.NotificationSourceOverview{ID: strings.Repeat("x", 4096)}); e == nil {
		t.Fatal("oversize source ID")
	}
}
