package httpapi

import (
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
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
