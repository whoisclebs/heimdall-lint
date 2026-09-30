package envfile

import (
	"strings"
	"testing"
)

func values(f File) map[string]string {
	out := map[string]string{}
	for _, e := range f.Entries {
		out[e.Key] = e.Value
	}
	return out
}

func TestParseValues(t *testing.T) {
	tests := []struct {
		name, input, key, want string
	}{
		{"plain", "FOO=bar", "FOO", "bar"},
		{"double quoted", `FOO="bar"`, "FOO", "bar"},
		{"single quoted", `FOO='bar'`, "FOO", "bar"},
		{"empty", "FOO=", "FOO", ""},
		{"spaces unquoted", "FOO=hello world", "FOO", "hello world"},
		{"spaces quoted", `FOO="hello world"`, "FOO", "hello world"},
		{"equals inside value", "URL=jdbc:pg://h/db?a=b&c=d", "URL", "jdbc:pg://h/db?a=b&c=d"},
		{"whitespace around", "  FOO  =  bar  ", "FOO", "bar"},
		{"export prefix", "export FOO=bar", "FOO", "bar"},
		{"inline comment", "FOO=bar # note", "FOO", "bar"},
		{"hash without space kept", "FOO=a#b", "FOO", "a#b"},
		{"hash inside quotes kept", `FOO="a # b"`, "FOO", "a # b"},
		{"comment after quote", `FOO="bar" # note`, "FOO", "bar"},
		{"escaped newline", `FOO="a\nb"`, "FOO", "a\nb"},
		{"escaped quote", `FOO="say \"hi\""`, "FOO", `say "hi"`},
		{"single quote is literal", `FOO='a\nb'`, "FOO", `a\nb`},
		{"dotted key", "api.timeout=5", "api.timeout", "5"},
		{"BOM", "\xEF\xBB\xBFFOO=bar", "FOO", "bar"},
		{"CRLF", "FOO=bar\r\nBAZ=qux\r\n", "BAZ", "qux"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := Parse([]byte(tt.input))
			if len(file.Issues) != 0 {
				t.Fatalf("unexpected issues: %+v", file.Issues)
			}
			got, present := values(file)[tt.key]
			if !present || got != tt.want {
				t.Fatalf("%s = %q (present=%v), want %q", tt.key, got, present, tt.want)
			}
		})
	}
}

func TestParseSkipsCommentsAndBlankLines(t *testing.T) {
	file := Parse([]byte("# comment\n\n   \nFOO=bar\n  # indented\n"))
	if len(file.Entries) != 1 || file.Entries[0].Line != 4 {
		t.Fatalf("entries = %+v", file.Entries)
	}
}

func TestParseDuplicates(t *testing.T) {
	file := Parse([]byte("FOO=1\nBAR=2\nFOO=3\n"))
	if len(file.Issues) != 1 {
		t.Fatalf("issues = %+v", file.Issues)
	}
	issue := file.Issues[0]
	if issue.Kind != IssueDuplicate || issue.Key != "FOO" || issue.Line != 3 || issue.FirstLine != 1 {
		t.Fatalf("issue = %+v", issue)
	}
	if got := file.Lookup()["FOO"].Value; got != "3" {
		t.Fatalf("last definition must win, got %q", got)
	}
}

func TestParseSyntaxIssues(t *testing.T) {
	tests := map[string]string{
		"no equals":          "JUSTAKEY",
		"bad name":           "1FOO=bar",
		"name with space":    "FOO BAR=baz",
		"unterminated dq":    `FOO="bar`,
		"unterminated sq":    `FOO='bar`,
		"trailing garbage":   `FOO="bar" baz`,
		"trailing backslash": `FOO="bar\`,
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			file := Parse([]byte(input))
			if len(file.Issues) != 1 || file.Issues[0].Kind != IssueSyntax || file.Issues[0].Line != 1 {
				t.Fatalf("issues = %+v", file.Issues)
			}
		})
	}
}

func TestParseRejectsBinary(t *testing.T) {
	file := Parse([]byte("FOO=bar\x00baz"))
	if len(file.Issues) != 1 || len(file.Entries) != 0 {
		t.Fatalf("file = %+v", file)
	}
}

func TestIssuesNeverContainValues(t *testing.T) {
	file := Parse([]byte(`PASSWORD="super-secret-value`))
	for _, issue := range file.Issues {
		if strings.Contains(issue.Message, "super-secret-value") {
			t.Fatalf("issue leaks value: %+v", issue)
		}
	}
}
