package cli

import (
	"context"
	"github.com/whoisclebs/heimdall-lint/internal/batch"
	"github.com/whoisclebs/heimdall-lint/internal/contractdiff"
	"github.com/whoisclebs/heimdall-lint/internal/render"
)

func diffCommand(ctx context.Context, args []string, env Environment) int {
	var format string
	set := newFlagSet("diff", "OLD NEW   (each a schema file or image://REF)", env)
	set.StringVar(&format, "format", "human", "output `format`: human or json")

	positional, code, ok := parseInterspersed(set, args)
	if !ok {
		return code
	}
	if len(positional) != 2 {
		return failUsage(env, usagef("diff takes exactly two schema files: OLD NEW"))
	}
	if format != "human" && format != "json" {
		return failUsage(env, usagef("invalid --format %q (use human or json)", format))
	}

	load := schemaLoader(ctx)
	old, err := load(positional[0])
	if err != nil {
		printSchemaError(env, positional[0], err)
		return batch.ExitContract
	}
	updated, err := load(positional[1])
	if err != nil {
		printSchemaError(env, positional[1], err)
		return batch.ExitContract
	}

	result := contractdiff.Compare(old, updated)
	write := render.Diff
	if format == "json" {
		write = render.DiffJSON
	}
	if err := write(env.Stdout, result); err != nil {
		return batch.ExitInvalid
	}
	return batch.ExitOK
}
