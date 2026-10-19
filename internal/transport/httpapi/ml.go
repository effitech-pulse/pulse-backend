package httpapi

import (
	"math"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func (s *Server) mlWells(c *gin.Context) error {
	var min *float64
	if raw := c.Query("min_risk"); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return validation("Некорректный min_risk")
		}
		min = &value
	}
	alertOnly, err := queryBool(c, "alert_only", false)
	if err != nil {
		return err
	}
	if s.ML.IsCohort() {
		out, e := s.ML.CohortWellsFiltered(c.Request.Context(), min, alertOnly)
		return respond(c, 200, out, e)
	}
	out, err := s.ML.Wells(c.Request.Context(), min, alertOnly)
	return respond(c, 200, out, err)
}

func (s *Server) mlByUnitek(c *gin.Context) error {
	id, err := id(c, "unitek_id")
	if err != nil {
		return err
	}
	if s.ML.IsCohort() {
		run, e := selectedRun(c)
		if e != nil {
			return e
		}
		out, e := s.ML.CohortByUnitek(c.Request.Context(), id, run)
		return respond(c, 200, out, e)
	}
	out, err := s.ML.ByUnitek(c.Request.Context(), id)
	return respond(c, 200, out, err)
}

func (s *Server) mlRefresh(c *gin.Context) error {
	started, err := s.ML.StartRun(c.Request.Context(), "manual")
	if err != nil {
		return err
	}
	if !started {
		return &domain.Error{Kind: domain.Duplicate, Message: "Прогон уже выполняется", Plain: true}
	}
	return writeJSON(c, 200, struct {
		Started bool `json:"started"`
	}{true})
}

func (s *Server) mlRuns(c *gin.Context) error {
	count, err := limit(c, 20, 1000)
	if err != nil {
		return err
	}
	out, err := s.ML.Runs(c.Request.Context(), count)
	return respond(c, 200, out, err)
}

func (s *Server) mlAlertStatus(c *gin.Context) error {
	id, err := id(c, "alert_id")
	if err != nil {
		return err
	}
	var input struct {
		Status string `json:"status"`
	}
	if err = readJSON(c, &input); err != nil {
		return err
	}
	if s.Reviews != nil {
		if _, err = s.Reviews.LegacyAlert(c.Request.Context(), strconv.FormatInt(id, 10), input.Status, s.reviewActor(c)); err != nil {
			return err
		}
		return writeJSON(c, 200, struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		}{id, input.Status})
	}
	out, err := s.ML.UpdateAlert(c.Request.Context(), id, input.Status)
	if err != nil {
		return err
	}
	return writeJSON(c, 200, struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}{out.ID, out.Status})
}

func (s *Server) mlExplain(c *gin.Context) error {
	id, err := id(c, "well_id")
	if err != nil {
		return err
	}
	top := int64(6)
	if raw := c.Query("top_n"); raw != "" {
		top, err = strconv.ParseInt(raw, 10, 32)
		if err != nil || top < 1 {
			return validation("Некорректный top_n")
		}
	}
	out, err := s.ML.Explain(c.Request.Context(), id, int32(top))
	return respond(c, 200, out, err)
}

func (s *Server) mlHistory(c *gin.Context) error {
	id, err := id(c, "well_id")
	if err != nil {
		return err
	}
	count, err := limit(c, 400, 2147483647)
	if err != nil {
		return err
	}
	out, err := s.ML.History(c.Request.Context(), id, int32(count))
	return respond(c, 200, out, err)
}

func (s *Server) mlMetrics(c *gin.Context) error {
	out, err := s.ML.Metrics(c.Request.Context())
	return respond(c, 200, out, err)
}

func (s *Server) mlHealth(c *gin.Context) error {
	out, err := s.ML.Health(c.Request.Context())
	return respond(c, 200, out, err)
}

func (s *Server) mlUpload(c *gin.Context) error {
	clean, err := uploadFile(c)
	if err != nil {
		return err
	}
	defer clean()
	header, err := c.FormFile("file")
	if err != nil {
		return validation("Не передан файл")
	}
	file, err := header.Open()
	if err != nil {
		return err
	}
	defer file.Close()
	if header.Size > 50*1024*1024 {
		return &domain.Error{Kind: domain.Invalid, Message: "Файл больше 50 МБ", Plain: true}
	}
	out, err := s.ML.Upload(c.Request.Context(), header.Filename, file)
	return respond(c, 200, out, err)
}

func (s *Server) mlByAppWell(c *gin.Context) error {
	n, err := id(c, "well_id")
	if err != nil {
		return err
	}
	if s.ML.IsCohort() {
		run, e := selectedRun(c)
		if e != nil {
			return e
		}
		v, e := s.ML.CohortWell(c.Request.Context(), n, run)
		return respond(c, 200, v, e)
	}
	v, err := s.ML.ByAppWell(c.Request.Context(), n)
	return respond(c, 200, v, err)
}
func (s *Server) mlHistoryByAppWell(c *gin.Context) error {
	n, err := id(c, "well_id")
	if err != nil {
		return err
	}
	count, err := limit(c, 400, 2147483647)
	if err != nil {
		return err
	}
	if s.ML.IsCohort() {
		run, e := selectedRun(c)
		if e != nil {
			return e
		}
		v, e := s.ML.CohortWell(c.Request.Context(), n, run)
		return respond(c, 200, v, e)
	}
	v, err := s.ML.HistoryByAppWell(c.Request.Context(), n, int32(count))
	return respond(c, 200, v, err)
}

func selectedRun(c *gin.Context) (*int64, error) {
	if raw := c.Query("run_id"); raw != "" {
		v, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || v < 1 {
			return nil, validation("Некорректный run_id")
		}
		return &v, nil
	}
	return nil, nil
}
func (s *Server) mlRunSnapshot(c *gin.Context) error {
	if !s.ML.IsCohort() {
		return domain.NewError(domain.Missing, "Снимок когорты недоступен")
	}
	v, e := id(c, "run_id")
	if e != nil {
		return e
	}
	out, e := s.ML.CohortWells(c.Request.Context(), &v)
	return respond(c, 200, out, e)
}
