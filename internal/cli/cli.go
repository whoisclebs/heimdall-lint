// Package cli translates arguments into discovery, resolution, validation and
// rendering. It holds no validation logic of its own.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/whoisclebs/heimdall-lint/internal/batch"
	"github.com/whoisclebs/heimdall-lint/internal/image"
	"github.com/whoisclebs/heimdall-lint/internal/schema"
)

// Environment is everything Run needs from the outside world, so tests can
// substitute it.
type Environment struct {
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	Version        string
}

// OSEnvironment is the real process environment.
func OSEnvironment(version string) Environment {
	return Environment{Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv, Version: version}
}

const usageText = `Heimdall Lint validates environment files against declarative contracts
before your containers start.

Usage:
  heimdall lint [FILE...]            validate files against env.schema.yaml
  heimdall lint --all DIR            validate every environment file in DIR
  heimdall lint                      validate the environment described by ./heimdall.yaml
  heimdall inspect NAME              describe an environment variable
  heimdall diff OLD NEW              compare two schema versions (files or image://REF)
  heimdall schema generate SOURCE    build a schema from Spring Boot metadata
                                     (alias: heimdall spring import SOURCE)
  heimdall version

Run "heimdall <command> -h" for the flags of a command.

Exit codes:
  0  valid          1  invalid environment
  2  usage error    3  invalid schema or manifest
`

// Run executes the CLI and returns the process exit code.
func Run(ctx context.Context, args []string, env Environment) int {
	if len(args) == 0 {
		fmt.Fprint(env.Stderr, usageText)
		return batch.ExitUsage
	}

	command, rest := args[0], args[1:]
	switch command {
	case "lint":
		return lint(ctx, rest, env)
	case "inspect":
		return inspect(ctx, rest, env)
	case "diff":
		return diffCommand(ctx, rest, env)
	case "schema":
		return schemaCommand(rest, env)
	case "spring":
		return springCommand(rest, env)
	case "version", "--version", "-version":
		fmt.Fprintf(env.Stdout, "heimdall %s\n", env.Version)
		return batch.ExitOK
	case "help", "-h", "--help", "-help":
		fmt.Fprint(env.Stdout, usageText)
		return batch.ExitOK
	}
	fmt.Fprintf(env.Stderr, "heimdall: unknown command %q\n\n%s", command, usageText)
	return batch.ExitUsage
}

// usageError is a problem with how the tool was invoked (exit code 2).
type usageError struct{ message string }

func (e usageError) Error() string { return e.message }

func usagef(format string, args ...any) error {
	return usageError{message: fmt.Sprintf(format, args...)}
}

// stringList is a repeatable flag.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// newFlagSet builds a flag set whose parse errors and help go to env.
func newFlagSet(name, synopsis string, env Environment) *flag.FlagSet {
	set := flag.NewFlagSet("heimdall "+name, flag.ContinueOnError)
	set.SetOutput(env.Stderr)
	set.Usage = func() {
		fmt.Fprintf(set.Output(), "Usage: heimdall %s %s\n\nFlags:\n", name, synopsis)
		set.PrintDefaults()
	}
	return set
}

// parseInterspersed parses flags that may appear before, between and after
// positional arguments, which the standard flag package does not allow.
// The second result is false when the caller should exit with the returned code.
func parseInterspersed(set *flag.FlagSet, args []string) (positional []string, exitCode int, ok bool) {
	for {
		if err := set.Parse(args); err != nil {
			if err == flag.ErrHelp {
				return nil, batch.ExitOK, false
			}
			return nil, batch.ExitUsage, false
		}
		args = set.Args()
		if len(args) == 0 {
			return positional, 0, true
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

func failUsage(env Environment, err error) int {
	fmt.Fprintf(env.Stderr, "heimdall: %s\n", err)
	return batch.ExitUsage
}

// schemaLoader loads schemas from files, or from local images for image:// references.
func schemaLoader(ctx context.Context) func(string) (*schema.Schema, error) {
	return image.New().Load(ctx)
}
