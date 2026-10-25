package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

type passportHTTPStore struct {
	service.EquipmentPassportStore
	err    error
	calls  int
	in     domain.PassportChangeSet
	source domain.PassportProvenance
}

func (s *passportHTTPStore) ReadPassport(_ context.Context, _ domain.PassportPolicy, id domain.PassportID) (domain.PassportView, error) {
	s.calls++
	return domain.PassportView{WellID: id, WellRevision: 1, Revision: 0, Slots: []domain.PassportInstallation{}, Provenance: []domain.PassportSlotProvenance{}}, s.err
}
func (s *passportHTTPStore) PreparePassport(_ context.Context, _ domain.PassportPolicy, in domain.PassportChangeSet, source domain.PassportProvenance, _ domain.PassportDate) (domain.PassportPreview, error) {
	s.calls++
	s.in = in
	s.source = source
	return domain.PassportPreview{ID: 1, Commands: in}, s.err
}

func (s *passportHTTPStore) LoadPassportExport(_ context.Context, _ domain.PassportPolicy, _ domain.PassportExportRequest, purpose domain.PassportSnapshotPurpose) (domain.PassportExport, error) {
	s.calls++
	return domain.PassportExport{Mode: "current_components", Template: purpose == domain.PassportPurposeTemplate}, s.err
}

type passportHTTPCodec struct {
	encode func(context.Context, io.Writer, domain.PassportExport) error
}

func (passportHTTPCodec) Decode(context.Context, io.Reader) (domain.PassportWorkbook, error) {
	return domain.PassportWorkbook{}, errors.New("invalid fixture")
}
func (c passportHTTPCodec) Encode(ctx context.Context, w io.Writer, in domain.PassportExport) error {
	if c.encode != nil {
		return c.encode(ctx, w, in)
	}
	return nil
}
func passportHTTPRouter(t *testing.T, store *passportHTTPStore, authenticated bool) *gin.Engine {
	t.Helper()
	svc, e := service.NewEquipmentPassports(store, passportHTTPCodec{}, service.PassportOptions{AppScope: "research"})
	if e != nil {
		t.Fatal(e)
	}
	s := &Server{EquipmentPassports: svc}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		id := int64(7)
		a := domain.AuthorizationSnapshot{AccountID: id, Active: true, Epoch: 1, Permissions: []domain.Permission{"wells.read", "equipment_passports.read", "equipment_passports.update", "equipment_passports.import", "equipment_passports.export", "equipment_passports.history.read"}}
		ctx := domain.WithRBACMode(domain.WithPrincipal(c.Request.Context(), domain.Principal{AccountID: &id, Authorization: &a, Authenticated: authenticated, Mechanism: "session"}), domain.RBACEnforce)
		c.Request = c.Request.WithContext(ctx)
	})
	s.registerEquipmentPassportRoutes(r.Group("/api/v1"))
	return r
}
func TestEquipmentPassportHTTPContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &passportHTTPStore{}
	r := passportHTTPRouter(t, store, true)
	request := func(method, path, body string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w.Code
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/wells/9007199254740993/equipment-passport", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"well_id":"9007199254740993"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, body := range []string{`{"schema_version":"equipment-passport-v1","rows":[],"actor":{}}`, `{"schema_version":"equipment-passport-v1","rows":[],"source":"manual"}`, `{"schema_version":"equipment-passport-v1","rows":[{"well_id":1}]}`, `{"schema_version":"equipment-passport-v1","rows":[{"well_id":"1","unknown":true}]}`} {
		if code := request("POST", "/api/v1/equipment-passports/previews", body); code != 400 {
			t.Fatal(code, body)
		}
	}
	valid := `{"schema_version":"equipment-passport-v1","rows":[{"row_key":"r","well_id":"9007199254740993","expected_well_revision":"1","expected_passport_revision":"0","slot":"control_station","action":"update","model":null,"control_output_frequency_min_hz":"0"}]}`
	if code := request("POST", "/api/v1/equipment-passports/previews", valid); code != 200 {
		t.Fatal(code)
	}
	m := store.in.Rows[0]
	if !m.Model.Null || !m.Model.Present || m.Manufacturer.Present || m.ControlOutputFrequencyMinHz.Value != "0" {
		t.Fatal(m)
	}
	for _, tt := range []struct {
		err  error
		want int
	}{{domain.NewError(domain.Invalid, "bad"), 400}, {domain.NewError(domain.Missing, "bad"), 404}, {domain.NewError(domain.Duplicate, "bad"), 409}, {domain.NewPassportError(domain.Missing, "preview_consumed", "done", nil), 410}, {domain.NewPassportError(domain.Invalid, "file_limit_exceeded", "large", nil), 413}, {domain.NewError(domain.Limited, "busy"), 429}, {domain.NewError(domain.Unavailable, "offline"), 503}} {
		store.err = tt.err
		if code := request("GET", "/api/v1/wells/1/equipment-passport", ""); code != tt.want {
			t.Fatalf("%v => %d want%d", tt.err, code, tt.want)
		}
	}
	anonymous := passportHTTPRouter(t, &passportHTTPStore{}, false)
	w = httptest.NewRecorder()
	anonymous.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/wells/1/equipment-passport", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	// Existing legacy mapper remains422 for generic validation.
	w = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	writeError(c, domain.NewError(domain.Invalid, "legacy"))
	if w.Code != 422 {
		t.Fatal(w.Code)
	}
	var out map[string]json.RawMessage
	if json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal("invalid legacy envelope")
	}
}

func TestEquipmentPassportHTTPInvalidJSONCode(t *testing.T) {
	r := passportHTTPRouter(t, &passportHTTPStore{}, true)
	for _, body := range []string{`{"schema_version":"equipment-passport-v1","rows":[],"actor":{}}`, `{"schema_version":"equipment-passport-v1","rows":[],"rows":[]}`, `null`, `{} {}`} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/equipment-passports/previews", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"invalid_json"`) {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
}

func TestEquipmentPassportHTTPMultipartBounds(t *testing.T) {
	r := passportHTTPRouter(t, &passportHTTPStore{}, true)
	for _, tt := range []struct {
		name  string
		extra bool
		size  int
		want  int
	}{
		{"oversize", false, passportUploadMax + 1, 413}, {"extra-part", true, 1, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var body bytes.Buffer
			mw := multipart.NewWriter(&body)
			part, _ := mw.CreateFormFile("file", "passport.xlsx")
			_, _ = part.Write(bytes.Repeat([]byte("x"), tt.size))
			if tt.extra {
				_ = mw.WriteField("actor", "7")
			}
			_ = mw.Close()
			w := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/v1/equipment-passports/import/previews", &body)
			req.Header.Set("Content-Type", mw.FormDataContentType())
			r.ServeHTTP(w, req)
			if w.Code != tt.want {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			passportHTTPWork.Lock()
			count := passportHTTPWork.count
			passportHTTPWork.Unlock()
			if count != 0 {
				t.Fatal("admission leaked", count)
			}
		})
	}
}

type passportBlockedBody struct {
	closed  chan struct{}
	entered chan struct{}
}

func (b *passportBlockedBody) Read([]byte) (int, error) {
	close(b.entered)
	<-b.closed
	return 0, io.ErrClosedPipe
}
func (b *passportBlockedBody) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}
func TestEquipmentPassportHTTPBodyCancellation(t *testing.T) {
	r := passportHTTPRouter(t, &passportHTTPStore{}, true)
	ctx, cancel := context.WithCancel(context.Background())
	body := &passportBlockedBody{closed: make(chan struct{}), entered: make(chan struct{})}
	req := httptest.NewRequest("POST", "/api/v1/equipment-passports/import/previews", nil).WithContext(ctx)
	req.Body = body
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
	done := make(chan int, 1)
	go func() { w := httptest.NewRecorder(); r.ServeHTTP(w, req); done <- w.Code }()
	select {
	case <-body.entered:
	case <-time.After(time.Second):
		t.Fatal("body not read")
	}
	cancel()
	select {
	case code := <-done:
		if code != 503 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation stalled")
	}
	passportHTTPWork.Lock()
	count := passportHTTPWork.count
	passportHTTPWork.Unlock()
	if count != 0 {
		t.Fatal("cancelled admission leaked")
	}
}

func TestEquipmentPassportHTTPDownloadAtomic(t *testing.T) {
	for _, tt := range []struct {
		name   string
		encode func(context.Context, io.Writer, domain.PassportExport) error
		want   int
	}{
		{"codec-error", func(_ context.Context, w io.Writer, _ domain.PassportExport) error {
			_, _ = w.Write([]byte("PK-partial"))
			return errors.New("failed")
		}, 503},
		{"output-limit", func(_ context.Context, w io.Writer, _ domain.PassportExport) error {
			_, e := w.Write(bytes.Repeat([]byte("x"), passportUploadMax+1))
			return e
		}, 413},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc, e := service.NewEquipmentPassports(&passportHTTPStore{}, passportHTTPCodec{encode: tt.encode}, service.PassportOptions{AppScope: "research"})
			if e != nil {
				t.Fatal(e)
			}
			s := &Server{EquipmentPassports: svc}
			r := passportHTTPRouter(t, &passportHTTPStore{}, true)
			// Reuse its principal middleware while exercising this codec instance.
			r.POST("/download", s.passportWrap(s.passportExport))
			req := httptest.NewRequest("POST", "/download", strings.NewReader(`{"well_ids":["1"],"mode":"current_components"}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tt.want || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || strings.Contains(w.Body.String(), "PK-partial") || w.Header().Get("Content-Disposition") != "" {
				t.Fatal(w.Code, w.Header(), w.Body.String())
			}
		})
	}
}

func TestEquipmentPassportOpenAPI(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]struct {
			Responses   map[string]json.RawMessage `json:"responses"`
			RequestBody json.RawMessage            `json:"requestBody"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(openAPI, &spec); err != nil {
		t.Fatal(err)
	}
	for _, policy := range authorizationPolicies {
		if !strings.Contains(policy.Path, "equipment-passport") {
			continue
		}
		path := policy.Path
		for _, name := range []string{"well_id", "preview_id", "receipt_id"} {
			path = strings.ReplaceAll(path, ":"+name, "{"+name+"}")
		}
		op, ok := spec.Paths["/api/v1"+path][strings.ToLower(policy.Method)]
		if !ok {
			t.Errorf("missing operation %s %s", policy.Method, path)
			continue
		}
		for _, code := range []string{"400", "401", "403", "404", "409", "410", "413", "429", "503"} {
			if _, ok := op.Responses[code]; !ok {
				t.Errorf("missing feature error %s: %s", path, code)
			}
		}
		if _, ok := op.Responses["422"]; ok {
			t.Errorf("feature mapper does not use422: %s", path)
		}
		if policy.Method == "POST" && len(op.RequestBody) == 0 {
			t.Errorf("missing request body %s", path)
		}
	}
}

func TestEquipmentPassportHTTPProfiles(t *testing.T) {
	for _, tt := range []struct {
		name     string
		personal bool
		mode     domain.RBACMode
		want     int
	}{
		{"legacy", false, domain.RBACEnforce, 401}, {"compat", true, domain.RBACCompat, 403}, {"unavailable", true, domain.RBACEnforce, 503},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := gin.New()
			r.Use(func(c *gin.Context) {
				p := domain.TrustedLegacyPrincipal("key")
				if tt.personal {
					id := int64(1)
					p = domain.Principal{Authenticated: true, AccountID: &id, Mechanism: "session"}
				}
				c.Request = c.Request.WithContext(domain.WithRBACMode(domain.WithPrincipal(c.Request.Context(), p), tt.mode))
			})
			(&Server{}).registerEquipmentPassportRoutes(r.Group("/api/v1"))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/wells/1/equipment-passport", nil))
			if w.Code != tt.want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestEquipmentPassportHTTPAdmission(t *testing.T) {
	contextFor := func(id int64) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/", nil).WithContext(domain.WithPrincipal(context.Background(), domain.Principal{AccountID: &id}))
		return c
	}
	release1, e := passportWorkAdmission(contextFor(1))
	if e != nil {
		t.Fatal(e)
	}
	defer release1()
	if _, e = passportWorkAdmission(contextFor(1)); e == nil {
		t.Fatal("same actor admitted twice")
	}
	release2, e := passportWorkAdmission(contextFor(2))
	if e != nil {
		t.Fatal(e)
	}
	defer release2()
	if _, e = passportWorkAdmission(contextFor(3)); e == nil {
		t.Fatal("global capacity exceeded")
	}
	// Envelope size/type errors are deterministic even when all work slots are held.
	r := passportHTTPRouter(t, &passportHTTPStore{}, true)
	for _, tt := range []struct {
		media  string
		length int64
		want   int
	}{
		{"multipart/form-data; boundary=b", passportUploadMax + 65537, 413},
		{"application/json", 1, 400},
	} {
		req := httptest.NewRequest("POST", "/api/v1/equipment-passports/import/previews", strings.NewReader("x"))
		req.ContentLength = tt.length
		req.Header.Set("Content-Type", tt.media)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != tt.want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestEquipmentPassportHTTPBodyDeadline(t *testing.T) {
	r := passportHTTPRouter(t, &passportHTTPStore{}, true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	body := &passportBlockedBody{closed: make(chan struct{}), entered: make(chan struct{})}
	req := httptest.NewRequest("POST", "/api/v1/equipment-passports/import/previews", nil).WithContext(ctx)
	req.Body = body
	req.Header.Set("Content-Type", "multipart/form-data; boundary=b")
	done := make(chan int, 1)
	go func() { w := httptest.NewRecorder(); r.ServeHTTP(w, req); done <- w.Code }()
	select {
	case code := <-done:
		if code != 503 {
			t.Fatal(code)
		}
	case <-time.After(time.Second):
		t.Fatal("deadline did not interrupt read")
	}
	passportHTTPWork.Lock()
	count := passportHTTPWork.count
	passportHTTPWork.Unlock()
	if count != 0 {
		t.Fatal("deadline leaked admission")
	}
}
