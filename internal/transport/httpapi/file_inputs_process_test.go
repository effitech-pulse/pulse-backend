package httpapi

// Q0 process proof. These tests deliberately require explicit opt-in and an
// isolated synthetic database. There are no production routes or debug hooks.
import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/fileinput"
	"github.com/effitech-pulse/pulse-backend/internal/adapter/grpcml"
	"github.com/effitech-pulse/pulse-backend/internal/adapter/parameterfile"
	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/migrations"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

const q0Well int64 = 9007199254740993
const q0Password = "q0-synthetic-local-password"

var q0LogMu sync.Mutex

func q0JSON(t testing.TB, path string, v any) {
	t.Helper()
	raw, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path+".tmp", raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(path+".tmp", path); e != nil {
		t.Fatal(e)
	}
}
func q0Read(t testing.TB, path string, v any) {
	t.Helper()
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, v); e != nil {
		t.Fatal(e)
	}
}
func q0Event(name string, v any) {
	q0LogMu.Lock()
	defer q0LogMu.Unlock()
	f, e := os.OpenFile(filepath.Join(os.Getenv("Q0_ROOT"), "evidence", "go-events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		panic(e)
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(map[string]any{"event": name, "pid": os.Getpid(), "utc_ns": time.Now().UnixNano(), "data": v})
}
func q0Enabled(t testing.TB) string {
	t.Helper()
	if os.Getenv("Q0_PROCESS_RUN") != "1" {
		t.Skip("Q0 actual-process proof is opt-in; no runtime PASS implied")
	}
	root := os.Getenv("Q0_ROOT")
	if root == "" || os.Getenv("TEST_DATABASE_URL") == "" {
		t.Fatal("GATE OPEN: Q0_ROOT and isolated TEST_DATABASE_URL required")
	}
	return root
}

type q0Tracer struct{}

func (q0Tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(strings.TrimSpace(strings.ToUpper(d.SQL)), "SELECT") && strings.Contains(d.SQL, "file_input_payload") {
		q0Event("payload_select", nil)
	}
	return ctx
}
func (q0Tracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func q0Pool(t testing.TB, schema string) *pgxpool.Pool {
	t.Helper()
	cfg, e := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	cfg.ConnConfig.Tracer = q0Tracer{}
	pool, e := pgxpool.NewWithConfig(context.Background(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	return pool
}
func q0Schema(t testing.TB) (string, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("q0_p_%d", time.Now().UnixNano())
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+schema); e != nil {
		t.Fatal(e)
	}
	pool := q0Pool(t, schema)
	t.Cleanup(func() {
		pool.Close()
		_, e := admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
		if e != nil {
			t.Error(e)
		}
	})
	if e = migrations.Apply(ctx, pool); e != nil {
		t.Fatal(e)
	}
	store := postgres.New(pool)
	access, e := service.NewAccess(store, true)
	if e != nil {
		t.Fatal(e)
	}
	active := true
	password := q0Password
	adminAccount, e := access.Bootstrap(ctx, domain.AccountInput{Login: "q0admin", Name: "synthetic", Role: "admin", Active: &active, Password: &password})
	if e != nil {
		t.Fatal(e)
	}
	ctx = domain.WithPrincipal(ctx, domain.AccountPrincipal(adminAccount))
	for _, login := range []string{"technologist", "sibling", "quota", "viewer"} {
		role := "technologist"
		if login == "viewer" {
			role = "viewer"
		}
		if _, e = access.Create(ctx, domain.AccountInput{Login: login, Name: login, Role: role, Active: &active, Password: &password, Scope: domain.AccessScope{Funds: []string{"operational"}}}); e != nil {
			t.Fatal(e)
		}
	}
	for _, id := range []int64{q0Well, q0Well + 1} {
		if _, e = pool.Exec(ctx, "INSERT INTO well(id,ngdu,department,active) OVERRIDING SYSTEM VALUE VALUES($1,0,0,true)", id); e != nil {
			t.Fatal(e)
		}
	}
	return schema, pool
}

type q0ObservedStore struct{ *postgres.Repository }

func (s q0ObservedStore) AcceptFileInput(ctx context.Context, v domain.PreparedFileInput) (domain.FileInputSummary, error) {
	at := time.Now()
	out, e := s.Repository.AcceptFileInput(ctx, v)
	q0Event("input_commit", map[string]any{"ms": float64(time.Since(at).Microseconds()) / 1000, "ok": e == nil, "id": out.ID})
	return out, e
}
func (s q0ObservedStore) CommitStoredResult(ctx context.Context, v domain.StoredFileResult) (domain.StoredFileResult, error) {
	at := time.Now()
	out, e := s.Repository.CommitStoredResult(ctx, v)
	q0Event("result_commit", map[string]any{"ms": float64(time.Since(at).Microseconds()) / 1000, "ok": e == nil, "id": out.InputID})
	return out, e
}
func q0Checks(t testing.TB, pool *pgxpool.Pool, amplify bool) *service.FileChecks {
	t.Helper()
	root := os.Getenv("Q0_ROOT")
	var pin domain.ExecutionPin
	q0Read(t, filepath.Join(root, "pin.json"), &pin)
	switch os.Getenv("Q0_TEST_PIN_FAULT") {
	case "policy":
		pin.DecisionRule.Watch.Threshold = .71
	case "bundle":
		pin.BundleSHA256 = domain.FileSHA256([]byte("unavailable synthetic bundle"))
	}
	file := "schema.json"
	if amplify {
		file = "amplify-schema.json"
	}
	raw, e := os.ReadFile(filepath.Join(root, file))
	if e != nil {
		t.Fatal(e)
	}
	schema, e := parameterfile.ParseTelemetrySchema(raw)
	if e != nil {
		t.Fatal(e)
	}
	client, e := grpcml.New(config.Config{MLTarget: os.Getenv("Q0_ML_ADDR"), MLKey: "q0-synthetic-key", MLTimeout: 30 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = client.Close() })
	repo := postgres.New(pool)
	out, e := service.NewFileChecksWithBuildInfo(q0ObservedStore{repo}, repo, sessionFileScope{repo}, fileinput.Reader{}, client, schema, pin, domain.DefaultFileInputLimits(), sessionFixtureBuildInfo(t))
	if e != nil {
		t.Fatal(e)
	}
	return out
}
func q0Spec(work string) domain.FileParseSpec {
	v := domain.FileParseSpec{Version: "file_input_v1", Format: "csv", Delimiter: ",", HeaderRow: 1, TimeColumn: "time", TimestampLayout: "rfc3339", Timezone: "UTC"}
	channels := []string{"F_out", "Ia", "CosF", "Ped_load", "Pump_intake_pressure_kgs", "Motor_winding_temperature", "R_izol", "qg", "set_Annular_pressure"}
	if strings.HasPrefix(work, "w1") {
		col := "pulse"
		v.WellColumn = &col
		v.WellIdentity = "pulse"
	}
	if strings.HasPrefix(work, "w2") {
		channels = channels[:8]
		v.Format = "xlsx"
		v.Delimiter = ""
		v.Sheet = "Telemetry"
	}
	if strings.HasPrefix(work, "w3") || work == "w4.csv" {
		channels = nil
		for i := 0; i < 64; i++ {
			channels = append(channels, fmt.Sprintf("synthetic_%02d", i))
		}
	}
	for _, c := range channels {
		scale := 1.
		if work == "w4.csv" {
			scale = 1.2345678901234567
		}
		v.Mappings = append(v.Mappings, domain.Mapping{Field: c, Parameter: c, Scale: scale})
	}
	return v
}

type q0Request struct {
	Action   string `json:"action"`
	Key      string `json:"key"`
	ID       string `json:"id"`
	File     string `json:"file"`
	Barrier  string `json:"barrier"`
	Mutation string `json:"mutation"`
	Actor    int64  `json:"actor_account_id"`
}

func TestQ0FileProcessChild(t *testing.T) {
	if os.Getenv("Q0_CHILD") == "" {
		t.Skip("internal process helper")
	}
	root := q0Enabled(t)
	if e := syscall.Setpgid(0, 0); e != nil {
		t.Fatal(e)
	}
	pool := q0Pool(t, os.Getenv("Q0_SCHEMA"))
	mode := os.Getenv("Q0_CHILD")
	if mode != "gateway" {
		q0ResourceChild(t, pool, mode)
		return
	}
	checks := q0Checks(t, pool, false)
	repo := postgres.New(pool)
	access, e := service.NewAccess(repo, true)
	if e != nil {
		t.Fatal(e)
	}
	server := &Server{Config: config.Config{Prefix: "/api/v1"}, Access: access, Catalog: service.NewCatalog(repo)}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(server.authorizeMiddleware())
	router.POST("/api/v1/auth/login", server.wrap(server.authLogin))
	router.POST("/api/v1/wells/:well_id/checks", server.wrap(func(c *gin.Context) error {
		var b q0Request
		if e := readJSON(c, &b); e != nil {
			return e
		}
		ctx := c.Request.Context()
		well, e := id(c, "well_id")
		if e != nil {
			return e
		}
		at := time.Now()
		defer func() {
			q0Event("http_operation", map[string]any{"action": b.Action, "ms": float64(time.Since(at).Microseconds()) / 1000})
		}()
		switch b.Action {
		case "accept":
			if filepath.Base(b.File) != b.File || !strings.HasPrefix(b.File, "w1") {
				return domain.NewError(domain.Invalid, "synthetic_fixture_required")
			}
			raw, e := os.ReadFile(filepath.Join(root, "datasets", b.File))
			if e != nil {
				return domain.NewError(domain.Unavailable, "synthetic_source_unavailable")
			}
			spec := q0Spec(b.File)
			switch b.Mutation {
			case "raw":
				raw = append(raw, '\n')
			case "mapping":
				spec.Mappings[0].Missing = "-999"
			case "timezone":
				spec.Timezone = "Europe/Moscow"
			}
			start := time.Now()
			v, e := checks.PrepareFileInput(ctx, well, b.Key, spec, bytes.NewReader(raw))
			q0Event("prepare", map[string]any{"ms": float64(time.Since(start).Microseconds()) / 1000, "ok": e == nil})
			if e != nil {
				return e
			}
			out, e := checks.AcceptFileInput(ctx, v)
			if e != nil {
				return e
			}
			if b.Barrier == "input" {
				q0JSON(t, filepath.Join(root, "evidence", "barrier-input.json"), out)
				select {}
			}
			return respond(c, 200, out, nil)
		case "compute":
			start := time.Now()
			v, e := checks.ComputeStoredInput(ctx, b.ID)
			q0Event("compute", map[string]any{"ms": float64(time.Since(start).Microseconds()) / 1000, "ok": e == nil})
			if e != nil {
				return e
			}
			out, e := checks.CommitStoredResult(ctx, v)
			if e != nil {
				return e
			}
			if b.Barrier == "result" {
				q0JSON(t, filepath.Join(root, "evidence", "barrier-result.json"), out)
				select {}
			}
			return respond(c, 200, out, nil)
		case "summary":
			out, e := checks.FileInputSummary(ctx, b.ID)
			return respond(c, 200, out, e)
		case "list":
			out, e := checks.FileInputSummaries(ctx, well, "")
			return respond(c, 200, out, e)
		case "result":
			out, e := checks.StoredFileResult(ctx, b.ID)
			return respond(c, 200, out, e)
		default:
			return domain.NewError(domain.Invalid, "invalid_synthetic_action")
		}
	}))
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	q0JSON(t, os.Getenv("Q0_READY"), map[string]any{"addr": listener.Addr().String(), "pid": os.Getpid(), "utc_ns": time.Now().UnixNano()})
	if e = http.Serve(listener, router); e != nil {
		t.Fatal(e)
	}
}

type q0Process struct {
	cmd     *exec.Cmd
	done    chan error
	stop    chan struct{}
	stopped bool
	name    string
	root    string
	maxRSS  int64
	maxTemp int64
	reason  string
	mu      sync.Mutex
}

// The timer remains outside the child group so a SIGKILL still produces OS high-water evidence.
func q0KillOwned(timerPID int) {
	out, err := exec.Command("/bin/ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		var pid, parent int
		if _, err := fmt.Sscan(line, &pid, &parent); err == nil && parent == timerPID {
			q0Event("owned_sigkill", map[string]any{"child_pid": pid, "timer_pid": timerPID, "signal": 9})
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

func q0DirBytes(path string) int64 {
	var total int64
	_ = filepath.Walk(path, func(_ string, i os.FileInfo, e error) error {
		if e == nil && i.Mode().IsRegular() {
			total += i.Size()
		}
		return nil
	})
	return total
}
func q0TreeRSS(pid int) int64 {
	out, e := exec.Command("/bin/ps", "-axo", "pid=,ppid=,rss=").Output()
	if e != nil {
		return 0
	}
	type row struct {
		pid, ppid int
		rss       int64
	}
	var rows []row
	for _, s := range strings.Split(string(out), "\n") {
		var r row
		if _, e = fmt.Sscan(s, &r.pid, &r.ppid, &r.rss); e == nil {
			rows = append(rows, r)
		}
	}
	owned := map[int]bool{pid: true}
	for i := 0; i < 8; i++ {
		for _, r := range rows {
			if owned[r.ppid] {
				owned[r.pid] = true
			}
		}
	}
	var rss int64
	for _, r := range rows {
		if owned[r.pid] {
			rss += r.rss * 1024
		}
	}
	return rss
}
func q0Start(t testing.TB, name string, args []string, env []string, budget int64, deadline time.Duration) *q0Process {
	t.Helper()
	root := os.Getenv("Q0_ROOT")
	ready := filepath.Join(root, "evidence", name+"-ready.json")
	_ = os.Remove(ready)
	timing := filepath.Join(root, "evidence", name+"-time.txt")
	argv := append([]string{"-l", "-o", timing}, args...)
	cmd := exec.Command("/usr/bin/time", argv...)
	cmd.Env = append(os.Environ(), append(env, "Q0_READY="+ready)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	log, e := os.OpenFile(filepath.Join(root, "evidence", name+".log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	cmd.Stdout = log
	cmd.Stderr = log
	if e = cmd.Start(); e != nil {
		log.Close()
		t.Fatal(e)
	}
	p := &q0Process{cmd: cmd, done: make(chan error, 1), stop: make(chan struct{}), name: name, root: root}
	go func() { p.done <- cmd.Wait(); _ = log.Close() }()
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		started := time.Now()
		for {
			select {
			case <-p.stop:
				return
			case <-ticker.C:
				rss := q0TreeRSS(cmd.Process.Pid)
				tmp := q0DirBytes(filepath.Join(root, "parser-temp")) + q0DirBytes(filepath.Join(root, "ml-temp"))
				p.mu.Lock()
				p.maxRSS = max(p.maxRSS, rss)
				p.maxTemp = max(p.maxTemp, tmp)
				if p.reason == "" {
					if rss > budget {
						p.reason = "RSS_budget"
					} else if tmp > 128<<20 {
						p.reason = "temp_budget"
					} else if time.Since(started) > deadline {
						p.reason = "deadline"
					}
				}
				reason := p.reason
				p.mu.Unlock()
				if reason != "" {
					q0KillOwned(cmd.Process.Pid)
					return
				}
			}
		}
	}()
	t.Cleanup(func() { p.finish(t, true) })
	return p
}
func (p *q0Process) finish(t testing.TB, kill bool) error {
	t.Helper()
	if p.stopped {
		return nil
	}
	p.stopped = true
	if kill {
		q0KillOwned(p.cmd.Process.Pid)
	}
	e := <-p.done
	close(p.stop)
	raw, _ := os.ReadFile(filepath.Join(p.root, "evidence", p.name+"-time.txt"))
	var high int64
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, "maximum resident set size") {
			fmt.Sscan(line, &high)
		}
	}
	p.mu.Lock()
	rss, tmp, reason := p.maxRSS, p.maxTemp, p.reason
	p.mu.Unlock()
	budget := int64(1 << 30)
	if strings.HasPrefix(p.name, "ml-") {
		budget = 2 << 30
	}
	if high > budget {
		reason = "OS_highwater_RSS_budget"
	}
	q0JSON(t, filepath.Join(p.root, "evidence", p.name+"-process.json"), map[string]any{"pid": p.cmd.Process.Pid, "utc_ns": time.Now().UnixNano(), "exit": fmt.Sprint(e), "sample_tree_peak_rss_bytes": rss, "os_highwater_rss_bytes": high, "sample_temp_peak_bytes": tmp, "stop_reason": reason, "temp_after_bytes": q0DirBytes(filepath.Join(p.root, "parser-temp")) + q0DirBytes(filepath.Join(p.root, "ml-temp")), "highwater_tool": "Darwin /usr/bin/time -l; bytes"})
	return e
}
func q0WaitJSON(t testing.TB, path string, v any) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		raw, e := os.ReadFile(path)
		if e == nil && json.Unmarshal(raw, v) == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("GATE OPEN: control signal deadline %s", filepath.Base(path))
}
func q0ML(t testing.TB, label string) (*q0Process, string) {
	t.Helper()
	root := os.Getenv("Q0_ROOT")
	python := os.Getenv("Q0_PYTHON")
	if python == "" {
		t.Fatal("GATE OPEN: accepted Q0_PYTHON missing")
	}
	p := q0Start(t, "ml-"+label, []string{python, filepath.Join(root, "ml_launcher.py")}, []string{"ML_BUNDLE_REGISTRY=" + filepath.Join(root, "registry"), "ML_SERVICE_API_KEY=q0-synthetic-key", "TMPDIR=" + filepath.Join(root, "ml-temp"), "ML_MODEL_NAME=q0-no-default-model", "ML_MODELS_DIR=" + filepath.Join(root, "unavailable-default"), "OBSERVABILITY_EXPORT_ENABLED=false"}, 2<<30, 5*time.Minute)
	var ready struct {
		Addr string `json:"addr"`
	}
	q0WaitJSON(t, filepath.Join(root, "evidence", "ml-"+label+"-ready.json"), &ready)
	return p, ready.Addr
}
func q0Gateway(t testing.TB, schema, ml, label string) (*q0Process, string) {
	t.Helper()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	p := q0Start(t, "go-"+label, []string{exe, "-test.run=^TestQ0FileProcessChild$", "-test.v"}, []string{"Q0_CHILD=gateway", "Q0_SCHEMA=" + schema, "Q0_ML_ADDR=" + ml, "TMPDIR=" + filepath.Join(os.Getenv("Q0_ROOT"), "parser-temp")}, 1<<30, 5*time.Minute)
	var ready struct {
		Addr string `json:"addr"`
	}
	q0WaitJSON(t, filepath.Join(os.Getenv("Q0_ROOT"), "evidence", "go-"+label+"-ready.json"), &ready)
	return p, "http://" + ready.Addr
}
func q0HTTP(t testing.TB, base, path string, body any, cookie *http.Cookie, want int) []byte {
	t.Helper()
	raw, e := json.Marshal(body)
	if e != nil {
		t.Fatal(e)
	}
	req, e := http.NewRequest("POST", base+path, bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Role", "admin")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	client := &http.Client{Timeout: 40 * time.Second}
	res, e := client.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	data, e := io.ReadAll(io.LimitReader(res.Body, 3<<20))
	if e != nil {
		t.Fatal(e)
	}
	if res.StatusCode != want {
		t.Fatalf("%s: status=%d want=%d body=%s", path, res.StatusCode, want, data)
	}
	return data
}
func q0Login(t testing.TB, base, login string) *http.Cookie {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"login": login, "password": q0Password})
	res, e := http.Post(base+"/api/v1/auth/login", "application/json", bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("login status %d", res.StatusCode)
	}
	cookies := res.Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing session")
	}
	return cookies[0]
}
func q0Path(well int64) string { return fmt.Sprintf("/api/v1/wells/%d/checks", well) }
func q0CountEvents(root, file, name string) int {
	raw, _ := os.ReadFile(filepath.Join(root, "evidence", file))
	n := 0
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		var v struct {
			Event string `json:"event"`
		}
		if json.Unmarshal(line, &v) == nil && v.Event == name {
			n++
		}
	}
	return n
}
func q0Async(base, path string, b any, cookie *http.Cookie) chan error {
	done := make(chan error, 1)
	go func() {
		raw, _ := json.Marshal(b)
		req, _ := http.NewRequest("POST", base+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		res, e := (&http.Client{Timeout: 40 * time.Second}).Do(req)
		if e == nil {
			res.Body.Close()
			e = fmt.Errorf("barrier unexpectedly returned status %d", res.StatusCode)
		}
		done <- e
	}()
	return done
}
func q0DB(t testing.TB, pool *pgxpool.Pool, label string) {
	t.Helper()
	ctx := context.Background()
	rows, e := pool.Query(ctx, `SELECT c.relname,pg_relation_size(c.oid),pg_total_relation_size(c.oid),CASE WHEN c.reltoastrelid=0 THEN 0 ELSE pg_relation_size(c.reltoastrelid) END,CASE WHEN c.reltoastrelid=0 THEN 0 ELSE pg_indexes_size(c.reltoastrelid) END FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND c.relkind='r' ORDER BY c.relname`)
	if e != nil {
		t.Fatal(e)
	}
	var relations []map[string]any
	for rows.Next() {
		var name string
		var relation, total, toast, index int64
		if e = rows.Scan(&name, &relation, &total, &toast, &index); e != nil {
			t.Fatal(e)
		}
		relations = append(relations, map[string]any{"name": name, "relation": relation, "total_including_toast_indexes": total, "toast_heap": toast, "toast_indexes": index})
	}
	rows.Close()
	var version, lsn string
	var count, payload, result, logical int64
	if e = pool.QueryRow(ctx, "SELECT version(),pg_current_wal_lsn()::text").Scan(&version, &lsn); e != nil {
		t.Fatal(e)
	}
	if e = pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM file_input),(SELECT count(*) FROM file_input_payload),(SELECT count(*) FROM file_check_result),(SELECT retained_bytes FROM file_input_budget WHERE budget_key='global')").Scan(&count, &payload, &result, &logical); e != nil {
		t.Fatal(e)
	}
	q0JSON(t, filepath.Join(os.Getenv("Q0_ROOT"), "evidence", label+"-db.json"), map[string]any{"version": version, "wal_lsn": lsn, "inputs": count, "payloads": payload, "results": result, "charged_bytes": logical, "relations": relations, "utc_ns": time.Now().UnixNano()})
}

// Removing the post-commit durable snapshot, using caller actor IDs, opening
// payload for metadata, or recomputing an already committed result breaks this test.
func TestQ0StoredFileProcessReplay(t *testing.T) {
	root := q0Enabled(t)
	schema, pool := q0Schema(t)
	ctx := context.Background()
	q0DB(t, pool, "W1-before")
	q0DB(t, pool, "W1-idle-start")
	time.Sleep(200 * time.Millisecond)
	q0DB(t, pool, "W1-idle-end")
	ml, addr := q0ML(t, "a")
	gateway, base := q0Gateway(t, schema, addr, "a")
	cookie := q0Login(t, base, "technologist")
	path := q0Path(q0Well)
	key := "q0_w1_original_request"
	body := q0Request{Action: "accept", Key: key, File: "w1.csv", Barrier: "input", Actor: 1}
	q0HTTP(t, base, path, body, nil, 401)
	q0HTTP(t, base, path, body, q0Login(t, base, "viewer"), 403)
	pending := q0Async(base, path, body, cookie)
	var accepted domain.FileInputSummary
	q0WaitJSON(t, filepath.Join(root, "evidence", "barrier-input.json"), &accepted)
	if accepted.PulseWellID != q0Well || accepted.ActorAccountID != 2 {
		t.Fatal("session actor/int64 identity lost")
	}
	repo := postgres.New(pool)
	stored, e := repo.StoredFileInput(ctx, accepted.ID)
	if e != nil {
		t.Fatal(e)
	}
	if stored.RawSHA256 != domain.FileSHA256(stored.Raw) || stored.CanonicalSHA256 != domain.FileSHA256(stored.Canonical) {
		t.Fatal("independent PG byte/hash mismatch")
	}
	if _, e = repo.StoredFileResult(ctx, accepted.ID); e == nil {
		t.Fatal("result exists before RPC")
	}
	if q0CountEvents(root, "ml-calls.jsonl", "features") != 0 || q0CountEvents(root, "ml-calls.jsonl", "predict") != 0 {
		t.Fatal("compute before input barrier")
	}
	// Canonical values are independently checked, not recomputed with normalizer.
	records, e := csv.NewReader(bytes.NewReader(stored.Canonical)).ReadAll()
	if e != nil {
		t.Fatal(e)
	}
	columns := map[string]int{}
	for i, c := range records[0] {
		columns[c] = i
	}
	if _, ok := columns["set_Annular_pressure"]; ok {
		t.Fatal("setpoint leaked")
	}
	if records[1][columns["R_izol"]] != "" || records[25][columns["F_out"]] != "0" || records[25][columns["Ia"]] != "0" {
		t.Fatal("marker or valid zero corrupted")
	}
	q0JSON(t, filepath.Join(root, "evidence", "W1-accepted.json"), accepted)
	q0DB(t, pool, "W1-barrier-input")
	gateway.finish(t, true)
	<-pending
	ml.finish(t, true)
	original, e := os.ReadFile(filepath.Join(root, "datasets", "w1.csv"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(root, "datasets", "w1.csv")); e != nil {
		t.Fatal(e)
	}
	defer os.WriteFile(filepath.Join(root, "datasets", "w1.csv"), original, 0600)
	ml, addr = q0ML(t, "b")
	gateway, base = q0Gateway(t, schema, addr, "b")
	cookie = q0Login(t, base, "technologist")
	pending = q0Async(base, path, q0Request{Action: "compute", ID: accepted.ID, Barrier: "result"}, cookie)
	var committed domain.StoredFileResult
	q0WaitJSON(t, filepath.Join(root, "evidence", "barrier-result.json"), &committed)
	observed, e := repo.StoredFileResult(ctx, accepted.ID)
	if e != nil {
		t.Fatal(e)
	}
	a, _ := json.Marshal(observed)
	b, _ := json.Marshal(committed)
	if !bytes.Equal(a, b) {
		t.Fatal("committed barrier not durable")
	}
	if committed.Result.Verdict != "norm" || committed.Result.LatestRisk != .1 || committed.Result.NWindows < 4 {
		t.Fatalf("actual fixed norm mismatch: %+v", committed.Result)
	}
	features := q0CountEvents(root, "ml-calls.jsonl", "features")
	predict := q0CountEvents(root, "ml-calls.jsonl", "predict")
	if features != 1 || predict != 1 {
		t.Fatalf("real feature/predict calls=%d/%d", features, predict)
	}
	q0DB(t, pool, "W1-barrier-result")
	gateway.finish(t, true)
	<-pending
	ml.finish(t, true)
	// New processes and new session; restore original bytes only for original request commitment.
	if e = os.WriteFile(filepath.Join(root, "datasets", "w1.csv"), original, 0600); e != nil {
		t.Fatal(e)
	}
	ml, addr = q0ML(t, "c")
	gateway, base = q0Gateway(t, schema, addr, "c")
	cookie = q0Login(t, base, "technologist")
	raw := q0HTTP(t, base, path, q0Request{Action: "accept", Key: key, File: "w1.csv", Actor: 1}, cookie, 200)
	var duplicate domain.FileInputSummary
	if e = json.Unmarshal(raw, &duplicate); e != nil {
		t.Fatal(e)
	}
	if duplicate.ID != accepted.ID || duplicate.FingerprintSHA256 != accepted.FingerprintSHA256 || duplicate.RawSHA256 != accepted.RawSHA256 || duplicate.CanonicalSHA256 != accepted.CanonicalSHA256 {
		t.Fatal("retry identity changed")
	}
	raw = q0HTTP(t, base, path, q0Request{Action: "compute", ID: accepted.ID}, cookie, 200)
	var replay domain.StoredFileResult
	if e = json.Unmarshal(raw, &replay); e != nil {
		t.Fatal(e)
	}
	b, _ = json.Marshal(replay)
	if !bytes.Equal(a, b) {
		t.Fatal("result replay changed bytes")
	}
	if q0CountEvents(root, "ml-calls.jsonl", "features") != features || q0CountEvents(root, "ml-calls.jsonl", "predict") != predict {
		t.Fatal("saved result recomputed")
	}
	for _, mutation := range []string{"raw", "mapping", "timezone"} {
		q0HTTP(t, base, path, q0Request{Action: "accept", Key: key, File: "w1.csv", Mutation: mutation}, cookie, 409)
	}
	q0HTTP(t, base, q0Path(q0Well+1), q0Request{Action: "accept", Key: key, File: "w1.csv"}, cookie, 409)
	q0HTTP(t, base, path, q0Request{Action: "summary", ID: accepted.ID}, q0Login(t, base, "sibling"), 404)
	q0HTTP(t, base, path, q0Request{Action: "compute", ID: accepted.ID}, q0Login(t, base, "viewer"), 403)
	q0HTTP(t, base, path, q0Request{Action: "result", ID: accepted.ID}, nil, 401)
	beforePayload := q0CountEvents(root, "go-events.jsonl", "payload_select")
	for _, action := range []string{"summary", "list", "result"} {
		q0HTTP(t, base, path, q0Request{Action: action, ID: accepted.ID}, cookie, 200)
	}
	if q0CountEvents(root, "go-events.jsonl", "payload_select") != beforePayload {
		t.Fatal("metadata selected payload")
	}
	for _, change := range []struct {
		query   string
		want    int
		restore string
	}{{`UPDATE auth_user SET scope='{"funds":["presentation"]}' WHERE id=2`, 404, `UPDATE auth_user SET scope='{"funds":["operational"]}' WHERE id=2`}, {"UPDATE well SET active=false WHERE id=9007199254740993", 404, "UPDATE well SET active=true WHERE id=9007199254740993"}, {"UPDATE auth_user SET active=false WHERE id=2", 401, "UPDATE auth_user SET active=true WHERE id=2"}} {
		if _, e = pool.Exec(ctx, change.query); e != nil {
			t.Fatal(e)
		}
		q0HTTP(t, base, path, q0Request{Action: "compute", ID: accepted.ID}, cookie, change.want)
		if _, e = pool.Exec(ctx, change.restore); e != nil {
			t.Fatal(e)
		}
		cookie = q0Login(t, base, "technologist")
	}
	// Insufficient running coverage must remain insufficient with the same fixed model.
	raw = q0HTTP(t, base, path, q0Request{Action: "accept", Key: "q0_w1_insufficient_request", File: "w1-insufficient.csv"}, cookie, 200)
	var insufficient domain.FileInputSummary
	if e = json.Unmarshal(raw, &insufficient); e != nil {
		t.Fatal(e)
	}
	raw = q0HTTP(t, base, path, q0Request{Action: "compute", ID: insufficient.ID}, cookie, 200)
	var ir domain.StoredFileResult
	if e = json.Unmarshal(raw, &ir); e != nil {
		t.Fatal(e)
	}
	if ir.Result.Verdict != "insufficient" {
		t.Fatal("absence presented as norm", ir.Result.Verdict)
	}
	// Revalidate a warmed registry: deletion cannot use the cached bundle or default.
	raw = q0HTTP(t, base, path, q0Request{Action: "accept", Key: "q0_w1_missing_bundle_request", File: "w1.csv"}, cookie, 200)
	var fault domain.FileInputSummary
	_ = json.Unmarshal(raw, &fault)
	bundle := filepath.Join(root, "registry", strings.TrimPrefix(accepted.ExecutionPin.BundleSHA256, "sha256:"))
	if e = os.Rename(bundle, bundle+"-held"); e != nil {
		t.Fatal(e)
	}
	func() {
		defer os.Rename(bundle+"-held", bundle)
		count := q0CountEvents(root, "ml-calls.jsonl", "features")
		q0HTTP(t, base, path, q0Request{Action: "compute", ID: fault.ID}, cookie, 503)
		if q0CountEvents(root, "ml-calls.jsonl", "features") != count {
			t.Fatal("missing warm bundle entered features")
		}
	}()
	if q0CountEvents(root, "ml-calls.jsonl", "legacy_loader") != 0 || q0CountEvents(root, "ml-calls.jsonl", "default_loader") != 0 {
		t.Fatal("mutable default loader used")
	}
	q0DB(t, pool, "W1-after")
	q0JSON(t, filepath.Join(root, "evidence", "W1-result.json"), map[string]any{"status": "PASS", "accepted_id": accepted.ID, "result_sha256": domain.FileSHA256(a), "features": q0CountEvents(root, "ml-calls.jsonl", "features"), "predict": q0CountEvents(root, "ml-calls.jsonl", "predict"), "norm": committed.Result, "insufficient": ir.Result, "metadata_payload_delta": 0, "two_sigkill_barriers": true})
	gateway.finish(t, true)
	ml.finish(t, true)
}

func q0Actor(t testing.TB, pool *pgxpool.Pool, login string) context.Context {
	t.Helper()
	var id int64
	if e := pool.QueryRow(context.Background(), "SELECT id FROM auth_user WHERE login=$1", login).Scan(&id); e != nil {
		t.Fatal(e)
	}
	a, e := postgres.New(pool).Account(context.Background(), id)
	if e != nil {
		t.Fatal(e)
	}
	return domain.WithPrincipal(context.Background(), domain.AccountPrincipal(a))
}
func q0Prepare(t testing.TB, checks *service.FileChecks, ctx context.Context, file, key string) (domain.PreparedFileInput, error) {
	t.Helper()
	f, e := os.Open(filepath.Join(os.Getenv("Q0_ROOT"), "datasets", file))
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	at := time.Now()
	v, e := checks.PrepareFileInput(ctx, q0Well, key, q0Spec(file), f)
	q0Event("resource_prepare", map[string]any{"file": file, "ms": float64(time.Since(at).Microseconds()) / 1000, "ok": e == nil, "reason": fmt.Sprint(e)})
	return v, e
}
func q0ResourceChild(t *testing.T, pool *pgxpool.Pool, mode string) {
	ctx := q0Actor(t, pool, "technologist")
	checks := q0Checks(t, pool, mode == "W4")
	root := os.Getenv("Q0_ROOT")
	if mode == "W5" || mode == "W5count" {
		q0Quota(t, pool, checks, ctx, mode)
		return
	}
	file, want := "", ""
	switch mode {
	case "W2":
		file = "w2-128.xlsx"
	case "W2columns":
		file = "w2-129.xlsx"
		want = "columns_limit"
	case "W3":
		file = "w3.csv"
	case "W3raw":
		file = "w3-over-raw.csv"
		want = "raw_limit"
	case "W3rows":
		file = "w3-over-rows.csv"
		want = "rows_limit"
	case "W4":
		file = "w4.csv"
		want = "canonical_limit"
	default:
		t.Fatal("unknown resource child")
	}
	q0Event("stage", map[string]string{"mode": mode, "stage": "prepare"})
	v, e := q0Prepare(t, checks, ctx, file, "q0_resource_"+mode+"_request")
	out := map[string]any{"status": "PASS", "expected_rejection": want, "error": fmt.Sprint(e)}
	if errors.Is(e, context.DeadlineExceeded) {
		q0JSON(t, filepath.Join(root, "evidence", mode+"-outcome.json"), map[string]any{"status": "STOP", "gate": "OPEN", "reason": "parse_deadline"})
		return
	}
	if want != "" {
		var de *domain.Error
		if !errors.As(e, &de) || de.Message != want {
			t.Fatalf("want %s got %v", want, e)
		}
	} else {
		if e != nil {
			t.Fatal(e)
		}
		q0Event("stage", map[string]string{"mode": mode, "stage": "accept"})
		summary, e := checks.AcceptFileInput(ctx, v)
		if e != nil {
			t.Fatal(e)
		}
		out["input"] = summary
		if mode == "W3" {
			path := filepath.Join(root, "prepared-w3.json")
			q0JSON(t, path, v)
		}
	}
	q0JSON(t, filepath.Join(root, "evidence", mode+"-outcome.json"), out)
}

// Re-keying is confined to a trusted server-prepared synthetic object. The
// current-account service wrapper still validates the bytes, config and identity.
func q0Rekey(t testing.TB, v domain.PreparedFileInput, actor domain.Account, n int) domain.PreparedFileInput {
	t.Helper()
	v.ID = fmt.Sprintf("%032x", n+1000)
	v.RequestKey = fmt.Sprintf("q0_quota_%024d", n)
	v.ActorAccountID = actor.ID
	v.Actor = domain.FileActorSnapshot{Name: actor.Name, Role: actor.Role, Revision: actor.Revision}
	spec := v.ParseSpec
	spec.SchemaSHA256 = ""
	spec.Mappings = append([]domain.Mapping(nil), spec.Mappings...)
	sort.Slice(spec.Mappings, func(i, j int) bool { return spec.Mappings[i].Field < spec.Mappings[j].Field })
	for i := range spec.Mappings {
		spec.Mappings[i].Unit = ""
		spec.Mappings[i].IsSet = false
		spec.Mappings[i].Missing = ""
	}
	clientJSON, _ := json.Marshal(spec)
	commitment, _ := json.Marshal(struct {
		Version     string
		Actor, Well int64
		Raw         string
		Spec        json.RawMessage
	}{"request_commitment_v1", actor.ID, v.PulseWellID, v.RawSHA256, clientJSON})
	v.RequestCommitmentSHA256 = domain.FileSHA256(commitment)
	identity, e := domain.FileInputIdentityJSON(v.FileInputSummary)
	if e != nil {
		t.Fatal(e)
	}
	v.IdentityJSON = identity
	v.FingerprintSHA256 = domain.FileSHA256(identity)
	if e = domain.ValidateFileInput(v); e != nil {
		t.Fatal(e)
	}
	return v
}

// q0QuotaRace requires evidence of the expected rejection, not just one winner.
func q0QuotaRace(outcomes []error) error {
	if len(outcomes) != 2 {
		return errors.New("quota_race_outcome_count")
	}
	winners, limited := 0, 0
	for _, err := range outcomes {
		if err == nil {
			winners++
			continue
		}
		var de *domain.Error
		if !errors.As(err, &de) || de.Kind != domain.Limited || de.Message != "file_input_quota" {
			return errors.New("quota_race_unexpected_error")
		}
		limited++
	}
	if winners != 1 || limited != 1 {
		return errors.New("quota_race_expected_one_winner_one_quota_rejection")
	}
	return nil
}

type q0OperationInterval struct {
	Client    int    `json:"client,omitempty"`
	Slot      int    `json:"slot,omitempty"`
	StartNS   int64  `json:"start_ns"`
	EndNS     int64  `json:"end_ns"`
	ErrorCode string `json:"error_code,omitempty"`
}
type q0MetadataWindow struct {
	OriginUTC             string                `json:"origin_utc"`
	DurationMS            int64                 `json:"duration_ms"`
	Clients               int                   `json:"clients"`
	RequestsPerSecondEach int                   `json:"requests_per_second_each"`
	ExpectedPerClient     int                   `json:"expected_per_client"`
	Samples               []q0OperationInterval `json:"samples"`
	Admissions            []q0OperationInterval `json:"admissions,omitempty"`
	SuccessfulSamples     int                   `json:"successful_samples"`
	Failures              int                   `json:"failures"`
	OverlapPerClient      [2]int                `json:"overlap_per_client"`
	P50MS                 float64               `json:"p50_ms"`
	P95MS                 float64               `json:"p95_ms"`
	MaxMS                 float64               `json:"max_ms"`
}

func q0SafeControlError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var de *domain.Error
	if errors.As(err, &de) {
		switch de.Kind {
		case domain.Invalid, domain.Forbidden, domain.Limited, domain.Missing, domain.Duplicate, domain.Unavailable, domain.Unauthorized:
			return string(de.Kind)
		}
	}
	return "unexpected_error"
}
func q0ValidateMetadataWindow(v *q0MetadataWindow, requireOverlap bool) error {
	if v.Clients != 2 || v.ExpectedPerClient < 1 {
		return errors.New("metadata_reader_configuration")
	}
	var counts [2]int
	var seen = map[[2]int]bool{}
	var successful []float64
	v.Failures = 0
	v.SuccessfulSamples = 0
	v.OverlapPerClient = [2]int{}
	for _, sample := range v.Samples {
		if sample.Client < 0 || sample.Client >= 2 || sample.Slot < 0 || sample.Slot >= v.ExpectedPerClient || seen[[2]int{sample.Client, sample.Slot}] || sample.EndNS < sample.StartNS {
			return errors.New("metadata_sample_identity")
		}
		seen[[2]int{sample.Client, sample.Slot}] = true
		counts[sample.Client]++
		if sample.ErrorCode != "" {
			v.Failures++
			continue
		}
		v.SuccessfulSamples++
		successful = append(successful, float64(sample.EndNS-sample.StartNS)/1e6)
		for _, admission := range v.Admissions {
			if admission.EndNS < admission.StartNS {
				return errors.New("admission_interval_invalid")
			}
			if sample.StartNS < admission.EndNS && admission.StartNS < sample.EndNS {
				v.OverlapPerClient[sample.Client]++
				break
			}
		}
	}
	if v.Failures != 0 {
		return errors.New("metadata_read_error")
	}
	for _, n := range counts {
		if n != v.ExpectedPerClient {
			return errors.New("metadata_sample_count")
		}
	}
	if requireOverlap && (len(v.Admissions) != 2 || v.OverlapPerClient[0] == 0 || v.OverlapPerClient[1] == 0) {
		return errors.New("metadata_admission_overlap_missing")
	}
	sort.Float64s(successful)
	if len(successful) > 0 {
		v.P50MS = successful[len(successful)/2]
		v.P95MS = successful[(len(successful)-1)*95/100]
		v.MaxMS = successful[len(successful)-1]
	}
	return nil
}

// Barriers establish readiness only. Timers begin immediately around the actual
// supplied read/admission calls, after readiness; no barrier wait is an interval.
func q0MeasureMetadataWindow(ctx context.Context, duration time.Duration, read func(context.Context, int) error, admit []func(context.Context) error) (q0MetadataWindow, []error, error) {
	const period = 200 * time.Millisecond
	out := q0MetadataWindow{DurationMS: duration.Milliseconds(), Clients: 2, RequestsPerSecondEach: 5, ExpectedPerClient: int(duration / period)}
	if duration < period || duration > 30*time.Second || read == nil || (len(admit) != 0 && len(admit) != 2) {
		return out, nil, errors.New("metadata_window_configuration")
	}
	type readerResult struct{ samples []q0OperationInterval }
	type admissionResult struct {
		interval q0OperationInterval
		err      error
	}
	readers := make(chan readerResult, 2)
	admissions := make(chan admissionResult, len(admit))
	ready := make(chan struct{}, 2+len(admit))
	start := make(chan struct{})
	var origin time.Time
	var windowCtx context.Context
	var cancel context.CancelFunc
	for client := 0; client < 2; client++ {
		go func(client int) {
			ready <- struct{}{}
			<-start
			batch := readerResult{}
			next := origin
			for slot := 0; slot < out.ExpectedPerClient; slot++ {
				timer := time.NewTimer(max(time.Until(next), 0))
				select {
				case <-windowCtx.Done():
					timer.Stop()
					readers <- batch
					return
				case <-timer.C:
				}
				at := time.Now()
				err := read(windowCtx, client)
				end := time.Now()
				batch.samples = append(batch.samples, q0OperationInterval{Client: client, Slot: slot, StartNS: at.Sub(origin).Nanoseconds(), EndNS: end.Sub(origin).Nanoseconds(), ErrorCode: q0SafeControlError(err)})
				// No catch-up bursts: dispatches stay at least200ms apart per client.
				next = at.Add(period)
			}
			readers <- batch
		}(client)
	}
	for index, call := range admit {
		go func(index int, call func(context.Context) error) {
			ready <- struct{}{}
			<-start
			at := time.Now()
			err := call(windowCtx)
			end := time.Now()
			admissions <- admissionResult{q0OperationInterval{Slot: index, StartNS: at.Sub(origin).Nanoseconds(), EndNS: end.Sub(origin).Nanoseconds(), ErrorCode: q0SafeControlError(err)}, err}
		}(index, call)
	}
	handshake := time.NewTimer(time.Second)
	for n := 0; n < 2+len(admit); n++ {
		select {
		case <-ctx.Done():
			handshake.Stop()
			windowCtx, cancel = context.WithCancel(ctx)
			cancel()
			close(start)
			return out, nil, errors.New("metadata_readiness_cancelled")
		case <-handshake.C:
			windowCtx, cancel = context.WithCancel(ctx)
			cancel()
			close(start)
			return out, nil, errors.New("metadata_readiness_deadline")
		case <-ready:
		}
	}
	handshake.Stop()
	windowCtx, cancel = context.WithTimeout(ctx, duration)
	defer cancel()
	origin = time.Now()
	out.OriginUTC = origin.UTC().Format(time.RFC3339Nano)
	close(start)
	// Bounded join; accepted PG callbacks receive the window deadline. A hung
	// callback cannot turn partial samples into a PASS or grow a pending queue.
	join := time.NewTimer(duration + time.Second)
	defer join.Stop()
	outcomes := make([]error, len(admit))
	readDone, admitDone := 0, 0
	for readDone < 2 || admitDone < len(admit) {
		select {
		case batch := <-readers:
			out.Samples = append(out.Samples, batch.samples...)
			readDone++
		case result := <-admissions:
			out.Admissions = append(out.Admissions, result.interval)
			outcomes[result.interval.Slot] = result.err
			admitDone++
		case <-join.C:
			return out, outcomes, errors.New("metadata_window_deadline")
		}
	}
	sort.Slice(out.Samples, func(i, j int) bool { return out.Samples[i].StartNS < out.Samples[j].StartNS })
	sort.Slice(out.Admissions, func(i, j int) bool { return out.Admissions[i].Slot < out.Admissions[j].Slot })
	err := q0ValidateMetadataWindow(&out, len(admit) > 0)
	return out, outcomes, err
}
func q0Latency(t testing.TB, checks *service.FileChecks, ctx context.Context, id string, duration time.Duration, admit []func(context.Context) error) (q0MetadataWindow, []error) {
	t.Helper()
	out, outcomes, err := q0MeasureMetadataWindow(ctx, duration, func(ctx context.Context, _ int) error {
		if id != "" {
			_, err := checks.FileInputSummary(ctx, id)
			return err
		}
		_, err := checks.FileInputSummaries(ctx, q0Well, "")
		return err
	}, admit)
	if err != nil {
		q0Event("metadata_window_rejected", out)
		t.Fatal("GATE OPEN: ", err)
	}
	return out, outcomes
}

func q0Quota(t *testing.T, pool *pgxpool.Pool, checks *service.FileChecks, ctx context.Context, mode string) {
	root := os.Getenv("Q0_ROOT")
	repo := postgres.New(pool)
	var prepared domain.PreparedFileInput
	var e error
	if mode == "W5count" {
		prepared, e = q0Prepare(t, checks, ctx, "w1.csv", "q0_quota_small_prepare")
		if e != nil {
			t.Fatal(e)
		}
	} else {
		q0Read(t, filepath.Join(root, "prepared-w3.json"), &prepared)
	}
	accounts := []domain.Account{}
	for _, login := range []string{"technologist", "sibling", "quota"} {
		var id int64
		if e = pool.QueryRow(ctx, "SELECT id FROM auth_user WHERE login=$1", login).Scan(&id); e != nil {
			t.Fatal(e)
		}
		a, e := repo.Account(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		accounts = append(accounts, a)
	}
	beforePayload := q0CountEvents(root, "go-events.jsonl", "payload_select")
	baseline, _ := q0Latency(t, checks, ctx, "", time.Second, nil)
	admit := func(n, actor int) (domain.FileInputSummary, error) {
		v := q0Rekey(t, prepared, accounts[actor], n)
		return checks.AcceptFileInput(domain.WithPrincipal(context.Background(), domain.AccountPrincipal(accounts[actor])), v)
	}
	var latency q0MetadataWindow
	var first domain.FileInputSummary
	if mode == "W5count" {
		for i := 0; i < 16; i++ {
			first, e = admit(i, 0)
			if e != nil {
				t.Fatal(e)
			}
		}
		if _, e = admit(16, 0); e == nil {
			t.Fatal("17th retained input accepted")
		}
		latency, _ = q0Latency(t, checks, ctx, first.ID, time.Second, nil)
	} else {
		for i := 0; i < 6; i++ {
			actor := i / 3
			first, e = admit(i, actor)
			if e != nil {
				t.Fatal(e)
			}
		}
		if _, e = admit(99, 0); e == nil {
			t.Fatal("per-account quota not enforced")
		}
		// Prepare/re-key outside the measured admission interval and outside workers.
		// No t.Fatal or fixture generation runs in an admission goroutine.
		var raceCalls []func(context.Context) error
		for n := 6; n < 8; n++ {
			input := q0Rekey(t, prepared, accounts[2], n)
			raceCalls = append(raceCalls, func(ctx context.Context) error {
				_, err := checks.AcceptFileInput(domain.WithPrincipal(ctx, domain.AccountPrincipal(accounts[2])), input)
				return err
			})
		}
		var outcomes []error
		latency, outcomes = q0Latency(t, checks, ctx, "", 3*time.Second, raceCalls)
		if err := q0QuotaRace(outcomes); err != nil {
			t.Fatal(err)
		}

		if _, e = admit(0, 0); e != nil {
			t.Fatal("duplicate charged", e)
		}
	}
	var count, charged, actual int64
	if e = pool.QueryRow(ctx, "SELECT retained_count,retained_bytes FROM file_input_budget WHERE budget_key='global'").Scan(&count, &charged); e != nil {
		t.Fatal(e)
	}
	if e = pool.QueryRow(ctx, "SELECT sum(raw_length+canonical_length+octet_length(parse_spec::text)-0+octet_length(parse_identity_bytes)) FROM file_input").Scan(&actual); e != nil {
		t.Fatal(e)
	} // JSONB spaces differ: exact charge is checked using each compact Go spec below.
	rows, e := pool.Query(ctx, "SELECT id FROM file_input")
	if e != nil {
		t.Fatal(e)
	}
	var ids []string
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	var expected int64
	for _, id := range ids {
		s, e := repo.FileInputSummary(ctx, id)
		if e != nil {
			t.Fatal(e)
		}
		spec, _ := json.Marshal(s.ParseSpec)
		identity, _ := domain.FileInputIdentityJSON(s)
		expected += s.RawBytes + s.CanonicalBytes + int64(len(spec)+len(identity))
	}
	if charged != expected || count != int64(len(ids)) {
		t.Fatal("quota drift", count, charged, expected)
	}
	afterPayload := q0CountEvents(root, "go-events.jsonl", "payload_select")
	if afterPayload != beforePayload {
		t.Fatal("metadata opened payload")
	}
	q0JSON(t, filepath.Join(root, "evidence", mode+"-outcome.json"), map[string]any{"status": "PASS", "inputs": count, "charged_bytes": charged, "baseline": baseline, "concurrent_metadata": latency, "payload_select_delta": afterPayload - beforePayload, "normalization_excluded": mode == "W5", "trusted_rekey_validated_by_service": true})
}

func TestQ0StoredFileResourceCaps(t *testing.T) {
	root := q0Enabled(t)
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{"W2", "W2columns", "W3raw", "W3rows", "W3", "W4", "W5", "W5count"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "W5" {
				if _, e := os.Stat(filepath.Join(root, "prepared-w3.json")); e != nil {
					q0JSON(t, filepath.Join(root, "evidence", mode+"-outcome.json"), map[string]any{"status": "OPEN", "gate": "W3_server_prepared_fixture_missing"})
					t.Error("GATE OPEN: W5 requires accepted W3 prepared fixture")
					return
				}
			}
			schema, pool := q0Schema(t)
			q0DB(t, pool, mode+"-before")
			features := q0CountEvents(root, "ml-calls.jsonl", "features")
			p := q0Start(t, "resource-"+mode, []string{exe, "-test.run=^TestQ0FileProcessChild$", "-test.v"}, []string{"Q0_CHILD=" + mode, "Q0_SCHEMA=" + schema, "Q0_ML_ADDR=127.0.0.1:1", "TMPDIR=" + filepath.Join(root, "parser-temp")}, 1<<30, 40*time.Second)
			exit := p.finish(t, false)
			q0DB(t, pool, mode+"-after")
			if q0CountEvents(root, "ml-calls.jsonl", "features") != features {
				t.Fatal("large workload executed ML")
			}
			var measured struct {
				Reason string `json:"stop_reason"`
			}
			q0Read(t, filepath.Join(root, "evidence", "resource-"+mode+"-process.json"), &measured)
			if measured.Reason != "" {
				q0JSON(t, filepath.Join(root, "evidence", mode+"-outcome.json"), map[string]any{"status": "STOP", "gate": "OPEN", "reason": measured.Reason})
				t.Errorf("STOP / gate OPEN: %s", measured.Reason)
				return
			}
			if exit != nil {
				t.Fatalf("resource child %v; inspect safe child log", exit)
			}
			var outcome map[string]any
			q0Read(t, filepath.Join(root, "evidence", mode+"-outcome.json"), &outcome)
			if outcome["status"] != "PASS" {
				t.Errorf("case outcome %v / gate OPEN", outcome["status"])
			} else {
				t.Logf("actual outcome %v", outcome["status"])
			}
		})
	}
}

// Fault fixtures are confined to disposable schemas; accepted positive snapshots
// and production guards are never altered.
func TestQ0StoredFileProcessFaults(t *testing.T) {
	root := q0Enabled(t)
	ml, addr := q0ML(t, "faults")
	defer ml.finish(t, true)
	for _, fault := range []string{"missing", "corrupt", "policy", "bundle"} {
		t.Run(fault, func(t *testing.T) {
			schema, pool := q0Schema(t)
			t.Setenv("Q0_TEST_PIN_FAULT", "")
			if fault == "policy" || fault == "bundle" {
				t.Setenv("Q0_TEST_PIN_FAULT", fault)
			}
			gateway, base := q0Gateway(t, schema, addr, "fault-"+fault)
			defer gateway.finish(t, true)
			cookie := q0Login(t, base, "technologist")
			path := q0Path(q0Well)
			raw := q0HTTP(t, base, path, q0Request{Action: "accept", Key: "q0_fault_" + fault + "_request", File: "w1.csv"}, cookie, 200)
			var input domain.FileInputSummary
			if e := json.Unmarshal(raw, &input); e != nil {
				t.Fatal(e)
			}
			before := q0CountEvents(root, "ml-calls.jsonl", "features")
			ctx := context.Background()
			q0DB(t, pool, "fault-"+fault+"-before")
			if fault == "missing" || fault == "corrupt" {
				if _, e := pool.Exec(ctx, "ALTER TABLE file_input_payload DISABLE TRIGGER file_input_immutable"); e != nil {
					t.Fatal(e)
				}
				func() {
					defer func() {
						if _, e := pool.Exec(ctx, "ALTER TABLE file_input_payload ENABLE TRIGGER file_input_immutable"); e != nil {
							t.Fatal(e)
						}
					}()
					query := "DELETE FROM file_input_payload WHERE input_id=$1"
					if fault == "corrupt" {
						query = "UPDATE file_input_payload SET raw_bytes=set_byte(raw_bytes,0,0) WHERE input_id=$1"
					}
					if _, e := pool.Exec(ctx, query, input.ID); e != nil {
						t.Fatal(e)
					}
				}()
			}
			rejection := q0HTTP(t, base, path, q0Request{Action: "compute", ID: input.ID}, cookie, 503)
			reason := map[string]string{"missing": "input_unavailable", "corrupt": "input_corrupt", "policy": "policy_mismatch", "bundle": "bundle_unavailable"}[fault]
			if !bytes.Contains(rejection, []byte(reason)) {
				t.Fatalf("safe rejection mismatch %s", rejection)
			}
			if q0CountEvents(root, "ml-calls.jsonl", "features") != before {
				t.Fatal("fault entered real feature pipeline")
			}
			if q0CountEvents(root, "ml-calls.jsonl", "legacy_loader") != 0 || q0CountEvents(root, "ml-calls.jsonl", "default_loader") != 0 {
				t.Fatal("fault fell back to mutable loader")
			}
			var results int64
			if e := pool.QueryRow(ctx, "SELECT count(*) FROM file_check_result").Scan(&results); e != nil || results != 0 {
				t.Fatal("fault committed result", e)
			}
			q0DB(t, pool, "fault-"+fault+"-after")
			q0JSON(t, filepath.Join(root, "evidence", "fault-"+fault+"-outcome.json"), map[string]any{"status": "PASS", "reason": reason, "feature_delta": 0, "result_count": 0, "fixture_schema_disposable": true, "guards_restored": true})
		})
	}
}

func TestQ0W5QuotaRaceControls(t *testing.T) {
	quota := domain.NewError(domain.Limited, "file_input_quota")
	for _, tc := range []struct {
		name      string
		outcomes  []error
		wantError bool
	}{
		{"expected quota loser", []error{nil, quota}, false},
		{"unexpected storage loser", []error{nil, errors.New("private storage details")}, true},
		{"wrong limited reason", []error{nil, domain.NewError(domain.Limited, "normalization_busy")}, true},
		{"two winners", []error{nil, nil}, true},
		{"no winner", []error{quota, quota}, true},
		{"missing outcome", []error{nil}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := q0QuotaRace(tc.outcomes)
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v want_error=%v", err, tc.wantError)
			}
		})
	}
}
func TestQ0W5MetadataIntervalControls(t *testing.T) {
	for _, tc := range []struct {
		name       string
		samples    []q0OperationInterval
		admissions []q0OperationInterval
		wantError  string
	}{
		{"real interval overlap", []q0OperationInterval{{Client: 0, StartNS: 10, EndNS: 20}, {Client: 1, StartNS: 12, EndNS: 18}}, []q0OperationInterval{{StartNS: 15, EndNS: 30}, {StartNS: 16, EndNS: 25}}, ""},
		{"reads after admissions", []q0OperationInterval{{Client: 0, StartNS: 30, EndNS: 40}, {Client: 1, StartNS: 32, EndNS: 38}}, []q0OperationInterval{{StartNS: 10, EndNS: 20}, {StartNS: 11, EndNS: 21}}, "metadata_admission_overlap_missing"},
		{"read failure", []q0OperationInterval{{Client: 0, StartNS: 10, EndNS: 20, ErrorCode: "unexpected_error"}, {Client: 1, StartNS: 12, EndNS: 18}}, nil, "metadata_read_error"},
		{"reader missing", []q0OperationInterval{{Client: 0, StartNS: 10, EndNS: 20}}, nil, "metadata_sample_count"},
		{"duplicate slot", []q0OperationInterval{{Client: 0, StartNS: 10, EndNS: 20}, {Client: 0, StartNS: 12, EndNS: 18}}, nil, "metadata_sample_identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := q0MetadataWindow{Clients: 2, ExpectedPerClient: 1, Samples: tc.samples, Admissions: tc.admissions}
			err := q0ValidateMetadataWindow(&v, len(tc.admissions) > 0)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != tc.wantError {
				t.Fatalf("got=%q want=%q", got, tc.wantError)
			}
		})
	}
}
func TestQ0W5MetadataWindowControls(t *testing.T) {
	t.Run("successful bounded reads", func(t *testing.T) {
		v, _, err := q0MeasureMetadataWindow(context.Background(), 400*time.Millisecond, func(context.Context, int) error { return nil }, nil)
		if err != nil || v.SuccessfulSamples != 4 || v.Failures != 0 {
			t.Fatal(v, err)
		}
		for client := 0; client < 2; client++ {
			var starts []int64
			for _, s := range v.Samples {
				if s.Client == client {
					starts = append(starts, s.StartNS)
				}
			}
			if starts[1]-starts[0] < int64(200*time.Millisecond) {
				t.Fatal("reader rate exceeded")
			}
		}
	})
	t.Run("all read failures reject", func(t *testing.T) {
		v, _, err := q0MeasureMetadataWindow(context.Background(), 200*time.Millisecond, func(context.Context, int) error { return errors.New("private reader failure") }, nil)
		if err == nil || err.Error() != "metadata_read_error" || v.Failures != 2 || v.SuccessfulSamples != 0 {
			t.Fatal(v, err)
		}
	})
	t.Run("actual call overlap", func(t *testing.T) {
		entered := make(chan struct{}, 2)
		release := make(chan struct{})
		var once sync.Once
		read := func(ctx context.Context, _ int) error {
			entered <- struct{}{}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		admission := func(ctx context.Context) error {
			for n := 0; n < 2; n++ {
				select {
				case <-entered:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			once.Do(func() { close(release) })
			return nil
		}
		second := func(context.Context) error { return domain.NewError(domain.Limited, "file_input_quota") }
		v, outcomes, err := q0MeasureMetadataWindow(context.Background(), 200*time.Millisecond, read, []func(context.Context) error{admission, second})
		if err != nil || q0QuotaRace(outcomes) != nil || v.OverlapPerClient[0] == 0 || v.OverlapPerClient[1] == 0 {
			t.Fatal(v, outcomes, err)
		}
	})
}

// Explicit negative controls are selected only in the private source test
// command; each must exit nonzero. They never open PG or run a workload.
func TestQ0W5ForcedFailureControl(t *testing.T) {
	switch os.Getenv("Q0_W5_FORCE_CONTROL") {
	case "race":
		if err := q0QuotaRace([]error{nil, errors.New("private loser")}); err != nil {
			t.Fatal(err)
		}
	case "reader":
		_, _, err := q0MeasureMetadataWindow(context.Background(), 200*time.Millisecond, func(context.Context, int) error { return errors.New("private read error") }, nil)
		if err != nil {
			t.Fatal(err)
		}
	case "overlap":
		v := q0MetadataWindow{Clients: 2, ExpectedPerClient: 1, Samples: []q0OperationInterval{{Client: 0, StartNS: 30, EndNS: 40}, {Client: 1, StartNS: 32, EndNS: 38}}, Admissions: []q0OperationInterval{{StartNS: 10, EndNS: 20}, {StartNS: 11, EndNS: 21}}}
		if err := q0ValidateMetadataWindow(&v, true); err != nil {
			t.Fatal(err)
		}
	case "samples":
		v := q0MetadataWindow{Clients: 2, ExpectedPerClient: 1, Samples: []q0OperationInterval{{Client: 0, StartNS: 10, EndNS: 20}}}
		if err := q0ValidateMetadataWindow(&v, false); err != nil {
			t.Fatal(err)
		}
	default:
		t.Skip("explicit private negative control only")
	}
}
