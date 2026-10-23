package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

type collectionHTTPStore struct{ service.CollectionJobStore }

func (collectionHTTPStore) CollectionJobStatus(context.Context, string) (domain.CollectionJobStatus, error) {
	return domain.CollectionJobStatus{ID: strings.Repeat("a", 32), State: domain.CollectionPendingDispatch}, nil
}
func TestCollectionHTTPReplayReadCancelSecurity(t *testing.T) {
	jobs, _ := service.NewCollectionJobs(collectionHTTPStore{}, false)
	s := &Server{Config: config.Config{Prefix: "/api/v1", CollectionJobMode: "off"}, CollectionJobs: jobs}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/collection-jobs/"+strings.Repeat("a", 32), nil))
	if w.Code != 401 {
		t.Fatalf("session boundary %d", w.Code)
	}
	id := int64(1)
	for _, method := range []string{"GET", "POST"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(method, "/api/v1/collection-jobs/"+strings.Repeat("a", 32), strings.NewReader(`{}`))
		c.Request = c.Request.WithContext(domain.WithPrincipal(c.Request.Context(), domain.TrustedCompatibilityPrincipal(domain.Principal{AccountID: &id, Authenticated: true, Mechanism: "session", Role: "admin"})))
		c.Params = gin.Params{{Key: "job_id", Value: strings.Repeat("a", 32)}}
		var e error
		if method == "GET" {
			e = s.getCollectionJob(c)
		} else {
			e = s.submitCollectionJob(c)
		}
		if e != nil {
			writeError(c, e)
		}
		want := 200
		if method == "POST" {
			want = 503
		}
		if w.Code != want {
			t.Fatalf("off %s: %d %s", method, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "0001-") {
			t.Fatal("zero timestamp exposed")
		}
	}
}

func TestCollectionHTTPStrictBodiesAndPrivacy(t *testing.T) {
	jobs, _ := service.NewCollectionJobs(collectionHTTPStore{}, true)
	s := &Server{Config: config.Config{CollectionJobMode: "synthetic"}, CollectionJobs: jobs}
	account := int64(1)
	for _, tc := range []struct {
		name, body string
		limit      int64
	}{{"unknown", `{"revision":1,"raw":"secret"}`, 128}, {"duplicate", `{"revision":1,"revision":2}`, 128}, {"extra", `{"revision":1}{}`, 128}, {"oversize", strings.Repeat(" ", 128) + `{"revision":1}`, 128}, {"null", `null`, 128}} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/collection-jobs/id/cancel", strings.NewReader(tc.body))
			var value domain.CollectionRevision
			if e := readCollectionJSON(c, &value, tc.limit); e == nil {
				t.Fatal("unsafe body accepted")
			}
		})
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/collection-jobs/id", nil)
	c.Request = c.Request.WithContext(domain.WithPrincipal(c.Request.Context(), domain.Principal{AccountID: &account, Authenticated: true, Mechanism: "api_key", Role: "admin"}))
	if e := s.collectionJobReady(c); e == nil {
		t.Fatal("legacy API key accepted")
	}
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/collection-jobs/id", nil)
	e := writeCollectionError(c, domain.NewError(domain.Unavailable, "postgres://private-secret"))
	writeError(c, e)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private-secret") {
		t.Fatal("adapter detail leaked", w.Code, w.Body.String())
	}
}
