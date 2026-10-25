package main

import (
	"strings"
	"testing"
)

func TestPassportEnrollmentInput(t *testing.T) {
	valid := `{"request_id":"ca1524bc-f2ec-48c5-bff4-e66b8be57d15","target_account_id":"9007199254740993","expected_account_revision":"1","expected_epoch":"1","expected_graph_revision":"1","permission_keys":["wells.read","equipment_passports.read"]}`
	in, e := decodePassportEnrollment(strings.NewReader(valid))
	if e != nil || int64(in.TargetAccountID) != 9007199254740993 {
		t.Fatal(in, e)
	}
	for _, bad := range []string{`null`, `[]`, ``, `{`, valid + ` trailing`, valid + ` {}`, strings.Replace(valid, `"target_account_id":"9007199254740993"`, `"target_account_id":9007199254740993`, 1), strings.Replace(valid, `"permission_keys"`, `"actor":{},"permission_keys"`, 1), strings.Repeat(" ", 16385)} {
		if _, e := decodePassportEnrollment(strings.NewReader(bad)); e == nil {
			t.Fatal("invalid stdin accepted")
		}
	}
	if e := passportEnrollmentFlags(true, "login", "", false, 0); e == nil {
		t.Fatal("bootstrap flags mixed")
	}
	if e := passportEnrollmentFlags(true, "", "", true, 0); e == nil {
		t.Fatal("activation flags mixed")
	}
}

func TestPassportEnrollmentFlagExclusivity(t *testing.T) {
	for _, tc := range []struct {
		login, name string
		activate    bool
		args        int
	}{
		{login: "login"}, {name: "name"}, {activate: true}, {args: 1},
	} {
		if e := passportEnrollmentFlags(true, tc.login, tc.name, tc.activate, tc.args); e == nil {
			t.Fatal("mixed flags accepted", tc)
		}
	}
	if e := passportEnrollmentFlags(true, "", "", false, 0); e != nil {
		t.Fatal(e)
	}
}
