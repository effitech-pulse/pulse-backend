package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/config"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
	"github.com/gin-gonic/gin"
)

func TestRBACWorkerHTTPProjectionViewer(t *testing.T) {
	for _, prefix := range []string{"filechecks", "cohortjobs", "collectionjobs"} {
		t.Run(prefix, func(t *testing.T) {
			keys := []domain.Permission{"wells.read", domain.Permission(prefix + ".read")}
			if prefix == "cohortjobs" {
				keys = append(keys, "cohort.runs.read", "risks.read")
			}
			if prefix == "collectionjobs" {
				keys = append(keys, "integrations.read")
			}
			id := int64(1)
			p := domain.Principal{AccountID: &id, Authenticated: true, Mechanism: "session", Role: "viewer", Authorization: &domain.AuthorizationSnapshot{Active: true, AccountID: 1, Epoch: 1, Permissions: keys}}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/status", nil).WithContext(domain.WithRBACMode(domain.WithPrincipal(context.Background(), p), domain.RBACEnforce))
			s := &Server{Config: config.Config{CohortJobMode: "synthetic", CollectionJobMode: "synthetic"}, CohortJobs: &service.CohortJobs{}, CollectionJobs: &service.CollectionJobs{}}
			var err error
			switch prefix {
			case "filechecks":
				err = fileJobSession(c)
			case "cohortjobs":
				err = s.cohortJobReady(c)
			case "collectionjobs":
				err = s.collectionJobReady(c)
			}
			if err != nil {
				t.Fatal("custom role blocked by compatibility projection", err)
			}
			p.Authorization.Permissions = []domain.Permission{}
			switch prefix {
			case "filechecks":
				err = fileJobSession(c)
			case "cohortjobs":
				err = s.cohortJobReady(c)
			case "collectionjobs":
				err = s.collectionJobReady(c)
			}
			if err == nil {
				t.Fatal("missing operation permission accepted")
			}
		})
	}
}

func TestRBACCapabilitiesEmptyRoleProfile(t *testing.T) {
	id := int64(1)
	principal := domain.Principal{AccountID: &id, Authenticated: true, Mechanism: "session", Role: "viewer", Authorization: &domain.AuthorizationSnapshot{Active: true, AccountID: 1, Epoch: 1, Permissions: []domain.Permission{}}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/v1/file-check-capabilities", nil).WithContext(domain.WithRBACMode(domain.WithPrincipal(context.Background(), principal), domain.RBACEnforce))
	s := &Server{Config: config.Config{FileCheckMode: "off"}}
	if err := s.fileCheckCapabilities(c); err != nil {
		t.Fatal("empty-role active profile cannot read disabled capability", err)
	}
}
