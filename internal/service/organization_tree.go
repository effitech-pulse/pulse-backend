package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// NormalizeOrganizationTreeQuery сохраняет literal search и прежние правила каталога.
func NormalizeOrganizationTreeQuery(q domain.OrganizationTreeQuery) (domain.OrganizationTreeQuery, error) {
	q.Search = strings.TrimSpace(q.Search)
	if !utf8.ValidString(q.Search) || utf8.RuneCountInString(q.Search) > 200 {
		return q, domain.NewError(domain.Invalid, "Некорректный запрос структуры")
	}
	return q, nil
}
func (a *Access) OrganizationTree(ctx context.Context, q domain.OrganizationTreeQuery) (domain.OrganizationTree, error) {
	ctx, cancel := context.WithTimeout(ctx, domain.OrganizationTreeReadTimeout)
	defer cancel()
	store, id, err := a.peopleStore(ctx)
	if err != nil {
		return domain.OrganizationTree{}, err
	}
	q, err = NormalizeOrganizationTreeQuery(q)
	if err != nil {
		return domain.OrganizationTree{}, err
	}
	rows, err := store.OrganizationTreePeople(ctx, id, q)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return domain.OrganizationTree{}, domain.NewError(domain.Unavailable, "organization_tree_timeout")
	}
	if err != nil {
		return domain.OrganizationTree{}, err
	}
	if len(rows) > domain.OrganizationTreeEmployeeLimit {
		return domain.OrganizationTree{}, domain.NewError(domain.Unavailable, "organization_tree_too_large")
	}
	tree := BuildOrganizationTree(rows)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return domain.OrganizationTree{}, domain.NewError(domain.Unavailable, "organization_tree_timeout")
	}
	return tree, nil
}

type organizationBuilder struct {
	node      domain.OrganizationNode
	divisions map[string]*divisionBuilder
}
type divisionBuilder struct {
	node        domain.DivisionNode
	departments map[string]*domain.DepartmentNode
}

func organizationPathKey(level string, path ...*string) string {
	// Typed JSON tuple keeps null distinct from every literal label and delimiter.
	parts := make([]json.RawMessage, 0, len(path)+1)
	raw, _ := json.Marshal(level)
	parts = append(parts, raw)
	for _, value := range path {
		raw, _ = json.Marshal(value)
		parts = append(parts, raw)
	}
	raw, _ = json.Marshal(parts)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func compareOrganizationValue(a, b *string) int {
	if a == nil {
		if b == nil {
			return 0
		}
		return 1
	}
	if b == nil {
		return -1
	}
	return strings.Compare(*a, *b)
}

// BuildOrganizationTree группирует точные nullable пути и задаёт детерминированный порядок.
func BuildOrganizationTree(rows []domain.OrganizationTreePerson) domain.OrganizationTree {
	organizations := map[string]*organizationBuilder{}
	for _, row := range rows {
		p := row.Workplace
		orgKey := organizationPathKey("organization", p.Organization)
		org := organizations[orgKey]
		if org == nil {
			org = &organizationBuilder{node: domain.OrganizationNode{Key: orgKey, Value: p.Organization}, divisions: map[string]*divisionBuilder{}}
			organizations[orgKey] = org
		}
		divKey := organizationPathKey("division", p.Organization, p.Division)
		div := org.divisions[divKey]
		if div == nil {
			div = &divisionBuilder{node: domain.DivisionNode{Key: divKey, Value: p.Division}, departments: map[string]*domain.DepartmentNode{}}
			org.divisions[divKey] = div
		}
		deptKey := organizationPathKey("department", p.Organization, p.Division, p.Department)
		dept := div.departments[deptKey]
		if dept == nil {
			dept = &domain.DepartmentNode{Key: deptKey, Value: p.Department, Employees: []domain.OrganizationEmployee{}}
			div.departments[deptKey] = dept
		}
		dept.Employees = append(dept.Employees, domain.OrganizationEmployee{ID: row.ID, Name: row.Name, JobTitle: row.JobTitle, Active: row.Active})
		dept.EmployeeCount++
		div.node.EmployeeCount++
		org.node.EmployeeCount++
	}
	tree := domain.OrganizationTree{Complete: true, EmployeeCount: len(rows), Organizations: []domain.OrganizationNode{}}
	for _, org := range organizations {
		org.node.Divisions = []domain.DivisionNode{}
		for _, div := range org.divisions {
			div.node.Departments = []domain.DepartmentNode{}
			for _, dept := range div.departments {
				slices.SortFunc(dept.Employees, func(a, b domain.OrganizationEmployee) int {
					if c := strings.Compare(a.Name, b.Name); c != 0 {
						return c
					}
					if a.ID < b.ID {
						return -1
					}
					if a.ID > b.ID {
						return 1
					}
					return 0
				})
				div.node.Departments = append(div.node.Departments, *dept)
			}
			slices.SortFunc(div.node.Departments, func(a, b domain.DepartmentNode) int { return compareOrganizationValue(a.Value, b.Value) })
			org.node.Divisions = append(org.node.Divisions, div.node)
		}
		slices.SortFunc(org.node.Divisions, func(a, b domain.DivisionNode) int { return compareOrganizationValue(a.Value, b.Value) })
		tree.Organizations = append(tree.Organizations, org.node)
	}
	slices.SortFunc(tree.Organizations, func(a, b domain.OrganizationNode) int { return compareOrganizationValue(a.Value, b.Value) })
	return tree
}
