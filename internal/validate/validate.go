// Package validate checks a parsed environment against a schema. It is pure:
// no filesystem, no CLI, no concurrency.
package validate

import (
	"fmt"
	"strconv"

	"github.com/whoisclebs/heimdall-lint/internal/diag"
	"github.com/whoisclebs/heimdall-lint/internal/envfile"
	"github.com/whoisclebs/heimdall-lint/internal/naming"
	"github.com/whoisclebs/heimdall-lint/internal/schema"
	"github.com/whoisclebs/heimdall-lint/internal/suggest"
)

type Options struct {
	AllowUnknown bool
}

// Validator is immutable after New and safe for concurrent use.
type Validator struct {
	schema    *schema.Schema
	suggester *suggest.Suggester
	options   Options
}

func New(contract *schema.Schema, strategy naming.Strategy, options Options) *Validator {
	return &Validator{
		schema:    contract,
		suggester: suggest.New(contract.Names(), strategy),
		options:   options,
	}
}

type Result struct {
	Diagnostics      []diag.Diagnostic
	VariablesChecked int
}

// Validate checks env, reported under file and application, and returns the
// diagnostics sorted deterministically.
func (v *Validator) Validate(file, application string, env envfile.File) Result {
	run := &session{Validator: v, file: file, application: application, missing: map[string]bool{}}

	run.reportEnvIssues(env)
	present := env.Lookup()
	for _, name := range v.schema.Names() {
		run.checkDeclared(v.schema.Variables[name], present)
	}
	run.checkRules(present)
	if !v.options.AllowUnknown {
		run.reportUnknown(env)
	}

	diag.Sort(run.diagnostics)
	return Result{Diagnostics: run.diagnostics, VariablesChecked: len(present)}
}

type session struct {
	*Validator
	file, application string
	diagnostics       []diag.Diagnostic
	// missing records variables already reported as missing, so cross-variable
	// rules do not report the same absence twice.
	missing map[string]bool
}

func (r *session) add(d diag.Diagnostic) {
	d.File, d.Application = r.file, r.application
	r.diagnostics = append(r.diagnostics, d)
}

func (r *session) reportEnvIssues(env envfile.File) {
	for _, issue := range env.Issues {
		switch issue.Kind {
		case envfile.IssueDuplicate:
			r.add(diag.Diagnostic{
				Severity: diag.SeverityError, Code: diag.DuplicateVariable, Line: issue.Line, Variable: issue.Key,
				Message: fmt.Sprintf("Variable is defined more than once (first defined on line %d). The last definition wins.", issue.FirstLine),
			})
		default:
			r.add(diag.Diagnostic{
				Severity: diag.SeverityError, Code: diag.InvalidEnvSyntax, Line: issue.Line,
				Message: "Cannot parse line: " + issue.Message + ".",
			})
		}
	}
}

func (r *session) checkDeclared(variable *schema.Variable, present map[string]envfile.Entry) {
	entry, isSet := present[variable.Name]
	if !isSet {
		if variable.Required {
			r.missing[variable.Name] = true
			r.add(diag.Diagnostic{
				Severity: diag.SeverityError, Code: diag.MissingRequired, Variable: variable.Name,
				Message: "Required environment variable is not set.", Expected: variable.Expectation(),
			})
		}
		return
	}

	if variable.Deprecated {
		r.reportDeprecated(variable, entry)
	}
	if entry.Value == "" && variable.Required {
		r.missing[variable.Name] = true
		r.add(diag.Diagnostic{
			Severity: diag.SeverityError, Code: diag.MissingRequired, Line: entry.Line, Variable: variable.Name,
			Message: "Required environment variable is set but empty.", Expected: variable.Expectation(),
		})
		return
	}
	if problem := variable.Check(entry.Value); problem != nil {
		r.reportValueProblem(variable, entry, problem)
	}
}

func (r *session) reportDeprecated(variable *schema.Variable, entry envfile.Entry) {
	d := diag.Diagnostic{
		Severity: diag.SeverityWarning, Code: diag.DeprecatedVariable, Line: entry.Line, Variable: variable.Name,
		Message: "This environment variable is deprecated.",
	}
	if variable.Replacement != "" {
		d.Suggestions, d.SuggestionKind = []string{variable.Replacement}, diag.Use
	}
	r.add(d)
}

func (r *session) reportValueProblem(variable *schema.Variable, entry envfile.Entry, problem *schema.ValueProblem) {
	d := diag.Diagnostic{
		Severity: diag.SeverityError, Line: entry.Line, Variable: variable.Name,
		Expected: problem.Expected, Received: receivedText(variable, entry.Value),
	}
	switch problem.Kind {
	case schema.NotInEnum:
		d.Code, d.Message = diag.InvalidEnum, "Value is not one of the allowed values."
		if !variable.Secret {
			d.Suggestions = suggest.NewForValues(variable.Values).Suggest(entry.Value).Suggestions
		}
	case schema.BelowMin:
		d.Code, d.Message = diag.BelowMinimum, "Value is below the allowed minimum."
	case schema.AboveMax:
		d.Code, d.Message = diag.AboveMaximum, "Value is above the allowed maximum."
	default:
		d.Code, d.Message = diag.InvalidType, "Value has the wrong type."
	}
	r.add(d)
}

// receivedText is the only place a value becomes displayable. Secrets never
// pass through it unredacted, and Quote escapes terminal control sequences.
func receivedText(variable *schema.Variable, value string) string {
	if variable.Secret {
		return diag.Redacted
	}
	return strconv.Quote(value)
}

func (r *session) reportUnknown(env envfile.File) {
	firstLines := env.FirstLines()
	for key, line := range firstLines {
		if _, declared := r.schema.Variables[key]; declared {
			continue
		}
		d := diag.Diagnostic{
			Severity: diag.SeverityError, Code: diag.UnknownVariable, Line: line, Variable: key,
			Message: "Unknown environment variable.",
		}
		result := r.suggester.Suggest(key)
		d.Suggestions = result.Suggestions
		switch result.Kind {
		case suggest.KindProperty:
			d.Message, d.SuggestionKind = "Unknown environment variable. Spring property notation detected.", diag.Use
		case suggest.KindCase:
			d.Message, d.SuggestionKind = "Unknown environment variable. Names are case-sensitive.", diag.Use
		}
		r.add(d)
	}
}
