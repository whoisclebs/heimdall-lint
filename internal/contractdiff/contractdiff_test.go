package contractdiff

import (
	"slices"
	"testing"

	"github.com/whoisclebs/heimdall-lint/internal/schema"
)

func parse(t *testing.T, variables string) *schema.Schema {
	t.Helper()
	s, err := schema.Parse([]byte("version: 1\nvariables:\n"+variables), "x.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func changeFor(result Result, name, attribute string) *Change {
	for _, c := range result.Changed {
		if c.Name != name {
			continue
		}
		for i := range c.Changes {
			if c.Changes[i].Attribute == attribute {
				return &c.Changes[i]
			}
		}
	}
	return nil
}

func TestAddedAndRemoved(t *testing.T) {
	old := parse(t, "  A: {type: string}\n  LEGACY: {type: string}\n")
	updated := parse(t, "  A: {type: string}\n  NEW_REQUIRED: {type: duration, required: true}\n  NEW_OPTIONAL: {type: string}\n")
	result := Compare(old, updated)

	if len(result.Added) != 2 || result.Added[0].Name != "NEW_OPTIONAL" || result.Added[1].Name != "NEW_REQUIRED" {
		t.Fatalf("added = %+v", result.Added)
	}
	if len(result.Removed) != 1 || result.Removed[0].Name != "LEGACY" {
		t.Fatalf("removed = %+v", result.Removed)
	}
	if !ActionRequiredForAdded(result.Added[1]) || ActionRequiredForAdded(result.Added[0]) {
		t.Fatal("only the required variable without default demands action")
	}
}

func TestDefaultChange(t *testing.T) {
	result := Compare(
		parse(t, "  T: {type: duration, default: 5s}\n"),
		parse(t, "  T: {type: duration, default: 10s}\n"))
	c := changeFor(result, "T", "default")
	if c == nil || c.From != "5s" || c.To != "10s" || result.Changed[0].ActionRequired {
		t.Fatalf("result = %+v", result)
	}
}

func TestRequiredChange(t *testing.T) {
	result := Compare(parse(t, "  A: {type: string}\n"), parse(t, "  A: {type: string, required: true}\n"))
	c := changeFor(result, "A", "required")
	if c == nil || c.From != "false" || c.To != "true" || !result.Changed[0].ActionRequired {
		t.Fatalf("result = %+v", result)
	}
}

func TestTypeChange(t *testing.T) {
	result := Compare(parse(t, "  A: {type: string}\n"), parse(t, "  A: {type: integer}\n"))
	if c := changeFor(result, "A", "type"); c == nil || c.From != "string" || c.To != "integer" || !result.Changed[0].ActionRequired {
		t.Fatalf("result = %+v", result)
	}
}

func TestLimitsValuesAndFlags(t *testing.T) {
	result := Compare(
		parse(t, "  A: {type: integer, min: 1, max: 10}\n  M: {type: enum, values: [X, Y]}\n  U: {type: url}\n  S: {type: string}\n"),
		parse(t, "  A: {type: integer, min: 5}\n  M: {type: enum, values: [X, Z]}\n  U: {type: url, schemes: [https]}\n  S: {type: string, secret: true}\n"))
	for _, want := range [][2]string{{"A", "min"}, {"A", "max"}, {"M", "values"}, {"U", "schemes"}, {"S", "secret"}} {
		if changeFor(result, want[0], want[1]) == nil {
			t.Errorf("missing change %v", want)
		}
	}
	if c := changeFor(result, "A", "max"); c.From != "10" || c.To != none {
		t.Errorf("max change = %+v", c)
	}
}

func TestDeprecationChange(t *testing.T) {
	result := Compare(
		parse(t, "  A: {type: string}\n  B: {type: string}\n"),
		parse(t, "  A: {type: string, deprecated: true, replacement: B}\n  B: {type: string}\n"))
	if changeFor(result, "A", "deprecated") == nil || changeFor(result, "A", "replacement") == nil {
		t.Fatalf("result = %+v", result)
	}
}

func TestSecretDefaultsAreRedacted(t *testing.T) {
	result := Compare(
		parse(t, "  K: {type: string, secret: true, default: old-secret}\n"),
		parse(t, "  K: {type: string, secret: true, default: new-secret}\n"))
	// Both redacted, so the change is invisible rather than leaking either value.
	if !result.Empty() {
		t.Fatalf("result = %+v", result)
	}
	result = Compare(
		parse(t, "  K: {type: string, default: visible}\n"),
		parse(t, "  K: {type: string, secret: true, default: new-secret}\n"))
	c := changeFor(result, "K", "default")
	if c == nil || c.To != "[REDACTED]" {
		t.Fatalf("change = %+v", c)
	}
}

func TestIdenticalSchemasAreEmptyAndOrderIsStable(t *testing.T) {
	s := parse(t, "  A: {type: string}\n  B: {type: string}\n")
	if !Compare(s, s).Empty() {
		t.Fatal("identical schemas must produce no changes")
	}
	names := []string{}
	for _, c := range Compare(parse(t, "  Z: {type: string}\n  A: {type: string}\n"), parse(t, "  Z: {type: integer}\n  A: {type: integer}\n")).Changed {
		names = append(names, c.Name)
	}
	if !slices.Equal(names, []string{"A", "Z"}) {
		t.Fatalf("names = %v", names)
	}
}

func TestRuleChanges(t *testing.T) {
	result := Compare(
		parse(t, "  FLAG: {type: boolean}\n  A: {type: string}\n  B: {type: string}\n"),
		parse(t, "  FLAG: {type: boolean}\n  A: {type: string, required_if: {FLAG: true}, requires: [B], conflicts_with: [B]}\n  B: {type: string}\n"))
	for attribute, want := range map[string]string{"required_if": "FLAG=true", "requires": "B", "conflicts_with": "B"} {
		c := changeFor(result, "A", attribute)
		if c == nil || c.From != none || c.To != want {
			t.Errorf("%s change = %+v", attribute, c)
		}
	}
}
