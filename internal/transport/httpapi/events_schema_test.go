package httpapi

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"testing"
)

// validateEventSchema characterizes the published subset used by F3 fixtures;
// it is deliberately not advertised as a complete JSON Schema implementation.
func validateEventSchema(value any, schema map[string]any, spec map[string]any) error {
	if ref, ok := schema["$ref"].(string); ok {
		const prefix = "#/components/schemas/"
		if len(ref) <= len(prefix) || ref[:len(prefix)] != prefix {
			return fmt.Errorf("unsupported ref %s", ref)
		}
		return validateEventSchema(value, spec["components"].(map[string]any)["schemas"].(map[string]any)[ref[len(prefix):]].(map[string]any), spec)
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		if branches, ok := schema[key].([]any); ok {
			matches := 0
			for _, branch := range branches {
				if validateEventSchema(value, branch.(map[string]any), spec) == nil {
					matches++
				}
			}
			if matches > 0 && (key == "anyOf" || matches == 1) {
				return nil
			}
			return fmt.Errorf("%s matched %d branches", key, matches)
		}
	}
	if typ, ok := schema["type"].(string); ok {
		valid := false
		switch typ {
		case "null":
			valid = value == nil
		case "string":
			_, valid = value.(string)
		case "boolean":
			_, valid = value.(bool)
		case "number", "integer":
			_, valid = value.(float64)
		case "object":
			_, valid = value.(map[string]any)
		case "array":
			_, valid = value.([]any)
		}
		if !valid {
			return fmt.Errorf("expected %s, got %T", typ, value)
		}
	}
	if enum, ok := schema["enum"].([]any); ok {
		valid := false
		for _, v := range enum {
			if reflect.DeepEqual(v, value) {
				valid = true
			}
		}
		if !valid {
			return fmt.Errorf("enum rejects %v", value)
		}
	}
	if constant, ok := schema["const"]; ok && !reflect.DeepEqual(constant, value) {
		return fmt.Errorf("const rejects %v", value)
	}
	if pattern, ok := schema["pattern"].(string); ok {
		if v, ok := value.(string); ok {
			matched, e := regexp.MatchString(pattern, v)
			if e != nil || !matched {
				return fmt.Errorf("pattern %s rejects %q", pattern, v)
			}
		}
	}
	if object, ok := value.(map[string]any); ok {
		properties, _ := schema["properties"].(map[string]any)
		required, _ := schema["required"].([]any)
		for _, key := range required {
			if _, ok := object[key.(string)]; !ok {
				return fmt.Errorf("missing %s", key)
			}
		}
		for key, v := range object {
			if field, ok := properties[key]; ok {
				if e := validateEventSchema(v, field.(map[string]any), spec); e != nil {
					return fmt.Errorf("%s: %w", key, e)
				}
			} else if schema["additionalProperties"] == false {
				return fmt.Errorf("unexpected %s", key)
			}
		}
	}
	if array, ok := value.([]any); ok {
		if items, ok := schema["items"].(map[string]any); ok {
			for i, v := range array {
				if e := validateEventSchema(v, items, spec); e != nil {
					return fmt.Errorf("[%d]: %w", i, e)
				}
			}
		}
	}
	return nil
}
func TestEventsPublishedSchemaAcceptsFrozenResponses(t *testing.T) {
	var spec, fixture map[string]any
	if e := json.Unmarshal(openAPI, &spec); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("testdata/events_examples.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	responses := fixture["responses"].(map[string]any)
	check := func(name string, value any, schemaName string) {
		t.Run(name, func(t *testing.T) {
			schema := spec["components"].(map[string]any)["schemas"].(map[string]any)[schemaName].(map[string]any)
			if e := validateEventSchema(value, schema, spec); e != nil {
				t.Fatal(e)
			}
		})
	}
	for _, name := range []string{"list_all_types", "list_continuation_example", "list_empty"} {
		check(name, responses[name].(map[string]any)["body"], "EventPageDTO")
	}
	for _, group := range []string{"details", "manual_variants", "errors"} {
		for _, raw := range responses[group].([]any) {
			v := raw.(map[string]any)
			schema := "EventDetailDTO"
			if group == "errors" {
				schema = "EventError"
			}
			check(v["name"].(string), v["response"].(map[string]any)["body"], schema)
		}
	}
	actorID := spec["components"].(map[string]any)["schemas"].(map[string]any)["EventActorDTO"].(map[string]any)["properties"].(map[string]any)["id"].(map[string]any)
	for _, tc := range []struct {
		value any
		valid bool
	}{{"user:41", true}, {"user:9223372036854775807", true}, {"shared", true}, {"service", true}, {nil, true}, {"41", false}, {"user:0", false}, {"user:01", false}, {"other", false}} {
		if got := validateEventSchema(tc.value, actorID, spec) == nil; got != tc.valid {
			t.Errorf("actor id %v valid=%v want=%v", tc.value, got, tc.valid)
		}
	}
}
