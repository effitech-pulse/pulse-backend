package httpapi

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func parseEventQuery(q url.Values) (domain.EventFilter, string, error) {
	f := domain.EventFilter{Limit: 25, Archive: "all"}
	bad := func() (domain.EventFilter, string, error) {
		return f, "", validation("Некорректный запрос.")
	}
	for k, v := range q {
		if len(v) != 1 || v[0] == "" {
			return bad()
		}
		switch k {
		case "limit", "types", "well_id", "from", "to", "archive", "cursor":
		default:
			return bad()
		}
	}
	if v, ok := q["limit"]; ok {
		n, e := strconv.Atoi(v[0])
		if e != nil || n < 1 || n > 100 {
			return bad()
		}
		f.Limit = n
	}
	if v, ok := q["well_id"]; ok {
		n, e := strconv.ParseInt(v[0], 10, 64)
		if e != nil || n <= 0 || strconv.FormatInt(n, 10) != v[0] {
			return bad()
		}
		f.WellID = &n
	}
	if v, ok := q["types"]; ok {
		f.Types = strings.Split(v[0], ",")
		seen := map[string]bool{}
		for _, typ := range f.Types {
			found := false
			for _, allowed := range service.EventTypes {
				if typ == allowed {
					found = true
				}
			}
			if !found || seen[typ] {
				return bad()
			}
			seen[typ] = true
		}
	}
	for _, entry := range []struct {
		key string
		dst **time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		if v, ok := q[entry.key]; ok {
			raw := v[0]
			if i := strings.IndexByte(raw, '.'); i >= 0 {
				n := 0
				for _, c := range raw[i+1:] {
					if c < '0' || c > '9' {
						break
					}
					n++
				}
				if n > 6 {
					return bad()
				}
			}
			t, e := time.Parse(time.RFC3339Nano, raw)
			if e != nil || strings.Contains(raw, ",") {
				return bad()
			}
			t = t.UTC()
			*entry.dst = &t
		}
	}
	if f.From != nil && f.To != nil && !f.From.Before(*f.To) {
		return bad()
	}
	if v, ok := q["archive"]; ok {
		f.Archive = v[0]
		if f.Archive != "all" && f.Archive != "current" && f.Archive != "archived" {
			return bad()
		}
	}
	return f, q.Get("cursor"), nil
}
func (s *Server) eventsHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		base := s.Config.Prefix + "/events"
		if path == base || strings.HasPrefix(path, base+"/") {
			c.Header("Cache-Control", "no-store")
		}
		c.Next()
	}
}
func (s *Server) eventWrap(fn handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if e := fn(c); e != nil {
			writeEventError(c, e)
			c.Abort()
		}
	}
}
func writeEventError(c *gin.Context, err error) {
	var eventErr *domain.EventReadError
	if !errors.As(err, &eventErr) || eventErr.Detail != "cursor_restart_required" {
		writeError(c, err)
		return
	}
	_ = writeJSON(c, 422, struct {
		Code    domain.Kind `json:"code"`
		Message string      `json:"message"`
		Detail  string      `json:"detail"`
	}{domain.Invalid, "История изменилась. Обновите список.", "cursor_restart_required"})
}
func eventResponse(c *gin.Context, v any) error {
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(raw)+1 > 16*1024*1024 {
		return domain.NewError(domain.Unavailable, "История временно недоступна.")
	}
	c.Data(200, "application/json", append(raw, '\n'))
	return nil
}
func eventRequestTarget(c *gin.Context) error {
	if !service.EventURLFits(c.Request.URL.EscapedPath() + func() string {
		if c.Request.URL.RawQuery != "" {
			return "?" + c.Request.URL.RawQuery
		}
		return ""
	}()) {
		return validation("Некорректный запрос.")
	}
	return nil
}
func (s *Server) listEvents(c *gin.Context) error {
	if e := eventRequestTarget(c); e != nil {
		return e
	}
	q, e := url.ParseQuery(c.Request.URL.RawQuery)
	if e != nil {
		return validation("Некорректный запрос.")
	}
	f, cursor, e := parseEventQuery(q)
	if e != nil {
		return e
	}
	if s.Events == nil {
		return domain.NewError(domain.Unavailable, "История временно недоступна.")
	}
	v, e := s.Events.List(c.Request.Context(), f, cursor)
	if e != nil {
		return e
	}
	out := eventPageDTO{Items: make([]eventSummaryDTO, 0, len(v.Items)), HasMore: v.HasMore, NextCursor: v.NextCursor}
	for _, item := range v.Items {
		out.Items = append(out.Items, toEventSummaryDTO(item))
	}
	return eventResponse(c, out)
}
func (s *Server) getEvent(c *gin.Context) error {
	if e := eventRequestTarget(c); e != nil {
		return e
	}
	if c.Request.URL.RawQuery != "" {
		return validation("Некорректный запрос.")
	}
	if s.Events == nil {
		return domain.NewError(domain.Unavailable, "История временно недоступна.")
	}
	v, e := s.Events.Get(c.Request.Context(), c.Param("event_id"))
	if e != nil {
		return e
	}
	out, e := toEventDetailDTO(v)
	if e != nil {
		return e
	}
	return eventResponse(c, out)
}
