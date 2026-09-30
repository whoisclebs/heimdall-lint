// Package compose reads the parts of a Docker Compose file that define a
// service's environment: env_file and environment. It does not run or fully
// interpret Compose (no include, extends or profiles).
package compose

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/whoisclebs/heimdall-lint/internal/diag"
	"github.com/whoisclebs/heimdall-lint/internal/envfile"
	"github.com/whoisclebs/heimdall-lint/internal/fsutil"
)

// Extension is the service-level key Heimdall reads:
//
//	x-heimdall:
//	  schema: schemas/reports-api.schema.yaml
//	  skip: true
const Extension = "x-heimdall"

// composeName matches compose.yaml, compose.prod.yml, docker-compose.yaml, ...
var composeName = regexp.MustCompile(`^(docker-)?compose(\.[A-Za-z0-9_-]+)*\.ya?ml$`)

// IsComposeFile reports whether a path looks like a Compose file by name.
func IsComposeFile(path string) bool { return composeName.MatchString(filepath.Base(path)) }

type EnvFile struct {
	Path     string // resolved against the compose file's directory
	Required bool
}

type Variable struct {
	Key   string
	Value string
}

type Service struct {
	Name string
	// SchemaPath is set by x-heimdall.schema, resolved against the compose file.
	SchemaPath string
	Skip       bool
	// Extends is set when the service uses "extends", which is not interpreted.
	Extends     bool
	EnvFiles    []EnvFile
	Environment []Variable
}

// HasEnvironment reports whether the service declares any configuration.
func (s Service) HasEnvironment() bool { return len(s.EnvFiles) > 0 || len(s.Environment) > 0 }

type Project struct {
	Path     string
	Dir      string
	Services []Service // sorted by name
	// HasInclude is set when the file uses top-level "include", which is not interpreted.
	HasInclude bool
}

// Error lists every problem found in the compose file.
type Error struct {
	Path     string
	Problems []string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: invalid compose file (%d problems)", e.Path, len(e.Problems))
}

// Load parses a compose file. lookup supplies variables for interpolation of
// environment values; a .env file next to the compose file fills the gaps, as
// Compose does, with the process environment taking precedence.
func Load(path string, lookup Lookup) (*Project, error) {
	data, err := fsutil.ReadFile(path, fsutil.MaxManifestBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, &Error{Path: path, Problems: []string{"compose file not found"}}
	}
	if err != nil {
		return nil, &Error{Path: path, Problems: []string{"cannot read compose file: " + err.Error()}}
	}
	return Parse(data, path, lookup)
}

func Parse(data []byte, path string, lookup Lookup) (*Project, error) {
	var document struct {
		Services map[string]yaml.Node `yaml:"services"`
		Include  yaml.Node            `yaml:"include"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, &Error{Path: path, Problems: []string{"invalid YAML: " + strings.TrimPrefix(err.Error(), "yaml: ")}}
	}
	if len(document.Services) == 0 {
		return nil, &Error{Path: path, Problems: []string{"compose file declares no services"}}
	}

	dir := filepath.Dir(path)
	lookup = withDotEnv(dir, lookup)

	project := &Project{Path: path, Dir: dir, HasInclude: document.Include.Kind != 0}
	var problems []string
	for _, name := range slices.Sorted(keys(document.Services)) {
		node := document.Services[name]
		service, serviceProblems := parseService(name, &node, dir, lookup)
		for _, problem := range serviceProblems {
			problems = append(problems, fmt.Sprintf("service %q: %s", name, problem))
		}
		project.Services = append(project.Services, service)
	}
	if len(problems) > 0 {
		return nil, &Error{Path: path, Problems: problems}
	}
	return project, nil
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

// withDotEnv layers the compose directory's .env under the given lookup.
func withDotEnv(dir string, lookup Lookup) Lookup {
	dotEnv := map[string]string{}
	if data, err := fsutil.ReadFile(filepath.Join(dir, ".env"), fsutil.MaxEnvBytes); err == nil {
		for _, entry := range envfile.Parse(data).Entries {
			dotEnv[entry.Key] = entry.Value
		}
	}
	return func(name string) (string, bool) {
		if value, ok := lookup(name); ok {
			return value, true
		}
		value, ok := dotEnv[name]
		return value, ok
	}
}

func parseService(name string, node *yaml.Node, dir string, lookup Lookup) (Service, []string) {
	service := Service{Name: name}
	if resolved := resolve(node); resolved == nil || resolved.Kind != yaml.MappingNode {
		return service, []string{"expected a mapping"}
	}

	var problems []string
	for _, entry := range pairs(node) {
		key, value := entry.key, entry.value
		var found []string
		switch key {
		case "env_file":
			service.EnvFiles, found = parseEnvFiles(value, dir, lookup)
		case "environment":
			service.Environment, found = parseEnvironment(value, lookup)
		case "extends":
			service.Extends = true
		case Extension:
			service.SchemaPath, service.Skip, found = parseExtension(value, dir)
		}
		problems = append(problems, found...)
	}
	return service, problems
}

// resolve follows a YAML alias (*name) to the node it refers to.
func resolve(node *yaml.Node) *yaml.Node {
	for depth := 0; node != nil && node.Kind == yaml.AliasNode && depth < maxMergeDepth; depth++ {
		node = node.Alias
	}
	return node
}

const maxMergeDepth = 32

type pair struct {
	key   string
	value *yaml.Node
}

// pairs returns a mapping's entries with aliases followed and merge keys
// (<<: *anchor, or a list of anchors) expanded. As in YAML, keys written in the
// mapping itself win over merged ones, and earlier merge sources win over later.
func pairs(node *yaml.Node) []pair {
	return collectPairs(node, 0)
}

func collectPairs(node *yaml.Node, depth int) []pair {
	node = resolve(node)
	if node == nil || node.Kind != yaml.MappingNode || depth > maxMergeDepth {
		return nil
	}

	var explicit, merged []pair
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Value != "<<" || key.Tag != "!!merge" {
			explicit = append(explicit, pair{key: key.Value, value: resolve(value)})
			continue
		}
		sources := []*yaml.Node{value}
		if resolved := resolve(value); resolved != nil && resolved.Kind == yaml.SequenceNode {
			sources = resolved.Content
		}
		for _, source := range sources {
			merged = append(merged, collectPairs(source, depth+1)...)
		}
	}

	seen := make(map[string]bool, len(explicit))
	for _, entry := range explicit {
		seen[entry.key] = true
	}
	for _, entry := range merged {
		if !seen[entry.key] {
			seen[entry.key] = true
			explicit = append(explicit, entry)
		}
	}
	return explicit
}

func resolvePath(dir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(dir, path)
}

func parseEnvFiles(node *yaml.Node, dir string, lookup Lookup) ([]EnvFile, []string) {
	pathOf := func(raw string) string { return resolvePath(dir, interpolate(raw, lookup)) }
	node = resolve(node)
	switch node.Kind {
	case yaml.ScalarNode:
		return []EnvFile{{Path: pathOf(node.Value), Required: true}}, nil
	case yaml.SequenceNode:
		var files []EnvFile
		var problems []string
		for _, item := range node.Content {
			item = resolve(item)
			switch item.Kind {
			case yaml.ScalarNode:
				files = append(files, EnvFile{Path: pathOf(item.Value), Required: true})
			case yaml.MappingNode:
				file, ok := parseEnvFileObject(item, pathOf)
				if !ok {
					problems = append(problems, "env_file entry needs a path")
					continue
				}
				files = append(files, file)
			default:
				problems = append(problems, "env_file entries must be paths")
			}
		}
		return files, problems
	}
	return nil, []string{"env_file must be a path or a list"}
}

func parseEnvFileObject(node *yaml.Node, pathOf func(string) string) (EnvFile, bool) {
	file := EnvFile{Required: true}
	for _, entry := range pairs(node) {
		switch entry.key {
		case "path":
			file.Path = pathOf(entry.value.Value)
		case "required":
			file.Required = entry.value.Value != "false"
		}
	}
	return file, file.Path != ""
}

func parseEnvironment(node *yaml.Node, lookup Lookup) ([]Variable, []string) {
	expand := func(key, value string) Variable { return Variable{Key: key, Value: interpolate(value, lookup)} }

	node = resolve(node)
	switch node.Kind {
	case yaml.MappingNode:
		var variables []Variable
		for _, entry := range pairs(node) {
			key, value := entry.key, entry.value
			if value.Kind == yaml.ScalarNode && value.Tag != "!!null" {
				variables = append(variables, expand(key, value.Value))
			} else if hostValue, set := lookup(key); set { // "KEY:" takes the value from the host
				variables = append(variables, Variable{Key: key, Value: hostValue})
			}
		}
		return variables, nil
	case yaml.SequenceNode:
		var variables []Variable
		for _, item := range node.Content {
			item = resolve(item)
			key, value, hasValue := strings.Cut(item.Value, "=")
			if item.Kind != yaml.ScalarNode || key == "" {
				return nil, []string{"environment entries must be KEY=VALUE"}
			}
			if hasValue {
				variables = append(variables, expand(key, value))
			} else if hostValue, set := lookup(key); set {
				variables = append(variables, Variable{Key: key, Value: hostValue})
			}
		}
		return variables, nil
	}
	return nil, []string{"environment must be a mapping or a list"}
}

func parseExtension(node *yaml.Node, dir string) (schemaPath string, skip bool, problems []string) {
	if node = resolve(node); node.Kind != yaml.MappingNode {
		return "", false, []string{Extension + " must be a mapping"}
	}
	for _, entry := range pairs(node) {
		key, value := entry.key, entry.value
		switch key {
		case "schema":
			schemaPath = resolvePath(dir, value.Value)
		case "skip":
			skip = value.Value == "true"
			if value.Value != "true" && value.Value != "false" {
				problems = append(problems, Extension+".skip must be true or false")
			}
		default:
			problems = append(problems, fmt.Sprintf("unknown %s field %q", Extension, key))
		}
	}
	if schemaPath != "" && skip {
		problems = append(problems, Extension+": schema and skip cannot be combined")
	}
	return schemaPath, skip, problems
}

// BuildEnvironment assembles the environment a service will run with: env_file
// entries in order, then environment on top (later definitions win, as in
// Compose). Problems with individual env_file entries (missing, unreadable,
// malformed) are returned as diagnostics attributed to that file. Entries carry
// no line numbers, since they may come from several sources.
func BuildEnvironment(service Service) (envfile.File, []diag.Diagnostic) {
	var merged envfile.File
	var diagnostics []diag.Diagnostic
	problem := func(file string, code diag.Code, line int, message string) {
		diagnostics = append(diagnostics, diag.Diagnostic{
			Severity: diag.SeverityError, Code: code, File: file, Line: line, Application: service.Name, Message: message,
		})
	}

	for _, ref := range service.EnvFiles {
		data, err := fsutil.ReadFile(ref.Path, fsutil.MaxEnvBytes)
		switch {
		case errors.Is(err, os.ErrNotExist) && !ref.Required:
			continue
		case errors.Is(err, os.ErrNotExist):
			problem(ref.Path, diag.EnvFileUnreadable, 0, "Environment file not found.")
			continue
		case err != nil:
			problem(ref.Path, diag.EnvFileUnreadable, 0, "Cannot read environment file: "+err.Error()+".")
			continue
		}

		parsed := envfile.Parse(data)
		for _, issue := range parsed.Issues {
			if issue.Kind == envfile.IssueDuplicate {
				problem(ref.Path, diag.DuplicateVariable, issue.Line, fmt.Sprintf("Variable %s is defined more than once (first defined on line %d). The last definition wins.", issue.Key, issue.FirstLine))
			} else {
				problem(ref.Path, diag.InvalidEnvSyntax, issue.Line, "Cannot parse line: "+issue.Message+".")
			}
		}
		for _, entry := range parsed.Entries {
			merged.Entries = append(merged.Entries, envfile.Entry{Key: entry.Key, Value: entry.Value})
		}
	}
	for _, variable := range service.Environment {
		merged.Entries = append(merged.Entries, envfile.Entry{Key: variable.Key, Value: variable.Value})
	}
	return merged, diagnostics
}
