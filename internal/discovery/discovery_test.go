package discovery

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/whoisclebs/heimdall-lint/internal/diag"
	"github.com/whoisclebs/heimdall-lint/internal/manifest"
)

func touch(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, name := range names {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("A=1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func relative(t *testing.T, root string, targets []Target) []string {
	t.Helper()
	var out []string
	for _, target := range targets {
		r, err := filepath.Rel(root, target.Path)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, filepath.ToSlash(r))
	}
	return out
}

func TestSingleDirectory(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "reports-api.env", "inventory-api.env", "notes.txt", "sub/deep.env")
	targets, diagnostics := Directories([]string{root}, Options{})
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	if got := relative(t, root, targets); !slices.Equal(got, []string{"inventory-api.env", "reports-api.env"}) {
		t.Fatalf("targets = %v (must not recurse by default)", got)
	}
	if targets[0].Application != "inventory-api" {
		t.Fatalf("application = %q", targets[0].Application)
	}
}

func TestRecursiveSkipsHiddenDirectories(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "a/one.env", "a/b/two.env", ".git/hooks.env", ".hidden/x.env")
	targets, _ := Directories([]string{root}, Options{Recursive: true})
	if got := relative(t, root, targets); !slices.Equal(got, []string{"a/b/two.env", "a/one.env"}) {
		t.Fatalf("targets = %v", got)
	}
}

func TestDefaultExcludesAndHiddenEnvFiles(t *testing.T) {
	root := t.TempDir()
	touch(t, root, ".env", ".env.production", "app.env", "app.example.env", ".env.example", ".env.template",
		".env.backup", "app.env.bak", "old.bak", "app.env~", "prod.backup.env", "app.env.orig")
	targets, _ := Directories([]string{root}, Options{})
	if got := relative(t, root, targets); !slices.Equal(got, []string{".env", ".env.production", "app.env"}) {
		t.Fatalf("targets = %v", got)
	}
}

func TestGlobAndExclude(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "a.production.env", "b.production.env", "c.staging.env", "legacy.production.env")
	targets, _ := Directories([]string{root}, Options{
		Globs: []string{"*.production.env"}, Excludes: []string{"legacy.*"},
	})
	if got := relative(t, root, targets); !slices.Equal(got, []string{"a.production.env", "b.production.env"}) {
		t.Fatalf("targets = %v", got)
	}
}

func TestExcludeMatchesRelativePath(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "keep/a.env", "skip/b.env")
	targets, _ := Directories([]string{root}, Options{Recursive: true, Excludes: []string{"skip/*"}})
	if got := relative(t, root, targets); !slices.Equal(got, []string{"keep/a.env"}) {
		t.Fatalf("targets = %v", got)
	}
}

func TestSymlinksAreNotFollowedByDefault(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	touch(t, root, "real.env")
	touch(t, outside, "secret.env", "nested/other.env")
	must(t, os.Symlink(filepath.Join(outside, "secret.env"), filepath.Join(root, "link.env")))
	must(t, os.Symlink(outside, filepath.Join(root, "linkdir")))
	must(t, os.Symlink(root, filepath.Join(root, "loop")))

	targets, _ := Directories([]string{root}, Options{Recursive: true})
	if got := relative(t, root, targets); !slices.Equal(got, []string{"real.env"}) {
		t.Fatalf("targets = %v", got)
	}

	targets, _ = Directories([]string{root}, Options{Recursive: true, FollowSymlinks: true})
	got := relative(t, root, targets)
	if !slices.Contains(got, "link.env") || !slices.Contains(got, "linkdir/nested/other.env") {
		t.Fatalf("opt-in must follow links, got %v", got)
	}
	if len(got) != len(slices.Compact(slices.Clone(got))) {
		t.Fatalf("duplicates in %v", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestEmptyMissingAndNonDirectoryRoots(t *testing.T) {
	empty := t.TempDir()
	touch(t, empty, "readme.txt")
	file := filepath.Join(empty, "readme.txt")

	for name, root := range map[string]string{"empty": empty, "missing": filepath.Join(empty, "nope"), "file": file} {
		targets, diagnostics := Directories([]string{root}, Options{})
		if len(targets) != 0 || len(diagnostics) != 1 || diagnostics[0].Severity != diag.SeverityError {
			t.Errorf("%s: targets=%v diagnostics=%+v", name, targets, diagnostics)
		}
	}
}

func TestApplicationName(t *testing.T) {
	root := t.TempDir()
	tests := map[string]string{
		"reports-api.env":                      "reports-api",
		"reports-api.production.env":           "reports-api.production",
		".env.staging":                         "staging",
		filepath.Join(root, "billing", ".env"): "billing",
	}
	for input, want := range tests {
		if got := ApplicationName(input); got != want {
			t.Errorf("ApplicationName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestFilesDeduplicates(t *testing.T) {
	targets := Files([]string{"a.env", "./a.env", "b.env"})
	if len(targets) != 2 {
		t.Fatalf("targets = %+v", targets)
	}
}

func TestValidatePatterns(t *testing.T) {
	if err := (Options{Globs: []string{"["}}).ValidatePatterns(); err == nil {
		t.Fatal("bad glob accepted")
	}
	if err := (Options{Globs: []string{"*.env"}, Excludes: []string{"a*"}}).ValidatePatterns(); err != nil {
		t.Fatal(err)
	}
}

func parseManifest(t *testing.T, root, body string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse([]byte(body), filepath.Join(root, "heimdall.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestFromManifestExpandsGlobsAndReportsOrphans(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "envs/reports-api-01.env", "envs/reports-api-02.env", "envs/scheduler.env", "envs/old-reports-api.env", "envs/ignored.env.example")
	m := parseManifest(t, root, `
version: 1
applications:
  reports-api: {schema: s.yaml, env: ["envs/reports-api-*.env"]}
  scheduler:   {schema: s.yaml, env: [envs/scheduler.env, envs/missing.env]}
`)
	targets, diagnostics := FromManifest(m)

	if got := relative(t, root, targets); !slices.Equal(got, []string{"envs/missing.env", "envs/reports-api-01.env", "envs/reports-api-02.env", "envs/scheduler.env"}) {
		t.Fatalf("targets = %v", got)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != diag.OrphanEnvFile || diagnostics[0].Severity != diag.SeverityWarning ||
		filepath.Base(diagnostics[0].File) != "old-reports-api.env" {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}

func TestFromManifestGlobMatchingNothingIsAnError(t *testing.T) {
	root := t.TempDir()
	m := parseManifest(t, root, "version: 1\napplications:\n  a: {schema: s.yaml, env: ['envs/*.env']}\n")
	_, diagnostics := FromManifest(m)
	if len(diagnostics) != 1 || diagnostics[0].Code != diag.InvalidManifest {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}

func TestFromManifestOverlappingGlobsAreConflicts(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "envs/api-1.env")
	m := parseManifest(t, root, `
version: 1
applications:
  a: {schema: s.yaml, env: ["envs/api-*.env"]}
  b: {schema: s.yaml, env: ["envs/*-1.env"]}
`)
	targets, diagnostics := FromManifest(m)
	if len(targets) != 1 || targets[0].Application != "a" {
		t.Fatalf("targets = %+v", targets)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != diag.InvalidManifest {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}
