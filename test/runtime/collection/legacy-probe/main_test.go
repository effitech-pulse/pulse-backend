package main

import "testing"

func TestLegacyProbeRejectsNonOwnTargets(t *testing.T) {
	if err := validateTarget("postgres://synthetic:fake@127.0.0.1:55442/collection_test?sslmode=disable", "q2c2_synthetic"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ url, schema string }{
		{"postgres://synthetic:fake@127.0.0.1:55439/collection_test", "q2c2_synthetic"},
		{"postgres://synthetic:fake@real.example:55442/collection_test", "q2c2_synthetic"},
		{"postgres://synthetic:fake@127.0.0.1:55442/production", "q2c2_synthetic"},
		{"postgres://synthetic:fake@127.0.0.1:55442/collection_test", "public"},
	} {
		if validateTarget(test.url, test.schema) == nil {
			t.Fatal("non-own target accepted")
		}
	}
}
