package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type cohortJobStatusDTO struct {
	ID               string                `json:"id"`
	RunID            string                `json:"run_id"`
	State            domain.CohortJobState `json:"state"`
	Revision         int64                 `json:"revision"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
	DeadlineAt       time.Time             `json:"deadline_at"`
	StartedAt        *time.Time            `json:"started_at"`
	FinishedAt       *time.Time            `json:"finished_at"`
	NextAttemptAt    *time.Time            `json:"next_attempt_at"`
	ErrorCode        *string               `json:"error_code"`
	Attempt          int                   `json:"attempt"`
	MaxAttempts      int                   `json:"max_attempts"`
	CompletedCount   int                   `json:"completed_count"`
	TotalCount       int                   `json:"total_count"`
	Counts           domain.CohortCoverage `json:"counts"`
	AsOf             time.Time             `json:"as_of"`
	CohortVersion    string                `json:"cohort_version"`
	StatusURL        string                `json:"status_url"`
	ResultURL        *string               `json:"result_url"`
	CancelURL        *string               `json:"cancel_url"`
	RedriveAvailable bool                  `json:"redrive_available"`
}

func (s *Server) cohortJobDTO(v domain.CohortJobStatus) cohortJobStatusDTO {
	url := s.Config.Prefix + "/ml/cohort-jobs/" + v.ID
	out := cohortJobStatusDTO{ID: v.ID, RunID: strconv.FormatInt(v.RunID, 10), State: v.State, Revision: v.Revision, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, DeadlineAt: v.DeadlineAt, StartedAt: v.StartedAt, FinishedAt: v.FinishedAt, NextAttemptAt: v.NextAttemptAt, ErrorCode: v.ErrorCode, Attempt: v.AttemptCount, MaxAttempts: 3, CompletedCount: v.CompletedCount, TotalCount: 100, Counts: v.Counts, AsOf: v.AsOf, CohortVersion: v.CohortVersion, StatusURL: url}
	if v.State == domain.CohortJobSucceeded {
		result := s.Config.Prefix + "/ml/runs/" + out.RunID + "/snapshot"
		out.ResultURL = &result
	}
	if !v.State.Terminal() {
		cancel := url + "/cancel"
		out.CancelURL = &cancel
	}
	return out
}
func (s *Server) cohortJobReady(c *gin.Context) error {
	c.Header("Cache-Control", "no-store")
	p, ok := domain.CurrentPrincipal(c.Request.Context())
	if !ok || !p.Authenticated || p.Mechanism != "session" || p.AccountID == nil || *p.AccountID <= 0 {
		return domain.NewError(domain.Unauthorized, "session_required")
	}
	if p.Role != "admin" {
		return domain.NewError(domain.Forbidden, "cohort_job_forbidden")
	}
	if c.Request.Method != "GET" && !s.allowedOrigin(c) {
		return domain.NewError(domain.Forbidden, "origin_forbidden")
	}
	if s.Config.CohortJobMode != "synthetic" || s.CohortJobs == nil {
		return domain.NewError(domain.Unavailable, "cohort_jobs_disabled")
	}
	return nil
}
func readCohortJobJSON(c *gin.Context, dest any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(dest) != nil {
		return domain.NewError(domain.Invalid, "invalid_cohort_job_request")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return domain.NewError(domain.Invalid, "invalid_cohort_job_request")
	}
	return nil
}

func writeCohortJobError(c *gin.Context, err error) error {
	var de *domain.Error
	if errors.As(err, &de) {
		if de.Kind == domain.Invalid {
			return writeJSON(c, 400, struct {
				Code    domain.Kind `json:"code"`
				Message string      `json:"message"`
			}{de.Kind, de.Message})
		}
		if de.Kind == domain.Limited {
			c.Header("Retry-After", "10")
		}
	}
	return err
}
func (s *Server) submitCohortJob(c *gin.Context) error {
	if err := s.cohortJobReady(c); err != nil {
		return err
	}
	var input struct {
		FixtureID string `json:"fixture_id"`
	}
	if err := readCohortJobJSON(c, &input); err != nil {
		return writeCohortJobError(c, err)
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	status, err := s.CohortJobs.Submit(ctx, c.GetHeader("Idempotency-Key"), input.FixtureID)
	if err != nil {
		return writeCohortJobError(c, err)
	}
	dto := s.cohortJobDTO(status)
	c.Header("Location", dto.StatusURL)
	c.Header("Retry-After", "2")
	return writeJSON(c, 202, dto)
}
func (s *Server) getCohortJob(c *gin.Context) error {
	if err := s.cohortJobReady(c); err != nil {
		return err
	}
	status, err := s.CohortJobs.Get(c.Request.Context(), c.Param("job_id"))
	if err != nil {
		return writeCohortJobError(c, err)
	}
	return writeJSON(c, 200, s.cohortJobDTO(status))
}
func (s *Server) cancelCohortJob(c *gin.Context) error {
	if err := s.cohortJobReady(c); err != nil {
		return err
	}
	var input struct {
		Revision int64 `json:"revision"`
	}
	if err := readCohortJobJSON(c, &input); err != nil {
		return writeCohortJobError(c, err)
	}
	status, err := s.CohortJobs.Cancel(c.Request.Context(), c.Param("job_id"), input.Revision)
	if err != nil {
		var de *domain.Error
		if errors.As(err, &de) && de.Kind == domain.Duplicate && status.ID != "" {
			return writeJSON(c, 409, struct {
				Code string             `json:"code"`
				Job  cohortJobStatusDTO `json:"job"`
			}{de.Message, s.cohortJobDTO(status)})
		}
		return writeCohortJobError(c, err)
	}
	return writeJSON(c, 200, s.cohortJobDTO(status))
}
