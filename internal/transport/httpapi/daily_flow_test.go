package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestDailyFlowStrictQuery(t *testing.T) {
	for _, query := range []string{"start_date=", "start_date=2026-10-24&start%5Fdate=2026-10-23", "seed=x", "start_date=2026-10-24", "dataset_version=x&dataset_version=y", "x=%zz"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/?"+query, nil)
		if _, e := dailyFlowQuery(c, false); e == nil {
			t.Fatal(query)
		}
	}
	for _, id := range []string{"0", "-1", "+1001", "1e3", "9223372036854775808", "01"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Params = gin.Params{{Key: "well_id", Value: id}}
		if _, e := dailyFlowID(c); e == nil {
			t.Fatal(id)
		}
	}
}

type dailyUnavailableCatalog struct{ service.Store }

func (dailyUnavailableCatalog) Well(context.Context, int64) (domain.Well, error) {
	return domain.Well{}, errors.New("SELECT private well unavailable")
}
func TestDailyFlowDependencyErrorNoAttachment(t *testing.T) {
	s := &Server{DailyFlow: service.NewDailyFlow(service.NewCatalog(dailyUnavailableCatalog{}), nil, nil, nil, "synthetic")}
	router := gin.New()
	router.GET("/wells/:well_id/daily-flow", s.wrap(s.dailyFlowRead))
	router.GET("/wells/:well_id/daily-flow/export", s.wrap(s.dailyFlowExport))
	for _, path := range []string{"/wells/1001/daily-flow", "/wells/1001/daily-flow/export?start_date=2026-10-24&end_date=2026-10-24&dataset_version=prep100-daily-v1&view_id=wrong"} {
		req := httptest.NewRequest("GET", path, nil)
		req = req.WithContext(domain.WithSystemPrincipal(req.Context()))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != 503 || w.Header().Get("Content-Disposition") != "" || strings.Contains(w.Body.String(), "SELECT") || !strings.Contains(w.Body.String(), `"detail":"daily_flow_unavailable"`) {
			t.Fatalf("%s %d %s", path, w.Code, w.Body.String())
		}
	}
}
