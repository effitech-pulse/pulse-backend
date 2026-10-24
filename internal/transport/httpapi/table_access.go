package httpapi

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func accessTableQuery(c *gin.Context) (domain.AccessPageQuery, error) {
	out := domain.AccessPageQuery{}
	values := c.Request.URL.Query()
	for _, name := range []string{"search", "q", "limit", "before"} {
		if values.Has(name) {
			return out, validation("invalid_table_query")
		}
	}
	raw, size, cursor, e := tableQueryParams(c)
	if e != nil {
		return out, e
	}
	dataset := "access_users"
	parent := int64(0)
	switch {
	case strings.Contains(c.FullPath(), "/access/preview"):
		dataset = "access_preview"
	case strings.Contains(c.FullPath(), "/groups"):
		dataset = "access_groups"
		if strings.Contains(c.FullPath(), "/members") {
			dataset = "access_members"
			parent, e = strconv.ParseInt(c.Param("group_id"), 10, 64)
			if e != nil || parent < 1 {
				return out, validation("invalid_table_query")
			}
		}
	case strings.Contains(c.FullPath(), "/roles"):
		dataset = "access_roles"
	}
	req, e := service.ParseAccessTableRequest(c.Request.Context(), dataset, parent, raw, size, cursor)
	if e != nil {
		return out, e
	}
	out.Table = &req
	if values.Has("graph_revision") {
		s := c.Query("graph_revision")
		v, err := strconv.ParseInt(s, 10, 64)
		if len(values["graph_revision"]) != 1 || err != nil || v < 1 || strconv.FormatInt(v, 10) != s {
			return out, validation("invalid_table_query")
		}
		out.GraphRevision = &v
	}
	return out, nil
}

func accessFilterOptionsSize(c *gin.Context) (int, error) {
	values := c.Request.URL.Query()
	for _, name := range []string{"field", "search", "page_size", "cursor"} {
		if len(values[name]) > 1 {
			return 0, validation("invalid_table_query")
		}
	}
	for _, name := range []string{"table_query", "q", "limit", "before", "graph_revision"} {
		if values.Has(name) {
			return 0, validation("invalid_table_query")
		}
	}
	size := 25
	if values.Has("page_size") {
		v, e := strconv.Atoi(c.Query("page_size"))
		if e != nil || v < 1 || v > 100 {
			return 0, validation("invalid_table_query")
		}
		size = v
	}
	return size, nil
}

func (s *Server) authUserFilterOptions(c *gin.Context) error {
	size, e := accessFilterOptionsSize(c)
	if e != nil {
		return e
	}
	out, e := s.Access.UserFilterOptions(c.Request.Context(), c.Query("field"), c.Query("search"), size, c.Query("cursor"))
	if e != nil {
		return e
	}
	return writeJSON(c, 200, out)
}

func (s *Server) authGroupFilterOptions(c *gin.Context) error {
	size, e := accessFilterOptionsSize(c)
	if e != nil {
		return e
	}
	out, e := s.Access.GroupFilterOptions(c.Request.Context(), c.Query("field"), c.Query("search"), size, c.Query("cursor"))
	if e != nil {
		return e
	}
	return writeJSON(c, 200, out)
}
