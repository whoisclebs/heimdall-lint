package resolve

import (
	"path/filepath"
	"testing"

	"github.com/whoisclebs/heimdall-lint/internal/discovery"
)

func TestResolvers(t *testing.T) {
	target := discovery.Target{Path: "envs/reports-api.env", Application: "reports-api"}

	if got, _ := Fixed("one.schema.yaml").SchemaFor(target); got != "one.schema.yaml" {
		t.Errorf("Fixed = %q", got)
	}
	if got, _ := (Convention{Dir: "schemas"}).SchemaFor(target); got != filepath.Join("schemas", "reports-api.schema.yaml") {
		t.Errorf("Convention = %q", got)
	}
	byApp := ByApplication{"reports-api": "x.yaml"}
	if got, _ := byApp.SchemaFor(target); got != "x.yaml" {
		t.Errorf("ByApplication = %q", got)
	}
	if _, err := byApp.SchemaFor(discovery.Target{Application: "other"}); err == nil {
		t.Error("undeclared application must fail")
	}
}

func TestConventionRejectsUnsafeNames(t *testing.T) {
	for _, app := range []string{"", ".", "..", "a/b"} {
		if _, err := (Convention{Dir: "s"}).SchemaFor(discovery.Target{Application: app}); err == nil {
			t.Errorf("application %q accepted", app)
		}
	}
}

func TestAll(t *testing.T) {
	targets := []discovery.Target{{Path: "a.env", Application: "a"}, {Path: "b.env", Application: "b"}}
	resolved := All(targets, Convention{Dir: "s"})
	if len(resolved) != 2 || resolved[1].SchemaPath != filepath.Join("s", "b.schema.yaml") || resolved[1].Err != nil {
		t.Fatalf("resolved = %+v", resolved)
	}
}
