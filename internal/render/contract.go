package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/whoisclebs/heimdall-lint/internal/contractdiff"
	"github.com/whoisclebs/heimdall-lint/internal/diag"
	"github.com/whoisclebs/heimdall-lint/internal/schema"
)

// InspectEntry is one place a variable is declared.
type InspectEntry struct {
	// Source names the schema, and the application when known.
	Source   string
	Variable *schema.Variable
}

// Inspect describes a variable. It only ever reads the schema, so it cannot
// leak environment values; defaults of secrets are redacted.
func Inspect(w io.Writer, name string, entries []InspectEntry) error {
	var out strings.Builder
	for i, entry := range entries {
		if i > 0 {
			out.WriteString("\n")
		}
		fmt.Fprintf(&out, "%s\n", clean(name))
		if len(entries) > 1 {
			fmt.Fprintf(&out, "(%s)\n", clean(entry.Source))
		}
		out.WriteString("\n")
		writeVariable(&out, entry.Variable)
	}
	_, err := io.WriteString(w, out.String())
	return err
}

func writeVariable(out *strings.Builder, v *schema.Variable) {
	field := func(label, value string) { fmt.Fprintf(out, "%-16s%s\n", label+":", clean(value)) }

	field("Type", string(v.Type))
	field("Required", yesNo(v.Required))
	switch {
	case v.Default == nil:
	case v.Secret:
		field("Default", diag.Redacted)
	default:
		field("Default", *v.Default)
	}
	if v.Min != nil {
		field("Minimum", v.Min.Raw)
	}
	if v.Max != nil {
		field("Maximum", v.Max.Raw)
	}
	if len(v.Values) > 0 {
		field("Values", strings.Join(v.Values, ", "))
	}
	if len(v.Schemes) > 0 {
		field("Schemes", strings.Join(v.Schemes, ", "))
	}
	if v.Secret {
		field("Secret", "yes (values are never displayed)")
	}
	if v.Deprecated {
		deprecated := "yes"
		if v.Replacement != "" {
			deprecated += ", use " + v.Replacement
		}
		field("Deprecated", deprecated)
	}
	if len(v.RequiredIf) > 0 {
		field("Required if", contractdiff.ConditionsText(v.RequiredIf))
	}
	if len(v.Requires) > 0 {
		field("Requires", strings.Join(v.Requires, ", "))
	}
	if len(v.ConflictsWith) > 0 {
		field("Conflicts with", strings.Join(v.ConflictsWith, ", "))
	}
	if v.Description != "" {
		fmt.Fprintf(out, "\nDescription:\n%s\n", clean(v.Description))
	}
}

func yesNo(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

// Diff writes the contract changes in human form.
func Diff(w io.Writer, result contractdiff.Result) error {
	if result.Empty() {
		_, err := io.WriteString(w, "No changes to the environment contract.\n")
		return err
	}

	var out strings.Builder
	out.WriteString("Environment contract changes\n")
	if len(result.Added) > 0 {
		out.WriteString("\nADDED\n")
		for _, v := range result.Added {
			fmt.Fprintf(&out, "\n  %s%s\n", clean(v.Name), actionTag(contractdiff.ActionRequiredForAdded(v)))
			fmt.Fprintf(&out, "    type: %s\n    required: %t\n", v.Type, v.Required)
			if v.Default != nil && !v.Secret {
				fmt.Fprintf(&out, "    default: %s\n", clean(*v.Default))
			}
		}
	}
	if len(result.Removed) > 0 {
		out.WriteString("\nREMOVED\n\n")
		for _, v := range result.Removed {
			fmt.Fprintf(&out, "  %s\n", clean(v.Name))
		}
	}
	if len(result.Changed) > 0 {
		out.WriteString("\nCHANGED\n")
		for _, changed := range result.Changed {
			fmt.Fprintf(&out, "\n  %s%s\n", clean(changed.Name), actionTag(changed.ActionRequired))
			for _, change := range changed.Changes {
				fmt.Fprintf(&out, "    %s: %s -> %s\n", change.Attribute, clean(change.From), clean(change.To))
			}
		}
	}
	_, err := io.WriteString(w, out.String())
	return err
}

func actionTag(required bool) string {
	if required {
		return "  [action required]"
	}
	return ""
}

type jsonAdded struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	Required       bool   `json:"required"`
	Default        string `json:"default,omitempty"`
	ActionRequired bool   `json:"action_required"`
}

type jsonChanged struct {
	Name           string       `json:"name"`
	ActionRequired bool         `json:"action_required"`
	Changes        []jsonChange `json:"changes"`
}

type jsonChange struct {
	Attribute string `json:"attribute"`
	From      string `json:"from"`
	To        string `json:"to"`
}

// DiffJSON writes the contract changes as JSON.
func DiffJSON(w io.Writer, result contractdiff.Result) error {
	document := struct {
		Added   []jsonAdded   `json:"added"`
		Removed []string      `json:"removed"`
		Changed []jsonChanged `json:"changed"`
	}{Added: []jsonAdded{}, Removed: []string{}, Changed: []jsonChanged{}}

	for _, v := range result.Added {
		added := jsonAdded{Name: v.Name, Type: string(v.Type), Required: v.Required, ActionRequired: contractdiff.ActionRequiredForAdded(v)}
		if v.Default != nil && !v.Secret {
			added.Default = *v.Default
		}
		document.Added = append(document.Added, added)
	}
	for _, v := range result.Removed {
		document.Removed = append(document.Removed, v.Name)
	}
	for _, changed := range result.Changed {
		converted := jsonChanged{Name: changed.Name, ActionRequired: changed.ActionRequired, Changes: []jsonChange{}}
		for _, change := range changed.Changes {
			converted.Changes = append(converted.Changes, jsonChange{Attribute: change.Attribute, From: change.From, To: change.To})
		}
		document.Changed = append(document.Changed, converted)
	}
	return writeJSON(w, document)
}
