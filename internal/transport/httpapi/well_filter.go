package httpapi

import (
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/gin-gonic/gin"
	"strconv"
)

func wellFilter(c *gin.Context) (domain.WellFilter, error) {
	f := domain.WellFilter{Archive: c.Query("archive"), Fund: c.Query("fund"), Search: c.Query("search"), Sort: c.Query("sort")}
	for key, dest := range map[string]**int64{"ngdu": &f.NGDU, "department": &f.Department} {
		if raw, ok := c.GetQuery(key); ok {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return f, validation("Некорректный " + key)
			}
			*dest = &n
		}
	}
	return f, nil
}
