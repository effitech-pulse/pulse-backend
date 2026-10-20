package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/fileinput"
	"github.com/effitech-pulse/pulse-backend/internal/adapter/parameterfile"
	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/migrations"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

type sessionFileScope struct{ store *postgres.Repository }

func (s sessionFileScope) Members(context.Context) (domain.Cohort, []domain.Well, error) {
	return domain.Cohort{}, nil, nil
}
func (s sessionFileScope) RequireWell(ctx context.Context, id int64) (domain.Well, error) {
	return s.store.Well(ctx, id)
}
func (s sessionFileScope) RequireUnitek(context.Context, int64) (domain.Well, error) {
	return domain.Well{}, domain.NewError(domain.Missing, "missing")
}

type sessionFileScorer struct{}

func (sessionFileScorer) UploadPinned(context.Context, domain.ExecutionPin, domain.PinnedInputIdentity, io.Reader) (domain.UploadResult, error) {
	return domain.UploadResult{}, nil
}
func TestFileInputSessionMiddlewareAuthority(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured; session-backed PG proof")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("q0_http_%d", time.Now().UnixNano())
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	cfg, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { pool.Close(); _, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	if e = migrations.Apply(ctx, pool); e != nil {
		t.Fatal(e)
	}
	store := postgres.New(pool)
	access, e := service.NewAccess(store, true)
	if e != nil {
		t.Fatal(e)
	}
	password := "synthetic-local-password"
	active := true
	a, e := access.Bootstrap(ctx, domain.AccountInput{Login: "q0admin", Name: "q0admin", Role: "admin", Active: &active, Password: &password})
	if e != nil {
		t.Fatal(e)
	}
	adminCtx := domain.WithPrincipal(ctx, domain.AccountPrincipal(a))
	for _, role := range []string{"technologist", "viewer"} {
		if _, e = access.Create(adminCtx, domain.AccountInput{Login: role, Name: role, Role: role, Active: &active, Password: &password, Scope: domain.AccessScope{Funds: []string{"operational"}}}); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = pool.Exec(ctx, "INSERT INTO well(ngdu,department,active) VALUES(0,0,true)"); e != nil {
		t.Fatal(e)
	}
	rawSchema := []byte(`{"tags":[{"alias":"F_out","server_parameter":{"is_set":false,"coef_k":1,"coef_b":0}}]}`)
	snapshot, e := parameterfile.ParseTelemetrySchema(rawSchema)
	if e != nil {
		t.Fatal(e)
	}
	h := domain.FileSHA256([]byte("fixture"))
	pin := domain.ExecutionPin{Version: "upload_pin_v1", ModelName: "fixture", BundleSHA256: h, ContextVersion: "legacy_v1", DecisionRule: &domain.DecisionRule{ContextVersion: "legacy_v1", PolicyID: "fixture", RuleSHA256: h, Watch: &domain.ThresholdRule{Scale: "raw", Comparison: ">"}}, FeatureRecipeSHA256: h, FeatureSchemaSHA256: h, RuntimeManifestSHA256: h, SourceManifestSHA256: h}
	checks, e := service.NewFileChecksWithBuildInfo(store, store, sessionFileScope{store}, fileinput.Reader{}, sessionFileScorer{}, snapshot, pin, domain.DefaultFileInputLimits(), sessionFixtureBuildInfo(t))
	if e != nil {
		t.Fatal(e)
	}
	server := &Server{Config: config.Config{Prefix: "/api/v1"}, Access: access, Catalog: service.NewCatalog(store)}
	// Test-only route reuses actual login/cookie authorization. Production routes stay untouched.
	router := gin.New()
	router.Use(server.authorizeMiddleware())
	router.POST("/api/v1/auth/login", server.wrap(server.authLogin))
	router.POST("/api/v1/wells/:well_id/checks", server.wrap(func(c *gin.Context) error {
		var body struct {
			Key string `json:"key"`
		}
		if e := readJSON(c, &body); e != nil {
			return e
		}
		id, e := id(c, "well_id")
		if e != nil {
			return e
		}
		spec := domain.FileParseSpec{Version: "file_input_v1", Format: "csv", Delimiter: ",", HeaderRow: 1, TimeColumn: "time", TimestampLayout: "rfc3339", Timezone: "UTC", Mappings: []domain.Mapping{{Field: "f", Parameter: "F_out", Scale: 1}}}
		v, e := checks.PrepareFileInput(c.Request.Context(), id, body.Key, spec, strings.NewReader("time,f\n2026-01-01T00:00:00Z,0\n"))
		if e != nil {
			return e
		}
		out, e := checks.AcceptFileInput(c.Request.Context(), v)
		return respond(c, 200, out, e)
	}))
	call := func(method, path, body string, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-User-Role", "admin")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d want%d %s", path, w.Code, want, w.Body.String())
		}
		return w
	}
	body := `{"key":"q0-session-request","actor_account_id":1,"role":"admin"}`
	call("POST", "/api/v1/wells/1/checks", body, nil, 401)
	login := func(role string) *http.Cookie {
		w := call("POST", "/api/v1/auth/login", `{"login":"`+role+`","password":"`+password+`"}`, nil, 200)
		cookies := w.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatal("no session")
		}
		return cookies[0]
	}
	tech := login("technologist")
	call("POST", "/api/v1/wells/1/checks", body, tech, 200)
	var actor int64
	if e = pool.QueryRow(ctx, "SELECT actor_account_id FROM file_input").Scan(&actor); e != nil || actor != 2 {
		t.Fatal("body actor used", actor, e)
	}
	call("POST", "/api/v1/wells/1/checks", body, login("viewer"), 403)
	if _, e = pool.Exec(ctx, `UPDATE auth_user SET scope='{"funds":["presentation"]}' WHERE id=2`); e != nil {
		t.Fatal(e)
	}
	call("POST", "/api/v1/wells/1/checks", body, tech, 404)
	if _, e = pool.Exec(ctx, "UPDATE auth_user SET active=false WHERE id=2"); e != nil {
		t.Fatal(e)
	}
	call("POST", "/api/v1/wells/1/checks", body, tech, 401)
}

// Fixture metadata is explicit test injection: test binaries omit dependency BuildInfo.
func sessionFixtureBuildInfo(t testing.TB) *debug.BuildInfo {
	t.Helper()
	mod, e := os.ReadFile("../../../go.mod")
	if e != nil {
		t.Fatal(e)
	}
	sum, e := os.ReadFile("../../../go.sum")
	if e != nil {
		t.Fatal(e)
	}
	selected := map[string]string{}
	for _, line := range strings.Split(string(mod), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasPrefix(fields[1], "v") {
			selected[fields[0]] = fields[1]
		}
	}
	sums := map[string]string{}
	for _, line := range strings.Split(string(sum), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 {
			sums[fields[0]+" "+fields[1]] = fields[2]
		}
	}
	info := &debug.BuildInfo{GoVersion: runtime.Version()}
	for _, path := range domain.FileParserModulePaths() {
		version := selected[path]
		hash := sums[path+" "+version]
		if version == "" || hash == "" {
			t.Fatal("missing explicit fixture module", path)
		}
		info.Deps = append(info.Deps, &debug.Module{Path: path, Version: version, Sum: hash})
	}
	return info
}
