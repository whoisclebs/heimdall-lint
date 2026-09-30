// Package e2e runs the real heimdall binary and checks stdout, stderr, exit
// codes and JSON. Fixtures are written into temporary directories so each test
// shows exactly the scenario it exercises; the shipped examples are covered too.
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "heimdall-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "heimdall")
	build := exec.Command("go", "build", "-o", binary, "github.com/whoisclebs/heimdall-lint/cmd/heimdall")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build failed: %v\n%s", err, output)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type result struct {
	stdout, stderr string
	exit           int
}

func heimdall(t *testing.T, dir string, args ...string) result {
	t.Helper()
	return heimdallEnv(t, dir, nil, args...)
}

func heimdallEnv(t *testing.T, dir string, env []string, args ...string) result {
	t.Helper()
	command := exec.Command(binary, args...)
	command.Dir = dir
	command.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr

	err := command.Run()
	exit := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exit = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("running heimdall: %v", err)
	}
	return result{stdout: stdout.String(), stderr: stderr.String(), exit: exit}
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (r result) expectExit(t *testing.T, want int) {
	t.Helper()
	if r.exit != want {
		t.Fatalf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", r.exit, want, r.stdout, r.stderr)
	}
}

func (r result) expectStdout(t *testing.T, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(r.stdout, fragment) {
			t.Errorf("stdout lacks %q\n%s", fragment, r.stdout)
		}
	}
}

const reportsSchema = `version: 1
variables:
  DATABASE_HOST: {type: hostname, required: true}
  DATABASE_PORT: {type: port, required: true}
  QUEUE_PROVIDER: {type: enum, required: true, values: [KAFKA, RABBITMQ, SQS, NATS]}
  API_TIMEOUT: {type: duration, default: 5s, min: 100ms, max: 30s}
  API_BASE_URL: {type: url, required: true, schemes: [https]}
  API_KEY: {type: string, required: true, secret: true}
  FEATURE_BETA: {type: boolean, default: false}
  LEGACY_API_URL: {type: string, deprecated: true, replacement: API_BASE_URL}
`

const validReportsEnv = `DATABASE_HOST=db.internal
DATABASE_PORT=5432
QUEUE_PROVIDER=KAFKA
API_BASE_URL=https://api.example.com
API_KEY=abcd-super-secret-value
`

func TestSingleFileValid(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "env.schema.yaml", reportsSchema)
	write(t, dir, "production.env", validReportsEnv)

	r := heimdall(t, dir, "lint", "production.env")
	r.expectExit(t, 0)
	r.expectStdout(t, "production.env\n  PASS\n  5 variables checked", "Environment validation passed.")
	if r.stderr != "" {
		t.Errorf("stderr = %q", r.stderr)
	}
}

func TestSingleFileWithExplicitFlags(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "reports-api.schema.yaml", reportsSchema)
	write(t, dir, "reports-api.env", validReportsEnv)
	heimdall(t, dir, "lint", "--schema", "reports-api.schema.yaml", "--env", "reports-api.env").expectExit(t, 0)
}

func TestTypoProducesDidYouMean(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "env.schema.yaml", reportsSchema)
	write(t, dir, "production.env", validReportsEnv+"API_TIMOUT=5000\napi.timeout=5000\n")

	r := heimdall(t, dir, "lint", "production.env")
	r.expectExit(t, 1)
	r.expectStdout(t,
		"ERROR API_TIMOUT (HML002, line 6)",
		"Unknown environment variable.\n\n    Did you mean:\n      API_TIMEOUT",
		"Spring property notation detected.\n\n    Use:\n      API_TIMEOUT",
		"Environment validation failed.")
}

func TestWrongTypeShowsExpectedAndReceived(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "env.schema.yaml", reportsSchema)
	write(t, dir, "production.env", strings.Replace(validReportsEnv, "5432", "banana", 1))

	heimdall(t, dir, "lint", "production.env").expectStdout(t,
		"DATABASE_PORT", "Expected:\n      port number between 1 and 65535", "Received:\n      \"banana\"")
}

func TestSecretsNeverAppearInAnyOutput(t *testing.T) {
	const secret = "abcd-super-secret-value"
	dir := t.TempDir()
	write(t, dir, "env.schema.yaml", `version: 1
variables:
  API_KEY: {type: port, required: true, secret: true}
  API_TOKEN: {type: enum, values: [A, B], secret: true}
  OLD_KEY: {type: string, secret: true, deprecated: true}
`)
	write(t, dir, "bad.env", "API_KEY="+secret+"\nAPI_TOKEN="+secret+"\nOLD_KEY="+secret+"\nAPI_KEY="+secret+"\n")

	for _, args := range [][]string{
		{"lint", "bad.env"},
		{"lint", "bad.env", "--format", "json"},
		{"lint", "--all", ".", "--quiet", "--color", "always"},
		{"inspect", "API_KEY"},
	} {
		r := heimdall(t, dir, args...)
		if strings.Contains(r.stdout+r.stderr, secret) {
			t.Fatalf("secret leaked with args %v:\n%s\n%s", args, r.stdout, r.stderr)
		}
	}
	r := heimdall(t, dir, "lint", "bad.env")
	r.expectStdout(t, "[REDACTED]")
}

func TestBatchWithSchemaDirAndDeterministicOutput(t *testing.T) {
	dir := t.TempDir()
	for i := range 40 {
		name := fmt.Sprintf("app-%02d", i)
		write(t, dir, "schemas/"+name+".schema.yaml", reportsSchema)
		env := validReportsEnv
		if i%7 == 0 {
			env += fmt.Sprintf("EXTRA_%d=1\n", i)
		}
		write(t, dir, "envs/"+name+".env", env)
	}

	serial := heimdall(t, dir, "lint", "--all", "envs", "--schema-dir", "schemas", "--jobs", "1")
	serial.expectExit(t, 1)
	serial.expectStdout(t, "Files discovered:     40", "Files invalid:        6")
	for _, jobs := range []string{"2", "8", "64"} {
		for range 5 {
			parallel := heimdall(t, dir, "lint", "--all", "envs", "--schema-dir", "schemas", "--jobs", jobs)
			if parallel.stdout != serial.stdout || parallel.exit != serial.exit {
				t.Fatalf("--jobs %s changed the output", jobs)
			}
		}
	}
}

func TestBatchFlagsAfterPositionalsWorkAsInDockerExamples(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "schemas/a.schema.yaml", reportsSchema)
	write(t, dir, "envs/a.env", validReportsEnv)
	heimdall(t, dir, "lint", "--all", filepath.Join(dir, "envs"), "--schema-dir", filepath.Join(dir, "schemas")).expectExit(t, 0)
}

func TestBatchAllValid(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "schemas/a.schema.yaml", reportsSchema)
	write(t, dir, "schemas/b.schema.yaml", reportsSchema)
	write(t, dir, "envs/a.env", validReportsEnv)
	write(t, dir, "envs/b.env", validReportsEnv)
	r := heimdall(t, dir, "lint", "--all", "envs", "--schema-dir", "schemas")
	r.expectExit(t, 0)
	r.expectStdout(t, "Files valid:          2", "Environment validation passed.")
}

func TestMissingSchemaIsAnExplicitError(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "schemas/a.schema.yaml", reportsSchema)
	write(t, dir, "envs/a.env", validReportsEnv)
	write(t, dir, "envs/search-api.env", "X=1\n")

	r := heimdall(t, dir, "lint", "--all", "envs", "--schema-dir", "schemas")
	r.expectExit(t, 1)
	r.expectStdout(t, "envs/search-api.env", "Schema not found.", "Expected:\n      "+filepath.Join("schemas", "search-api.schema.yaml"))
}

func TestExplicitSchemaForManyNodes(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "reports-api.schema.yaml", reportsSchema)
	for _, node := range []string{"01", "02", "03"} {
		write(t, dir, "nodes/reports-api-"+node+".env", validReportsEnv)
	}
	r := heimdall(t, dir, "lint", "--all", "nodes", "--schema", "reports-api.schema.yaml")
	r.expectExit(t, 0)
	r.expectStdout(t, "Files discovered:     3")
}

func TestDiscoveryFlags(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "s.schema.yaml", reportsSchema)
	write(t, dir, "envs/a.production.env", validReportsEnv)
	write(t, dir, "envs/b.staging.env", "BROKEN LINE\n")
	write(t, dir, "envs/a.example.env", "BROKEN LINE\n")
	write(t, dir, "envs/nested/c.production.env", validReportsEnv)
	write(t, dir, "envs/skip.production.env", "BROKEN LINE\n")

	r := heimdall(t, dir, "lint", "--all", "envs", "--schema", "s.schema.yaml", "--glob", "*.production.env", "--exclude", "skip.*")
	r.expectExit(t, 0)
	r.expectStdout(t, "Files discovered:     1")

	r = heimdall(t, dir, "lint", "--all", "envs", "--recursive", "--schema", "s.schema.yaml", "--glob", "*.production.env", "--exclude", "skip.*")
	r.expectExit(t, 0)
	r.expectStdout(t, "Files discovered:     2")
}

func TestManifestMode(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "schemas/reports-api.schema.yaml", reportsSchema)
	write(t, dir, "envs/reports-api-01.env", validReportsEnv)
	write(t, dir, "envs/reports-api-02.env", validReportsEnv)
	write(t, dir, "envs/old-reports-api.env", validReportsEnv)
	write(t, dir, "heimdall.yaml", `version: 1
applications:
  reports-api:
    schema: schemas/reports-api.schema.yaml
    env:
      - envs/reports-api-*.env
`)

	r := heimdall(t, dir, "lint")
	r.expectExit(t, 0)
	r.expectStdout(t, "WARNING orphan_env_file (HML012)", "not declared in heimdall.yaml", "Files discovered:     2", "passed with warnings")

	heimdall(t, dir, "lint", "--warnings-as-errors").expectExit(t, 1)
	heimdall(t, dir, "lint", "--manifest", "heimdall.yaml").expectExit(t, 0)

	write(t, dir, "envs/reports-api-02.env", strings.Replace(validReportsEnv, "5432", "nope", 1))
	heimdall(t, dir, "lint").expectExit(t, 1)
}

func TestInvalidManifestAndSchemaExitWithContractCode(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "heimdall.yaml", "version: 1\napplications:\n  a: {env: [x.env]}\n")
	r := heimdall(t, dir, "lint")
	r.expectExit(t, 3)
	r.expectStdout(t, "HML011", "missing required field: schema")

	dir = t.TempDir()
	write(t, dir, "env.schema.yaml", "version: 1\nvariables:\n  A: {type: banana}\n")
	write(t, dir, "a.env", "A=1\n")
	r = heimdall(t, dir, "lint", "a.env")
	r.expectExit(t, 3)
	r.expectStdout(t, "HML009", `unknown type "banana"`)
}

func TestFailClosedOnUnreadableInputs(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "env.schema.yaml", reportsSchema)
	r := heimdall(t, dir, "lint", "missing.env")
	r.expectExit(t, 1)
	r.expectStdout(t, "Environment file not found.")

	write(t, dir, "broken.env", "THIS IS NOT VALID\n")
	heimdall(t, dir, "lint", "broken.env").expectStdout(t, "HML013")
}

func TestWarningsDoNotFailUnlessAsked(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "env.schema.yaml", reportsSchema)
	write(t, dir, "warn.env", validReportsEnv+"LEGACY_API_URL=x\n")

	r := heimdall(t, dir, "lint", "warn.env")
	r.expectExit(t, 0)
	r.expectStdout(t, "WARNING LEGACY_API_URL (HML007, line 6)", "Use:\n      API_BASE_URL")
	heimdall(t, dir, "lint", "warn.env", "--warnings-as-errors").expectExit(t, 1)
}

func TestJSONOutput(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "env.schema.yaml", reportsSchema)
	write(t, dir, "a.env", validReportsEnv)
	write(t, dir, "b.env", validReportsEnv+"API_TIMOUT=1\n")

	r := heimdall(t, dir, "lint", "a.env", "b.env", "--format", "json")
	r.expectExit(t, 1)

	var report struct {
		Valid    bool `json:"valid"`
		ExitCode int  `json:"exit_code"`
		Summary  struct {
			Files        int `json:"files"`
			ValidFiles   int `json:"valid_files"`
			InvalidFiles int `json:"invalid_files"`
			Errors       int `json:"errors"`
		} `json:"summary"`
		Files []struct {
			Path        string `json:"path"`
			Valid       bool   `json:"valid"`
			Diagnostics []struct {
				Code        string   `json:"code"`
				Variable    string   `json:"variable"`
				Suggestions []string `json:"suggestions"`
			} `json:"diagnostics"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &report); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, r.stdout)
	}
	if report.Valid || report.ExitCode != 1 || report.Summary.Files != 2 || report.Summary.ValidFiles != 1 || report.Summary.Errors != 1 {
		t.Fatalf("report = %+v", report)
	}
	if !report.Files[0].Valid || report.Files[1].Valid {
		t.Fatalf("files = %+v", report.Files)
	}
	d := report.Files[1].Diagnostics[0]
	if d.Code != "HML002" || d.Variable != "API_TIMOUT" || len(d.Suggestions) != 1 || d.Suggestions[0] != "API_TIMEOUT" {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestQuietPrintsOnlyProblems(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "env.schema.yaml", reportsSchema)
	write(t, dir, "good.env", validReportsEnv)
	write(t, dir, "bad.env", validReportsEnv+"API_TIMOUT=1\n")
	r := heimdall(t, dir, "lint", "--all", ".", "--schema", "env.schema.yaml", "--quiet")
	if strings.Contains(r.stdout, "good.env") || !strings.Contains(r.stdout, "bad.env") {
		t.Fatalf("stdout:\n%s", r.stdout)
	}
}

func TestColorHandling(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "env.schema.yaml", reportsSchema)
	write(t, dir, "a.env", validReportsEnv)

	// Not a TTY: no ANSI, even with a colorful TERM.
	if r := heimdallEnv(t, dir, []string{"TERM=xterm-256color"}, "lint", "a.env"); strings.Contains(r.stdout, "\x1b[") {
		t.Error("ANSI escape sequences in non-TTY output")
	}
	if r := heimdall(t, dir, "lint", "a.env", "--color", "always"); !strings.Contains(r.stdout, "\x1b[") {
		t.Error("--color always must colorize")
	}
	if r := heimdallEnv(t, dir, []string{"NO_COLOR=1"}, "lint", "a.env", "--color", "never"); strings.Contains(r.stdout, "\x1b[") {
		t.Error("--color never must not colorize")
	}
}

func TestUsageErrors(t *testing.T) {
	dir := t.TempDir()
	tests := map[string][]string{
		"no command":            {},
		"unknown command":       {"frobnicate"},
		"nothing to validate":   {"lint"},
		"no schema":             {"lint", "a.env"},
		"bad format":            {"lint", "a.env", "--format", "xml"},
		"bad flag":              {"lint", "--nope"},
		"recursive without all": {"lint", "a.env", "--recursive"},
		"both schema flags":     {"lint", "a.env", "--schema", "s", "--schema-dir", "d"},
		"bad glob":              {"lint", "--all", ".", "--glob", "["},
		"tui without tty":       {"lint", "a.env", "--tui"},
		"inspect no name":       {"inspect"},
		"diff one arg":          {"diff", "a.yaml"},
		"image with schema":     {"lint", "a.env", "--image", "app:1", "--schema", "s.yaml"},
		"image with schema-dir": {"lint", "a.env", "--image", "app:1", "--schema-dir", "d"},
		"inspect image+schema":  {"inspect", "X", "--image", "app:1", "--schema", "s.yaml"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			r := heimdall(t, dir, args...)
			r.expectExit(t, 2)
			if r.stderr == "" {
				t.Error("usage errors must explain themselves on stderr")
			}
		})
	}
}

func TestHelpAndVersion(t *testing.T) {
	dir := t.TempDir()
	r := heimdall(t, dir, "--help")
	r.expectExit(t, 0)
	r.expectStdout(t, "heimdall lint", "Exit codes")
	heimdall(t, dir, "lint", "-h").expectExit(t, 0)
	r = heimdall(t, dir, "version")
	r.expectExit(t, 0)
	r.expectStdout(t, "heimdall ")
}

func TestInspect(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "env.schema.yaml", "version: 1\nvariables:\n  API_TIMEOUT: {type: duration, default: 5s, min: 100ms, max: 30s, description: Upstream timeout}\n")

	r := heimdall(t, dir, "inspect", "API_TIMEOUT")
	r.expectExit(t, 0)
	r.expectStdout(t, "Type:           duration", "Default:        5s", "Minimum:        100ms", "Maximum:        30s", "Upstream timeout")

	r = heimdall(t, dir, "inspect", "API_TIMOUT")
	r.expectExit(t, 1)
	if !strings.Contains(r.stderr, "API_TIMEOUT") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

func TestInspectWithApplications(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "schemas/a.schema.yaml", "version: 1\nvariables:\n  SHARED: {type: integer}\n")
	write(t, dir, "schemas/b.schema.yaml", "version: 1\nvariables:\n  SHARED: {type: string}\n")
	both := heimdall(t, dir, "inspect", "SHARED", "--schema-dir", "schemas")
	both.expectExit(t, 0)
	both.expectStdout(t, "Type:           integer", "Type:           string")

	one := heimdall(t, dir, "inspect", "SHARED", "--schema-dir", "schemas", "--application", "b")
	one.expectExit(t, 0)
	if strings.Contains(one.stdout, "integer") {
		t.Errorf("--application must narrow the search\n%s", one.stdout)
	}
	heimdall(t, dir, "inspect", "SHARED", "--schema-dir", "schemas", "--application", "zzz").expectExit(t, 2)
}

func TestDiff(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "v1.yaml", "version: 1\nvariables:\n  API_TIMEOUT: {type: duration, default: 5s}\n  LEGACY_API_URL: {type: string}\n")
	write(t, dir, "v2.yaml", "version: 1\nvariables:\n  API_TIMEOUT: {type: duration, default: 10s}\n  API_READ_TIMEOUT: {type: duration, required: true}\n")

	r := heimdall(t, dir, "diff", "v1.yaml", "v2.yaml")
	r.expectExit(t, 0)
	r.expectStdout(t, "ADDED", "API_READ_TIMEOUT  [action required]", "REMOVED", "LEGACY_API_URL", "CHANGED", "default: 5s -> 10s")

	r = heimdall(t, dir, "diff", "v1.yaml", "v2.yaml", "--format", "json")
	if !json.Valid([]byte(r.stdout)) {
		t.Fatalf("not JSON: %s", r.stdout)
	}

	write(t, dir, "bad.yaml", "version: 1\nvariables:\n  A: {type: banana}\n")
	heimdall(t, dir, "diff", "v1.yaml", "bad.yaml").expectExit(t, 3)
	heimdall(t, dir, "diff", "v1.yaml", "missing.yaml").expectExit(t, 3)
}

func TestTerminalEscapeSequencesInFilesAreNeutralised(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "env.schema.yaml", "version: 1\nvariables:\n  PORT: {type: integer}\n")
	write(t, dir, "a.env", "PORT=\"\\x1b[2Jpwned\"\n")
	r := heimdall(t, dir, "lint", "a.env")
	if strings.ContainsRune(r.stdout, 0x1b) {
		t.Fatalf("raw ESC reached stdout: %q", r.stdout)
	}
}

func TestShippedExamples(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	example := func(name string) string { return filepath.Join(root, "examples", name) }

	tests := []struct {
		example string
		args    []string
		exit    int
		want    string
	}{
		{"basic", []string{"lint", "production.env"}, 0, "PASS"},
		{"basic", []string{"lint", "broken.env"}, 1, "Did you mean:\n      API_TIMEOUT"},
		{"batch", []string{"lint", "--all", "envs", "--schema-dir", "schemas"}, 1, "INVENTORY_TIMEOUT"},
		{"manifest", []string{"lint"}, 0, "orphan_env_file"},
		{"spring-boot", []string{"lint", "application.env"}, 0, "PASS"},
		{"spring-boot", []string{"lint", "mistakes.env"}, 1, "Spring property notation detected."},
		{"docker-compose", []string{"lint", "--all", "envs", "--schema-dir", "schemas"}, 0, "Files discovered:     2"},
		{"docker-compose", []string{"lint", "compose.yaml", "--schema-dir", "schemas"}, 0, "compose.yaml [service reports-api]"},
	}
	for _, tt := range tests {
		t.Run(tt.example+"/"+strings.Join(tt.args[1:], " "), func(t *testing.T) {
			r := heimdall(t, example(tt.example), tt.args...)
			r.expectExit(t, tt.exit)
			r.expectStdout(t, tt.want)
		})
	}
}

func TestHundredFilesArePromptEnough(t *testing.T) {
	dir := t.TempDir()
	for i := range 100 {
		name := fmt.Sprintf("app-%03d", i)
		write(t, dir, "schemas/"+name+".schema.yaml", reportsSchema)
		write(t, dir, "envs/"+name+".env", validReportsEnv)
	}
	start := time.Now()
	r := heimdall(t, dir, "lint", "--all", "envs", "--schema-dir", "schemas")
	elapsed := time.Since(start)
	r.expectExit(t, 0)
	t.Logf("100 files validated in %s (process start included)", elapsed)
	if elapsed > 3*time.Second {
		t.Errorf("100 files took %s", elapsed)
	}
}

const springMetadata = `{"properties":[
  {"name":"api.timeout","type":"java.time.Duration","defaultValue":"5s","description":"Upstream timeout."},
  {"name":"api.enabled","type":"java.lang.Boolean","defaultValue":false},
  {"name":"server.port","type":"java.lang.Integer","defaultValue":8080}
]}`

func TestSpringImportProducesASchemaThatLintAccepts(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "META-INF/spring-configuration-metadata.json", springMetadata)

	r := heimdall(t, dir, "schema", "generate", ".", "--prefix", "api.", "--output", "env.schema.yaml")
	r.expectExit(t, 0)
	if !strings.Contains(r.stderr, "wrote env.schema.yaml") {
		t.Errorf("stderr = %q", r.stderr)
	}

	write(t, dir, "ok.env", "API_TIMEOUT=10s\nAPI_ENABLED=true\n")
	heimdall(t, dir, "lint", "ok.env").expectExit(t, 0)

	write(t, dir, "bad.env", "API_TIMEOUT=soon\napi.enabled=true\n")
	r = heimdall(t, dir, "lint", "bad.env")
	r.expectExit(t, 1)
	r.expectStdout(t, "Spring property notation detected.", "Use:\n      API_ENABLED")

	heimdall(t, dir, "inspect", "API_TIMEOUT").expectStdout(t, "Type:           duration", "Default:        5s", "Upstream timeout.")
}

func TestSpringImportAliasStdoutAndOverwriteProtection(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "spring-configuration-metadata.json", springMetadata)

	r := heimdall(t, dir, "spring", "import", "spring-configuration-metadata.json")
	r.expectExit(t, 0)
	r.expectStdout(t, "version: 1", "SERVER_PORT:", "API_ENABLED:")

	write(t, dir, "existing.yaml", "keep me")
	heimdall(t, dir, "spring", "import", "spring-configuration-metadata.json", "--output", "existing.yaml").expectExit(t, 2)
	if data, _ := os.ReadFile(filepath.Join(dir, "existing.yaml")); string(data) != "keep me" {
		t.Fatal("existing file was overwritten without --force")
	}
	heimdall(t, dir, "spring", "import", "spring-configuration-metadata.json", "--output", "existing.yaml", "--force").expectExit(t, 0)
}

func TestSpringImportFailures(t *testing.T) {
	dir := t.TempDir()
	heimdall(t, dir, "schema", "generate", "missing.jar").expectExit(t, 1)
	heimdall(t, dir, "schema").expectExit(t, 2)
	heimdall(t, dir, "spring", "bogus").expectExit(t, 2)
	write(t, dir, "m.json", springMetadata)
	heimdall(t, dir, "schema", "generate", "m.json", "--prefix", "nope.").expectExit(t, 1)
}

func TestRulesBetweenVariables(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "env.schema.yaml", `version: 1
variables:
  REDIS_ENABLED: {type: boolean, default: false}
  REDIS_URL:     {type: url, required_if: {REDIS_ENABLED: true}, conflicts_with: [MEMCACHED_URL]}
  MEMCACHED_URL: {type: string}
  TLS_CERT:      {type: string, requires: [TLS_KEY]}
  TLS_KEY:       {type: string, secret: true}
`)
	write(t, dir, "ok.env", "REDIS_ENABLED=false\nMEMCACHED_URL=m:11211\n")
	heimdall(t, dir, "lint", "ok.env").expectExit(t, 0)

	write(t, dir, "bad.env", "REDIS_ENABLED=true\nTLS_CERT=/c.pem\n")
	r := heimdall(t, dir, "lint", "bad.env")
	r.expectExit(t, 1)
	r.expectStdout(t,
		"REDIS_URL (HML016)", "Required because REDIS_ENABLED is true.",
		"TLS_KEY (HML017)", "Required because TLS_CERT is set.")

	write(t, dir, "conflict.env", "REDIS_ENABLED=true\nREDIS_URL=redis://r\nMEMCACHED_URL=m\n")
	heimdall(t, dir, "lint", "conflict.env").expectStdout(t, "MEMCACHED_URL (HML018, line 3)", "Cannot be set together with REDIS_URL.")

	heimdall(t, dir, "inspect", "REDIS_URL").expectStdout(t, "Required if:    REDIS_ENABLED=true", "Conflicts with: MEMCACHED_URL")
}

const composeSchema = `version: 1
variables:
  DATABASE_PORT: {type: port, required: true}
  API_TIMEOUT: {type: duration, default: 5s, min: 100ms, max: 30s}
  API_KEY: {type: string, required: true, secret: true}
`

func TestComposeValidatesEffectiveEnvironmentPerService(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "schemas/reports-api.schema.yaml", composeSchema)
	write(t, dir, "envs/reports-api.env", "DATABASE_PORT=banana\nAPI_KEY=abcd-super-secret-value\n")
	write(t, dir, "compose.yaml", `services:
  db:
    image: postgres
  reports-api:
    image: x
    env_file: envs/reports-api.env
    environment:
      API_TIMEOUT: 50ms
`)

	r := heimdall(t, dir, "lint", "compose.yaml", "--schema-dir", "schemas")
	r.expectExit(t, 1)
	r.expectStdout(t,
		"compose.yaml [service reports-api]",
		"DATABASE_PORT", "Received:\n      \"banana\"",
		"API_TIMEOUT", "below the allowed minimum",
		"Files discovered:     1") // db has no environment, so it is not a target
	if strings.Contains(r.stdout+r.stderr, "abcd-super-secret-value") {
		t.Fatal("secret leaked")
	}
}

func TestComposeEnvironmentOverridesEnvFileAndInterpolates(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "s.schema.yaml", composeSchema)
	write(t, dir, "base.env", "DATABASE_PORT=banana\nAPI_KEY=k\n")
	write(t, dir, "compose.yaml", `services:
  app:
    env_file: [base.env]
    environment:
      DATABASE_PORT: ${DB_PORT:-5432}
`)
	heimdall(t, dir, "lint", "compose.yaml", "--schema", "s.schema.yaml").expectExit(t, 0)

	r := heimdallEnv(t, dir, []string{"DB_PORT=oops"}, "lint", "--compose", "compose.yaml", "--schema", "s.schema.yaml")
	r.expectExit(t, 1)
	r.expectStdout(t, `"oops"`)
}

func TestComposeSchemaFromExtensionSkipAndMissing(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "contracts/api.yaml", composeSchema)
	write(t, dir, "compose.yaml", `services:
  api:
    environment: {DATABASE_PORT: "5432", API_KEY: k}
    x-heimdall:
      schema: contracts/api.yaml
  postgres:
    environment: {POSTGRES_PASSWORD: pw}
    x-heimdall: {skip: true}
  worker:
    environment: {A: b}
`)
	r := heimdall(t, dir, "lint", "compose.yaml")
	r.expectExit(t, 1)
	r.expectStdout(t, "compose.yaml [service worker]", "schema_not_found (HML010)", "no schema declared for \"worker\"", "Files discovered:     2")
	if strings.Contains(r.stdout, "postgres") {
		t.Errorf("skipped service was validated:\n%s", r.stdout)
	}

	write(t, dir, "compose.yaml", strings.Replace(mustRead(t, dir, "compose.yaml"), "  worker:\n    environment: {A: b}\n", "", 1))
	heimdall(t, dir, "lint", "compose.yaml").expectExit(t, 0)
}

func TestComposeProblemsFailClosed(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "s.schema.yaml", composeSchema)
	write(t, dir, "compose.yaml", "services:\n  a:\n    env_file: missing.env\n    environment: {DATABASE_PORT: '1', API_KEY: k}\n")
	r := heimdall(t, dir, "lint", "compose.yaml", "--schema", "s.schema.yaml")
	r.expectExit(t, 1)
	r.expectStdout(t, "Environment file not found.")

	write(t, dir, "bad-compose.yaml", "services:\n  a:\n    x-heimdall: {schma: s}\n")
	r = heimdall(t, dir, "lint", "--compose", "bad-compose.yaml", "--schema", "s.schema.yaml")
	r.expectExit(t, 3)
	r.expectStdout(t, "HML011", "unknown x-heimdall field")

	heimdall(t, dir, "lint", "--compose", "compose.yaml", "other.env").expectExit(t, 2)
	heimdall(t, dir, "lint", "--compose", "compose.yaml", "--all").expectExit(t, 2)
}

func mustRead(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Hostile image references are refused before docker is ever invoked, so this
// runs without Docker and with an empty PATH.
func TestImageReferencesAreValidatedBeforeDockerRuns(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a.env", "A=1\n")
	for _, ref := range []string{"--privileged", "-v", "a b", "$(id)"} {
		r := heimdallEnv(t, dir, []string{"PATH=" + dir}, "lint", "a.env", "--image="+ref)
		r.expectExit(t, 3)
		r.expectStdout(t, "HML009", "invalid image reference")
	}
	heimdallEnv(t, dir, []string{"PATH=" + dir}, "diff", "image://-v", "image://-w").expectExit(t, 3)
}

// Compose files lean heavily on anchors and merge keys. A service whose
// environment comes from an anchor must be validated, never silently skipped.
func TestComposeAnchorsAndMergeKeysAreNotSilentlySkipped(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "s.schema.yaml", composeSchema)
	write(t, dir, "compose.yaml", `x-common: &common
  DATABASE_PORT: "5432"
  API_KEY: k
x-defaults: &defaults
  environment: *common

services:
  direct:
    environment: {<<: *common}
  inherited:
    <<: *defaults
  broken:
    environment:
      <<: *common
      DATABASE_PORT: banana
`)
	r := heimdall(t, dir, "lint", "compose.yaml", "--schema", "s.schema.yaml")
	r.expectExit(t, 1)
	r.expectStdout(t, "Files discovered:     3", "compose.yaml [service direct]", "compose.yaml [service inherited]", "Received:\n      \"banana\"")
	if strings.Contains(r.stdout, "API_KEY (HML001)") {
		t.Errorf("inherited variables were lost:\n%s", r.stdout)
	}
}

func TestComposeWarnsAboutConstructsItDoesNotInterpret(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "s.schema.yaml", composeSchema)
	write(t, dir, "compose.yaml", `include: [more.yaml]
services:
  api:
    extends: {service: base}
    environment: {DATABASE_PORT: "5432", API_KEY: k}
`)
	r := heimdall(t, dir, "lint", "compose.yaml", "--schema", "s.schema.yaml")
	r.expectExit(t, 0) // a warning, not a failure
	r.expectStdout(t, "unsupported_construct (HML019)", "include is not interpreted", `service "api" uses extends`, "passed with warnings")
	heimdall(t, dir, "lint", "compose.yaml", "--schema", "s.schema.yaml", "--warnings-as-errors").expectExit(t, 1)
}
