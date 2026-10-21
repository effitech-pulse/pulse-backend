package integration

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestFileJobOldBinaryCannotSeePrivateResultOrReview(t *testing.T) {
	binary := os.Getenv("Q1_LEGACY_API_BINARY")
	if binary == "" {
		t.Skip("explicit immutable legacy binary required")
	}
	pool, ctx, store, checks, v := fileJobSetup(t)
	if _, e := store.AcceptFileJob(ctx, v); e != nil {
		t.Fatal(e)
	}
	lease, e := store.ClaimFileJob(context.Background(), fileJobMessage(t, pool, v.Input.ID), strings.Repeat("c", 32))
	if e != nil {
		t.Fatal(e)
	}
	result, e := checks.ComputeStoredInput(ctx, v.Input.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.FinishFileJob(context.Background(), *lease, &result, nil); e != nil {
		t.Fatal(e)
	}
	well := int64(1)
	if _, e = service.NewReviews(store).Update(ctx, "file_check", v.Input.ID, &well, domain.ReviewInput{RequestID: "private-review-0001", Status: "in_progress", Owner: "private-q1-owner", Comment: "private-q1-comment"}, domain.ReviewActor{ID: "1", Name: "synthetic", Authenticated: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO manual_check(id,well_id,request_id,start_at,end_at,created_at,input_sha256,points_count,resolution,result) VALUES('legacy-check',1,'legacy-request','2026-01-01','2026-02-01',now(),'legacy',1,'raw','{"verdict":"watch"}');INSERT INTO case_review(kind,target_id,check_id,well_id,state,revision) VALUES('check','legacy-check','legacy-check',1,'{"revision":0,"status":"new","owner":"","conclusion":""}',0)`); e != nil {
		t.Fatal(e)
	}
	dsn, e := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if e != nil {
		t.Fatal(e)
	}
	q := dsn.Query()
	q.Set("search_path", pool.Config().ConnConfig.RuntimeParams["search_path"])
	dsn.RawQuery = q.Encode()
	addr := os.Getenv("Q1_LEGACY_API_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8022"
	}
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "DATABASE_URL="+dsn.String(), "HTTP_ADDR="+addr, "AUTH_MODE=legacy", "APP_SCOPE=research", "API_KEY=q1-legacy-test-key", "AUTO_RUN=false", "PULSE_OBSERVABILITY_ENABLED=false", "ML_SERVICE_TARGET=127.0.0.1:59999")
	var logs bytes.Buffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		response, e := client.Get("http://" + addr + "/health")
		if e == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		t.Fatal("legacy binary failed startup")
	}
	request := func(method, path, body string, want int) string {
		t.Helper()
		req, e := http.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("X-API-Key", "q1-legacy-test-key")
		req.Header.Set("Content-Type", "application/json")
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		data, e := io.ReadAll(resp.Body)
		if e != nil || resp.StatusCode != want {
			t.Fatalf("legacy %s %s status%d want%d error%v", method, path, resp.StatusCode, want, e)
		}
		for _, private := range []string{v.Input.ID, "q1_" + v.Input.ID, "private-q1-owner", "private-q1-comment", "q0-fixed-model"} {
			if strings.Contains(string(data), private) {
				t.Fatal("legacy private leak", path)
			}
		}
		return string(data)
	}
	list := request("GET", "/api/v1/wells/1/checks", "", 200)
	if !strings.Contains(list, "legacy-check") {
		t.Fatal("legacy data lost")
	}
	request("GET", "/api/v1/wells/1/checks/legacy-check", "", 200)
	request("GET", "/api/v1/wells/1/checks/legacy-check/review", "", 200)
	request("PUT", "/api/v1/wells/1/checks/legacy-check/review", `{"request_id":"legacy-update-0001","revision":0,"status":"in_progress","owner":"legacy-owner","comment":"legacy update"}`, 200)
	request("GET", "/api/v1/presentation/summary", "", 200)
	request("GET", "/api/v1/wells/1/checks/"+v.Input.ID, "", 404)
	request("GET", "/api/v1/wells/1/checks/q1_"+v.Input.ID+"/review", "", 404)
	request("GET", "/api/v1/wells/1/file-check-jobs/"+v.Input.ID, "", 404)
	request("GET", "/api/v1/wells/1/file-check-jobs/"+v.Input.ID+"/result", "", 404)
	request("GET", "/api/v1/wells/1/file-check-jobs/"+v.Input.ID+"/review", "", 404)
	t.Log("immutable pre-Q1 binary legacy read/write, review, presentation and private route404 proof passed")
}
