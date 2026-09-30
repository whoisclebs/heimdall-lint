package manifest

import "testing"

func FuzzParse(f *testing.F) {
	f.Add([]byte("version: 1\napplications:\n  a: {schema: s.yaml, env: [e.env]}\n"))
	f.Add([]byte("version: 1\napplications:\n  a: {schema: s.yaml, env: ['[']}\n"))
	f.Add([]byte(""))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Parse(data, "heimdall.yaml")
	})
}
