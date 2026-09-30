package envfile

import "testing"

// staticMessages are the only texts an issue may carry: fixed strings cannot
// echo input, so they cannot leak secrets.
var staticMessages = map[string]bool{
	"file contains NUL bytes and is not a text file": true,
	"expected KEY=VALUE":                             true,
	"invalid variable name":                          true,
	"unterminated single quote":                      true,
	"unterminated double quote":                      true,
	"unexpected characters after closing quote":      true,
	"variable is defined more than once":             true,
}

// FuzzParse checks that arbitrary input never panics, never yields an entry
// with an invalid key, and only emits fixed issue messages.
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"FOO=bar", "FOO=\"bar", "export A=b", "\xEF\xBB\xBFA=1", "A=1\r\nB=2", "=x", "A==", "# c", "A='x' #y", "A=\"\\", "\x00",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		file := Parse([]byte(input))
		for _, entry := range file.Entries {
			if !isValidKey(entry.Key) || entry.Line < 1 {
				t.Fatalf("bad entry %+v", entry)
			}
		}
		for _, issue := range file.Issues {
			if !staticMessages[issue.Message] {
				t.Fatalf("suspicious issue %+v", issue)
			}
		}
	})
}
