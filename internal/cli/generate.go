package cli

import (
	"fmt"
	"os"

	"github.com/whoisclebs/heimdall-lint/internal/batch"
	"github.com/whoisclebs/heimdall-lint/internal/spring"
)

// schemaCommand handles `heimdall schema generate SOURCE`.
func schemaCommand(args []string, env Environment) int {
	if len(args) == 0 || args[0] != "generate" {
		return failUsage(env, usagef("usage: heimdall schema generate SOURCE (a .jar, a directory, or spring-configuration-metadata.json)"))
	}
	return generate("schema generate", args[1:], env)
}

// springCommand handles `heimdall spring import SOURCE`, an alias of schema generate.
func springCommand(args []string, env Environment) int {
	if len(args) == 0 || args[0] != "import" {
		return failUsage(env, usagef("usage: heimdall spring import SOURCE (a .jar, a directory, or spring-configuration-metadata.json)"))
	}
	return generate("spring import", args[1:], env)
}

func generate(name string, args []string, env Environment) int {
	var output string
	var force bool
	var prefixes stringList
	set := newFlagSet(name, "SOURCE", env)
	set.Var(&prefixes, "prefix", "only import properties starting with this `prefix`, such as api. (repeatable)")
	set.StringVar(&output, "output", "", "write the schema to this `file` instead of stdout")
	set.BoolVar(&force, "force", false, "with --output, overwrite an existing file")

	positional, code, ok := parseInterspersed(set, args)
	if !ok {
		return code
	}
	if len(positional) != 1 {
		return failUsage(env, usagef("%s takes exactly one SOURCE", name))
	}
	if output != "" && !force {
		if _, err := os.Stat(output); err == nil {
			return failUsage(env, usagef("%s already exists; use --force to overwrite it", output))
		}
	}

	metadata, err := spring.Load(positional[0])
	if err != nil {
		fmt.Fprintf(env.Stderr, "heimdall: %s\n", err)
		return batch.ExitInvalid
	}
	document, warnings, err := spring.Generate(metadata, spring.Options{Prefixes: prefixes})
	for _, warning := range warnings {
		fmt.Fprintf(env.Stderr, "heimdall: warning: %s\n", warning)
	}
	if err != nil {
		fmt.Fprintf(env.Stderr, "heimdall: %s\n", err)
		return batch.ExitInvalid
	}

	if output == "" {
		_, err = env.Stdout.Write(document)
	} else {
		err = os.WriteFile(output, document, 0o644)
	}
	if err != nil {
		fmt.Fprintf(env.Stderr, "heimdall: cannot write schema: %s\n", err)
		return batch.ExitInvalid
	}
	if output != "" {
		fmt.Fprintf(env.Stderr, "heimdall: wrote %s\n", output)
	}
	return batch.ExitOK
}
