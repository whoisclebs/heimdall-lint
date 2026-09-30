package batch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/whoisclebs/heimdall-lint/internal/diag"
	"github.com/whoisclebs/heimdall-lint/internal/discovery"
	"github.com/whoisclebs/heimdall-lint/internal/resolve"
)

const reportsSchema = `
version: 1
variables:
  DATABASE_PORT: {type: port, required: true}
  API_TIMEOUT: {type: duration, default: 5s}
  LEGACY_URL: {type: string, deprecated: true}
`

type workspace struct {
	t   *testing.T
	dir string
}

func newWorkspace(t *testing.T) *workspace {
	w := &workspace{t: t, dir: t.TempDir()}
	w.write("schemas/reports-api.schema.yaml", reportsSchema)
	return w
}

func (w *workspace) write(name, content string) string {
	w.t.Helper()
	full := filepath.Join(w.dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		w.t.Fatal(err)
	}
	return full
}

func (w *workspace) run(workers int, envs ...string) Report {
	w.t.Helper()
	var paths []string
	for _, env := range envs {
		paths = append(paths, filepath.Join(w.dir, "envs", env))
	}
	jobs := resolve.All(discovery.Files(paths), resolve.Convention{Dir: filepath.Join(w.dir, "schemas")})
	report, err := Run(context.Background(), jobs, nil, Options{Workers: workers})
	if err != nil {
		w.t.Fatal(err)
	}
	return report
}

func TestAllValid(t *testing.T) {
	w := newWorkspace(t)
	w.write("envs/reports-api.env", "DATABASE_PORT=5432\n")
	report := w.run(0, "reports-api.env")
	summary := report.Summary()
	if summary.Files != 1 || summary.ValidFiles != 1 || summary.Errors != 0 || summary.VariablesChecked != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	if report.ExitCode(false) != ExitOK {
		t.Fatalf("exit = %d", report.ExitCode(false))
	}
}

func TestOneInvalidFailsTheRun(t *testing.T) {
	w := newWorkspace(t)
	w.write("envs/reports-api.env", "DATABASE_PORT=5432\n")
	w.write("schemas/other.schema.yaml", reportsSchema)
	w.write("envs/other.env", "DATABASE_PORT=banana\n")
	report := w.run(0, "reports-api.env", "other.env")
	summary := report.Summary()
	if summary.ValidFiles != 1 || summary.InvalidFiles != 1 || summary.Errors != 1 || report.ExitCode(false) != ExitInvalid {
		t.Fatalf("summary = %+v exit=%d", summary, report.ExitCode(false))
	}
}

func TestMultipleInvalid(t *testing.T) {
	w := newWorkspace(t)
	for _, name := range []string{"reports-api", "b", "c"} {
		w.write("schemas/"+name+".schema.yaml", reportsSchema)
		w.write("envs/"+name+".env", "DATABASE_PORT=x\nAPI_TIMOUT=1\n")
	}
	summary := w.run(0, "reports-api.env", "b.env", "c.env").Summary()
	if summary.InvalidFiles != 3 || summary.Errors != 6 {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestWarningsOnly(t *testing.T) {
	w := newWorkspace(t)
	w.write("envs/reports-api.env", "DATABASE_PORT=5432\nLEGACY_URL=x\n")
	report := w.run(0, "reports-api.env")
	summary := report.Summary()
	if summary.Warnings != 1 || summary.WarningFiles != 1 || summary.ValidFiles != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	if report.ExitCode(false) != ExitOK || report.ExitCode(true) != ExitInvalid {
		t.Fatalf("exit codes: %d / %d", report.ExitCode(false), report.ExitCode(true))
	}
}

func TestSchemaMissingIsExplicit(t *testing.T) {
	w := newWorkspace(t)
	w.write("envs/search-api.env", "A=1\n")
	report := w.run(0, "search-api.env")
	d := report.Files[0].Diagnostics
	if len(d) != 1 || d[0].Code != diag.SchemaNotFound || d[0].Expected != filepath.Join(w.dir, "schemas", "search-api.schema.yaml") {
		t.Fatalf("diagnostics = %+v", d)
	}
	if report.ExitCode(false) != ExitInvalid {
		t.Fatalf("exit = %d", report.ExitCode(false))
	}
}

func TestInvalidSchemaIsReportedOnceAndExitsWithContractCode(t *testing.T) {
	w := newWorkspace(t)
	w.write("schemas/broken.schema.yaml", "version: 1\nvariables:\n  A: {type: banana}\n  B: {type: integer, min: x}\n")
	for _, name := range []string{"one", "two"} {
		w.write("envs/"+name+".env", "A=1\n")
		w.write("schemas/"+name+".schema.yaml", "version: 1\nvariables:\n  A: {type: banana}\n")
	}
	w.write("envs/broken.env", "A=1\n")
	report := w.run(0, "broken.env")
	if len(report.General) != 2 {
		t.Fatalf("general = %+v", report.General)
	}
	if got := report.Files[0].Diagnostics[0].Code; got != diag.InvalidSchema {
		t.Fatalf("file diagnostic = %s", got)
	}
	if report.ExitCode(false) != ExitContract {
		t.Fatalf("exit = %d", report.ExitCode(false))
	}
}

func TestMissingEnvFile(t *testing.T) {
	w := newWorkspace(t)
	report := w.run(0, "reports-api.env")
	if d := report.Files[0].Diagnostics; len(d) != 1 || d[0].Code != diag.EnvFileUnreadable {
		t.Fatalf("diagnostics = %+v", d)
	}
}

func TestExplicitSchemaAppliesToEveryFile(t *testing.T) {
	w := newWorkspace(t)
	w.write("envs/reports-api-01.env", "DATABASE_PORT=1\n")
	w.write("envs/reports-api-02.env", "DATABASE_PORT=2\n")
	paths := []string{filepath.Join(w.dir, "envs/reports-api-01.env"), filepath.Join(w.dir, "envs/reports-api-02.env")}
	jobs := resolve.All(discovery.Files(paths), resolve.Fixed(filepath.Join(w.dir, "schemas/reports-api.schema.yaml")))
	report, err := Run(context.Background(), jobs, nil, Options{})
	if err != nil || report.Summary().ValidFiles != 2 {
		t.Fatalf("report = %+v, err = %v", report.Summary(), err)
	}
}

func TestOutputOrderDoesNotDependOnWorkers(t *testing.T) {
	w := newWorkspace(t)
	var names []string
	for i := range 60 {
		name := fmt.Sprintf("app-%02d", 59-i) // reverse creation order
		w.write("schemas/"+name+".schema.yaml", reportsSchema)
		w.write("envs/"+name+".env", fmt.Sprintf("DATABASE_PORT=%d\nUNKNOWN_%d=1\n", i, i))
		names = append(names, name+".env")
	}
	serial := w.run(1, names...)
	for _, workers := range []int{2, 8, 32} {
		for range 10 {
			parallel := w.run(workers, names...)
			if !reflect.DeepEqual(serial, parallel) {
				t.Fatalf("workers=%d produced a different report", workers)
			}
		}
	}
	for i := 1; i < len(serial.Files); i++ {
		if serial.Files[i-1].Application > serial.Files[i].Application {
			t.Fatal("files are not sorted")
		}
	}
}

func TestCancelledContext(t *testing.T) {
	w := newWorkspace(t)
	w.write("envs/reports-api.env", "DATABASE_PORT=1\n")
	jobs := resolve.All(discovery.Files([]string{filepath.Join(w.dir, "envs/reports-api.env")}), resolve.Convention{Dir: filepath.Join(w.dir, "schemas")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, jobs, nil, Options{}); err == nil {
		t.Fatal("expected context error")
	}
}

func TestWorkerCount(t *testing.T) {
	if workerCount(8, 3) != 3 || workerCount(1, 100) != 1 || workerCount(0, 0) != 1 || workerCount(-1, 5) < 1 {
		t.Fatal("unexpected worker counts")
	}
}

func TestExtraDiagnosticsAreMergedAndAffectExit(t *testing.T) {
	extra := []diag.Diagnostic{{Severity: diag.SeverityError, Code: diag.InvalidManifest, Message: "bad"}}
	report, err := Run(context.Background(), nil, extra, Options{})
	if err != nil || report.ExitCode(false) != ExitContract {
		t.Fatalf("report = %+v err = %v exit = %d", report, err, report.ExitCode(false))
	}
}
