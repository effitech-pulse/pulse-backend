package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestOrganizationTreePathsAndCounts(t *testing.T) {
	text := func(v string) *string { return &v }
	row := func(id int64, name string, org, div, dept *string) domain.OrganizationTreePerson {
		return domain.OrganizationTreePerson{ID: id, Name: name, Active: true, Workplace: domain.Workplace{Organization: org, Division: div, Department: dept}}
	}
	rows := []domain.OrganizationTreePerson{
		row(7, "Б", text("Б"), text("Общий"), text("Отдел")),
		row(5, "А", text("А"), text("Два"), text("Отдел")),
		row(9007199254740993, "А", text("А"), text("Один"), text("Отдел")),
		row(3, "А", text("А"), text("Один"), text("Отдел")),
		row(8, "Без сведений", nil, nil, nil),
		row(9, "Частичный", nil, nil, text("Геология")),
		row(10, "Настоящий текст", text("Не указано"), nil, nil),
		row(11, "Кавычки", text(`А/"Б`), text("В"), nil),
		row(12, "Другой путь", text("А"), text(`/"Б/В`), nil),
		row(13, "Нижний регистр", text("а"), nil, nil),
		row(14, "NFC", text("é"), nil, nil), row(15, "NFD", text("e\u0301"), nil, nil),
	}
	tree := BuildOrganizationTree(rows)
	if !tree.Complete || tree.EmployeeCount != 12 || len(tree.Organizations) != 8 {
		t.Fatalf("lost paths/count: %+v", tree)
	}
	seen := map[int64]bool{}
	keys := map[string]bool{}
	lastOrgNull := false
	for oi, org := range tree.Organizations {
		if org.Value == nil {
			lastOrgNull = oi == len(tree.Organizations)-1
		}
		raw, e := base64.RawURLEncoding.DecodeString(org.Key)
		if e != nil {
			t.Fatal(e)
		}
		var path []any
		if e = json.Unmarshal(raw, &path); e != nil || len(path) != 2 || path[0] != "organization" {
			t.Fatal(string(raw), e)
		}
		sum := 0
		for _, div := range org.Divisions {
			dsum := 0
			for _, dept := range div.Departments {
				if keys[dept.Key] {
					t.Fatal("path collision", dept.Key)
				}
				keys[dept.Key] = true
				if dept.EmployeeCount != len(dept.Employees) {
					t.Fatal("department count", dept)
				}
				for _, employee := range dept.Employees {
					if seen[employee.ID] {
						t.Fatal("duplicate leaf", employee.ID)
					}
					seen[employee.ID] = true
				}
				dsum += dept.EmployeeCount
			}
			if dsum != div.EmployeeCount {
				t.Fatal("division count", div)
			}
			sum += dsum
		}
		if sum != org.EmployeeCount {
			t.Fatal("organization count", org)
		}
	}
	if !lastOrgNull || len(seen) != 12 {
		t.Fatal("null ordering or leaf lost")
	}
	// Equal names break ties by exact numeric ID, not decimal lexical order.
	a := tree.Organizations[0]
	if a.Value == nil || *a.Value != "e\u0301" {
		t.Fatal("UTF8 ordering", a)
	}
	for _, org := range tree.Organizations {
		if org.Value != nil && *org.Value == "А" {
			for _, div := range org.Divisions {
				if div.Value != nil && *div.Value == "Один" {
					leaves := div.Departments[0].Employees
					if leaves[0].ID != 3 || leaves[1].ID != 9007199254740993 {
						t.Fatal(leaves)
					}
				}
			}
		}
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	if reverse := BuildOrganizationTree(rows); !reflect.DeepEqual(tree, reverse) {
		t.Fatal("input order changes tree")
	}
	empty := BuildOrganizationTree(nil)
	raw, _ := json.Marshal(empty)
	if string(raw) != `{"complete":true,"employee_count":0,"organizations":[]}` {
		t.Fatal(string(raw))
	}
}

type organizationTreeStore struct {
	AccessStore
	EmployeeProfileStore
	rows     []domain.OrganizationTreePerson
	err      error
	deadline time.Time
}

func (s *organizationTreeStore) OrganizationTreePeople(ctx context.Context, _ int64, _ domain.OrganizationTreeQuery) ([]domain.OrganizationTreePerson, error) {
	s.deadline, _ = ctx.Deadline()
	return s.rows, s.err
}
func TestOrganizationTreeLimitsAndDeadline(t *testing.T) {
	id := int64(1)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Authenticated: true, AccountID: &id, Mechanism: "session"})
	store := &organizationTreeStore{}
	access := &Access{store: store, enabled: true}
	for _, n := range []int{5000, 5001} {
		store.rows = make([]domain.OrganizationTreePerson, n)
		for i := range store.rows {
			store.rows[i].ID = int64(i + 1)
		}
		tree, e := access.OrganizationTree(ctx, domain.OrganizationTreeQuery{})
		if n == 5000 {
			if e != nil || tree.EmployeeCount != 5000 || !tree.Complete {
				t.Fatal(tree, e)
			}
		} else {
			var problem *domain.Error
			if !errors.As(e, &problem) || problem.Kind != domain.Unavailable || problem.Message != "organization_tree_too_large" || tree.Complete {
				t.Fatal(tree, e)
			}
		}
	}
	store.rows = nil
	store.err = fmt.Errorf("database request failed: %w", context.DeadlineExceeded)
	started := time.Now()
	_, e := access.OrganizationTree(ctx, domain.OrganizationTreeQuery{})
	var problem *domain.Error
	if !errors.As(e, &problem) || problem.Kind != domain.Unavailable || problem.Message != "organization_tree_timeout" || strings.Contains(e.Error(), "database") {
		t.Fatal(e)
	}
	if store.deadline.Before(started) || store.deadline.After(time.Now().Add(5*time.Second)) {
		t.Fatal("unbounded read", store.deadline, started)
	}
	if _, e = access.OrganizationTree(ctx, domain.OrganizationTreeQuery{Search: strings.Repeat("я", 201)}); e == nil {
		t.Fatal("oversized query accepted")
	}
}
