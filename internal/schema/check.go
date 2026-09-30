package schema

import (
	"cmp"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type ProblemKind int

const (
	WrongType ProblemKind = iota
	NotInEnum
	BelowMin
	AboveMax
)

// ValueProblem describes why a value violates its variable. It never contains
// the value itself.
type ValueProblem struct {
	Kind     ProblemKind
	Expected string
}

var floatPattern = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)

// Check validates raw against the variable's type and limits. It returns nil
// when the value is acceptable.
func (v *Variable) Check(raw string) *ValueProblem {
	wrong := &ValueProblem{Kind: WrongType, Expected: v.Expectation()}

	switch v.Type {
	case String:
		return nil
	case Integer, Port:
		number, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return wrong
		}
		low, high := v.effectiveMin(), v.effectiveMax()
		return v.boundsProblem(low != nil && number < low.Int, high != nil && number > high.Int)
	case Float:
		number, err := strconv.ParseFloat(raw, 64)
		if err != nil || !floatPattern.MatchString(raw) || math.IsInf(number, 0) {
			return wrong
		}
		return v.boundsProblem(v.Min != nil && number < v.Min.Float, v.Max != nil && number > v.Max.Float)
	case Duration:
		duration, err := time.ParseDuration(raw)
		if err != nil {
			return wrong
		}
		return v.boundsProblem(v.Min != nil && duration < v.Min.Duration, v.Max != nil && duration > v.Max.Duration)
	case Boolean:
		if lower := strings.ToLower(raw); lower != "true" && lower != "false" {
			return wrong
		}
	case Enum:
		if !slices.Contains(v.Values, raw) {
			return &ValueProblem{Kind: NotInEnum, Expected: v.Expectation()}
		}
	case URL:
		if !v.validURL(raw) {
			return wrong
		}
	case Hostname:
		if !validHostname(raw) {
			return wrong
		}
	case IP:
		if _, err := netip.ParseAddr(raw); err != nil {
			return wrong
		}
	case CIDR:
		if _, err := netip.ParsePrefix(raw); err != nil {
			return wrong
		}
	}
	return nil
}

func (v *Variable) boundsProblem(belowMin, aboveMax bool) *ValueProblem {
	switch {
	case belowMin:
		return &ValueProblem{Kind: BelowMin, Expected: v.Expectation()}
	case aboveMax:
		return &ValueProblem{Kind: AboveMax, Expected: v.Expectation()}
	}
	return nil
}

// Effective numeric limits: port carries implicit 1..65535.
func (v *Variable) effectiveMin() *Bound {
	if v.Min == nil && v.Type == Port {
		return &Bound{Raw: "1", Int: 1}
	}
	return v.Min
}

func (v *Variable) effectiveMax() *Bound {
	if v.Max == nil && v.Type == Port {
		return &Bound{Raw: "65535", Int: 65535}
	}
	return v.Max
}

func parseBound(t Type, raw string) (*Bound, string) {
	bound := &Bound{Raw: raw}
	switch t {
	case Integer, Port:
		number, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, "expected an integer"
		}
		bound.Int = number
	case Float:
		number, err := strconv.ParseFloat(raw, 64)
		if err != nil || !floatPattern.MatchString(raw) {
			return nil, "expected a number"
		}
		bound.Float = number
	case Duration:
		duration, err := time.ParseDuration(raw)
		if err != nil {
			return nil, "expected a duration such as 100ms or 5s"
		}
		bound.Duration = duration
	}
	return bound, ""
}

func compareBounds(t Type, a, b *Bound) int {
	switch t {
	case Float:
		return cmp.Compare(a.Float, b.Float)
	case Duration:
		return cmp.Compare(a.Duration, b.Duration)
	}
	return cmp.Compare(a.Int, b.Int)
}

// webSchemes always carry an authority (host); every other scheme may be
// opaque, like jdbc:postgresql://db:5432/app.
var webSchemes = []string{"http", "https", "ws", "wss", "ftp"}

func (v *Variable) validURL(raw string) bool {
	if raw == "" || strings.IndexFunc(raw, unicode.IsSpace) >= 0 || strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if len(v.Schemes) > 0 && !slices.Contains(v.Schemes, scheme) {
		return false
	}
	if parsed.Host != "" {
		return true
	}
	return parsed.Opaque != "" && !slices.Contains(webSchemes, scheme)
}

var hostLabel = regexp.MustCompile(`^[A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?$`)

// validHostname accepts RFC 1123 names, plus '_' because Docker service names
// may contain it, and IP addresses because DATABASE_HOST is often one.
func validHostname(raw string) bool {
	if _, err := netip.ParseAddr(raw); err == nil {
		return true
	}
	if raw == "" || len(raw) > 253 {
		return false
	}
	for _, label := range strings.Split(raw, ".") {
		if !hostLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// Expectation describes what a valid value looks like, for messages.
func (v *Variable) Expectation() string {
	switch v.Type {
	case String:
		return "any text"
	case Boolean:
		return "boolean (true or false)"
	case Enum:
		return "one of: " + strings.Join(v.Values, ", ")
	case URL:
		if len(v.Schemes) > 0 {
			return "URL with scheme " + strings.Join(v.Schemes, " or ")
		}
		return "absolute URL"
	case Hostname:
		return "hostname or IP address"
	case IP:
		return "IP address"
	case CIDR:
		return "CIDR prefix such as 10.0.0.0/8"
	}

	noun := map[Type]string{Integer: "integer", Float: "number", Port: "port number", Duration: "duration (for example 5s or 100ms)"}[v.Type]
	low, high := v.effectiveMin(), v.effectiveMax()
	switch {
	case low != nil && high != nil:
		return fmt.Sprintf("%s between %s and %s", noun, low.Raw, high.Raw)
	case low != nil:
		return fmt.Sprintf("%s >= %s", noun, low.Raw)
	case high != nil:
		return fmt.Sprintf("%s <= %s", noun, high.Raw)
	}
	return noun
}
