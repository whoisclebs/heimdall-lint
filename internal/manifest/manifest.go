// Package manifest loads heimdall.yaml, the declarative inventory of
// applications, their schemas and their environment files.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/whoisclebs/heimdall-lint/internal/fsutil"
)

const (
	SupportedVersion = 1
	DefaultFileName  = "heimdall.yaml"
)

type Application struct {
	Name string
	// SchemaPath is resolved against the manifest directory.
	SchemaPath string
	// EnvPatterns are literal paths or globs, resolved against the manifest directory.
	EnvPatterns []string
}

type Manifest struct {
	Path         string
	Dir          string
	Applications []Application // sorted by name
}

// Error lists every problem found, so operators fix them in one pass.
type Error struct {
	Path     string
	Problems []string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: invalid manifest (%d problems)", e.Path, len(e.Problems))
}

type rawManifest struct {
	Version      int                       `yaml:"version"`
	Applications map[string]rawApplication `yaml:"applications"`
}

type rawApplication struct {
	Schema string   `yaml:"schema"`
	Env    []string `yaml:"env"`
}

var applicationName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func Load(path string) (*Manifest, error) {
	data, err := fsutil.ReadFile(path, fsutil.MaxManifestBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, &Error{Path: path, Problems: []string{"manifest not found"}}
	}
	if err != nil {
		return nil, &Error{Path: path, Problems: []string{"cannot read manifest: " + err.Error()}}
	}
	return Parse(data, path)
}

func Parse(data []byte, path string) (*Manifest, error) {
	fail := func(problems ...string) (*Manifest, error) {
		return nil, &Error{Path: path, Problems: problems}
	}

	var raw rawManifest
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		return fail("invalid YAML: " + err.Error())
	}

	var problems []string
	switch {
	case raw.Version == 0:
		problems = append(problems, "missing required field: version")
	case raw.Version != SupportedVersion:
		problems = append(problems, fmt.Sprintf("unsupported manifest version %d (supported: %d)", raw.Version, SupportedVersion))
	}
	if len(raw.Applications) == 0 {
		problems = append(problems, "manifest declares no applications")
	}

	dir := filepath.Dir(path)
	built := &Manifest{Path: path, Dir: dir}
	declaredBy := map[string]string{}
	for _, name := range slices.Sorted(keys(raw.Applications)) {
		app := raw.Applications[name]
		if !applicationName.MatchString(name) {
			problems = append(problems, fmt.Sprintf("application %q: invalid name", name))
		}
		if app.Schema == "" {
			problems = append(problems, fmt.Sprintf("application %q: missing required field: schema", name))
		}
		if len(app.Env) == 0 {
			problems = append(problems, fmt.Sprintf("application %q: missing required field: env", name))
		}

		patterns := make([]string, 0, len(app.Env))
		for _, pattern := range app.Env {
			if _, err := filepath.Match(pattern, ""); err != nil {
				problems = append(problems, fmt.Sprintf("application %q: invalid glob %q", name, pattern))
				continue
			}
			if owner, taken := declaredBy[pattern]; taken {
				problems = append(problems, fmt.Sprintf("env %q is declared by both %q and %q", pattern, owner, name))
			}
			declaredBy[pattern] = name
			patterns = append(patterns, resolve(dir, pattern))
		}
		built.Applications = append(built.Applications, Application{Name: name, SchemaPath: resolve(dir, app.Schema), EnvPatterns: patterns})
	}

	if len(problems) > 0 {
		return fail(problems...)
	}
	return built, nil
}

func resolve(dir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(dir, path)
}

func keys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for key := range m {
			if !yield(key) {
				return
			}
		}
	}
}
