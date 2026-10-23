package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type fileJobStatusDTO struct {
	ID            string                         `json:"id"`
	WellID        string                         `json:"well_id"`
	State         domain.FileJobState            `json:"state"`
	Revision      int64                          `json:"revision"`
	CreatedAt     time.Time                      `json:"created_at"`
	UpdatedAt     time.Time                      `json:"updated_at"`
	DeadlineAt    time.Time                      `json:"deadline_at"`
	StartedAt     *time.Time                     `json:"started_at"`
	FinishedAt    *time.Time                     `json:"finished_at"`
	NextAttemptAt *time.Time                     `json:"next_attempt_at"`
	Attempt       int                            `json:"attempt"`
	MaxAttempts   int                            `json:"max_attempts"`
	ErrorCode     *string                        `json:"error_code"`
	StatusURL     string                         `json:"status_url"`
	ResultURL     *string                        `json:"result_url"`
	CancelURL     *string                        `json:"cancel_url"`
	CheckID       *string                        `json:"check_id"`
	Input         domain.FileJobInputSummary     `json:"input"`
	Execution     domain.FileJobExecutionSummary `json:"execution"`
}

func (s *Server) fileJobDTO(v domain.FileJobStatus) fileJobStatusDTO {
	url := s.Config.Prefix + "/wells/" + strconv.FormatInt(v.WellID, 10) + "/file-check-jobs/" + v.ID
	out := fileJobStatusDTO{ID: v.ID, WellID: strconv.FormatInt(v.WellID, 10), State: v.State, Revision: v.Revision, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, DeadlineAt: v.DeadlineAt, StartedAt: v.StartedAt, FinishedAt: v.FinishedAt, NextAttemptAt: v.NextAttemptAt, Attempt: v.AttemptCount, MaxAttempts: 3, ErrorCode: v.ErrorCode, StatusURL: url, Input: v.Input, Execution: v.Execution}
	if v.State == domain.FileJobSucceeded {
		u := url + "/result"
		out.ResultURL = &u
		out.CheckID = v.CheckID
	}
	if !v.State.Terminal() {
		u := url + "/cancel"
		out.CancelURL = &u
	}
	return out
}
func fileJobSession(c *gin.Context) error {
	c.Header("Cache-Control", "no-store")
	p, ok := domain.CurrentPrincipal(c.Request.Context())
	if !ok || !p.Authenticated || p.Mechanism != "session" || p.AccountID == nil || *p.AccountID <= 0 {
		return domain.NewError(domain.Unauthorized, "session_required")
	}
	permission := domain.Permission("filechecks.read")
	if c.Request.Method != "GET" {
		permission = "filechecks.run"
	}
	if strings.HasSuffix(c.Request.URL.Path, "/cancel") {
		permission = "filechecks.cancel"
	}
	if c.Request.Method == "PUT" && strings.HasSuffix(c.Request.URL.Path, "/review") {
		permission = "filechecks.review"
	}
	if strings.HasSuffix(c.Request.URL.Path, "/file-check-capabilities") {
		if p.Authorization == nil {
			return domain.NewError(domain.Forbidden, "file_check_forbidden")
		}
		return p.Authorization.Require()
	}
	if p.Authorization == nil || p.Authorization.Require(permission) != nil {
		return domain.NewError(domain.Forbidden, "file_check_forbidden")
	}
	return nil
}
func (s *Server) fileJobReady(c *gin.Context) error {
	if e := fileJobSession(c); e != nil {
		return e
	}
	if c.Request.Method != "GET" && !s.allowedOrigin(c) {
		return domain.NewError(domain.Forbidden, "origin_forbidden")
	}
	if s.Config.FileCheckMode != "synthetic" || s.FileJobs == nil {
		return domain.NewError(domain.Unavailable, "file_checks_disabled")
	}
	return nil
}
func (s *Server) fileCheckCapabilities(c *gin.Context) error {
	if e := fileJobSession(c); e != nil {
		return e
	}
	enabled := s.Config.FileCheckMode == "synthetic" && s.FileJobs != nil && s.FileJobs.Eligible(c.Request.Context())
	if !enabled {
		return writeJSON(c, 200, struct {
			Enabled bool   `json:"enabled"`
			Mode    string `json:"mode"`
		}{false, "off"})
	}
	return writeJSON(c, 200, struct {
		Enabled     bool     `json:"enabled"`
		Mode        string   `json:"mode"`
		Formats     []string `json:"formats"`
		MaxRawBytes int      `json:"max_raw_bytes"`
		MaxRows     int      `json:"max_rows"`
		MaxWells    int      `json:"max_wells"`
	}{true, "synthetic", []string{"csv", "xlsx"}, 20 << 20, 100000, 1})
}
func fileJobWell(c *gin.Context) (int64, error) {
	v, e := id(c, "well_id")
	if e != nil || v <= 0 {
		return 0, validation("invalid_well_id")
	}
	return v, nil
}
func writeFileJobError(c *gin.Context, e error) error {
	if e == nil {
		return nil
	}
	var problem *domain.FileJobProblem
	if errors.As(e, &problem) {
		status := 0
		switch problem.Code {
		case "body_limit", "raw_limit":
			status = 413
		case "result_cancelled", "result_expired":
			status = 410
		}
		if status != 0 {
			var de *domain.Error
			_ = errors.As(e, &de)
			code := domain.Invalid
			if status == 410 {
				code = domain.Missing
			}
			_ = writeJSON(c, status, struct {
				Code    domain.Kind `json:"code"`
				Message string      `json:"message"`
				Detail  any         `json:"detail"`
			}{Code: code, Message: problem.Code})
			return nil
		}
	}
	var de *domain.Error
	if errors.As(e, &de) {
		if de.Kind == domain.Limited {
			c.Header("Retry-After", "10")
		}
		if de.Message == "result_not_ready" {
			c.Header("Retry-After", "2")
		}
	}
	return e
}
func readFileJobMultipart(c *gin.Context) (string, domain.FileParseSpec, []byte, error) {
	var spec domain.FileParseSpec
	bad := func() (string, domain.FileParseSpec, []byte, error) {
		return "", spec, nil, validation("invalid_file_job_multipart")
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 21<<20)
	media, _, e := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if e != nil || media != "multipart/form-data" {
		return bad()
	}
	reader, e := c.Request.MultipartReader()
	if e != nil {
		return bad()
	}
	seen := map[string]bool{}
	key := ""
	var raw []byte
	for {
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			var max *http.MaxBytesError
			if errors.As(e, &max) {
				return "", spec, nil, domain.NewFileJobProblem(domain.Invalid, "body_limit")
			}
			return bad()
		}
		name := part.FormName()
		if seen[name] || name != "request_id" && name != "parse_spec" && name != "file" {
			return bad()
		}
		seen[name] = true
		cap := int64(65536)
		if name == "request_id" {
			cap = 80
		}
		if name == "file" {
			cap = 20 << 20
		} else if part.FileName() != "" {
			return bad()
		}
		value, e := io.ReadAll(io.LimitReader(part, cap+1))
		_ = part.Close()
		if e != nil {
			var max *http.MaxBytesError
			if errors.As(e, &max) {
				return "", spec, nil, domain.NewFileJobProblem(domain.Invalid, "body_limit")
			}
			return bad()
		}
		if int64(len(value)) > cap {
			if name == "file" {
				return "", spec, nil, domain.NewFileJobProblem(domain.Invalid, "raw_limit")
			}
			return bad()
		}
		switch name {
		case "request_id":
			key = string(value)
		case "file":
			raw = value
		case "parse_spec":
			var fields map[string]json.RawMessage
			if json.Unmarshal(value, &fields) != nil {
				return bad()
			}
			if _, exists := fields["schema_sha256"]; exists {
				return bad()
			}
			decoder := json.NewDecoder(bytes.NewReader(value))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&spec) != nil {
				return bad()
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF || spec.SchemaSHA256 != "" {
				return bad()
			}
		}
	}
	if len(seen) != 3 || len(raw) == 0 || len(key) < 16 {
		return bad()
	}
	return key, spec, raw, nil
}
func (s *Server) submitFileJob(c *gin.Context) error {
	if e := s.fileJobReady(c); e != nil {
		return e
	}
	well, e := fileJobWell(c)
	if e != nil {
		return e
	}
	key, spec, raw, e := readFileJobMultipart(c)
	if e != nil {
		return writeFileJobError(c, e)
	}
	ctx, cancel := contextWithFileJobAdmission(c)
	defer cancel()
	v, e := s.FileJobs.Submit(ctx, well, key, spec, bytes.NewReader(raw))
	if e != nil {
		return writeFileJobError(c, e)
	}
	out := s.fileJobDTO(v)
	c.Header("Location", out.StatusURL)
	c.Header("Retry-After", "2")
	return writeJSON(c, 202, out)
}
func (s *Server) getFileJob(c *gin.Context) error {
	if e := s.fileJobReady(c); e != nil {
		return e
	}
	well, e := fileJobWell(c)
	if e != nil {
		return e
	}
	v, e := s.FileJobs.Get(c.Request.Context(), well, c.Param("job_id"))
	if e != nil {
		return writeFileJobError(c, e)
	}
	return writeJSON(c, 200, s.fileJobDTO(v))
}
func (s *Server) listFileJobs(c *gin.Context) error {
	if e := s.fileJobReady(c); e != nil {
		return e
	}
	well, e := fileJobWell(c)
	if e != nil {
		return e
	}
	v, e := s.FileJobs.List(c.Request.Context(), well, c.Query("before"))
	if e != nil {
		return writeFileJobError(c, e)
	}
	items := make([]fileJobStatusDTO, 0, len(v.Items))
	for _, job := range v.Items {
		items = append(items, s.fileJobDTO(job))
	}
	return writeJSON(c, 200, struct {
		Items      []fileJobStatusDTO `json:"items"`
		NextBefore *string            `json:"next_before"`
	}{items, v.NextBefore})
}
func (s *Server) fileJobResult(c *gin.Context) error {
	if e := s.fileJobReady(c); e != nil {
		return e
	}
	well, e := fileJobWell(c)
	if e != nil {
		return e
	}
	v, e := s.FileJobs.Result(c.Request.Context(), well, c.Param("job_id"))
	if e != nil {
		return writeFileJobError(c, e)
	}
	type saved struct {
		ID          string              `json:"id"`
		JobID       string              `json:"job_id"`
		WellID      string              `json:"well_id"`
		SourceKind  string              `json:"source_kind"`
		Start       time.Time           `json:"start"`
		End         time.Time           `json:"end"`
		CreatedAt   time.Time           `json:"created_at"`
		InputSHA256 string              `json:"input_sha256"`
		PointsCount int                 `json:"points_count"`
		Resolution  string              `json:"resolution"`
		Result      domain.UploadResult `json:"result"`
	}
	return writeJSON(c, 200, struct {
		JobID string `json:"job_id"`
		Check saved  `json:"check"`
	}{v.JobID, saved{v.ID, v.JobID, strconv.FormatInt(v.WellID, 10), v.SourceKind, v.Start, v.End, v.CreatedAt, v.InputSHA256, v.PointsCount, v.Resolution, v.Result}})
}
func (s *Server) cancelFileJob(c *gin.Context) error {
	if e := s.fileJobReady(c); e != nil {
		return e
	}
	well, e := fileJobWell(c)
	if e != nil {
		return e
	}
	var in struct {
		Revision int64 `json:"revision"`
	}
	if e = readFileJobJSON(c, &in); e != nil {
		return e
	}
	v, e := s.FileJobs.Cancel(c.Request.Context(), well, c.Param("job_id"), in.Revision)
	if e != nil {
		return writeFileJobError(c, e)
	}
	return writeJSON(c, 200, s.fileJobDTO(v))
}
func readFileJobJSON(c *gin.Context, dest any) error {
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 65536))
	dec.DisallowUnknownFields()
	if dec.Decode(dest) != nil {
		return validation("invalid_file_job_request")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return validation("invalid_file_job_request")
	}
	return nil
}
func (s *Server) fileJobReview(c *gin.Context) error {
	if e := s.fileJobReady(c); e != nil {
		return e
	}
	well, e := fileJobWell(c)
	if e != nil {
		return e
	}
	before, e := reviewBefore(c)
	if e != nil {
		return e
	}
	v, e := s.Reviews.Get(c.Request.Context(), "file_check", c.Param("job_id"), &well, before)
	return respond(c, 200, v, e)
}
func (s *Server) updateFileJobReview(c *gin.Context) error {
	if e := s.fileJobReady(c); e != nil {
		return e
	}
	well, e := fileJobWell(c)
	if e != nil {
		return e
	}
	var in domain.ReviewInput
	if e = readFileJobJSON(c, &in); e != nil {
		return e
	}
	v, e := s.Reviews.Update(c.Request.Context(), "file_check", c.Param("job_id"), &well, in, s.reviewActor(c))
	return respond(c, 200, v, e)
}

func contextWithFileJobAdmission(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), 45*time.Second)
}
