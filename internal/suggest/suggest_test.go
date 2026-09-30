package suggest

import (
	"slices"
	"testing"

	"github.com/whoisclebs/heimdall-lint/internal/naming"
)

var declared = []string{
	"DATABASE_HOST", "DATABASE_PORT", "QUEUE_PROVIDER", "QUEUE_PROVIDER_TIMEOUT",
	"API_TIMEOUT", "API_BASE_URL", "FEATURE_BETA",
}

func TestDamerauLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0}, {"abc", "abc", 0}, {"abc", "", 3},
		{"TIMEOUT", "TIEMOUT", 1}, // transposition is one edit
		{"TIMEOUT", "TIMOUT", 1},  // deletion
		{"PORT", "HOST", 2},
	}
	for _, tt := range tests {
		if got := damerauLevenshtein(tt.a, tt.b); got != tt.want {
			t.Errorf("distance(%q,%q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestSuggestTypos(t *testing.T) {
	s := New(declared, naming.Spring{})
	tests := map[string]string{
		"API_TIMOUT":            "API_TIMEOUT",
		"API_TIEMOUT":           "API_TIMEOUT",
		"AIP_TIMEOUT":           "API_TIMEOUT",
		"QUEUE_PROVIDR_TIMEOUT": "QUEUE_PROVIDER_TIMEOUT",
		"DATABASE_PORTT":        "DATABASE_PORT",
	}
	for input, want := range tests {
		got := s.Suggest(input)
		if got.Kind != KindTypo || !slices.Equal(got.Suggestions, []string{want}) {
			t.Errorf("Suggest(%q) = %+v, want %s", input, got, want)
		}
	}
}

func TestSuggestSpringNotation(t *testing.T) {
	s := New(declared, naming.Spring{})
	for _, input := range []string{"api.timeout", "api-timeout"} {
		got := s.Suggest(input)
		if got.Kind != KindProperty || !slices.Equal(got.Suggestions, []string{"API_TIMEOUT"}) {
			t.Errorf("Suggest(%q) = %+v", input, got)
		}
	}
}

func TestSuggestCaseOnly(t *testing.T) {
	got := New(declared, naming.Spring{}).Suggest("api_timeout")
	if got.Kind != KindCase || !slices.Equal(got.Suggestions, []string{"API_TIMEOUT"}) {
		t.Errorf("got %+v", got)
	}
}

func TestSuggestNothingForUnrelatedNames(t *testing.T) {
	s := New(declared, naming.Spring{})
	for _, input := range []string{
		"FOO", "TOTALLY_DIFFERENT_NAME", "DATABASE_USER", "DATABASE_NAME", "PATH", "HOME",
		"API_CURRENCY", "A",
	} {
		if got := s.Suggest(input); len(got.Suggestions) != 0 {
			t.Errorf("Suggest(%q) = %+v, want nothing", input, got)
		}
	}
}

func TestSuggestNothingForDifferentSiblings(t *testing.T) {
	s := New([]string{"DATABASE_HOST"}, naming.Spring{})
	if got := s.Suggest("DATABASE_PORT"); len(got.Suggestions) != 0 {
		t.Errorf("PORT must not suggest HOST: %+v", got)
	}
}

func TestSuggestIsDeterministic(t *testing.T) {
	s := New([]string{"APP_LEVEL_A", "APP_LEVEL_B", "APP_LEVEL_C"}, naming.Spring{})
	first := s.Suggest("APP_LEVEL")
	for range 20 {
		if got := s.Suggest("APP_LEVEL"); !slices.Equal(got.Suggestions, first.Suggestions) {
			t.Fatalf("unstable: %v vs %v", got, first)
		}
	}
}
