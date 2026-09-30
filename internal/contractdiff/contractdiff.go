// Package contractdiff compares two versions of a schema so operators know
// exactly what a release changes.
package contractdiff

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/whoisclebs/heimdall-lint/internal/diag"
	"github.com/whoisclebs/heimdall-lint/internal/schema"
)

const none = "(none)"

type Change struct {
	Attribute string
	From, To  string
}

type Changed struct {
	Name    string
	Changes []Change
	// ActionRequired is true when operators must edit environment files:
	// the variable became required without a default, or its type changed.
	ActionRequired bool
}

type Result struct {
	Added   []*schema.Variable
	Removed []*schema.Variable
	Changed []Changed
}

func (r Result) Empty() bool {
	return len(r.Added)+len(r.Removed)+len(r.Changed) == 0
}

// ActionRequiredForAdded reports whether a new variable forces a new setting.
func ActionRequiredForAdded(v *schema.Variable) bool {
	return v.Required && v.Default == nil
}

// Compare returns changes from old to updated, sorted by variable name.
func Compare(old, updated *schema.Schema) Result {
	var result Result
	for _, name := range updated.Names() {
		if _, existed := old.Variables[name]; !existed {
			result.Added = append(result.Added, updated.Variables[name])
		}
	}
	for _, name := range old.Names() {
		after, kept := updated.Variables[name]
		if !kept {
			result.Removed = append(result.Removed, old.Variables[name])
			continue
		}
		if changes := compareVariable(old.Variables[name], after); len(changes) > 0 {
			result.Changed = append(result.Changed, Changed{
				Name: name, Changes: changes, ActionRequired: needsAction(old.Variables[name], after),
			})
		}
	}
	slices.SortFunc(result.Changed, func(a, b Changed) int { return cmp.Compare(a.Name, b.Name) })
	return result
}

func needsAction(before, after *schema.Variable) bool {
	becameRequired := !before.Required && after.Required && after.Default == nil
	return becameRequired || before.Type != after.Type
}

func compareVariable(before, after *schema.Variable) []Change {
	var changes []Change
	record := func(attribute, from, to string) {
		if from != to {
			changes = append(changes, Change{Attribute: attribute, From: from, To: to})
		}
	}

	record("type", string(before.Type), string(after.Type))
	record("required", strconv.FormatBool(before.Required), strconv.FormatBool(after.Required))
	record("default", defaultText(before), defaultText(after))
	record("min", boundText(before.Min), boundText(after.Min))
	record("max", boundText(before.Max), boundText(after.Max))
	record("values", listText(before.Values), listText(after.Values))
	record("schemes", listText(before.Schemes), listText(after.Schemes))
	record("secret", strconv.FormatBool(before.Secret), strconv.FormatBool(after.Secret))
	record("deprecated", strconv.FormatBool(before.Deprecated), strconv.FormatBool(after.Deprecated))
	record("replacement", orNone(before.Replacement), orNone(after.Replacement))
	record("required_if", ConditionsText(before.RequiredIf), ConditionsText(after.RequiredIf))
	record("requires", listText(before.Requires), listText(after.Requires))
	record("conflicts_with", listText(before.ConflictsWith), listText(after.ConflictsWith))
	return changes
}

// defaultText never shows the default of a secret.
func defaultText(v *schema.Variable) string {
	switch {
	case v.Default == nil:
		return none
	case v.Secret:
		return diag.Redacted
	}
	return *v.Default
}

func boundText(b *schema.Bound) string {
	if b == nil {
		return none
	}
	return b.Raw
}

func listText(values []string) string {
	if len(values) == 0 {
		return none
	}
	return strings.Join(values, ", ")
}

func orNone(s string) string {
	if s == "" {
		return none
	}
	return s
}

// ConditionsText renders required_if conditions as "A=x, B=y", or "(none)".
func ConditionsText(conditions []schema.Condition) string {
	if len(conditions) == 0 {
		return none
	}
	parts := make([]string, len(conditions))
	for i, condition := range conditions {
		parts[i] = condition.Name + "=" + condition.Value
	}
	return strings.Join(parts, ", ")
}
