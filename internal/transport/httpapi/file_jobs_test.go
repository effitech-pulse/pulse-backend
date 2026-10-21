package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestFileJobHTTPOffRequiresSession(t *testing.T) {
	s := &Server{Config: config.Config{Prefix: "/api/v1", FileCheckMode: "off"}}
	r := httptest.NewRequest("GET", "/api/v1/file-check-capabilities", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
}
func TestFileJobStatusPreservesInt64WellAndPrivateFields(t *testing.T) {
	s := &Server{Config: config.Config{Prefix: "/api/v1"}}
	dto := s.fileJobDTO(domain.FileJobStatus{ID: strings.Repeat("a", 32), WellID: 9007199254740993, State: domain.FileJobPendingDispatch})
	raw, e := json.Marshal(dto)
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	if e = json.Unmarshal(raw, &v); e != nil {
		t.Fatal(e)
	}
	if v["well_id"] != "9007199254740993" || v["attempt"] != float64(0) || v["max_attempts"] != float64(3) || v["result_url"] != nil {
		t.Fatal(string(raw))
	}
	for _, key := range []string{"actor", "token", "fence", "cancel_requested_at", "attempt_count"} {
		if _, ok := v[key]; ok {
			t.Fatal("private/extra field", key)
		}
	}
}
func TestFileJobHTTPRejectsDuplicateUnknownMultipart(t *testing.T) {
	for _, fields := range [][]string{{"request_id", "request_id"}, {"actor"}} {
		body := new(bytes.Buffer)
		writer := multipart.NewWriter(body)
		for _, f := range fields {
			_ = writer.WriteField(f, "abcdefghijklmnop")
		}
		_ = writer.Close()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/", body)
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		if _, _, _, e := readFileJobMultipart(c); e == nil {
			t.Fatal("accepted invalid fields", fields)
		}
	}
}

func TestFileJobHTTPResultProblemsPreserveEnvelopeAndRetry(t *testing.T) {
	for _, tc := range []struct {
		reason string
		kind   domain.Kind
		status int
		retry  string
	}{{"result_not_ready", domain.Duplicate, 409, "2"}, {"result_unavailable", domain.Duplicate, 409, ""}, {"result_cancelled", domain.Missing, 410, ""}, {"result_expired", domain.Missing, 410, ""}, {"file_job_not_found", domain.Missing, 404, ""}, {"raw_limit", domain.Invalid, 413, ""}} {
		t.Run(tc.reason, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("GET", "/", nil)
			if e := writeFileJobError(c, domain.NewFileJobProblem(tc.kind, tc.reason)); e != nil {
				writeError(c, e)
			}
			if recorder.Code != tc.status || recorder.Header().Get("Retry-After") != tc.retry {
				t.Fatal(recorder.Code, recorder.Header())
			}
			var body struct {
				Code    domain.Kind `json:"code"`
				Message string      `json:"message"`
				Detail  any         `json:"detail"`
			}
			if e := json.Unmarshal(recorder.Body.Bytes(), &body); e != nil || body.Code != tc.kind || body.Message != tc.reason || body.Detail != nil {
				t.Fatal(e, recorder.Body.String())
			}
		})
	}
}
