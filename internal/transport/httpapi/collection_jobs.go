package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type collectionJobStatusDTO struct {
	domain.CollectionJobStatus
	Start      *time.Time `json:"start"`
	End        *time.Time `json:"end"`
	AcceptedAt *time.Time `json:"accepted_at"`
	Deadline   *time.Time `json:"deadline"`
}

func collectionTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}
func collectionJobDTO(v domain.CollectionJobStatus) collectionJobStatusDTO {
	return collectionJobStatusDTO{v, collectionTime(v.Start), collectionTime(v.End), collectionTime(v.AcceptedAt), collectionTime(v.Deadline)}
}
func (s *Server) collectionJobReady(c *gin.Context) error {
	c.Header("Cache-Control", "no-store")
	p, ok := domain.CurrentPrincipal(c.Request.Context())
	if !ok || !p.Authenticated || p.Mechanism != "session" || p.AccountID == nil || *p.AccountID <= 0 {
		return domain.NewError(domain.Unauthorized, "session_required")
	}
	if p.Role != "admin" {
		return domain.NewError(domain.Forbidden, "collection_access_denied")
	}
	if c.Request.Method != "GET" && !s.allowedOrigin(c) {
		return domain.NewError(domain.Forbidden, "collection_access_denied")
	}
	if s.CollectionJobs == nil || (c.Request.Method != "GET" && s.Config.CollectionJobMode != "synthetic") {
		return domain.NewError(domain.Unavailable, "collection_jobs_disabled")
	}
	return nil
}
func readCollectionJSON(c *gin.Context, dest any, limit int64) error {
	invalid := func() error { return domain.NewError(domain.Invalid, "invalid_collection_request") }
	body, e := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, limit))
	if e != nil {
		return invalid()
	}
	// Request DTOs are flat objects: duplicate fields must not select a winner.
	check := json.NewDecoder(bytes.NewReader(body))
	tok, e := check.Token()
	if e != nil || tok != json.Delim('{') {
		return invalid()
	}
	keys := map[string]bool{}
	for check.More() {
		tok, e = check.Token()
		if e != nil {
			return invalid()
		}
		key, ok := tok.(string)
		if !ok || keys[key] {
			return invalid()
		}
		keys[key] = true
		var value json.RawMessage
		if check.Decode(&value) != nil {
			return invalid()
		}
	}
	if _, e = check.Token(); e != nil {
		return invalid()
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(dest) != nil {
		return invalid()
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return invalid()
	}
	return nil
}
func writeCollectionError(c *gin.Context, e error) error {
	var de *domain.Error
	if !errors.As(e, &de) {
		return domain.NewError(domain.Unavailable, "collection_store_unavailable")
	}
	// Collection errors expose only fixed protocol codes, never adapter messages.
	switch de.Message {
	case "invalid_collection_request", "invalid_period", "fixture_unavailable", "source_not_allowed", "session_required", "collection_access_denied", "collection_job_missing", "replay_conflict", "source_busy", "revision_conflict", "collection_capacity", "collection_jobs_disabled", "collection_store_unavailable":
	default:
		return domain.NewError(domain.Unavailable, "collection_store_unavailable")
	}
	if de.Kind == domain.Invalid {
		return writeJSON(c, 400, struct {
			Code    domain.Kind `json:"code"`
			Message string      `json:"message"`
			Detail  any         `json:"detail"`
		}{de.Kind, de.Message, nil})
	}
	return domain.NewError(de.Kind, de.Message)
}
func (s *Server) submitCollectionJob(c *gin.Context) error {
	if e := s.collectionJobReady(c); e != nil {
		return e
	}
	var input domain.CollectionSubmit
	if e := readCollectionJSON(c, &input, 4096); e != nil {
		return writeCollectionError(c, e)
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	status, e := s.CollectionJobs.Submit(ctx, input)
	if e != nil {
		return writeCollectionError(c, e)
	}
	c.Header("Location", s.Config.Prefix+"/collection-jobs/"+status.ID)
	return writeJSON(c, 202, collectionJobDTO(status))
}
func (s *Server) getCollectionJob(c *gin.Context) error {
	if e := s.collectionJobReady(c); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	status, e := s.CollectionJobs.Status(ctx, c.Param("job_id"))
	if e != nil {
		return writeCollectionError(c, e)
	}
	return writeJSON(c, 200, collectionJobDTO(status))
}
func (s *Server) cancelCollectionJob(c *gin.Context) error {
	if e := s.collectionJobReady(c); e != nil {
		return e
	}
	var input domain.CollectionRevision
	if e := readCollectionJSON(c, &input, 128); e != nil {
		return writeCollectionError(c, e)
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	status, e := s.CollectionJobs.Cancel(ctx, c.Param("job_id"), input.Revision)
	if e != nil {
		return writeCollectionError(c, e)
	}
	return writeJSON(c, 200, collectionJobDTO(status))
}
