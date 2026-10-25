package httpapi

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAccessTableHTTPRejectsLegacyMix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, suffix := range []string{"&search=", "&q=old", "&limit=25", "&before=1", "&page_size=0", "&graph_revision=01"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/api/v1/auth/users?table_query="+url.QueryEscape(`{"version":1,"filters":[],"sort":[]}`)+suffix, nil)
		if _, e := accessQuery(c); e == nil {
			t.Fatalf("accepted %s", suffix)
		}
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/v1/auth/users?table_query="+url.QueryEscape(`{"version":1,"filters":[],"sort":[]}`), nil)
	q, e := accessQuery(c)
	if e != nil || q.Table == nil || q.Table.PageSize != 25 {
		t.Fatalf("query mode default: %+v %v", q, e)
	}
	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/v1/auth/users?search=old&limit=7", nil)
	q, e = accessQuery(c)
	if e != nil || q.Table != nil || q.Search != "old" || q.Limit != 7 {
		t.Fatalf("legacy: %+v %v", q, e)
	}
}
