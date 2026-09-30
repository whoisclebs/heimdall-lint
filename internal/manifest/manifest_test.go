package manifest

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseResolvesPathsAgainstManifestDir(t *testing.T) {
	m, err := Parse([]byte(`
version: 1
applications:
  scheduler:
    schema: schemas/scheduler.schema.yaml
    env: [envs/scheduler.env]
  reports-api:
    schema: schemas/reports-api.schema.yaml
    env:
      - envs/reports-api-*.env
      - /abs/reports.env
`), "/opt/company/heimdall.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if m.Applications[0].Name != "reports-api" || m.Applications[1].Name != "scheduler" {
		t.Fatalf("applications must be sorted: %+v", m.Applications)
	}
	reports := m.Applications[0]
	if reports.SchemaPath != filepath.Join("/opt/company", "schemas/reports-api.schema.yaml") {
		t.Fatalf("schema = %q", reports.SchemaPath)
	}
	if reports.EnvPatterns[0] != filepath.Join("/opt/company", "envs/reports-api-*.env") || reports.EnvPatterns[1] != "/abs/reports.env" {
		t.Fatalf("patterns = %v", reports.EnvPatterns)
	}
}

func TestParseInvalid(t *testing.T) {
	tests := map[string]struct{ yaml, want string }{
		"no version":      {"applications:\n  a: {schema: s, env: [e]}\n", "missing required field: version"},
		"bad version":     {"version: 9\napplications:\n  a: {schema: s, env: [e]}\n", "unsupported manifest version 9"},
		"no applications": {"version: 1\n", "declares no applications"},
		"missing schema":  {"version: 1\napplications:\n  a: {env: [e]}\n", "missing required field: schema"},
		"missing env":     {"version: 1\napplications:\n  a: {schema: s}\n", "missing required field: env"},
		"bad glob":        {"version: 1\napplications:\n  a: {schema: s, env: ['[']}\n", "invalid glob"},
		"duplicate app":   {"version: 1\napplications:\n  a: {schema: s, env: [e]}\n  a: {schema: s, env: [e]}\n", "already defined"},
		"duplicate env":   {"version: 1\napplications:\n  a: {schema: s, env: [e.env]}\n  b: {schema: s, env: [e.env]}\n", "declared by both"},
		"unknown field":   {"version: 1\napplications:\n  a: {schema: s, env: [e], schma: x}\n", "schma"},
		"bad name":        {"version: 1\napplications:\n  '../x': {schema: s, env: [e]}\n", "invalid name"},
		"broken yaml":     {"version: 1\napplications: [\n", "invalid YAML"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml), "heimdall.yaml")
			var manifestErr *Error
			if !errors.As(err, &manifestErr) || !strings.Contains(strings.Join(manifestErr.Problems, "\n"), tt.want) {
				t.Fatalf("err = %v, want problem containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadMissing(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "heimdall.yaml"))
	var manifestErr *Error
	if !errors.As(err, &manifestErr) || manifestErr.Problems[0] != "manifest not found" {
		t.Fatalf("err = %v", err)
	}
}
