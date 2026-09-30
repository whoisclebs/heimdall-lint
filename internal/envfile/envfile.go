// Package envfile parses Docker-compatible .env files.
//
// Issues never contain variable values: values may be secrets and issues end
// up in logs.
package envfile

import (
	"bytes"
	"strings"
)

type Entry struct {
	Key   string
	Value string
	Line  int
}

type IssueKind int

const (
	IssueSyntax IssueKind = iota
	IssueDuplicate
)

type Issue struct {
	Kind      IssueKind
	Line      int
	Key       string // set for duplicates only
	FirstLine int    // set for duplicates only
	Message   string
}

type File struct {
	Entries []Entry
	Issues  []Issue
}

// Lookup returns the effective value of key. As in Docker Compose, the last
// definition wins.
func (f File) Lookup() map[string]Entry {
	effective := make(map[string]Entry, len(f.Entries))
	for _, entry := range f.Entries {
		effective[entry.Key] = entry
	}
	return effective
}

// FirstLines returns the line of the first definition of every key.
func (f File) FirstLines() map[string]int {
	first := make(map[string]int, len(f.Entries))
	for _, entry := range f.Entries {
		if _, seen := first[entry.Key]; !seen {
			first[entry.Key] = entry.Line
		}
	}
	return first
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func Parse(data []byte) File {
	var file File

	if bytes.IndexByte(data, 0) >= 0 {
		file.Issues = append(file.Issues, Issue{Kind: IssueSyntax, Message: "file contains NUL bytes and is not a text file"})
		return file
	}
	data = bytes.TrimPrefix(data, utf8BOM)

	firstSeen := make(map[string]int)
	for index, rawLine := range strings.Split(string(data), "\n") {
		lineNumber := index + 1
		line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		entry, problem := parseLine(line)
		if problem != "" {
			file.Issues = append(file.Issues, Issue{Kind: IssueSyntax, Line: lineNumber, Message: problem})
			continue
		}
		entry.Line = lineNumber

		if first, duplicated := firstSeen[entry.Key]; duplicated {
			file.Issues = append(file.Issues, Issue{
				Kind: IssueDuplicate, Line: lineNumber, Key: entry.Key, FirstLine: first,
				Message: "variable is defined more than once",
			})
		} else {
			firstSeen[entry.Key] = lineNumber
		}
		file.Entries = append(file.Entries, entry)
	}
	return file
}

func parseLine(line string) (Entry, string) {
	if rest, found := strings.CutPrefix(line, "export"); found && rest != "" && (rest[0] == ' ' || rest[0] == '\t') {
		line = strings.TrimSpace(rest)
	}

	rawKey, rawValue, found := strings.Cut(line, "=")
	if !found {
		return Entry{}, "expected KEY=VALUE"
	}
	key := strings.TrimSpace(rawKey)
	if !isValidKey(key) {
		return Entry{}, "invalid variable name"
	}

	value, problem := parseValue(strings.TrimLeft(rawValue, " \t"))
	if problem != "" {
		return Entry{}, problem
	}
	return Entry{Key: key, Value: value}, ""
}

// isValidKey accepts what Docker accepts: letters, digits, '_', '.', '-'.
// Dots and dashes are kept so Spring notation can be diagnosed instead of
// being reported as a syntax error.
func isValidKey(key string) bool {
	if key == "" || (key[0] >= '0' && key[0] <= '9') {
		return false
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
		default:
			return false
		}
	}
	return true
}

func parseValue(raw string) (string, string) {
	if raw == "" {
		return "", ""
	}
	switch raw[0] {
	case '"':
		return parseDoubleQuoted(raw[1:])
	case '\'':
		closing := strings.IndexByte(raw[1:], '\'')
		if closing < 0 {
			return "", "unterminated single quote"
		}
		return checkTrailer(raw[1:1+closing], raw[2+closing:])
	}
	return stripInlineComment(raw), ""
}

func parseDoubleQuoted(raw string) (string, string) {
	var value strings.Builder
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '\\':
			if i+1 >= len(raw) {
				return "", "unterminated double quote"
			}
			i++
			value.WriteString(unescape(raw[i]))
		case '"':
			return checkTrailer(value.String(), raw[i+1:])
		default:
			value.WriteByte(raw[i])
		}
	}
	return "", "unterminated double quote"
}

func unescape(escaped byte) string {
	switch escaped {
	case 'n':
		return "\n"
	case 'r':
		return "\r"
	case 't':
		return "\t"
	case '"', '\\':
		return string(escaped)
	}
	return "\\" + string(escaped)
}

// checkTrailer allows only whitespace or a comment after a closing quote.
func checkTrailer(value, trailer string) (string, string) {
	trailer = strings.TrimSpace(trailer)
	if trailer == "" || strings.HasPrefix(trailer, "#") {
		return value, ""
	}
	return "", "unexpected characters after closing quote"
}

func stripInlineComment(raw string) string {
	for i := 1; i < len(raw); i++ {
		if raw[i] == '#' && (raw[i-1] == ' ' || raw[i-1] == '\t') {
			raw = raw[:i]
			break
		}
	}
	return strings.TrimSpace(raw)
}
