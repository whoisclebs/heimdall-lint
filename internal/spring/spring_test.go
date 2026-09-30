package spring

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/whoisclebs/heimdall-lint/internal/schema"
)

const sampleMetadata = `{
  "groups": [{"name": "api"}],
  "properties": [
    {"name": "api.timeout", "type": "java.time.Duration", "description": "Upstream timeout.", "defaultValue": "5s"},
    {"name": "api.retry-count", "type": "java.lang.Integer", "defaultValue": 3},
    {"name": "api.retrycount", "type": "java.lang.Integer"},
    {"name": "api.enabled", "type": "java.lang.Boolean", "defaultValue": false},
    {"name": "queue.provider", "type": "java.lang.String"},
    {"name": "api.base-url", "type": "java.net.URL"},
    {"name": "api.legacy-url", "type": "java.lang.String", "deprecation": {"level": "warning", "replacement": "api.base-url"}},
    {"name": "api.gone", "type": "java.lang.String", "deprecation": {"replacement": "not.imported"}},
    {"name": "api.bad-default", "type": "java.time.Duration", "defaultValue": 5000},
    {"name": "api.ratio", "type": "java.lang.Double", "defaultValue": 0.5},
    {"name": "api.hosts", "type": "java.util.List<java.lang.String>", "defaultValue": ["a", "b"]},
    {"name": "api.slots[0].id", "type": "java.lang.String"},
    {"name": "server.port", "type": "java.lang.Integer", "defaultValue": 8080},
    {"name": "spring.jpa.hibernate.ddl-auto", "type": "java.lang.String", "defaultValue": "none"}
  ],
  "hints": [
    {"name": "queue.provider", "values": [{"value": "KAFKA"}, {"value": "RABBITMQ"}]},
    {"name": "spring.jpa.hibernate.ddl-auto", "values": [{"value": "none"}, {"value": "validate"}]}
  ]
}`

func generate(t *testing.T, prefixes ...string) (*schema.Schema, string, []string) {
	t.Helper()
	metadata, err := Parse([]byte(sampleMetadata))
	if err != nil {
		t.Fatal(err)
	}
	document, warnings, err := Generate(metadata, Options{Prefixes: prefixes})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := schema.Parse(document, "generated")
	if err != nil {
		t.Fatalf("generated schema rejected: %v\n%s", err, document)
	}
	return parsed, string(document), warnings
}

func TestEnvironmentName(t *testing.T) {
	tests := map[string]string{
		"spring.datasource.url": "SPRING_DATASOURCE_URL",
		"api.retry-count":       "API_RETRYCOUNT", // Spring removes dashes
		"server.port":           "SERVER_PORT",
	}
	for property, want := range tests {
		if got, ok := EnvironmentName(property); !ok || got != want {
			t.Errorf("EnvironmentName(%q) = %q, %v", property, got, ok)
		}
	}
	for _, property := range []string{"a.slots[0].id", "a.*.b", ""} {
		if _, ok := EnvironmentName(property); ok {
			t.Errorf("%q must have no environment name", property)
		}
	}
}

func TestGenerateMapsTypesDefaultsAndRules(t *testing.T) {
	parsed, document, _ := generate(t)
	check := func(name string, wantType schema.Type) *schema.Variable {
		t.Helper()
		variable := parsed.Variables[name]
		if variable == nil || variable.Type != wantType {
			t.Fatalf("%s = %+v, want type %s\n%s", name, variable, wantType, document)
		}
		return variable
	}

	if v := check("API_TIMEOUT", schema.Duration); *v.Default != "5s" || v.Description != "Upstream timeout." {
		t.Errorf("timeout = %+v", v)
	}
	if v := check("API_RETRYCOUNT", schema.Integer); *v.Default != "3" {
		t.Errorf("retry = %+v", v)
	}
	if v := check("API_ENABLED", schema.Boolean); v.Default == nil || *v.Default != "false" {
		t.Errorf("a false default must survive: %+v", v)
	}
	check("API_BASEURL", schema.URL)
	check("API_RATIO", schema.Float)
	check("API_HOSTS", schema.String)
	if v := check("SERVER_PORT", schema.Integer); v.Min.Raw != "0" || v.Max.Raw != "65535" || *v.Default != "8080" {
		t.Errorf("port = %+v", v)
	}
	if v := check("QUEUE_PROVIDER", schema.Enum); len(v.Values) != 2 || v.Values[0] != "KAFKA" {
		t.Errorf("enum from hints = %+v", v)
	}
	if v := check("SPRING_JPA_HIBERNATE_DDLAUTO", schema.Enum); *v.Default != "none" {
		t.Errorf("ddl-auto = %+v", v)
	}
}

func TestGenerateDeprecationAndRepairs(t *testing.T) {
	parsed, _, warnings := generate(t)
	legacy := parsed.Variables["API_LEGACYURL"]
	if !legacy.Deprecated || legacy.Replacement != "API_BASEURL" {
		t.Errorf("legacy = %+v", legacy)
	}
	if gone := parsed.Variables["API_GONE"]; !gone.Deprecated || gone.Replacement != "" {
		t.Errorf("a replacement that was not imported must be dropped: %+v", gone)
	}
	if bad := parsed.Variables["API_BADDEFAULT"]; bad.Default != nil {
		t.Errorf("invalid default kept: %+v", bad)
	}

	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"API_BADDEFAULT: dropped default", "skipped api.slots[0].id", "skipped api.retrycount: maps to API_RETRYCOUNT, already used by api.retry-count"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings lack %q:\n%s", want, joined)
		}
	}
}

func TestGenerateHonorsPrefixesAndIsDeterministic(t *testing.T) {
	parsed, _, _ := generate(t, "server.")
	if len(parsed.Variables) != 1 || parsed.Variables["SERVER_PORT"] == nil {
		t.Fatalf("variables = %v", parsed.Names())
	}
	_, first, _ := generate(t)
	for range 10 {
		if _, again, _ := generate(t); again != first {
			t.Fatal("generation is not deterministic")
		}
	}
	if !strings.HasPrefix(first, "# Generated by") {
		t.Errorf("missing header:\n%s", first)
	}
}

func TestGenerateWithNothingSelectedFails(t *testing.T) {
	metadata, _ := Parse([]byte(sampleMetadata))
	if _, _, err := Generate(metadata, Options{Prefixes: []string{"nope."}}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestParseRejectsBadMetadata(t *testing.T) {
	for _, input := range []string{"", "{", `{"properties": []}`, `[]`} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("Parse(%q) accepted", input)
		}
	}
}

func writeJar(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.jar")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, content := range entries {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadSources(t *testing.T) {
	plain := writeJar(t, map[string]string{"META-INF/spring-configuration-metadata.json": sampleMetadata})
	boot := writeJar(t, map[string]string{"BOOT-INF/classes/META-INF/spring-configuration-metadata.json": sampleMetadata})
	for name, path := range map[string]string{"jar": plain, "boot jar": boot} {
		if metadata, err := Load(path); err != nil || len(metadata.Properties) != 14 {
			t.Errorf("%s: %v", name, err)
		}
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "META-INF"), 0o755); err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join(dir, "META-INF", "spring-configuration-metadata.json")
	if err := os.WriteFile(jsonPath, []byte(sampleMetadata), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"directory": dir, "json file": jsonPath} {
		if metadata, err := Load(path); err != nil || len(metadata.Properties) != 14 {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestLoadFailures(t *testing.T) {
	empty := writeJar(t, map[string]string{"other.txt": "x"})
	if _, err := Load(empty); err == nil || !strings.Contains(err.Error(), "spring-boot-configuration-processor") {
		t.Errorf("missing entry: %v", err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.jar")); err == nil {
		t.Error("missing file accepted")
	}
	notAZip := filepath.Join(t.TempDir(), "fake.jar")
	if err := os.WriteFile(notAZip, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(notAZip); err == nil {
		t.Error("corrupt jar accepted")
	}
	if _, err := Load(t.TempDir()); err == nil {
		t.Error("directory without metadata accepted")
	}
}

// A tiny archive can declare a huge entry; loading must refuse it rather than
// inflate it into memory.
func TestLoadRefusesOversizedEntry(t *testing.T) {
	huge := strings.Repeat("A", maxMetadataBytes+1024)
	path := writeJar(t, map[string]string{"META-INF/spring-configuration-metadata.json": `{"properties":[{"name":"a","description":"` + huge + `"}]}`})
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v", err)
	}
}
