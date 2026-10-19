package domain

import "testing"

func TestScopePreservesNilEmptyZeroNegativeAndFund(t *testing.T) {
	zero := []int64{0}
	negative := []int64{-900}
	empty := []int64{}
	p := Principal{Role: "viewer", Scope: AccessScope{Funds: []string{"operational"}, NGDUs: &zero}}
	if !p.Allows(Well{NGDU: 0, Department: 0}) || p.Allows(Well{NGDU: 1}) || p.Allows(Well{NGDU: 0, FundType: "presentation"}) {
		t.Fatal("scope intersection")
	}
	p.Scope.Departments = &empty
	if p.Allows(Well{NGDU: 0}) {
		t.Fatal("empty is unrestricted")
	}
	p.Scope.Departments = nil
	p.Scope.NGDUs = &negative
	p.Scope.Funds = []string{"presentation"}
	if !p.Allows(Well{NGDU: -900, FundType: "presentation"}) {
		t.Fatal("negative code lost")
	}
	p.Role = "admin"
	if !p.Allows(Well{}) {
		t.Fatal("admin cannot read")
	}
}
