// Package naming isolates framework-specific naming conventions so the core
// never learns about Spring (or any other framework).
package naming

import "strings"

// Strategy maps between property-style names and environment variable names.
type Strategy interface {
	// Normalize returns the canonical form used to compare names.
	Normalize(name string) string
	// EnvironmentNames returns every environment variable name the given
	// name may refer to, most likely first. A plain environment name returns
	// itself.
	EnvironmentNames(name string) []string
	// IsPropertyNotation reports whether name is written as a framework
	// property (for example "api.timeout") rather than an environment name.
	IsPropertyNotation(name string) bool
}

// Spring implements Spring Boot relaxed binding for environment variables:
// dots become underscores, dashes are removed (or, leniently, become
// underscores) and the result is upper-cased.
type Spring struct{}

func (Spring) Normalize(name string) string {
	replacer := strings.NewReplacer(".", "_", "-", "_")
	return strings.ToUpper(replacer.Replace(name))
}

func (Spring) IsPropertyNotation(name string) bool {
	return strings.ContainsAny(name, ".-")
}

func (Spring) EnvironmentNames(name string) []string {
	underscored := strings.ToUpper(strings.NewReplacer(".", "_", "-", "_").Replace(name))
	dashesRemoved := strings.ToUpper(strings.NewReplacer(".", "_", "-", "").Replace(name))
	if underscored == dashesRemoved {
		return []string{underscored}
	}
	return []string{underscored, dashesRemoved}
}
