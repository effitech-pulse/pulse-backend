package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

const passportUploadMax = 2 * 1024 * 1024

var passportHTTPWork = struct {
	sync.Mutex
	actors map[int64]bool
	count  int
}{actors: map[int64]bool{}}

func passportHTTPError(kind domain.Kind, code string) error {
	return domain.NewPassportError(kind, code, code, nil)
}
func writePassportError(c *gin.Context, err error) {
	status, code, message := 503, "equipment_passport_unavailable", "Паспорт временно недоступен"
	var de *domain.Error
	if errors.As(err, &de) {
		code = string(de.Kind)
		message = de.Message
		switch de.Kind {
		case domain.Invalid:
			status = 400
		case domain.Unauthorized:
			status = 401
		case domain.Forbidden:
			status = 403
		case domain.Missing:
			status = 404
		case domain.Duplicate:
			status = 409
		case domain.Limited:
			status = 429
		}
	}
	var detail struct {
		Issues []domain.PassportIssue `json:"issues,omitempty"`
	}
	var pe *domain.PassportError
	if errors.As(err, &pe) {
		code = pe.Code
		detail.Issues = pe.Issues
		switch code {
		case "preview_expired", "preview_consumed":
			status = 410
		case "file_limit_exceeded":
			status = 413
		}
	}
	var max *http.MaxBytesError
	if errors.As(err, &max) {
		status = 413
		code = "file_limit_exceeded"
		message = "Превышен размер файла"
	}
	_ = writeJSON(c, status, struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Detail  any    `json:"detail"`
	}{code, message, detail})
}
func (s *Server) passportWrap(fn handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		p, ok := domain.CurrentPrincipal(c.Request.Context())
		var e error
		if !ok || !p.Authenticated || p.AccountID == nil || p.Mechanism != "session" {
			e = passportHTTPError(domain.Unauthorized, "personal_session_required")
		} else if !domain.RBACEnforced(c.Request.Context()) {
			e = passportHTTPError(domain.Forbidden, "equipment_passports_require_enforce")
		} else if s.EquipmentPassports == nil {
			e = domain.NewError(domain.Unavailable, "equipment_passport_unavailable")
		}
		if e == nil {
			_, cleanup, bodyErr := passportBodyContext(c)
			if bodyErr != nil {
				e = bodyErr
			} else {
				defer cleanup()
				e = fn(c)
			}
		}
		if e != nil {
			writePassportError(c, e)
			c.Abort()
		}
	}
}
func (s *Server) registerEquipmentPassportRoutes(api *gin.RouterGroup) {
	api.GET("/wells/:well_id/equipment-passport", s.passportWrap(s.passportRead))
	api.GET("/wells/:well_id/equipment-passport/history", s.passportWrap(s.passportHistory))
	api.POST("/equipment-passports/previews", s.passportWrap(s.passportPrepare))
	api.POST("/equipment-passports/import/previews", s.passportWrap(s.passportImport))
	api.GET("/equipment-passports/previews/:preview_id", s.passportWrap(s.passportPreview))
	api.DELETE("/equipment-passports/previews/:preview_id", s.passportWrap(s.passportCancel))
	api.POST("/equipment-passports/previews/:preview_id/commit", s.passportWrap(s.passportCommit))
	api.GET("/equipment-passports/receipts/:receipt_id", s.passportWrap(s.passportReceipt))
	api.POST("/equipment-passports/template", s.passportWrap(s.passportTemplate))
	api.POST("/equipment-passports/export", s.passportWrap(s.passportExport))
}
func passportPathID(c *gin.Context, name string) (domain.PassportID, error) {
	var id domain.PassportID
	raw, _ := json.Marshal(c.Param(name))
	if e := json.Unmarshal(raw, &id); e != nil {
		return 0, passportHTTPError(domain.Invalid, "invalid_id")
	}
	return id, nil
}

// Network deadlines and Body.Close interrupt stalled body reads on cancellation.
func passportBodyContext(c *gin.Context) (context.Context, func(), error) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	controller := http.NewResponseController(c.Writer)
	deadline, _ := ctx.Deadline()
	_ = controller.SetReadDeadline(deadline)
	stopped := make(chan struct{})
	body := c.Request.Body
	stop := context.AfterFunc(ctx, func() {
		_ = controller.SetReadDeadline(time.Now())
		if body != nil {
			_ = body.Close()
		}
		close(stopped)
	})
	cleanup := func() {
		if !stop() {
			<-stopped
		}
		cancel()
		_ = controller.SetReadDeadline(time.Time{})
	}
	c.Request = c.Request.WithContext(ctx)
	return ctx, cleanup, nil
}
func passportWorkAdmission(c *gin.Context) (func(), error) {
	p, _ := domain.CurrentPrincipal(c.Request.Context())
	if p.AccountID == nil {
		return nil, passportHTTPError(domain.Unauthorized, "personal_session_required")
	}
	id := *p.AccountID
	passportHTTPWork.Lock()
	if passportHTTPWork.count >= 2 || passportHTTPWork.actors[id] {
		passportHTTPWork.Unlock()
		return nil, passportHTTPError(domain.Limited, "equipment_passport_busy")
	}
	passportHTTPWork.count++
	passportHTTPWork.actors[id] = true
	passportHTTPWork.Unlock()
	return func() {
		passportHTTPWork.Lock()
		passportHTTPWork.count--
		delete(passportHTTPWork.actors, id)
		passportHTTPWork.Unlock()
	}, nil
}
func passportBody(c *gin.Context, limit int64) ([]byte, error) {
	if c.Request.ContentLength > limit {
		return nil, passportHTTPError(domain.Invalid, "file_limit_exceeded")
	}
	raw, e := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, limit))
	if c.Request.Context().Err() != nil {
		return nil, c.Request.Context().Err()
	}
	return raw, e
}
func passportJSON(c *gin.Context, out any) error {
	media, _, e := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if e != nil || media != "application/json" {
		return passportHTTPError(domain.Invalid, "invalid_json")
	}
	raw, e := passportBody(c, 512*1024)
	if e != nil {
		return e
	}
	return passportDecode(raw, out)
}
func (s *Server) passportRead(c *gin.Context) error {
	id, e := passportPathID(c, "well_id")
	if e != nil {
		return e
	}
	v, e := s.EquipmentPassports.Read(c.Request.Context(), id)
	return respond(c, 200, v, e)
}
func (s *Server) passportHistory(c *gin.Context) error {
	id, e := passportPathID(c, "well_id")
	if e != nil {
		return e
	}
	q, e := url.ParseQuery(c.Request.URL.RawQuery)
	if e != nil {
		return passportHTTPError(domain.Invalid, "invalid_table_query")
	}
	size := 25
	raw := q.Get("table_query")
	for k, v := range q {
		if len(v) != 1 {
			return passportHTTPError(domain.Invalid, "invalid_table_query")
		}
		switch k {
		case "table_query", "page_size", "cursor", "limit":
		default:
			return passportHTTPError(domain.Invalid, "invalid_table_query")
		}
	}
	if q.Has("limit") && (q.Has("table_query") || q.Has("page_size")) {
		return passportHTTPError(domain.Invalid, "invalid_table_query")
	}
	n := q.Get("page_size")
	if q.Has("limit") {
		n = q.Get("limit")
	}
	if n != "" {
		size, e = strconv.Atoi(n)
		if e != nil || size < 1 || size > 50 {
			return passportHTTPError(domain.Invalid, "invalid_table_query")
		}
	}
	v, e := s.EquipmentPassports.HistoryTable(c.Request.Context(), id, raw, size, q.Get("cursor"))
	return respond(c, 200, v, e)
}
func (s *Server) passportPrepare(c *gin.Context) error {
	var in passportChangeSetDTO
	if e := passportJSON(c, &in); e != nil {
		return e
	}
	v, e := s.EquipmentPassports.Prepare(c.Request.Context(), domain.PassportChangeSet{SchemaVersion: in.SchemaVersion, Rows: in.Rows}, domain.PassportProvenance{Source: "manual"})
	return respond(c, 200, v, e)
}
func (s *Server) passportPreview(c *gin.Context) error {
	id, e := passportPathID(c, "preview_id")
	if e != nil {
		return e
	}
	v, e := s.EquipmentPassports.Preview(c.Request.Context(), id)
	return respond(c, 200, v, e)
}
func (s *Server) passportCancel(c *gin.Context) error {
	id, e := passportPathID(c, "preview_id")
	if e != nil {
		return e
	}
	if e = s.EquipmentPassports.CancelPreview(c.Request.Context(), id); e != nil {
		return e
	}
	c.Status(204)
	return nil
}
func (s *Server) passportCommit(c *gin.Context) error {
	id, e := passportPathID(c, "preview_id")
	if e != nil {
		return e
	}
	var in passportCommitDTO
	if e = passportJSON(c, &in); e != nil {
		return e
	}
	v, e := s.EquipmentPassports.Commit(c.Request.Context(), domain.PassportCommitInput{PreviewID: id, Digest: in.Digest, AcknowledgeWarnings: in.AcknowledgeWarnings, IdempotencyKey: c.GetHeader("Idempotency-Key")})
	return respond(c, 200, v, e)
}
func (s *Server) passportReceipt(c *gin.Context) error {
	id, e := passportPathID(c, "receipt_id")
	if e != nil {
		return e
	}
	v, e := s.EquipmentPassports.Receipt(c.Request.Context(), id)
	return respond(c, 200, v, e)
}
func (s *Server) passportImport(c *gin.Context) error {
	media, params, e := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if e != nil || media != "multipart/form-data" || params["boundary"] == "" {
		return passportHTTPError(domain.Invalid, "workbook_structure")
	}
	if c.Request.ContentLength > passportUploadMax+65536 {
		return passportHTTPError(domain.Invalid, "file_limit_exceeded")
	}
	release, e := passportWorkAdmission(c)
	if e != nil {
		return e
	}
	defer release()
	raw, e := passportBody(c, passportUploadMax+65536)
	if e != nil {
		return e
	}
	reader := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	part, e := reader.NextPart()
	if e != nil {
		return passportHTTPError(domain.Invalid, "workbook_structure")
	}
	defer part.Close()
	if part.FormName() != "file" || part.FileName() == "" {
		return passportHTTPError(domain.Invalid, "workbook_structure")
	}
	file, e := io.ReadAll(io.LimitReader(part, passportUploadMax+1))
	if e != nil {
		return e
	}
	if len(file) > passportUploadMax {
		return passportHTTPError(domain.Invalid, "file_limit_exceeded")
	}
	if _, e = reader.NextPart(); e != io.EOF {
		return passportHTTPError(domain.Invalid, "workbook_structure")
	}
	v, e := s.EquipmentPassports.Import(c.Request.Context(), bytes.NewReader(file))
	return respond(c, 200, v, e)
}

type passportDownloadBuffer struct{ bytes.Buffer }

func (b *passportDownloadBuffer) Write(raw []byte) (int, error) {
	if b.Len()+len(raw) > passportUploadMax {
		return 0, passportHTTPError(domain.Invalid, "file_limit_exceeded")
	}
	return b.Buffer.Write(raw)
}
func (s *Server) passportTemplate(c *gin.Context) error {
	var in passportTemplateDTO
	if e := passportJSON(c, &in); e != nil {
		return e
	}
	release, e := passportWorkAdmission(c)
	if e != nil {
		return e
	}
	defer release()
	var out passportDownloadBuffer
	if e = s.EquipmentPassports.Template(c.Request.Context(), in.WellIDs, &out); e != nil {
		return e
	}
	c.Header("Content-Disposition", `attachment; filename="equipment-passport-template.xlsx"`)
	c.Data(200, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", out.Bytes())
	return nil
}
func (s *Server) passportExport(c *gin.Context) error {
	var in domain.PassportExportRequest
	if e := passportJSON(c, &in); e != nil {
		return e
	}
	release, e := passportWorkAdmission(c)
	if e != nil {
		return e
	}
	defer release()
	var out passportDownloadBuffer
	if e = s.EquipmentPassports.Export(c.Request.Context(), in, &out); e != nil {
		return e
	}
	c.Header("Content-Disposition", `attachment; filename="equipment-passport.xlsx"`)
	c.Data(200, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", out.Bytes())
	return nil
}
