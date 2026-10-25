package httpapi

import (
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func notificationSourceURLFits(id string) bool {
	return len("/api/v1/notification-targets/source?source_id="+url.QueryEscape(id)) <= 4096 && len("/integrations/source-overview?source_id="+url.QueryEscape(id)) <= 4096
}
func (s *Server) notificationAlert(c *gin.Context) error {
	if _, e := notificationRequest(c); e != nil {
		return e
	}
	ids := make([]int64, 3)
	for i, key := range []string{"well_id", "run_id", "alert_id"} {
		raw := c.Param(key)
		n, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || n <= 0 || strconv.FormatInt(n, 10) != raw {
			return validation("Некорректный запрос.")
		}
		ids[i] = n
	}
	if e := s.notificationService(); e != nil {
		return e
	}
	v, e := s.Notifications.Alert(c.Request.Context(), domain.NotificationAlertLocator{WellID: ids[0], RunID: ids[1], AlertID: ids[2]})
	if e != nil {
		return e
	}
	out, e := notificationAlertContext(v)
	if e != nil {
		return e
	}
	return notificationResponse(c, out)
}
func (s *Server) notificationSource(c *gin.Context) error {
	q, e := notificationRequest(c, "source_id")
	if e != nil {
		return e
	}
	if !q.Has("source_id") {
		return validation("Некорректный запрос.")
	}
	if e = s.notificationService(); e != nil {
		return e
	}
	v, e := s.Notifications.Source(c.Request.Context(), q.Get("source_id"))
	if e != nil {
		return e
	}
	out, e := notificationSource(v)
	if e != nil {
		return e
	}
	return notificationResponse(c, out)
}
