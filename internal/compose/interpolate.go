package compose

import "strings"

// Lookup resolves a variable for interpolation.
type Lookup func(name string) (string, bool)

// interpolate expands ${VAR}, $VAR, ${VAR:-default}, ${VAR-default} and $$ the
// way Docker Compose does. An unset variable expands to the empty string.
// Forms Heimdall does not evaluate (${VAR:?err}, ${VAR:+alt}) are left as
// written, so validation sees, and reports, the literal text.
func interpolate(value string, lookup Lookup) string {
	if !strings.Contains(value, "$") {
		return value
	}
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '$' {
			out.WriteByte(value[i])
			continue
		}
		if i+1 >= len(value) {
			out.WriteByte('$')
			break
		}
		switch next := value[i+1]; {
		case next == '$':
			out.WriteByte('$')
			i++
		case next == '{':
			end := strings.IndexByte(value[i:], '}')
			if end < 0 {
				out.WriteString(value[i:])
				return out.String()
			}
			out.WriteString(expandBraced(value[i+2:i+end], value[i:i+end+1], lookup))
			i += end
		case isNameStart(next):
			j := i + 1
			for j < len(value) && isNameChar(value[j]) {
				j++
			}
			resolved, _ := lookup(value[i+1 : j])
			out.WriteString(resolved)
			i = j - 1
		default:
			out.WriteByte('$')
		}
	}
	return out.String()
}

// expandBraced handles the inside of ${...}; original is returned untouched
// for unsupported forms.
func expandBraced(inner, original string, lookup Lookup) string {
	if name, fallback, found := strings.Cut(inner, ":-"); found && validName(name) {
		if resolved, set := lookup(name); set && resolved != "" {
			return resolved
		}
		return fallback
	}
	if name, fallback, found := strings.Cut(inner, "-"); found && validName(name) {
		if resolved, set := lookup(name); set {
			return resolved
		}
		return fallback
	}
	if validName(inner) {
		resolved, _ := lookup(inner)
		return resolved
	}
	return original
}

func validName(name string) bool {
	if name == "" || !isNameStart(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isNameChar(name[i]) {
			return false
		}
	}
	return true
}

func isNameStart(c byte) bool { return c == '_' || (c|0x20 >= 'a' && c|0x20 <= 'z') }
func isNameChar(c byte) bool  { return isNameStart(c) || (c >= '0' && c <= '9') }
