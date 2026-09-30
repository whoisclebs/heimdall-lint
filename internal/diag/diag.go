// Package diag defines the structured diagnostics produced by every Heimdall
// check. Rules never format strings for the user; renderers consume these.
package diag

import (
	"cmp"
	"slices"
)

// Redacted replaces the value of every variable declared as secret.
const Redacted = "[REDACTED]"

type Severity int

const (
	SeverityError Severity = iota
	SeverityWarning
)

func (s Severity) String() string {
	if s == SeverityWarning {
		return "warning"
	}
	return "error"
}

// Code is a stable, documented identifier for a class of problems.
type Code string

const (
	MissingRequired      Code = "HML001"
	UnknownVariable      Code = "HML002"
	InvalidType          Code = "HML003"
	InvalidEnum          Code = "HML004"
	BelowMinimum         Code = "HML005"
	AboveMaximum         Code = "HML006"
	DeprecatedVariable   Code = "HML007"
	DuplicateVariable    Code = "HML008"
	InvalidSchema        Code = "HML009"
	SchemaNotFound       Code = "HML010"
	InvalidManifest      Code = "HML011"
	OrphanEnvFile        Code = "HML012"
	InvalidEnvSyntax     Code = "HML013"
	EnvFileUnreadable    Code = "HML014"
	NoEnvFiles           Code = "HML015"
	RequiredByCondition  Code = "HML016"
	MissingDependency    Code = "HML017"
	ConflictingVariables Code = "HML018"
	UnsupportedConstruct Code = "HML019"
)

var codeNames = map[Code]string{
	MissingRequired:      "missing_required_variable",
	UnknownVariable:      "unknown_variable",
	InvalidType:          "invalid_type",
	InvalidEnum:          "invalid_enum",
	BelowMinimum:         "below_minimum",
	AboveMaximum:         "above_maximum",
	DeprecatedVariable:   "deprecated_variable",
	DuplicateVariable:    "duplicate_variable",
	InvalidSchema:        "invalid_schema",
	SchemaNotFound:       "schema_not_found",
	InvalidManifest:      "invalid_manifest",
	OrphanEnvFile:        "orphan_env_file",
	InvalidEnvSyntax:     "invalid_env_syntax",
	EnvFileUnreadable:    "env_file_unreadable",
	NoEnvFiles:           "no_env_files",
	RequiredByCondition:  "required_by_condition",
	MissingDependency:    "missing_dependency",
	ConflictingVariables: "conflicting_variables",
	UnsupportedConstruct: "unsupported_construct",
}

// Name returns the human-readable slug of the code.
func (c Code) Name() string { return codeNames[c] }

// IsContractFailure reports whether the code means the contract itself
// (schema or manifest) is broken, as opposed to an environment file.
func (c Code) IsContractFailure() bool {
	return c == InvalidSchema || c == InvalidManifest
}

// SuggestionKind tells renderers how to introduce Suggestions.
type SuggestionKind int

const (
	// DidYouMean lists fuzzy candidates for a typo.
	DidYouMean SuggestionKind = iota
	// Use lists the definite replacement.
	Use
)

type Diagnostic struct {
	Severity       Severity
	Code           Code
	File           string
	Line           int
	Application    string
	Variable       string
	Message        string
	Expected       string
	Received       string // already quoted, or Redacted for secrets
	Suggestions    []string
	SuggestionKind SuggestionKind
}

// Compare orders diagnostics deterministically: errors first, then by
// variable, code, line and message.
func Compare(a, b Diagnostic) int {
	return cmp.Or(
		cmp.Compare(a.Severity, b.Severity),
		cmp.Compare(a.File, b.File),
		cmp.Compare(a.Variable, b.Variable),
		cmp.Compare(a.Code, b.Code),
		cmp.Compare(a.Line, b.Line),
		cmp.Compare(a.Message, b.Message),
	)
}

func Sort(diagnostics []Diagnostic) {
	slices.SortStableFunc(diagnostics, Compare)
}

func Count(diagnostics []Diagnostic, severity Severity) int {
	total := 0
	for _, d := range diagnostics {
		if d.Severity == severity {
			total++
		}
	}
	return total
}
