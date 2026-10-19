package httpapi

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/config"
)

func TestAllLegacyRoutesRegistered(t *testing.T) {
	server := &Server{Config: config.Config{Prefix: "/api/v1"}}
	router := server.routes()
	// Имена параметров различаются между OpenAPI и Gin; положение сегмента сохраняется.
	parameter := regexp.MustCompile(`\{[^}]+\}|:[^/]+`)
	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		registered[route.Method+" "+parameter.ReplaceAllString(route.Path, "{}")] = true
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(openAPI, &doc); err != nil {
		t.Fatal(err)
	}
	for path, methods := range doc.Paths {
		for method := range methods {
			if method == "parameters" {
				continue
			}
			key := strings.ToUpper(method) + " " + parameter.ReplaceAllString(path, "{}")
			if !registered[key] {
				t.Errorf("route missing: %s %s", method, path)
			}
		}
	}
}
