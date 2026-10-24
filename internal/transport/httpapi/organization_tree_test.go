package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestOrganizationTreeStrictQuery(t *testing.T) {
	for _, raw := range []string{"limit=25", "cursor=", "search=a&search=b", "include_inactive=true&include_inactive=false", "include_inactive=1", "include_inactive=", "search=%GG", "search=%FF", "search=a;b", "scope=all", "search=" + strings.Repeat("я", 201)} {
		if _, e := organizationTreeQuery(raw); e == nil {
			t.Fatal("invalid tree query accepted", raw)
		}
	}
	q, e := organizationTreeQuery("search=%20%D0%90%3B%25_%5C%2B%26%20&include_inactive=true")
	if e != nil || q.Search != `А;%_\+&` || !q.IncludeInactive {
		t.Fatal(q, e)
	}
	if q, e := organizationTreeQuery("search="); e != nil || q.Search != "" || q.IncludeInactive {
		t.Fatal(q, e)
	}
}
func TestOrganizationTreeResponseBounds(t *testing.T) {
	tree := service.BuildOrganizationTree([]domain.OrganizationTreePerson{{ID: 1, Name: "", Active: true}})
	base, _ := json.Marshal(tree)
	for _, extra := range []int{0, 1} {
		tree.Organizations[0].Divisions[0].Departments[0].Employees[0].Name = strings.Repeat("x", domain.OrganizationTreeResponseBytes-len(base)-1+extra)
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		err := writeOrganizationTree(c, tree)
		if extra == 0 {
			if err != nil || recorder.Code != 200 || recorder.Body.Len() != domain.OrganizationTreeResponseBytes || recorder.Body.Bytes()[recorder.Body.Len()-1] != '\n' {
				t.Fatal(recorder.Code, recorder.Body.Len(), err)
			}
		} else {
			if recorder.Body.Len() != 0 || c.Writer.Written() {
				t.Fatal("partial response before overflow error")
			}
			writeError(c, err)
			if recorder.Code != 503 || recorder.Body.String() != `{"code":"external_service_error","message":"organization_tree_too_large","detail":null}`+"\n" {
				t.Fatal(recorder.Code, recorder.Body.String())
			}
		}
	}
}

type organizationTreeDeadlineStore struct {
	service.AccessStore
	service.EmployeeProfileStore
}

func (s *organizationTreeDeadlineStore) OrganizationTreePeople(context.Context, int64, domain.OrganizationTreeQuery) ([]domain.OrganizationTreePerson, error) {
	return nil, context.DeadlineExceeded
}
func TestOrganizationTreeDeadline(t *testing.T) {
	access, e := service.NewAccess(&organizationTreeDeadlineStore{}, true)
	if e != nil {
		t.Fatal(e)
	}
	id := int64(1)
	request := httptest.NewRequest("GET", "/api/v1/people/organization/tree", nil)
	request = request.WithContext(domain.WithPrincipal(request.Context(), domain.Principal{Authenticated: true, Mechanism: "session", AccountID: &id}))
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = request
	s := &Server{Access: access}
	s.wrap(s.organizationTree)(c)
	if recorder.Code != 503 || recorder.Body.String() != `{"code":"external_service_error","message":"organization_tree_timeout","detail":null}`+"\n" || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
}

type organizationTreeOverflowStore struct {
	service.AccessStore
	service.EmployeeProfileStore
}

func (s *organizationTreeOverflowStore) OrganizationTreePeople(context.Context, int64, domain.OrganizationTreeQuery) ([]domain.OrganizationTreePerson, error) {
	return make([]domain.OrganizationTreePerson, 5001), nil
}
func TestOrganizationTreeEmployeeOverflowHTTP(t *testing.T) {
	access, e := service.NewAccess(&organizationTreeOverflowStore{}, true)
	if e != nil {
		t.Fatal(e)
	}
	id := int64(1)
	request := httptest.NewRequest("GET", "/api/v1/people/organization/tree", nil)
	request = request.WithContext(domain.WithPrincipal(request.Context(), domain.Principal{Authenticated: true, Mechanism: "session", AccountID: &id}))
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = request
	s := &Server{Access: access}
	s.wrap(s.organizationTree)(c)
	if recorder.Code != 503 || recorder.Body.String() != `{"code":"external_service_error","message":"organization_tree_too_large","detail":null}`+"\n" || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(recorder.Code, recorder.Body.String())
	}
}
