package validate

import (
	"fmt"
	"strings"

	"github.com/whoisclebs/heimdall-lint/internal/diag"
	"github.com/whoisclebs/heimdall-lint/internal/envfile"
	"github.com/whoisclebs/heimdall-lint/internal/schema"
)

// checkRules evaluates required_if, requires and conflicts_with.
//
// Conditions of required_if look at the effective value (the file's value, or
// the schema default when the variable is absent), because that is what the
// application will see. "Set" for requires and conflicts_with means present in
// the file with a non-empty value.
func (r *session) checkRules(present map[string]envfile.Entry) {
	isSet := func(name string) bool { return present[name].Value != "" }

	for _, name := range r.schema.Names() {
		variable := r.schema.Variables[name]
		r.checkRequiredIf(variable, present, isSet)
		if isSet(name) {
			r.checkRequires(variable, isSet)
		}
		r.checkConflicts(variable, present, isSet)
	}
}

func (r *session) effectiveValue(name string, present map[string]envfile.Entry) (string, bool) {
	if entry, ok := present[name]; ok {
		return entry.Value, true
	}
	if def := r.schema.Variables[name].Default; def != nil {
		return *def, true
	}
	return "", false
}

func (r *session) checkRequiredIf(variable *schema.Variable, present map[string]envfile.Entry, isSet func(string) bool) {
	if len(variable.RequiredIf) == 0 || isSet(variable.Name) || r.missing[variable.Name] {
		return
	}
	reasons := make([]string, 0, len(variable.RequiredIf))
	for _, condition := range variable.RequiredIf {
		actual, known := r.effectiveValue(condition.Name, present)
		if !known || !sameValue(r.schema.Variables[condition.Name], actual, condition.Value) {
			return
		}
		reasons = append(reasons, fmt.Sprintf("%s is %s", condition.Name, condition.Value))
	}
	r.missing[variable.Name] = true
	r.add(diag.Diagnostic{
		Severity: diag.SeverityError, Code: diag.RequiredByCondition, Variable: variable.Name,
		Message:  "Required because " + strings.Join(reasons, " and ") + ".",
		Expected: variable.Expectation(),
	})
}

func (r *session) checkRequires(variable *schema.Variable, isSet func(string) bool) {
	for _, target := range variable.Requires {
		if isSet(target) || r.missing[target] {
			continue
		}
		r.missing[target] = true
		r.add(diag.Diagnostic{
			Severity: diag.SeverityError, Code: diag.MissingDependency, Variable: target,
			Message:  fmt.Sprintf("Required because %s is set.", variable.Name),
			Expected: r.schema.Variables[target].Expectation(),
		})
	}
}

// checkConflicts reports each conflicting pair once, on the variable that
// sorts first, even when both declare the conflict.
func (r *session) checkConflicts(variable *schema.Variable, present map[string]envfile.Entry, isSet func(string) bool) {
	if !isSet(variable.Name) {
		return
	}
	for _, other := range variable.ConflictsWith {
		if !isSet(other) {
			continue
		}
		first, second := variable.Name, other
		if second < first {
			first, second = second, first
		}
		if variable.Name != first && r.declaresConflict(other, variable.Name) {
			continue // the other side reports it
		}
		r.add(diag.Diagnostic{
			Severity: diag.SeverityError, Code: diag.ConflictingVariables, Line: present[first].Line, Variable: first,
			Message: fmt.Sprintf("Cannot be set together with %s.", second),
		})
	}
}

func (r *session) declaresConflict(name, other string) bool {
	for _, target := range r.schema.Variables[name].ConflictsWith {
		if target == other {
			return true
		}
	}
	return false
}

// sameValue compares a condition literal with an effective value. Booleans are
// case-insensitive, matching how they are validated.
func sameValue(variable *schema.Variable, actual, expected string) bool {
	if variable.Type == schema.Boolean {
		return strings.EqualFold(actual, expected)
	}
	return actual == expected
}
