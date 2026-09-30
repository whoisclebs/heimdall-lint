// Package render turns reports into text and JSON. Every string that came from
// the outside world passes through clean before reaching a terminal.
package render

import (
	"fmt"
	"strings"
	"unicode"
)

type style struct{ color bool }

func (s style) paint(code, text string) string {
	if !s.color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s style) error(text string) string   { return s.paint("1;31", text) }
func (s style) warning(text string) string { return s.paint("1;33", text) }
func (s style) pass(text string) string    { return s.paint("1;32", text) }
func (s style) bold(text string) string    { return s.paint("1", text) }
func (s style) dim(text string) string     { return s.paint("2", text) }

// clean replaces control characters (including ESC, which starts terminal
// escape sequences) with visible \xNN or \u escapes.
func clean(text string) string {
	if !strings.ContainsFunc(text, unicode.IsControl) {
		return text
	}
	var out strings.Builder
	for _, r := range text {
		if unicode.IsControl(r) {
			if r < 0x100 {
				fmt.Fprintf(&out, `\x%02x`, r)
			} else {
				fmt.Fprintf(&out, `\u%04x`, r)
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
