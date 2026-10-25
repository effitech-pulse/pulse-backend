package httpapi

import (
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/gin-gonic/gin"
)

func (s *Server) manualChecks(c *gin.Context) error {
	well, err := s.appTelemetryWell(c)
	if err != nil {
		return err
	}
	if tableQueryRequested(c) {
		raw, size, cursor, err := tableQueryParams(c)
		if err != nil {
			return err
		}
		page, err := s.Checks.ListTable(c.Request.Context(), well.ID, raw, size, cursor)
		return respond(c, 200, page, err)
	}
	v, err := s.Checks.List(c.Request.Context(), well.ID, c.Query("before"))
	return respond(c, 200, v, err)
}
func (s *Server) manualCheck(c *gin.Context) error {
	well, err := s.appTelemetryWell(c)
	if err != nil {
		return err
	}
	v, err := s.Checks.Get(c.Request.Context(), well.ID, c.Param("check_id"))
	return respond(c, 200, v, err)
}
func (s *Server) createManualCheck(c *gin.Context) error {
	well, err := s.appTelemetryWell(c)
	if err != nil {
		return err
	}
	var in domain.CheckInput
	if err = readJSON(c, &in); err != nil {
		return err
	}
	v, err := s.Checks.Create(c.Request.Context(), well, in)
	return respond(c, 200, v, err)
}
