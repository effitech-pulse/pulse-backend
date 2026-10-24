package httpapi

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func (s *Server) listWells(c *gin.Context) error {
	active, err := queryBool(c, "active_only", true)
	if err != nil {
		return err
	}
	filter, err := wellFilter(c)
	if err != nil {
		return err
	}
	out, err := s.Catalog.FilteredWells(c.Request.Context(), active, filter)
	return respond(c, 200, out, err)
}

func (s *Server) getWell(c *gin.Context) error {
	n, err := id(c, "well_id")
	if err != nil {
		return err
	}
	out, err := s.Catalog.Well(c.Request.Context(), n)
	include, e := queryBool(c, "include_archived", false)
	if e != nil {
		return e
	}
	if err == nil && out.ArchivedAt != nil && !include {
		return domain.NewError(domain.Missing, "Скважина в архиве")
	}
	return respond(c, 200, out, err)
}

func (s *Server) createWell(c *gin.Context) error {
	var in domain.WellInput
	if err := readJSON(c, &in); err != nil {
		return err
	}
	out, err := s.Catalog.CreateWell(c.Request.Context(), in)
	return respond(c, 201, out, err)
}

func (s *Server) patchWell(c *gin.Context) error {
	n, err := id(c, "well_id")
	if err != nil {
		return err
	}
	var in domain.WellPatch
	if err = readJSON(c, &in); err != nil {
		return err
	}
	if s.Access.Enabled() && !in.Revision.Present {
		return validation("Для изменения нужна версия карточки")
	}
	out, err := s.Catalog.PatchWell(c.Request.Context(), n, in)
	return respond(c, 200, out, err)
}

func (s *Server) bulkWells(c *gin.Context) error {
	var in []domain.WellInput
	if err := readJSON(c, &in); err != nil {
		return err
	}
	if in == nil {
		return validation("Ожидается массив скважин")
	}
	out, err := s.Catalog.ImportWells(c.Request.Context(), in)
	return respond(c, 200, out, err)
}

func (s *Server) listReference(c *gin.Context) error {
	out, err := s.Catalog.References(c.Request.Context(), s.referenceKind(c))
	return respond(c, 200, out, err)
}

func (s *Server) createReference(c *gin.Context) error {
	var in domain.ReferenceInput
	if err := readJSON(c, &in); err != nil {
		return err
	}
	out, err := s.Catalog.CreateReference(c.Request.Context(), s.referenceKind(c), in)
	return respond(c, 201, out, err)
}

func (s *Server) updateReference(c *gin.Context) error {
	n, err := id(c, "ref_id")
	if err != nil {
		return err
	}
	var in domain.ReferenceInput
	if err = readJSON(c, &in); err != nil {
		return err
	}
	out, err := s.Catalog.UpdateReference(c.Request.Context(), s.referenceKind(c), n, in.Name)
	return respond(c, 200, out, err)
}

func (s *Server) incidents(c *gin.Context) error {
	n, err := id(c, "well_id")
	if err != nil {
		return err
	}
	out, err := s.Catalog.Incidents(c.Request.Context(), n)
	return respond(c, 200, out, err)
}

func (s *Server) createIncident(c *gin.Context) error {
	n, err := id(c, "well_id")
	if err != nil {
		return err
	}
	var in domain.IncidentInput
	if err = readJSON(c, &in); err != nil {
		return err
	}
	out, err := s.Catalog.CreateIncident(c.Request.Context(), n, in)
	return respond(c, 201, out, err)
}

func (s *Server) incidentStats(c *gin.Context) error {
	filter, err := wellFilter(c)
	if err != nil {
		return err
	}
	out, err := s.Catalog.FilteredIncidentStats(c.Request.Context(), filter)
	return respond(c, 200, out, err)
}

func (s *Server) predictions(c *gin.Context) error {
	n, err := id(c, "well_id")
	if err != nil {
		return err
	}
	count, err := limit(c, 100, 1000)
	if err != nil {
		return err
	}
	out, err := s.Catalog.Predictions(c.Request.Context(), n, count)
	return respond(c, 200, out, err)
}

func (s *Server) complications(c *gin.Context) error {
	n, err := id(c, "well_id")
	if err != nil {
		return err
	}
	out, err := s.Catalog.Complications(c.Request.Context(), n)
	return respond(c, 200, out, err)
}

func (s *Server) classification(c *gin.Context) error {
	n, err := id(c, "well_id")
	if err != nil {
		return err
	}
	out, err := s.Catalog.Classification(c.Request.Context(), n)
	return respond(c, 200, out, err)
}

func (s *Server) runs(c *gin.Context) error {
	filter, err := runFilters(c, 100)
	if err != nil {
		return err
	}
	out, err := s.Catalog.Runs(c.Request.Context(), filter)
	return respond(c, 200, out, err)
}

func (s *Server) runEvents(c *gin.Context) error {
	out, err := s.Catalog.RunEvents(c.Request.Context(), c.Param("dag_id"), c.Param("run_id"))
	return respond(c, 200, out, err)
}

func (s *Server) audit(c *gin.Context) error {
	if tableQueryRequested(c) {
		raw, size, cursor, err := tableQueryParams(c)
		if err != nil {
			return err
		}
		page, err := s.Catalog.AuditTable(c.Request.Context(), raw, size, cursor)
		return respond(c, 200, page, err)
	}
	count, err := limit(c, 200, 1000)
	if err != nil {
		return err
	}
	out, err := s.Catalog.Audit(c.Request.Context(), count)
	return respond(c, 200, out, err)
}

func (s *Server) parameters(c *gin.Context) error {
	out, err := s.Parameters.List(c.Request.Context())
	return respond(c, 200, out, err)
}

func (s *Server) deleteWell(c *gin.Context) error {
	n, err := id(c, "well_id")
	if err != nil {
		return err
	}
	if s.Access.Enabled() {
		raw := strings.Trim(c.GetHeader("If-Match"), "\"")
		revision, e := strconv.ParseInt(raw, 10, 64)
		if e != nil {
			return validation("Укажите версию карточки в If-Match")
		}
		_, e = s.Catalog.SetWellArchive(c.Request.Context(), n, domain.WellLifecycleInput{Revision: revision, Reason: "Архивирование через DELETE API"}, true)
		if e != nil {
			return e
		}
		c.Status(204)
		return nil
	}
	if err = s.Catalog.DeleteWell(c.Request.Context(), n); err != nil {
		return err
	}
	c.Status(204)
	return nil
}

func (s *Server) deleteReference(c *gin.Context) error {
	n, err := id(c, "ref_id")
	if err != nil {
		return err
	}
	if err = s.Catalog.DeleteReference(c.Request.Context(), s.referenceKind(c), n); err != nil {
		return err
	}
	c.Status(204)
	return nil
}

func (s *Server) deleteIncident(c *gin.Context) error {
	n, err := id(c, "incident_id")
	if err != nil {
		return err
	}
	if err = s.Catalog.DeleteIncident(c.Request.Context(), n); err != nil {
		return err
	}
	c.Status(204)
	return nil
}

func (s *Server) referenceKind(c *gin.Context) domain.ReferenceKind {
	if strings.HasPrefix(c.Request.URL.Path, s.Config.Prefix+"/ngdu") {
		return domain.NGDUReference
	}
	return domain.DepartmentReference
}

func runFilters(c *gin.Context, defaultLimit int) (domain.RunFilter, error) {
	count, err := limit(c, defaultLimit, 1000)
	if defaultLimit == 5000 {
		count = 5000
		err = nil
	}
	if err != nil {
		return domain.RunFilter{}, err
	}
	filter := domain.RunFilter{Limit: count}
	q := c.Request.URL.Query()
	if v, ok := q["dag_id"]; ok && len(v) > 0 {
		filter.DAGID = &v[0]
	}
	if v, ok := q["status_type"]; ok && len(v) > 0 {
		filter.StatusType = &v[0]
	}
	if v, ok := q["well_id"]; ok && len(v) > 0 {
		n, err := strconv.ParseInt(v[0], 10, 64)
		if err != nil {
			return filter, validation("Некорректный идентификатор")
		}
		filter.WellID = &n
	}
	return filter, nil
}

func (s *Server) analyticsStatus(c *gin.Context) error {
	if s.ML != nil && s.ML.IsCohort() {
		return writeJSON(c, 200, struct {
			Enabled    bool  `json:"enabled"`
			Dashboards []any `json:"dashboards"`
		}{false, []any{}})
	}
	return writeJSON(c, 200, s.Analytics.Status())
}

func (s *Server) analyticsEmbed(c *gin.Context) error {
	if s.ML != nil && s.ML.IsCohort() {
		return domain.NewError(domain.Missing, "Несегментированный дашборд недоступен в пользовательском фонде")
	}
	n, err := id(c, "dashboard_id")
	if err != nil {
		return err
	}
	out, err := s.Analytics.EmbedContext(c.Request.Context(), n)
	return respond(c, 200, out, err)
}
