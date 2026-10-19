package architecture

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestDependencyDirection(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "../..")
	rules := map[string][]string{
		"internal/domain":            {"net/http", "github.com/", "google.golang.org/", "github.com/effitech-pulse/pulse-backend/internal/"},
		"internal/service":           {"net/http", "github.com/", "google.golang.org/", "github.com/effitech-pulse/pulse-backend/internal/adapter", "github.com/effitech-pulse/pulse-backend/internal/repository", "github.com/effitech-pulse/pulse-backend/internal/transport"},
		"internal/transport/httpapi": {"github.com/jackc/", "google.golang.org/grpc", "github.com/effitech-pulse/pulse-backend/internal/repository"},
		"internal/adapter/grpcml":    {"encoding/json", "google.golang.org/protobuf/reflect/"},
	}
	for dir, forbidden := range rules {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, imp := range f.Imports {
				value, _ := strconv.Unquote(imp.Path.Value)
				// O1: сервисы используют только безопасный общий observability, без транспортов/адаптеров.
				if dir == "internal/service" && (value == "github.com/effitech-pulse/pulse-backend/internal/domain" || value == "github.com/effitech-pulse/pulse-backend/internal/observability") {
					continue
				}
				for _, prefix := range forbidden {
					if strings.HasPrefix(value, prefix) {
						t.Errorf("%s imports forbidden dependency %s", path, value)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
func TestConsumersDoNotOwnGeneratedContracts(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "../..")
	if _, err := os.Stat(filepath.Join(root, "internal/mlpb")); !os.IsNotExist(err) {
		t.Fatal("backend must consume contracts module, not copy generated code")
	}
	if _, err := os.Stat(filepath.Join(root, "proto")); !os.IsNotExist(err) {
		t.Fatal("proto belongs to pulse-contracts")
	}
}
