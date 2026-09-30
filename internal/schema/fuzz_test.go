package schema

import "testing"

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"version: 1\nvariables:\n  A: {type: string}\n",
		"version: 1\nvariables:\n  A: {type: integer, min: 1, max: 5, default: 3}\n",
		"version: 1\nvariables:\n  A: &x {type: string}\n  B: *x\n",
		"version: 9\n", "", "[", "version: 1\nvariables:\n  A: {type: enum, values: [a, b]}\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		parsed, err := Parse(data, "fuzz.yaml")
		if err != nil {
			return
		}
		// A schema that loads must be internally consistent.
		for name, variable := range parsed.Variables {
			if variable.Name != name {
				t.Fatalf("name mismatch %q vs %q", name, variable.Name)
			}
			if variable.Default != nil && variable.Check(*variable.Default) != nil {
				t.Fatalf("accepted an invalid default for %s", name)
			}
		}
	})
}
