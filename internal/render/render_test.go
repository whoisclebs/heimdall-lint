package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/whoisclebs/heimdall-lint/internal/batch"
	"github.com/whoisclebs/heimdall-lint/internal/contractdiff"
	"github.com/whoisclebs/heimdall-lint/internal/diag"
	"github.com/whoisclebs/heimdall-lint/internal/schema"
)

func sampleReport() batch.Report {
	return batch.Report{Files: []batch.FileResult{
		{Path: "envs/inventory-api.env", Application: "inventory-api", SchemaPath: "image://app:2", VariablesChecked: 4, Diagnostics: []diag.Diagnostic{
			{Severity: diag.SeverityError, Code: diag.UnknownVariable, Line: 3, Variable: "INVENTORY_TIMOUT",
				Message: "Unknown environment variable.", Suggestions: []string{"INVENTORY_TIMEOUT"}},
			{Severity: diag.SeverityError, Code: diag.InvalidType, Line: 1, Variable: "DATABASE_PORT",
				Message: "Value has the wrong type.", Expected: "integer between 1 and 65535", Received: `"banana"`},
		}},
		{Path: "envs/notification.env", Application: "notification", VariablesChecked: 2, Diagnostics: []diag.Diagnostic{
			{Severity: diag.SeverityWarning, Code: diag.DeprecatedVariable, Variable: "LEGACY_URL",
				Message: "This environment variable is deprecated.", Suggestions: []string{"NOTIFICATION_API_URL"}, SuggestionKind: diag.Use},
		}},
		{Path: "envs/reports-api.env", Application: "reports-api", VariablesChecked: 31},
	}}
}

func TestHumanOutput(t *testing.T) {
	var out bytes.Buffer
	if err := Human(&out, sampleReport(), Options{}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"Heimdall Lint",
		"envs/reports-api.env\n  PASS\n  31 variables checked",
		"envs/inventory-api.env\n  FAIL\n  schema: image://app:2",
		"ERROR INVENTORY_TIMOUT (HML002, line 3)",
		"Did you mean:\n      INVENTORY_TIMEOUT",
		"Expected:\n      integer between 1 and 65535",
		"Received:\n      \"banana\"",
		"WARNING LEGACY_URL (HML007)",
		"Use:\n      NOTIFICATION_API_URL",
		"Files discovered:     3",
		"Files invalid:        1",
		"Files with warnings:  1",
		"Variables checked:    37",
		"Errors:               2",
		"Environment validation failed.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output lacks %q\n%s", want, text)
		}
	}
	if strings.Contains(text, "\x1b[") {
		t.Error("color must be off unless requested")
	}
}

func TestHumanQuietOmitsPassingFiles(t *testing.T) {
	var out bytes.Buffer
	if err := Human(&out, sampleReport(), Options{Quiet: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "reports-api.env") || !strings.Contains(out.String(), "Summary") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestHumanColor(t *testing.T) {
	var out bytes.Buffer
	if err := Human(&out, sampleReport(), Options{Color: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\x1b[1;31mERROR\x1b[0m") {
		t.Fatal("expected ANSI color")
	}
}

func TestHumanPassedFooters(t *testing.T) {
	var out bytes.Buffer
	clean := batch.Report{Files: []batch.FileResult{{Path: "a.env", VariablesChecked: 1}}}
	_ = Human(&out, clean, Options{})
	if !strings.Contains(out.String(), "Environment validation passed.") {
		t.Fatalf("output:\n%s", out.String())
	}

	out.Reset()
	warnOnly := batch.Report{Files: []batch.FileResult{{Path: "a.env", Diagnostics: []diag.Diagnostic{{Severity: diag.SeverityWarning, Code: diag.DeprecatedVariable, Variable: "X", Message: "m"}}}}}
	_ = Human(&out, warnOnly, Options{})
	if !strings.Contains(out.String(), "passed with warnings") {
		t.Fatalf("output:\n%s", out.String())
	}
	out.Reset()
	_ = Human(&out, warnOnly, Options{WarningsAsErrors: true})
	if !strings.Contains(out.String(), "validation failed") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestTerminalEscapesAreNeutralised(t *testing.T) {
	report := batch.Report{Files: []batch.FileResult{{
		Path:        "evil\x1b[2Jname.env",
		Diagnostics: []diag.Diagnostic{{Severity: diag.SeverityError, Code: diag.UnknownVariable, Variable: "A\x1b]0;pwned\x07", Message: "m\x1b[31m"}},
	}}}
	var out bytes.Buffer
	if err := Human(&out, report, Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out.String(), 0x1b) || strings.ContainsRune(out.String(), 0x07) {
		t.Fatalf("raw control characters reached the terminal: %q", out.String())
	}
}

func TestJSONShape(t *testing.T) {
	var out bytes.Buffer
	if err := JSON(&out, sampleReport(), false); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Valid    bool `json:"valid"`
		ExitCode int  `json:"exit_code"`
		Summary  struct {
			Files  int `json:"files"`
			Errors int `json:"errors"`
		} `json:"summary"`
		General []any `json:"general"`
		Files   []struct {
			Path        string `json:"path"`
			Valid       bool   `json:"valid"`
			Diagnostics []struct {
				Code        string   `json:"code"`
				Name        string   `json:"name"`
				Suggestions []string `json:"suggestions"`
				Received    string   `json:"received"`
			} `json:"diagnostics"`
		} `json:"files"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if decoded.Valid || decoded.ExitCode != 1 || decoded.Summary.Files != 3 || decoded.Summary.Errors != 2 || decoded.General == nil {
		t.Fatalf("decoded = %+v", decoded)
	}
	first := decoded.Files[0]
	if first.Valid || first.Diagnostics[0].Code != "HML002" || first.Diagnostics[0].Name != "unknown_variable" || first.Diagnostics[0].Suggestions[0] != "INVENTORY_TIMEOUT" {
		t.Fatalf("first = %+v", first)
	}
	if !decoded.Files[2].Valid || decoded.Files[2].Diagnostics == nil {
		t.Fatalf("passing file must be valid with an empty diagnostics array: %+v", decoded.Files[2])
	}
}

func mustSchema(t *testing.T, variables string) *schema.Schema {
	t.Helper()
	s, err := schema.Parse([]byte("version: 1\nvariables:\n"+variables), "s.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestInspect(t *testing.T) {
	s := mustSchema(t, "  API_TIMEOUT: {type: duration, default: 5s, min: 100ms, max: 30s, description: Timeout for the upstream service}\n  KEY: {type: string, secret: true, default: hunter2}\n")
	var out bytes.Buffer
	if err := Inspect(&out, "API_TIMEOUT", []InspectEntry{{Source: "s", Variable: s.Variables["API_TIMEOUT"]}}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"API_TIMEOUT\n", "Type:           duration", "Required:       false", "Default:        5s", "Minimum:        100ms", "Maximum:        30s", "Description:\nTimeout for the upstream service"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q\n%s", want, out.String())
		}
	}

	out.Reset()
	_ = Inspect(&out, "KEY", []InspectEntry{{Source: "s", Variable: s.Variables["KEY"]}})
	if strings.Contains(out.String(), "hunter2") || !strings.Contains(out.String(), "[REDACTED]") {
		t.Fatalf("secret default leaked:\n%s", out.String())
	}
}

func TestDiff(t *testing.T) {
	old := mustSchema(t, "  API_TIMEOUT: {type: duration, default: 5s}\n  LEGACY_API_URL: {type: string}\n")
	updated := mustSchema(t, "  API_TIMEOUT: {type: duration, default: 10s}\n  API_READ_TIMEOUT: {type: duration, required: true}\n")
	result := contractdiff.Compare(old, updated)

	var out bytes.Buffer
	if err := Diff(&out, result); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Environment contract changes", "ADDED", "API_READ_TIMEOUT  [action required]", "type: duration", "required: true",
		"REMOVED", "LEGACY_API_URL", "CHANGED", "default: 5s -> 10s",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := DiffJSON(&out, result); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(out.Bytes()) {
		t.Fatalf("invalid JSON: %s", out.String())
	}

	out.Reset()
	_ = Diff(&out, contractdiff.Result{})
	if !strings.Contains(out.String(), "No changes") {
		t.Fatalf("output: %s", out.String())
	}
}
