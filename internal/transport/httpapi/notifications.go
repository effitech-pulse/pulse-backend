package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func notificationHTTPUnavailable() error {
	return domain.NewError(domain.Unavailable, "Уведомления временно недоступны.")
}
func notificationHTTPError(err error) error {
	var e *domain.Error
	if errors.As(err, &e) {
		switch e.Kind {
		case domain.Unauthorized:
			return domain.NewError(domain.Unauthorized, "Требуется вход.")
		case domain.Forbidden:
			return domain.NewError(domain.Forbidden, "Недостаточно прав.")
		case domain.Missing:
			return domain.NewError(domain.Missing, "Уведомление недоступно.")
		case domain.Invalid:
			return validation("Некорректный запрос.")
		}
	}
	return notificationHTTPUnavailable()
}
func (s *Server) notificationsHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		p := strings.TrimPrefix(c.Request.URL.Path, s.Config.Prefix)
		if p == "/notifications" || strings.HasPrefix(p, "/notifications/") || p == "/notification-targets/source" || (strings.HasPrefix(p, "/wells/") && strings.Contains(p, "/runs/") && strings.Contains(p, "/alerts/")) {
			c.Set("notification_private", true)
			c.Header("Cache-Control", "no-store")
		}
		c.Next()
	}
}
func parseNotificationQuery(raw string, allowed ...string) (url.Values, error) {
	q, e := url.ParseQuery(raw)
	if e != nil {
		return nil, validation("Некорректный запрос.")
	}
	for k, v := range q {
		if !slices.Contains(allowed, k) || len(v) != 1 || v[0] == "" {
			return nil, validation("Некорректный запрос.")
		}
	}
	return q, nil
}
func notificationRequest(c *gin.Context, allowed ...string) (url.Values, error) {
	if len(c.Request.URL.EscapedPath())+len(c.Request.URL.RawQuery)+1 > 8192 {
		return nil, validation("Некорректный запрос.")
	}
	if c.Request.Body != nil {
		b := make([]byte, 1)
		n, e := c.Request.Body.Read(b)
		if n != 0 || e != io.EOF {
			return nil, validation("Некорректный запрос.")
		}
	}
	return parseNotificationQuery(c.Request.URL.RawQuery, allowed...)
}
func notificationResponse(c *gin.Context, v any) error {
	raw, e := json.Marshal(v)
	if e != nil || len(raw)+1 > 256*1024 {
		return notificationHTTPUnavailable()
	}
	c.Data(200, "application/json", append(raw, '\n'))
	return nil
}
func (s *Server) notificationService() error {
	if s.Notifications == nil {
		return notificationHTTPUnavailable()
	}
	return nil
}
func (s *Server) notificationCapabilities(c *gin.Context) error {
	if _, e := notificationRequest(c); e != nil {
		return e
	}
	if e := s.notificationService(); e != nil {
		return e
	}
	v, e := s.Notifications.Capabilities(c.Request.Context())
	if e != nil {
		return e
	}
	return notificationResponse(c, notificationCapabilitiesDTO{v.Enabled, v.Reason, v.Families})
}
func (s *Server) listNotifications(c *gin.Context) error {
	q, e := notificationRequest(c, "state", "limit", "cursor")
	if e != nil {
		return e
	}
	state := domain.NotificationStateUnread
	if q.Has("state") {
		state = domain.NotificationState(q.Get("state"))
	}
	if state != domain.NotificationStateUnread && state != domain.NotificationStateAll {
		return validation("Некорректный запрос.")
	}
	limit := 25
	if q.Has("limit") {
		limit, e = strconv.Atoi(q.Get("limit"))
		if e != nil || limit < 1 || limit > 100 || strconv.Itoa(limit) != q.Get("limit") {
			return validation("Некорректный запрос.")
		}
	}
	if len(q.Get("cursor")) > 4096 {
		return validation("Некорректный запрос.")
	}
	if e = s.notificationService(); e != nil {
		return e
	}
	v, e := s.Notifications.List(c.Request.Context(), state, limit, q.Get("cursor"))
	if e != nil {
		return e
	}
	out := notificationPageDTO{Items: make([]notificationItemDTO, 0, len(v.Items)), NextCursor: v.NextCursor}
	for _, r := range v.Items {
		item, e := notificationItem(r)
		if e != nil {
			return e
		}
		out.Items = append(out.Items, item)
	}
	return notificationResponse(c, out)
}
func (s *Server) notificationUnreadCount(c *gin.Context) error {
	if _, e := notificationRequest(c); e != nil {
		return e
	}
	if e := s.notificationService(); e != nil {
		return e
	}
	v, e := s.Notifications.UnreadCount(c.Request.Context())
	if e != nil {
		return e
	}
	return notificationResponse(c, notificationCountDTO{v.Value, v.Capped})
}
func (s *Server) markNotificationRead(c *gin.Context) error {
	if _, e := notificationRequest(c); e != nil {
		return e
	}
	if e := s.notificationService(); e != nil {
		return e
	}
	v, e := s.Notifications.MarkRead(c.Request.Context(), c.Param("notification_id"))
	if e != nil {
		return e
	}
	id, e := service.EncodeNotificationID(v.ID)
	if e != nil {
		return notificationHTTPUnavailable()
	}
	return notificationResponse(c, notificationReadDTO{id, notificationTime(v.ReadAt)})
}
