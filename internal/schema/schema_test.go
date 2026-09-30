package schema

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseValidSchema(t *testing.T) {
	parsed := mustParse(t, `
version: 1
variables:
  API_TIMEOUT:
    type: duration
    default: 5s
    min: 100ms
    max: 30s
    description: Timeout
  FEATURE_BETA: {type: boolean, default: false}
  API_BASE_URL: {type: url, required: true, schemes: [HTTPS]}
  API_KEY: {type: string, required: true, secret: true}
  LEGACY_URL: {type: string, deprecated: true, replacement: API_BASE_URL}
`)
	timeout := parsed.Variables["API_TIMEOUT"]
	if timeout.Default == nil || *timeout.Default != "5s" || timeout.Min.Raw != "100ms" {
		t.Fatalf("timeout = %+v", timeout)
	}
	if got := *parsed.Variables["FEATURE_BETA"].Default; got != "false" {
		t.Fatalf("boolean default = %q", got)
	}
	if got := parsed.Variables["API_BASE_URL"].Schemes[0]; got != "https" {
		t.Fatalf("schemes must be lower-cased, got %q", got)
	}
	if !parsed.Variables["API_KEY"].Secret {
		t.Fatal("secret flag lost")
	}
	if names := parsed.Names(); names[0] != "API_BASE_URL" || len(names) != 5 {
		t.Fatalf("names = %v", names)
	}
}

func TestParseInvalidSchemas(t *testing.T) {
	tests := map[string]struct{ yaml, wantProblem string }{
		"missing version":          {"variables:\n  A: {type: string}\n", "missing required field: version"},
		"unknown version":          {"version: 2\nvariables:\n  A: {type: string}\n", "unsupported schema version 2"},
		"no variables":             {"version: 1\nvariables: {}\n", "declares no variables"},
		"unknown type":             {"version: 1\nvariables:\n  A: {type: banana}\n", `unknown type "banana"`},
		"missing type":             {"version: 1\nvariables:\n  A: {required: true}\n", "missing required field: type"},
		"typo in field":            {"version: 1\nvariables:\n  A: {type: string, requird: true}\n", "requird"},
		"future rule field":        {"version: 1\nvariables:\n  A: {type: string, fututre_rule: 1}\n", "fututre_rule"},
		"enum without values":      {"version: 1\nvariables:\n  A: {type: enum}\n", "non-empty values"},
		"values on string":         {"version: 1\nvariables:\n  A: {type: string, values: [x]}\n", "only valid for type enum"},
		"duplicate enum value":     {"version: 1\nvariables:\n  A: {type: enum, values: [x, x]}\n", "duplicate enum value"},
		"schemes on string":        {"version: 1\nvariables:\n  A: {type: string, schemes: [https]}\n", "only valid for type url"},
		"bad min":                  {"version: 1\nvariables:\n  A: {type: integer, min: abc}\n", "min is invalid"},
		"min above max":            {"version: 1\nvariables:\n  A: {type: integer, min: 5, max: 1}\n", "min is greater than max"},
		"bounds on string":         {"version: 1\nvariables:\n  A: {type: string, min: 1}\n", "not supported for type string"},
		"port limit":               {"version: 1\nvariables:\n  A: {type: port, max: 70000}\n", "between 1 and 65535"},
		"required and default":     {"version: 1\nvariables:\n  A: {type: string, required: true, default: x}\n", "both required and have a default"},
		"invalid default":          {"version: 1\nvariables:\n  A: {type: integer, default: banana}\n", "default value is invalid"},
		"default out of range":     {"version: 1\nvariables:\n  A: {type: integer, min: 1, max: 5, default: 9}\n", "default value is invalid"},
		"replacement no dep":       {"version: 1\nvariables:\n  A: {type: string, replacement: B}\n  B: {type: string}\n", "requires deprecated"},
		"unknown replacement":      {"version: 1\nvariables:\n  A: {type: string, deprecated: true, replacement: NOPE}\n", "not declared"},
		"bad variable name":        {"version: 1\nvariables:\n  bad-name: {type: string}\n", "invalid variable name"},
		"broken yaml":              {"version: 1\nvariables: [\n", "invalid YAML"},
		"duplicate key":            {"version: 1\nvariables:\n  A: {type: string}\n  A: {type: string}\n", "already defined"},
		"required_if self":         {"version: 1\nvariables:\n  A: {type: string, required_if: {A: x}}\n", "cannot refer to the variable itself"},
		"required_if unknown":      {"version: 1\nvariables:\n  A: {type: string, required_if: {NOPE: x}}\n", "undeclared variable"},
		"required_if bad value":    {"version: 1\nvariables:\n  A: {type: string, required_if: {B: maybe}}\n  B: {type: boolean}\n", "required_if value for B is invalid"},
		"required and required_if": {"version: 1\nvariables:\n  A: {type: string, required: true, required_if: {B: true}}\n  B: {type: boolean}\n", "contradictory"},
		"requires unknown":         {"version: 1\nvariables:\n  A: {type: string, requires: [NOPE]}\n", "requires refers to undeclared"},
		"requires self":            {"version: 1\nvariables:\n  A: {type: string, requires: [A]}\n", "requires cannot refer to the variable itself"},
		"conflicts unknown":        {"version: 1\nvariables:\n  A: {type: string, conflicts_with: [NOPE]}\n", "conflicts_with refers to undeclared"},
		"conflicts duplicate":      {"version: 1\nvariables:\n  A: {type: string, conflicts_with: [B, B]}\n  B: {type: string}\n", "more than once"},
		"list default":             {"version: 1\nvariables:\n  A: {type: string, default: [a]}\n", "single value"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml), "bad.schema.yaml")
			var schemaErr *Error
			if !errors.As(err, &schemaErr) {
				t.Fatalf("err = %v, want *Error", err)
			}
			var all []string
			for _, p := range schemaErr.Problems {
				all = append(all, p.Message)
			}
			if !strings.Contains(strings.Join(all, "\n"), tt.wantProblem) {
				t.Fatalf("problems = %q, want mention of %q", all, tt.wantProblem)
			}
		})
	}
}

func TestParseEmptyDocument(t *testing.T) {
	if _, err := Parse(nil, "empty.yaml"); err == nil {
		t.Fatal("empty schema must be rejected")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()

	var notFound *NotFoundError
	if _, err := Load(filepath.Join(dir, "missing.yaml")); !errors.As(err, &notFound) {
		t.Fatalf("err = %v, want NotFoundError", err)
	}

	path := filepath.Join(dir, "ok.schema.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nvariables:\n  A: {type: string}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || loaded.Path != path {
		t.Fatalf("Load = %+v, %v", loaded, err)
	}

	var schemaErr *Error
	if _, err := Load(dir); !errors.As(err, &schemaErr) {
		t.Fatalf("directory must be rejected, got %v", err)
	}
}

// TestYAMLAliasBombIsRejectedWithoutExpansion feeds a "billion laughs" document
// (9 levels of 9x fan-out, ~387 million scalars if expanded). Schema fields
// decode into fixed scalar types, so the first alias that points at a nested
// list is refused before anything is expanded. The test guards that property:
// a fast, clean rejection instead of memory exhaustion.
func TestYAMLAliasBombIsRejectedWithoutExpansion(t *testing.T) {
	const fanOut = 9
	var bomb strings.Builder
	bomb.WriteString("version: 1\nvariables:\n  A:\n    type: enum\n    values: &l0 [lol, lol, lol, lol, lol, lol, lol, lol, lol]\n")
	// The anchors live in an unused sibling variable so the bomb is decoded via A.values.
	previous := "l0"
	for level := 1; level <= fanOut; level++ {
		refs := strings.TrimSuffix(strings.Repeat("*"+previous+", ", fanOut), ", ")
		fmt.Fprintf(&bomb, "  B%d:\n    type: enum\n    values: &l%d [%s]\n", level, level, refs)
		previous = fmt.Sprintf("l%d", level)
	}
	fmt.Fprintf(&bomb, "  C:\n    type: enum\n    values: *%s\n", previous)

	start := time.Now()
	_, err := Parse([]byte(bomb.String()), "bomb.schema.yaml")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("took %s: the bomb was expanded", elapsed)
	}
	if err == nil {
		t.Fatal("the bomb must be rejected")
	}
}
