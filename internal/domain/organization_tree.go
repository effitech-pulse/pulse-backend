package domain

import "time"

const (
	OrganizationTreeEmployeeLimit = 5000
	OrganizationTreeResponseBytes = 8 * 1024 * 1024
	OrganizationTreeReadTimeout   = 5 * time.Second
)

type OrganizationTreeQuery struct {
	Search          string
	IncludeInactive bool
}

// OrganizationTreePerson — безопасная строка снимка для группировки, без Account/manager.
type OrganizationTreePerson struct {
	ID        int64     `json:"id,string"`
	Name      string    `json:"name"`
	JobTitle  *string   `json:"job_title"`
	Active    bool      `json:"active"`
	Workplace Workplace `json:"workplace"`
}
type OrganizationEmployee struct {
	ID       int64   `json:"id,string"`
	Name     string  `json:"name"`
	JobTitle *string `json:"job_title"`
	Active   bool    `json:"active"`
}
type DepartmentNode struct {
	Key           string                 `json:"key"`
	Value         *string                `json:"value"`
	EmployeeCount int                    `json:"employee_count"`
	Employees     []OrganizationEmployee `json:"employees"`
}
type DivisionNode struct {
	Key           string           `json:"key"`
	Value         *string          `json:"value"`
	EmployeeCount int              `json:"employee_count"`
	Departments   []DepartmentNode `json:"departments"`
}
type OrganizationNode struct {
	Key           string         `json:"key"`
	Value         *string        `json:"value"`
	EmployeeCount int            `json:"employee_count"`
	Divisions     []DivisionNode `json:"divisions"`
}
type OrganizationTree struct {
	Complete      bool               `json:"complete"`
	EmployeeCount int                `json:"employee_count"`
	Organizations []OrganizationNode `json:"organizations"`
}
