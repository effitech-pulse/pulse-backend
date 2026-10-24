package httpapi

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// Custom strict decoder is necessary because Field.UnmarshalJSON otherwise resets strictness.
type profileInputDTO struct {
	Revision  domain.Field[decimal]               `json:"revision"`
	JobTitle  domain.Field[string]                `json:"job_title"`
	Workplace domain.Field[domain.WorkplaceInput] `json:"workplace"`
	ManagerID domain.Field[decimal]               `json:"manager_id"`
}

func (p *profileInputDTO) UnmarshalJSON(raw []byte) error {
	type plain profileInputDTO
	var v plain
	if e := strictProfileJSON(raw, &v); e != nil {
		return e
	}
	p.Revision = v.Revision
	p.JobTitle = v.JobTitle
	p.Workplace = v.Workplace
	p.ManagerID = v.ManagerID
	return nil
}
func strictProfileJSON(raw []byte, dest any) error {
	// Validate nested keys explicitly, including objects decoded through domain.Field.
	var obj map[string]any
	if e := json.Unmarshal(raw, &obj); e != nil {
		return e
	}
	for k, v := range obj {
		switch k {
		case "revision", "job_title", "manager_id":
		case "workplace":
			if m, ok := v.(map[string]any); ok {
				for key := range m {
					if key != "organization" && key != "division" && key != "department" {
						return fmt.Errorf("unknown workplace field")
					}
				}
			}
		default:
			return fmt.Errorf("unknown profile field")
		}
	}
	return json.Unmarshal(raw, dest)
}
func (p profileInputDTO) value() domain.ProfileInput {
	return domain.ProfileInput{Revision: domain.Field[int64]{Present: p.Revision.Present, Null: p.Revision.Null, Value: int64(p.Revision.Value)}, JobTitle: p.JobTitle, Workplace: p.Workplace, ManagerID: domain.Field[int64]{Present: p.ManagerID.Present, Null: p.ManagerID.Null, Value: int64(p.ManagerID.Value)}}
}
func (s *Server) people(c *gin.Context) error {
	c.Header("Cache-Control", "no-store")
	if c.Param("account_id") != "" {
		raw := c.Param("account_id")
		n, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || n < 1 || strconv.FormatInt(n, 10) != raw {
			c.JSON(400, gin.H{"code": "invalid", "message": "Некорректный ID"})
			return nil
		}
		v, e := s.Access.EmployeeProfile(c.Request.Context(), n)
		if e != nil {
			return e
		}
		return writeJSON(c, 200, v)
	}
	if tableQueryRequested(c) {
		raw, size, cursor, e := tableQueryParams(c)
		if e != nil {
			return e
		}
		for key := range c.Request.URL.Query() {
			if key != "table_query" && key != "page_size" && key != "cursor" {
				return validation("invalid_table_query")
			}
		}
		page, e := s.Access.ListPeopleTable(c.Request.Context(), raw, size, cursor)
		return respond(c, 200, page, e)
	}
	values, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		c.JSON(400, gin.H{"code": "invalid", "message": "Некорректный запрос справочника"})
		return nil
	}
	q := domain.PeopleQuery{Limit: 25, Search: strings.TrimSpace(values.Get("search")), Cursor: values.Get("cursor")}
	invalid := false
	for k, v := range values {
		if len(v) != 1 {
			invalid = true
		}
		switch k {
		case "search", "cursor", "limit", "include_inactive":
		default:
			invalid = true
		}
	}
	if raw, ok := values["limit"]; ok {
		n, e := strconv.Atoi(raw[0])
		if e != nil || n < 1 || n > 100 || strconv.Itoa(n) != raw[0] {
			invalid = true
		}
		q.Limit = n
	}
	if raw, ok := values["include_inactive"]; ok {
		if raw[0] != "true" && raw[0] != "false" {
			invalid = true
		}
		q.IncludeInactive = raw[0] == "true"
	}
	if !utf8.ValidString(q.Search) || utf8.RuneCountInString(q.Search) > 200 || len(q.Cursor) > 1024 {
		invalid = true
	}
	if invalid {
		c.JSON(400, gin.H{"code": "invalid", "message": "Некорректный запрос справочника"})
		return nil
	}
	v, e := s.Access.ListPeople(c.Request.Context(), q)
	if e != nil {
		if d, ok := e.(*domain.Error); ok && d.Kind == domain.Invalid {
			c.JSON(400, gin.H{"code": "invalid", "message": d.Error()})
			return nil
		}
		return e
	}
	return writeJSON(c, 200, v)
}
