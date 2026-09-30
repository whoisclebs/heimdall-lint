package compose

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/whoisclebs/heimdall-lint/internal/diag"
)

func lookupFrom(values map[string]string) Lookup {
	return func(name string) (string, bool) { value, ok := values[name]; return value, ok }
}

func TestInterpolate(t *testing.T) {
	lookup := lookupFrom(map[string]string{"A": "1", "EMPTY": "", "PORT_1": "8080"})
	tests := map[string]string{
		"plain":                "plain",
		"${A}":                 "1",
		"$A":                   "1",
		"x-${A}-y":             "x-1-y",
		"$$A":                  "$A",
		"${MISSING}":           "",
		"${MISSING:-fallback}": "fallback",
		"${EMPTY:-fallback}":   "fallback",
		"${EMPTY-fallback}":    "",
		"${MISSING-fallback}":  "fallback",
		"${A:-fallback}":       "1",
		"$PORT_1":              "8080",
		"price: 5$":            "price: 5$",
		"cost $ 5":             "cost $ 5",
		"${A:?required}":       "${A:?required}",
		"${A:+alt}":            "${A:+alt}",
		"${unterminated":       "${unterminated",
		"${A}${A}":             "11",
	}
	for input, want := range tests {
		if got := interpolate(input, lookup); got != want {
			t.Errorf("interpolate(%q) = %q, want %q", input, got, want)
		}
	}
}

const sampleCompose = `
services:
  db:
    image: postgres
  reports-api:
    image: x
    env_file:
      - envs/common.env
      - path: envs/optional.env
        required: false
      - ${ENVS:-envs}/reports.env
    environment:
      API_TIMEOUT: 10s
      FEATURE_BETA: true
      FROM_HOST:
      EMPTY:
      FROM_INTERPOLATION: "${HOSTNAME_X:-fallback}-1"
    x-heimdall:
      schema: schemas/reports.schema.yaml
  inventory-api:
    env_file: envs/inventory.env
    environment:
      - DATABASE_PORT=5432
      - FROM_HOST
      - LITERAL=a=b
  etl:
    environment: {A: b}
    x-heimdall: {skip: true}
`

func TestParse(t *testing.T) {
	project, err := Parse([]byte(sampleCompose), "/srv/app/compose.yaml", lookupFrom(map[string]string{"FROM_HOST": "from-host"}))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, s := range project.Services {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"db", "etl", "inventory-api", "reports-api"}) {
		t.Fatalf("services = %v", names)
	}

	reports := project.Services[3]
	if reports.SchemaPath != "/srv/app/schemas/reports.schema.yaml" || reports.Skip {
		t.Fatalf("reports = %+v", reports)
	}
	wantFiles := []EnvFile{
		{Path: "/srv/app/envs/common.env", Required: true},
		{Path: "/srv/app/envs/optional.env", Required: false},
		{Path: "/srv/app/envs/reports.env", Required: true},
	}
	if !slices.Equal(reports.EnvFiles, wantFiles) {
		t.Fatalf("env files = %+v", reports.EnvFiles)
	}
	got := map[string]string{}
	for _, v := range reports.Environment {
		got[v.Key] = v.Value
	}
	for key, want := range map[string]string{"API_TIMEOUT": "10s", "FEATURE_BETA": "true", "FROM_HOST": "from-host", "FROM_INTERPOLATION": "fallback-1"} {
		if got[key] != want {
			t.Errorf("%s = %q, want %q", key, got[key], want)
		}
	}
	if _, present := got["EMPTY"]; present {
		t.Error("KEY: (null) without a host value must stay unset")
	}

	inventory := project.Services[2]
	if len(inventory.EnvFiles) != 1 || inventory.EnvFiles[0].Path != "/srv/app/envs/inventory.env" {
		t.Fatalf("inventory files = %+v", inventory.EnvFiles)
	}
	values := map[string]string{}
	for _, v := range inventory.Environment {
		values[v.Key] = v.Value
	}
	if values["DATABASE_PORT"] != "5432" || values["FROM_HOST"] != "from-host" || values["LITERAL"] != "a=b" {
		t.Fatalf("inventory env = %v", values)
	}

	if !project.Services[1].Skip || project.Services[0].HasEnvironment() {
		t.Fatalf("skip/has-environment wrong: %+v %+v", project.Services[1], project.Services[0])
	}
}

func TestParseUsesDotEnvForInterpolation(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TAG=from-dotenv\nSHARED=dotenv\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := "services:\n  a:\n    environment:\n      TAG: ${TAG}\n      SHARED: ${SHARED}\n"
	project, err := Parse([]byte(data), filepath.Join(dir, "compose.yaml"), lookupFrom(map[string]string{"SHARED": "process"}))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range project.Services[0].Environment {
		got[v.Key] = v.Value
	}
	if got["TAG"] != "from-dotenv" || got["SHARED"] != "process" {
		t.Fatalf("got %v (the process environment must win over .env)", got)
	}
}

func TestParseProblems(t *testing.T) {
	tests := map[string]struct{ yaml, want string }{
		"no services":         {"version: '3'\n", "declares no services"},
		"broken yaml":         {"services: [\n", "invalid YAML"},
		"service not mapping": {"services:\n  a: 5\n", "expected a mapping"},
		"env_file scalar bad": {"services:\n  a:\n    env_file: {x: y}\n", "env_file must be a path or a list"},
		"env_file entry":      {"services:\n  a:\n    env_file: [{required: true}]\n", "needs a path"},
		"environment bad":     {"services:\n  a:\n    environment: 5\n", "mapping or a list"},
		"environment item":    {"services:\n  a:\n    environment: ['=x']\n", "KEY=VALUE"},
		"extension unknown":   {"services:\n  a:\n    x-heimdall: {schma: s}\n", `unknown x-heimdall field "schma"`},
		"extension both":      {"services:\n  a:\n    x-heimdall: {schema: s, skip: true}\n", "cannot be combined"},
		"extension bad skip":  {"services:\n  a:\n    x-heimdall: {skip: maybe}\n", "must be true or false"},
		"extension scalar":    {"services:\n  a:\n    x-heimdall: yes\n", "must be a mapping"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml), "compose.yaml", lookupFrom(nil))
			composeErr, ok := err.(*Error)
			if !ok || !strings.Contains(strings.Join(composeErr.Problems, "\n"), tt.want) {
				t.Fatalf("err = %v, want problem containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadMissing(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "compose.yaml"), lookupFrom(nil)); err == nil || !strings.Contains(err.Error(), "invalid compose file") {
		t.Fatalf("err = %v", err)
	}
}

func TestIsComposeFile(t *testing.T) {
	for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.prod.yaml", "/x/y/compose.override.yml"} {
		if !IsComposeFile(name) {
			t.Errorf("%s not recognised", name)
		}
	}
	for _, name := range []string{"reports.env", "heimdall.yaml", "env.schema.yaml", "composer.yaml", "compose.env"} {
		if IsComposeFile(name) {
			t.Errorf("%s wrongly recognised", name)
		}
	}
}

func TestBuildEnvironmentMergesInOrder(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	first := write("first.env", "A=1\nB=from-first\n")
	second := write("second.env", "B=from-second\nC=3\n")
	service := Service{
		Name:        "svc",
		EnvFiles:    []EnvFile{{Path: first, Required: true}, {Path: second, Required: true}, {Path: filepath.Join(dir, "optional.env"), Required: false}},
		Environment: []Variable{{Key: "C", Value: "from-environment"}},
	}

	environment, diagnostics := BuildEnvironment(service)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	effective := environment.Lookup()
	for key, want := range map[string]string{"A": "1", "B": "from-second", "C": "from-environment"} {
		if effective[key].Value != want {
			t.Errorf("%s = %q, want %q", key, effective[key].Value, want)
		}
	}
	if len(environment.Issues) != 0 {
		t.Fatalf("overriding across sources is not a duplicate: %+v", environment.Issues)
	}
}

func TestBuildEnvironmentReportsFileProblems(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken.env")
	if err := os.WriteFile(broken, []byte("A=1\nA=2\nNOT A LINE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := Service{Name: "svc", EnvFiles: []EnvFile{{Path: broken, Required: true}, {Path: filepath.Join(dir, "missing.env"), Required: true}}}

	_, diagnostics := BuildEnvironment(service)
	codes := map[diag.Code]bool{}
	for _, d := range diagnostics {
		codes[d.Code] = true
		if d.Severity != diag.SeverityError || d.Application != "svc" {
			t.Errorf("diagnostic = %+v", d)
		}
	}
	for _, want := range []diag.Code{diag.DuplicateVariable, diag.InvalidEnvSyntax, diag.EnvFileUnreadable} {
		if !codes[want] {
			t.Errorf("missing %s in %+v", want, diagnostics)
		}
	}
}

const anchoredCompose = `
x-common: &common
  A: "from-common"
  B: "from-common"
x-more: &more
  B: "from-more"
  C: "from-more"
x-defaults: &defaults
  env_file: shared.env
  environment: *common
  x-heimdall: {schema: s.yaml}

services:
  merged:
    environment:
      <<: *common
      B: explicit
  listed:
    environment:
      <<: [*common, *more]
  inherits:
    <<: *defaults
  whole-alias: *defaults
  alias-list:
    environment: *common
    env_file: [{<<: {path: from-merge.env}, required: false}]
`

func TestParseResolvesAnchorsAndMergeKeys(t *testing.T) {
	project, err := Parse([]byte(anchoredCompose), "/srv/compose.yaml", lookupFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Service{}
	for _, service := range project.Services {
		byName[service.Name] = service
	}
	values := func(name string) map[string]string {
		got := map[string]string{}
		for _, v := range byName[name].Environment {
			got[v.Key] = v.Value
		}
		return got
	}

	if got := values("merged"); got["A"] != "from-common" || got["B"] != "explicit" {
		t.Errorf("explicit keys must win over merged ones: %v", got)
	}
	if got := values("listed"); got["A"] != "from-common" || got["B"] != "from-common" || got["C"] != "from-more" {
		t.Errorf("earlier merge sources must win over later ones: %v", got)
	}
	for _, name := range []string{"inherits", "whole-alias"} {
		service := byName[name]
		if values(name)["A"] != "from-common" || len(service.EnvFiles) != 1 || service.EnvFiles[0].Path != "/srv/shared.env" || service.SchemaPath != "/srv/s.yaml" {
			t.Errorf("%s did not inherit env_file/environment/x-heimdall: %+v", name, service)
		}
		if !service.HasEnvironment() {
			t.Errorf("%s must not look like a service without configuration", name)
		}
	}
	if got := byName["alias-list"].EnvFiles; len(got) != 1 || got[0].Path != "/srv/from-merge.env" || got[0].Required {
		t.Errorf("merge inside an env_file object: %+v", got)
	}
}

func TestParseFlagsUnsupportedConstructs(t *testing.T) {
	project, err := Parse([]byte("include: [other.yaml]\nservices:\n  a:\n    extends: {service: base}\n    environment: {A: b}\n  b:\n    image: x\n"), "compose.yaml", lookupFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !project.HasInclude || !project.Services[0].Extends || project.Services[1].Extends {
		t.Fatalf("project = %+v", project)
	}
}
