package httpapi

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func dailyFlowID(c *gin.Context) (int64, error) {
	raw := c.Param("well_id")
	n, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || n <= 0 || strconv.FormatInt(n, 10) != raw {
		return 0, validation("daily_flow_invalid_query")
	}
	return n, nil
}
func dailyFlowQuery(c *gin.Context, export bool) (domain.DailyFlowQuery, error) {
	bad := validation("daily_flow_invalid_query")
	q, e := url.ParseQuery(c.Request.URL.RawQuery)
	if e != nil {
		return domain.DailyFlowQuery{}, bad
	}
	for k, v := range q {
		if len(v) != 1 || v[0] == "" {
			return domain.DailyFlowQuery{}, bad
		}
		switch k {
		case "start_date", "end_date", "dataset_version":
		case "view_id":
			if !export {
				return domain.DailyFlowQuery{}, bad
			}
		default:
			return domain.DailyFlowQuery{}, bad
		}
	}
	out := domain.DailyFlowQuery{StartDate: q.Get("start_date"), EndDate: q.Get("end_date"), DatasetVersion: q.Get("dataset_version"), ViewID: q.Get("view_id")}
	if (out.StartDate == "") != (out.EndDate == "") || export && (out.StartDate == "" || out.EndDate == "" || out.DatasetVersion == "" || out.ViewID == "") {
		return out, bad
	}
	return out, nil
}
func dailyFlowError(c *gin.Context, e error) error {
	var de *domain.Error
	if errors.As(e, &de) {
		status := 503
		switch de.Kind {
		case domain.Invalid:
			status = 422
		case domain.Missing:
			status = 404
		case domain.Forbidden:
			status = 403
		case domain.Unauthorized:
			status = 401
		case domain.Duplicate:
			status = 409
		case domain.Limited:
			status = 429
		}
		return writeJSON(c, status, struct {
			Code    domain.Kind `json:"code"`
			Message string      `json:"message"`
			Detail  string      `json:"detail"`
		}{de.Kind, de.Message, de.Message})
	}
	return e
}
func (s *Server) dailyFlowRead(c *gin.Context) error {
	c.Header("Cache-Control", "private, no-store")
	id, e := dailyFlowID(c)
	if e != nil {
		return dailyFlowError(c, e)
	}
	q, e := dailyFlowQuery(c, false)
	if e != nil {
		return dailyFlowError(c, e)
	}
	if s.DailyFlow == nil {
		return dailyFlowError(c, domain.NewError(domain.Unavailable, "daily_flow_unavailable"))
	}
	v, e := s.DailyFlow.Read(c.Request.Context(), id, q)
	if e != nil {
		return dailyFlowError(c, e)
	}
	var dto any = dailyFlowDisabledDTO{Enabled: false, Reason: "disabled"}
	if v.Enabled {
		dto = v.View
	}
	raw, e := json.Marshal(dto)
	if e != nil || len(raw)+1 > service.DailyFlowJSONLimit {
		return dailyFlowError(c, domain.NewError(domain.Unavailable, "daily_flow_unavailable"))
	}
	c.Data(200, "application/json", append(raw, '\n'))
	return nil
}
func (s *Server) dailyFlowExport(c *gin.Context) error {
	c.Header("Cache-Control", "private, no-store")
	id, e := dailyFlowID(c)
	if e != nil {
		return dailyFlowError(c, e)
	}
	q, e := dailyFlowQuery(c, true)
	if e != nil {
		return dailyFlowError(c, e)
	}
	if s.DailyFlow == nil {
		return dailyFlowError(c, domain.NewError(domain.Unavailable, "daily_flow_unavailable"))
	}
	v, e := s.DailyFlow.Export(c.Request.Context(), id, q)
	if e != nil {
		return dailyFlowError(c, e)
	}
	c.Header("Content-Disposition", `attachment; filename="`+v.Filename+`"`)
	c.Data(200, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", v.Bytes)
	return nil
}

func (s *Server) sessionCacheControl(c *gin.Context) {
	value := "no-store"
	if c.FullPath() == s.Config.Prefix+"/wells/:well_id/daily-flow" || c.FullPath() == s.Config.Prefix+"/wells/:well_id/daily-flow/export" {
		value = "private, no-store"
	}
	c.Header("Cache-Control", value)
}
