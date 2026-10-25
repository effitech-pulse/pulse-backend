package httpapi

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/gin-gonic/gin"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func organizationTreeQuery(raw string) (domain.OrganizationTreeQuery, error) {
	invalid := func() (domain.OrganizationTreeQuery, error) {
		return domain.OrganizationTreeQuery{}, domain.NewError(domain.Invalid, "Некорректный запрос структуры")
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return invalid()
	}
	q := domain.OrganizationTreeQuery{}
	for key, v := range values {
		if len(v) != 1 {
			return invalid()
		}
		switch key {
		case "search":
			q.Search = v[0]
		case "include_inactive":
			if v[0] != "true" && v[0] != "false" {
				return invalid()
			}
			q.IncludeInactive = v[0] == "true"
		default:
			return invalid()
		}
	}
	return service.NormalizeOrganizationTreeQuery(q)
}
func (s *Server) organizationTree(c *gin.Context) error {
	c.Header("Cache-Control", "no-store")
	q, err := organizationTreeQuery(c.Request.URL.RawQuery)
	if err != nil {
		c.JSON(400, gin.H{"code": "invalid", "message": "Некорректный запрос структуры", "detail": nil})
		return nil
	}
	tree, err := s.Access.OrganizationTree(c.Request.Context(), q)
	if err != nil {
		return err
	}
	return writeOrganizationTree(c, tree)
}
func writeOrganizationTree(c *gin.Context, tree domain.OrganizationTree) error {
	raw, err := json.Marshal(tree)
	if err != nil {
		return fmt.Errorf("encode organization tree: %w", err)
	}
	if len(raw)+1 > domain.OrganizationTreeResponseBytes {
		return domain.NewError(domain.Unavailable, "organization_tree_too_large")
	}
	c.Data(200, "application/json", append(raw, '\n'))
	return nil
}
