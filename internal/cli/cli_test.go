package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/whoisclebs/heimdall-lint/internal/batch"
)

func TestParseInterspersedAcceptsFlagsAfterPositionals(t *testing.T) {
	var all bool
	var schemaDir string
	set := newFlagSet("lint", "", Environment{Stderr: &bytes.Buffer{}})
	set.BoolVar(&all, "all", false, "")
	set.StringVar(&schemaDir, "schema-dir", "", "")

	positional, _, ok := parseInterspersed(set, []string{"--all", "/envs", "--schema-dir", "/schemas", "/more"})
	if !ok || !all || schemaDir != "/schemas" || !slices.Equal(positional, []string{"/envs", "/more"}) {
		t.Fatalf("positional=%v all=%v schemaDir=%q ok=%v", positional, all, schemaDir, ok)
	}
}

func TestParseInterspersedReportsHelpAndErrors(t *testing.T) {
	set := newFlagSet("lint", "", Environment{Stderr: &bytes.Buffer{}})
	if _, code, ok := parseInterspersed(set, []string{"-h"}); ok || code != batch.ExitOK {
		t.Fatalf("help: code=%d ok=%v", code, ok)
	}
	set = newFlagSet("lint", "", Environment{Stderr: &bytes.Buffer{}})
	if _, code, ok := parseInterspersed(set, []string{"--unknown"}); ok || code != batch.ExitUsage {
		t.Fatalf("error: code=%d ok=%v", code, ok)
	}
}

func inTempDir(t *testing.T, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
}

func TestPlanLintRejectsConflictingInvocations(t *testing.T) {
	inTempDir(t, map[string]string{"heimdall.yaml": "version: 1\n", "env.schema.yaml": "x"})
	tests := map[string]lintInput{
		"schema and schema-dir":        {positional: []string{"a.env"}, schema: "s", schemaDir: "d"},
		"discovery flag without --all": {positional: []string{"a.env"}, recursive: true, schema: "s"},
		"env with --all":               {all: true, envFiles: []string{"a.env"}, schema: "s"},
		"nothing to validate":          {schema: "s", recursive: false, all: false, manifestPath: "", envFiles: nil, positional: nil, globs: []string{"*.env"}},
		"manifest with files":          {manifestPath: "heimdall.yaml", positional: []string{"a.env"}},
		"manifest with --all":          {manifestPath: "heimdall.yaml", all: true},
		"bad glob":                     {all: true, globs: []string{"["}, schema: "s"},
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := planLint(input); err == nil {
				t.Fatal("expected a usage error")
			} else if _, isUsage := err.(usageError); !isUsage {
				t.Fatalf("err = %T, want usageError", err)
			}
		})
	}
}

func TestPlanLintWithoutAnySchemaSourceIsAUsageError(t *testing.T) {
	inTempDir(t, nil)
	if _, err := planLint(lintInput{positional: []string{"a.env"}}); err == nil || !strings.Contains(err.Error(), "no schema") {
		t.Fatalf("err = %v", err)
	}
}

func TestPlanLintUsesDefaultSchemaAndManifest(t *testing.T) {
	inTempDir(t, map[string]string{"env.schema.yaml": "x"})
	plan, err := planLint(lintInput{positional: []string{"a.env"}})
	if err != nil || len(plan.targets) != 1 || plan.resolver == nil {
		t.Fatalf("plan = %+v err = %v", plan, err)
	}

	inTempDir(t, map[string]string{"heimdall.yaml": "version: 1\napplications:\n  a: {schema: s.yaml, env: [a.env]}\n", "a.env": "X=1\n"})
	plan, err = planLint(lintInput{})
	if err != nil || len(plan.targets) != 1 || plan.targets[0].Application != "a" {
		t.Fatalf("manifest plan = %+v err = %v", plan, err)
	}
}

func TestInvalidManifestBecomesDiagnosticsNotUsageError(t *testing.T) {
	inTempDir(t, map[string]string{"heimdall.yaml": "version: 1\n"})
	plan, err := planLint(lintInput{})
	if err != nil || len(plan.diagnostics) == 0 || plan.resolver != nil {
		t.Fatalf("plan = %+v err = %v", plan, err)
	}
}

func TestRunDispatch(t *testing.T) {
	run := func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), args, Environment{Stdout: &stdout, Stderr: &stderr, Getenv: func(string) string { return "" }, Version: "9.9.9"})
		return code, stdout.String(), stderr.String()
	}
	if code, out, _ := run("version"); code != 0 || out != "heimdall 9.9.9\n" {
		t.Fatalf("version: %d %q", code, out)
	}
	if code, _, errText := run(); code != batch.ExitUsage || !strings.Contains(errText, "Usage") {
		t.Fatalf("no args: %d %q", code, errText)
	}
	if code, _, _ := run("nope"); code != batch.ExitUsage {
		t.Fatalf("unknown command: %d", code)
	}
}

func TestNoColorAndTerminalDetection(t *testing.T) {
	flags := lintFlags{color: "auto"}
	env := Environment{Stdout: &bytes.Buffer{}, Getenv: func(string) string { return "" }}
	if flags.colorEnabled(env) {
		t.Fatal("a buffer is not a terminal")
	}
	always := lintFlags{color: "always"}
	if !always.colorEnabled(Environment{Stdout: &bytes.Buffer{}, Getenv: func(string) string { return "1" }}) {
		t.Fatal("--color always must win")
	}
}
