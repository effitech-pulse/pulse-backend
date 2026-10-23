package main

import (
	"flag"
	"os"
	"strings"
	"testing"
)

// Invalid activation combinations must fail before reading stdin or connecting DB.
func TestActivationRejectsBootstrapFlags(t *testing.T) {
	for _, args := range [][]string{{"-activate-rbac", "-login=x"}, {"-activate-rbac", "-name=x"}, {"-activate-rbac", "positional"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			previousArgs, previousFlags := os.Args, flag.CommandLine
			t.Cleanup(func() { os.Args = previousArgs; flag.CommandLine = previousFlags })
			os.Args = append([]string{"adminuser"}, args...)
			flag.CommandLine = flag.NewFlagSet("adminuser", flag.ContinueOnError)
			if err := run(); err == nil || !strings.Contains(err.Error(), "bootstrap flags") {
				t.Fatal("activation mode mixed with bootstrap", err)
			}
		})
	}
}
