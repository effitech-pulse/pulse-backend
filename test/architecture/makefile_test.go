package architecture

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMakeCheckFormatting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Makefile requires a Unix shell")
	}
	makePath, err := exec.LookPath("make")
	if err != nil {
		t.Fatal(err)
	}
	_, file, _, _ := runtime.Caller(0)
	makefile := filepath.Join(filepath.Dir(file), "../../Makefile")
	for _, tc := range []struct {
		name    string
		source  string
		missing bool
		wantErr bool
	}{
		{name: "missing formatter", source: "package fixture\n", missing: true, wantErr: true},
		{name: "parse error", source: "package fixture\nfunc (\n", wantErr: true},
		{name: "unformatted", source: "package fixture\nvar x=1\n", wantErr: true},
		{name: "formatted", source: "package fixture\n\nvar x = 1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := t.TempDir()
			for _, dir := range []string{"cmd", "internal", "api", "test", "bin"} {
				if err := os.Mkdir(filepath.Join(fixture, dir), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(fixture, "cmd/fixture.go"), []byte(tc.source), 0o600); err != nil {
				t.Fatal(err)
			}
			bin := filepath.Join(fixture, "bin")
			if !tc.missing {
				if err := os.Symlink(filepath.Join(runtime.GOROOT(), "bin/gofmt"), filepath.Join(bin, "gofmt")); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(makePath, "-f", makefile, "-o", "test", "-o", "vet", "check")
			cmd.Dir = fixture
			cmd.Env = append(os.Environ(), "PATH="+bin)
			output, err := cmd.CombinedOutput()
			if (err != nil) != tc.wantErr {
				t.Fatalf("make check error = %v, want error %t\n%s", err, tc.wantErr, output)
			}
		})
	}
}
