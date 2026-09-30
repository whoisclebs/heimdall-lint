package naming

import (
	"slices"
	"testing"
)

func TestSpring(t *testing.T) {
	var s Spring
	tests := []struct {
		in       string
		want     []string
		notation bool
	}{
		{"api.timeout", []string{"API_TIMEOUT"}, true},
		{"api-timeout", []string{"API_TIMEOUT", "APITIMEOUT"}, true},
		{"api_timeout", []string{"API_TIMEOUT"}, false},
		{"API_TIMEOUT", []string{"API_TIMEOUT"}, false},
	}
	for _, tt := range tests {
		if got := s.EnvironmentNames(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("EnvironmentNames(%q) = %v, want %v", tt.in, got, tt.want)
		}
		if got := s.IsPropertyNotation(tt.in); got != tt.notation {
			t.Errorf("IsPropertyNotation(%q) = %v", tt.in, got)
		}
	}
	if s.Normalize("Api.Time-out") != "API_TIME_OUT" {
		t.Errorf("Normalize = %q", s.Normalize("Api.Time-out"))
	}
}
