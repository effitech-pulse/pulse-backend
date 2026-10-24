package httpapi

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

func tableQueryParams(c *gin.Context) (string, int, string, error) {
	values, parseErr := url.ParseQuery(c.Request.URL.RawQuery)
	if parseErr != nil {
		return "", 0, "", validation("invalid_table_query")
	}
	for _, name := range []string{"table_query", "page_size", "cursor"} {
		if len(values[name]) > 1 {
			return "", 0, "", validation("invalid_table_query")
		}
	}
	if values.Has("before") || values.Has("limit") {
		return "", 0, "", validation("invalid_table_query")
	}
	size := 25
	if values.Has("page_size") {
		n, err := strconv.Atoi(c.Query("page_size"))
		if err != nil || n < 1 || n > 100 {
			return "", 0, "", validation("invalid_table_query")
		}
		size = n
	}
	return c.Query("table_query"), size, c.Query("cursor"), nil
}

// Наличие opt-in определяется по ключу даже при повреждённом URL-encoded значении.
func tableQueryRequested(c *gin.Context) bool {
	for _, part := range strings.Split(c.Request.URL.RawQuery, "&") {
		key, _, _ := strings.Cut(part, "=")
		decoded, err := url.QueryUnescape(key)
		if err == nil && decoded == "table_query" {
			return true
		}
	}
	return false
}
