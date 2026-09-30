package compose

import "testing"

func FuzzInterpolate(f *testing.F) {
	for _, seed := range []string{"", "$", "$$", "${", "${A", "${A}", "${A:-b}", "${A-b}", "$A$B", "${:-}", "${-}", "a$", "${A:?x}", "$1", "${A:-${B}}"} {
		f.Add(seed)
	}
	lookup := func(name string) (string, bool) { return "v", len(name)%2 == 0 }
	f.Fuzz(func(t *testing.T, input string) {
		_ = interpolate(input, lookup)
	})
}

func FuzzParse(f *testing.F) {
	f.Add([]byte("services:\n  a:\n    env_file: [x.env]\n    environment: {A: '${B:-c}'}\n"))
	f.Add([]byte("services:\n  a:\n    environment: ['A=b', 'C']\n    x-heimdall: {skip: true}\n"))
	f.Add([]byte("services: {a: {env_file: [{path: p, required: false}]}}"))
	f.Add([]byte("services:\n  a: &x {environment: {A: 1}}\n  b: *x\n"))
	lookup := func(string) (string, bool) { return "", false }
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Parse(data, "/tmp/fuzz/compose.yaml", lookup)
	})
}
