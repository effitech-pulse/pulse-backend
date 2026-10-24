package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func (s *Server) changeWellArchive(c *gin.Context, archive bool) error {
	n, err := id(c, "well_id")
	if err != nil {
		return err
	}
	var in domain.WellLifecycleInput
	if err = readJSON(c, &in); err != nil {
		return err
	}
	v, err := s.Catalog.SetWellArchive(c.Request.Context(), n, in, archive)
	return respond(c, 200, v, err)
}
func (s *Server) archiveWell(c *gin.Context) error { return s.changeWellArchive(c, true) }
func (s *Server) restoreWell(c *gin.Context) error { return s.changeWellArchive(c, false) }
func (s *Server) wellHistory(c *gin.Context) error {
	n, err := id(c, "well_id")
	if err != nil {
		return err
	}
	if tableQueryRequested(c) {
		raw, size, cursor, err := tableQueryParams(c)
		if err != nil {
			return err
		}
		page, err := s.Catalog.WellHistoryTable(c.Request.Context(), n, raw, size, cursor)
		return respond(c, 200, page, err)
	}
	before, err := reviewBefore(c)
	if err != nil {
		return err
	}
	v, err := s.Catalog.WellHistory(c.Request.Context(), n, before)
	return respond(c, 200, v, err)
}
