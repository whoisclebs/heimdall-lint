package schema

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, yamlText string) *Schema {
	t.Helper()
	parsed, err := Parse([]byte(yamlText), "test.schema.yaml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return parsed
}

func TestCheckValues(t *testing.T) {
	parsed := mustParse(t, `
version: 1
variables:
  COUNT:    {type: integer, min: 1, max: 10}
  RATIO:    {type: float, min: 0, max: 1}
  FLAG:     {type: boolean}
  MODE:     {type: enum, values: [FAST, SLOW]}
  API_URL:  {type: url, schemes: [https]}
  ANY_URL:  {type: url}
  JDBC:     {type: url, schemes: [jdbc]}
  TIMEOUT:  {type: duration, min: 100ms, max: 30s}
  HOST:     {type: hostname}
  PORT:     {type: port}
  NARROW:   {type: port, min: 1024}
  ADDR:     {type: ip}
  NET:      {type: cidr}
  TEXT:     {type: string}
`)
	tests := []struct {
		variable, value string
		want            *ProblemKind
	}{
		{"COUNT", "5", nil}, {"COUNT", "banana", kind(WrongType)}, {"COUNT", "5.5", kind(WrongType)},
		{"COUNT", "", kind(WrongType)}, {"COUNT", "0", kind(BelowMin)}, {"COUNT", "11", kind(AboveMax)},
		{"COUNT", "1_0", kind(WrongType)}, {"COUNT", "0x10", kind(WrongType)},
		{"RATIO", "0.5", nil}, {"RATIO", "nan", kind(WrongType)}, {"RATIO", "inf", kind(WrongType)},
		{"RATIO", "1.5", kind(AboveMax)}, {"RATIO", "0x1p-2", kind(WrongType)},
		{"FLAG", "true", nil}, {"FLAG", "FALSE", nil}, {"FLAG", "yes", kind(WrongType)}, {"FLAG", "1", kind(WrongType)},
		{"MODE", "FAST", nil}, {"MODE", "fast", kind(NotInEnum)}, {"MODE", "", kind(NotInEnum)},
		{"API_URL", "https://api.example.com/v1", nil}, {"API_URL", "http://api.example.com", kind(WrongType)},
		{"API_URL", "not a url", kind(WrongType)}, {"API_URL", "https://", kind(WrongType)},
		{"ANY_URL", "jdbc:postgresql://db:5432/app", nil}, {"JDBC", "jdbc:postgresql://db:5432/app", nil}, {"JDBC", "jdbc:", kind(WrongType)}, {"JDBC", "https://x.example.com", kind(WrongType)}, {"API_URL", "https:nohost", kind(WrongType)}, {"ANY_URL", "just-text", kind(WrongType)},
		{"TIMEOUT", "5s", nil}, {"TIMEOUT", "100ms", nil}, {"TIMEOUT", "50ms", kind(BelowMin)},
		{"TIMEOUT", "1m", kind(AboveMax)}, {"TIMEOUT", "5000", kind(WrongType)}, {"TIMEOUT", "5 seconds", kind(WrongType)},
		{"HOST", "db.internal", nil}, {"HOST", "orders_db", nil}, {"HOST", "10.0.0.1", nil},
		{"HOST", "-bad.example", kind(WrongType)}, {"HOST", "a..b", kind(WrongType)}, {"HOST", "has space", kind(WrongType)},
		{"PORT", "5432", nil}, {"PORT", "banana", kind(WrongType)}, {"PORT", "0", kind(BelowMin)}, {"PORT", "70000", kind(AboveMax)},
		{"NARROW", "80", kind(BelowMin)}, {"NARROW", "8080", nil},
		{"ADDR", "::1", nil}, {"ADDR", "999.1.1.1", kind(WrongType)},
		{"NET", "10.0.0.0/8", nil}, {"NET", "10.0.0.0", kind(WrongType)},
		{"TEXT", "", nil}, {"TEXT", "anything at all", nil},
	}
	for _, tt := range tests {
		got := parsed.Variables[tt.variable].Check(tt.value)
		switch {
		case tt.want == nil && got != nil:
			t.Errorf("%s=%q: unexpected problem %+v", tt.variable, tt.value, got)
		case tt.want != nil && got == nil:
			t.Errorf("%s=%q: expected problem kind %d, got none", tt.variable, tt.value, *tt.want)
		case tt.want != nil && got.Kind != *tt.want:
			t.Errorf("%s=%q: kind = %d, want %d", tt.variable, tt.value, got.Kind, *tt.want)
		}
	}
}

func kind(k ProblemKind) *ProblemKind { return &k }

func TestProblemsNeverEchoTheValue(t *testing.T) {
	parsed := mustParse(t, "version: 1\nvariables:\n  N: {type: integer}\n")
	problem := parsed.Variables["N"].Check("super-secret-value")
	if problem == nil || strings.Contains(problem.Expected, "super-secret-value") {
		t.Fatalf("problem = %+v", problem)
	}
}

func TestExpectations(t *testing.T) {
	parsed := mustParse(t, `
version: 1
variables:
  PORT:    {type: port}
  TIMEOUT: {type: duration, min: 100ms, max: 30s}
  LOW:     {type: integer, min: 3}
  HIGH:    {type: integer, max: 9}
  MODE:    {type: enum, values: [A, B]}
  URL:     {type: url, schemes: [https]}
`)
	want := map[string]string{
		"PORT":    "port number between 1 and 65535",
		"TIMEOUT": "duration (for example 5s or 100ms) between 100ms and 30s",
		"LOW":     "integer >= 3",
		"HIGH":    "integer <= 9",
		"MODE":    "one of: A, B",
		"URL":     "URL with scheme https",
	}
	for name, expected := range want {
		if got := parsed.Variables[name].Expectation(); got != expected {
			t.Errorf("%s: %q, want %q", name, got, expected)
		}
	}
}
