// Package resolve decides which schema governs each environment file. It is
// the only place the naming convention between the two lives.
package resolve

import (
	"fmt"
	"path/filepath"

	"github.com/whoisclebs/heimdall-lint/internal/discovery"
)

const SchemaSuffix = ".schema.yaml"

type Resolver interface {
	SchemaFor(target discovery.Target) (string, error)
}

// Fixed applies one schema to every target (--schema).
type Fixed string

func (f Fixed) SchemaFor(discovery.Target) (string, error) { return string(f), nil }

// Convention maps <name>.env to <dir>/<name>.schema.yaml (--schema-dir).
type Convention struct{ Dir string }

func (c Convention) SchemaFor(target discovery.Target) (string, error) {
	name := target.Application
	if name == "" || name == "." || name == ".." || name != filepath.Base(name) {
		return "", fmt.Errorf("cannot derive a schema name from %q", target.Path)
	}
	return filepath.Join(c.Dir, name+SchemaSuffix), nil
}

// ByApplication uses the schemas declared in a manifest.
type ByApplication map[string]string

func (b ByApplication) SchemaFor(target discovery.Target) (string, error) {
	schemaPath, declared := b[target.Application]
	if !declared {
		return "", fmt.Errorf("application %q is not declared", target.Application)
	}
	return schemaPath, nil
}

// Overrides uses a specific schema for the applications that declare one and
// falls back to Default for the rest (a nil Default means "no schema known").
type Overrides struct {
	ByApplication map[string]string
	Default       Resolver
}

func (o Overrides) SchemaFor(target discovery.Target) (string, error) {
	if schemaPath, declared := o.ByApplication[target.Application]; declared {
		return schemaPath, nil
	}
	if o.Default == nil {
		return "", fmt.Errorf("no schema declared for %q (use x-heimdall.schema, --schema-dir or --schema)", target.Application)
	}
	return o.Default.SchemaFor(target)
}

// Resolved is a target paired with its schema.
type Resolved struct {
	discovery.Target
	SchemaPath string
	// Err is set when no schema could be determined.
	Err error
}

func All(targets []discovery.Target, resolver Resolver) []Resolved {
	resolved := make([]Resolved, len(targets))
	for i, target := range targets {
		schemaPath, err := resolver.SchemaFor(target)
		resolved[i] = Resolved{Target: target, SchemaPath: schemaPath, Err: err}
	}
	return resolved
}
