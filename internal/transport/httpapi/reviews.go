package httpapi

import (
	"strconv"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/gin-gonic/gin"
)

func (s *Server) reviewActor(c *gin.Context) domain.ReviewActor {
	if p, ok := domain.CurrentPrincipal(c.Request.Context()); ok && p.AccountID != nil {
		return domain.ReviewActor{ID: "user:" + strconv.FormatInt(*p.AccountID, 10), Name: p.Name, Authenticated: p.Authenticated}
	}
	name := "Локальный сеанс"
	if s.Config.APIKey != "" {
		name = "Общий API-ключ"
	}
	return domain.ReviewActor{ID: "shared", Name: name, Authenticated: false}
}
func reviewBefore(c *gin.Context) (int64, error) {
	if c.Query("before") == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(c.Query("before"), 10, 64)
	if err != nil || n < 1 {
		return 0, validation("Некорректная версия истории")
	}
	return n, nil
}
func (s *Server) checkReview(c *gin.Context) error {
	well, err := s.appTelemetryWell(c)
	if err != nil {
		return err
	}
	before, err := reviewBefore(c)
	if err != nil {
		return err
	}
	v, err := s.Reviews.Get(c.Request.Context(), "check", c.Param("check_id"), &well.ID, before)
	return respond(c, 200, v, err)
}
func (s *Server) updateCheckReview(c *gin.Context) error {
	well, err := s.appTelemetryWell(c)
	if err != nil {
		return err
	}
	var in domain.ReviewInput
	if err = readJSON(c, &in); err != nil {
		return err
	}
	v, err := s.Reviews.Update(c.Request.Context(), "check", c.Param("check_id"), &well.ID, in, s.reviewActor(c))
	return respond(c, 200, v, err)
}
func (s *Server) alertReview(c *gin.Context) error {
	n, err := id(c, "alert_id")
	if err != nil {
		return err
	}
	before, err := reviewBefore(c)
	if err != nil {
		return err
	}
	v, err := s.Reviews.Get(c.Request.Context(), "alert", strconv.FormatInt(n, 10), nil, before)
	return respond(c, 200, v, err)
}
func (s *Server) updateAlertReview(c *gin.Context) error {
	n, err := id(c, "alert_id")
	if err != nil {
		return err
	}
	var in domain.ReviewInput
	if err = readJSON(c, &in); err != nil {
		return err
	}
	v, err := s.Reviews.Update(c.Request.Context(), "alert", strconv.FormatInt(n, 10), nil, in, s.reviewActor(c))
	return respond(c, 200, v, err)
}
