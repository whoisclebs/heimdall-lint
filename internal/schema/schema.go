// Package schema loads and validates env.schema.yaml contracts.
package schema

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/whoisclebs/heimdall-lint/internal/fsutil"
)

const SupportedVersion = 1

type Type string

const (
	String   Type = "string"
	Integer  Type = "integer"
	Float    Type = "float"
	Boolean  Type = "boolean"
	Enum     Type = "enum"
	URL      Type = "url"
	Duration Type = "duration"
	Hostname Type = "hostname"
	Port     Type = "port"
	IP       Type = "ip"
	CIDR     Type = "cidr"
)

var allTypes = []Type{String, Integer, Float, Boolean, Enum, URL, Duration, Hostname, Port, IP, CIDR}

// Bound is a parsed min/max limit. Only the field matching the variable type
// is meaningful.
type Bound struct {
	Raw      string
	Int      int64
	Float    float64
	Duration time.Duration
}

type Variable struct {
	Name        string
	Type        Type
	Description string
	Required    bool
	Secret      bool
	Default     *string
	Min, Max    *Bound
	Values      []string
	Schemes     []string
	Deprecated  bool
	Replacement string
	// RequiredIf makes the variable required when every condition holds.
	RequiredIf []Condition
	// Requires lists variables that must be set whenever this one is set.
	Requires []string
	// ConflictsWith lists variables that must not be set together with this one.
	ConflictsWith []string
}

// Condition is "Name has effective value Value".
type Condition struct {
	Name  string
	Value string
}

type Schema struct {
	Path      string
	Version   int
	Variables map[string]*Variable
}

// Names returns the declared variable names in sorted order.
func (s *Schema) Names() []string {
	return slices.Sorted(func(yield func(string) bool) {
		for name := range s.Variables {
			if !yield(name) {
				return
			}
		}
	})
}

type Problem struct {
	Variable string
	Message  string
}

// Error is returned for a schema that exists but cannot be trusted.
type Error struct {
	Path     string
	Problems []Problem
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: invalid schema (%d problems)", e.Path, len(e.Problems))
}

// NotFoundError is returned when the schema file does not exist.
type NotFoundError struct{ Path string }

func (e *NotFoundError) Error() string { return "schema not found: " + e.Path }

// scalar reads any YAML scalar (5s, 5000, false) as its literal text.
type scalar string

func (s *scalar) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: expected a single value", node.Line)
	}
	*s = scalar(node.Value)
	return nil
}

type rawSchema struct {
	Version   int                    `yaml:"version"`
	Variables map[string]rawVariable `yaml:"variables"`
}

type rawVariable struct {
	Type        string   `yaml:"type"`
	Description string   `yaml:"description"`
	Required    bool     `yaml:"required"`
	Secret      bool     `yaml:"secret"`
	Default     *scalar  `yaml:"default"`
	Min         *scalar  `yaml:"min"`
	Max         *scalar  `yaml:"max"`
	Values      []scalar `yaml:"values"`
	Schemes     []string `yaml:"schemes"`
	Deprecated  bool     `yaml:"deprecated"`
	Replacement string   `yaml:"replacement"`

	RequiredIf    map[string]scalar `yaml:"required_if"`
	Requires      []string          `yaml:"requires"`
	ConflictsWith []string          `yaml:"conflicts_with"`
}

var variableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Load reads and parses a schema file.
func Load(path string) (*Schema, error) {
	data, err := fsutil.ReadFile(path, fsutil.MaxSchemaBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, &NotFoundError{Path: path}
	}
	if err != nil {
		return nil, &Error{Path: path, Problems: []Problem{{Message: "cannot read schema: " + err.Error()}}}
	}
	return Parse(data, path)
}

// Parse decodes a schema strictly: unknown fields are errors so that a typo
// such as "requird" never silently weakens the contract.
func Parse(data []byte, path string) (*Schema, error) {
	fail := func(problems ...Problem) (*Schema, error) {
		return nil, &Error{Path: path, Problems: problems}
	}

	var header struct {
		Version int `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil {
		return fail(Problem{Message: yamlMessage(err)})
	}
	switch {
	case header.Version == 0:
		return fail(Problem{Message: "missing required field: version"})
	case header.Version != SupportedVersion:
		return fail(Problem{Message: fmt.Sprintf("unsupported schema version %d (supported: %d)", header.Version, SupportedVersion)})
	}

	var raw rawSchema
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		return fail(Problem{Message: yamlMessage(err)})
	}
	if len(raw.Variables) == 0 {
		return fail(Problem{Message: "schema declares no variables"})
	}

	built := &Schema{Path: path, Version: raw.Version, Variables: make(map[string]*Variable, len(raw.Variables))}
	var problems []Problem
	for _, name := range slices.Sorted(mapKeys(raw.Variables)) {
		variable, variableProblems := buildVariable(name, raw.Variables[name])
		for _, message := range variableProblems {
			problems = append(problems, Problem{Variable: name, Message: message})
		}
		built.Variables[name] = variable
	}
	problems = append(problems, crossChecks(built)...)
	if len(problems) > 0 {
		return fail(problems...)
	}
	return built, nil
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for key := range m {
			if !yield(key) {
				return
			}
		}
	}
}

// yamlMessage keeps yaml.v3 diagnostics (which carry line numbers) but drops
// the internal Go type names it leaks.
func yamlMessage(err error) string {
	message := strings.ReplaceAll(err.Error(), "yaml: unmarshal errors:\n  ", "")
	message = strings.ReplaceAll(message, "\n  ", "; ")
	for _, typeName := range []string{"schema.rawVariable", "schema.rawSchema"} {
		message = strings.ReplaceAll(message, "in type "+typeName, "in this section")
	}
	return "invalid YAML: " + strings.TrimPrefix(message, "yaml: ")
}

func buildVariable(name string, raw rawVariable) (*Variable, []string) {
	var problems []string
	variable := &Variable{
		Name: name, Type: Type(raw.Type), Description: strings.TrimSpace(raw.Description),
		Required: raw.Required, Secret: raw.Secret, Deprecated: raw.Deprecated,
		Replacement: raw.Replacement, Schemes: raw.Schemes,
		Requires: raw.Requires, ConflictsWith: raw.ConflictsWith,
	}
	for _, name := range slices.Sorted(mapKeys(raw.RequiredIf)) {
		variable.RequiredIf = append(variable.RequiredIf, Condition{Name: name, Value: string(raw.RequiredIf[name])})
	}

	if !variableName.MatchString(name) {
		problems = append(problems, "invalid variable name (use letters, digits and underscores)")
	}
	if !slices.Contains(allTypes, variable.Type) {
		if raw.Type == "" {
			return variable, append(problems, "missing required field: type")
		}
		return variable, append(problems, fmt.Sprintf("unknown type %q", raw.Type))
	}

	problems = append(problems, buildValues(variable, raw)...)
	problems = append(problems, buildSchemes(variable)...)
	problems = append(problems, buildBounds(variable, raw)...)

	if variable.Required && raw.Default != nil {
		problems = append(problems, "cannot be both required and have a default")
	}
	if variable.Required && variable.Deprecated {
		problems = append(problems, "cannot be both required and deprecated")
	}
	if variable.Replacement != "" && !variable.Deprecated {
		problems = append(problems, "replacement requires deprecated: true")
	}
	if variable.Required && len(variable.RequiredIf) > 0 {
		problems = append(problems, "required and required_if are contradictory: use one")
	}

	if raw.Default != nil && len(problems) == 0 {
		value := string(*raw.Default)
		variable.Default = &value
		if problem := variable.Check(value); problem != nil {
			problems = append(problems, "default value is invalid: expected "+problem.Expected)
		}
	}
	return variable, problems
}

func buildValues(variable *Variable, raw rawVariable) []string {
	switch {
	case variable.Type == Enum && len(raw.Values) == 0:
		return []string{"enum requires a non-empty values list"}
	case variable.Type != Enum && len(raw.Values) > 0:
		return []string{"values is only valid for type enum"}
	}
	seen := map[string]bool{}
	var problems []string
	for _, value := range raw.Values {
		text := string(value)
		if text == "" {
			problems = append(problems, "enum values cannot be empty")
		} else if seen[text] {
			problems = append(problems, fmt.Sprintf("duplicate enum value %q", text))
		}
		seen[text] = true
		variable.Values = append(variable.Values, text)
	}
	return problems
}

func buildSchemes(variable *Variable) []string {
	if len(variable.Schemes) > 0 && variable.Type != URL {
		return []string{"schemes is only valid for type url"}
	}
	for i, scheme := range variable.Schemes {
		variable.Schemes[i] = strings.ToLower(scheme)
	}
	return nil
}

func buildBounds(variable *Variable, raw rawVariable) []string {
	if raw.Min == nil && raw.Max == nil {
		return nil
	}
	if !supportsBounds(variable.Type) {
		return []string{fmt.Sprintf("min/max are not supported for type %s", variable.Type)}
	}
	var problems []string
	parse := func(label string, source *scalar) *Bound {
		if source == nil {
			return nil
		}
		bound, err := parseBound(variable.Type, string(*source))
		if err != "" {
			problems = append(problems, fmt.Sprintf("%s is invalid: %s", label, err))
			return nil
		}
		return bound
	}
	variable.Min, variable.Max = parse("min", raw.Min), parse("max", raw.Max)
	if variable.Min != nil && variable.Max != nil && compareBounds(variable.Type, variable.Min, variable.Max) > 0 {
		problems = append(problems, "min is greater than max")
	}
	if variable.Type == Port {
		for _, bound := range []*Bound{variable.Min, variable.Max} {
			if bound != nil && (bound.Int < 1 || bound.Int > 65535) {
				problems = append(problems, "port limits must be between 1 and 65535")
			}
		}
	}
	return problems
}

func supportsBounds(t Type) bool {
	return t == Integer || t == Float || t == Duration || t == Port
}

// crossChecks validates rules that involve more than one variable.
func crossChecks(s *Schema) []Problem {
	var problems []Problem
	report := func(name, format string, args ...any) {
		problems = append(problems, Problem{Variable: name, Message: fmt.Sprintf(format, args...)})
	}

	for _, name := range s.Names() {
		variable := s.Variables[name]
		if variable.Replacement != "" {
			if _, declared := s.Variables[variable.Replacement]; !declared {
				report(name, "replacement %q is not declared in this schema", variable.Replacement)
			}
		}
		for _, condition := range variable.RequiredIf {
			other, declared := s.Variables[condition.Name]
			switch {
			case condition.Name == name:
				report(name, "required_if cannot refer to the variable itself")
			case !declared:
				report(name, "required_if refers to undeclared variable %q", condition.Name)
			case other.Type != "" && other.Check(condition.Value) != nil:
				report(name, "required_if value for %s is invalid: expected %s", condition.Name, other.Expectation())
			}
		}
		for _, list := range []struct {
			rule  string
			names []string
		}{{"requires", variable.Requires}, {"conflicts_with", variable.ConflictsWith}} {
			seen := map[string]bool{}
			for _, target := range list.names {
				_, declared := s.Variables[target]
				switch {
				case target == name:
					report(name, "%s cannot refer to the variable itself", list.rule)
				case !declared:
					report(name, "%s refers to undeclared variable %q", list.rule, target)
				case seen[target]:
					report(name, "%s lists %q more than once", list.rule, target)
				}
				seen[target] = true
			}
		}
	}
	return problems
}
