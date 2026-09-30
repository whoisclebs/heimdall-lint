package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/whoisclebs/heimdall-lint/internal/batch"
	"github.com/whoisclebs/heimdall-lint/internal/image"
	"github.com/whoisclebs/heimdall-lint/internal/naming"
	"github.com/whoisclebs/heimdall-lint/internal/render"
	"github.com/whoisclebs/heimdall-lint/internal/resolve"
	"github.com/whoisclebs/heimdall-lint/internal/tui"
	"github.com/whoisclebs/heimdall-lint/internal/validate"
)

type lintFlags struct {
	lintInput
	jobs             int
	format           string
	color            string
	quiet            bool
	warningsAsErrors bool
	allowUnknown     bool
	interactive      bool
	image            string
}

func lint(ctx context.Context, args []string, env Environment) int {
	var flags lintFlags
	set := newFlagSet("lint", "[FILE...] | --all [DIR...]", env)
	var envFiles, globs, excludes stringList
	set.Var(&envFiles, "env", "environment `file` to validate (repeatable)")
	set.StringVar(&flags.schema, "schema", "", "schema `file` applied to every environment file")
	set.StringVar(&flags.image, "image", "", "read the schema from a local Docker `image` (its /env.schema.yaml); nothing is pulled or run")
	set.StringVar(&flags.schemaDir, "schema-dir", "", "`directory` of <name>.schema.yaml files, matched to <name>.env")
	set.StringVar(&flags.manifestPath, "manifest", "", "manifest `file` (default: ./heimdall.yaml when it exists)")
	set.StringVar(&flags.composePath, "compose", "", "Docker Compose `file`: validate each service's env_file and environment")
	set.BoolVar(&flags.all, "all", false, "treat arguments as directories and validate every environment file in them")
	set.BoolVar(&flags.recursive, "recursive", false, "with --all, descend into subdirectories")
	set.Var(&globs, "glob", "with --all, only files matching this `pattern` (default: *.env, .env, .env.*)")
	set.Var(&excludes, "exclude", "with --all, skip files matching this `pattern` (repeatable)")
	set.BoolVar(&flags.followSymlinks, "follow-symlinks", false, "with --all, follow symbolic links (off by default)")
	set.IntVar(&flags.jobs, "jobs", 0, "number of parallel validations (default: number of CPUs)")
	set.StringVar(&flags.format, "format", "human", "output `format`: human or json")
	set.StringVar(&flags.color, "color", "auto", "color output: auto, always or never")
	set.BoolVar(&flags.quiet, "quiet", false, "print only problems and the summary")
	set.BoolVar(&flags.warningsAsErrors, "warnings-as-errors", false, "exit 1 when there are warnings")
	set.BoolVar(&flags.allowUnknown, "allow-unknown", false, "do not report variables missing from the schema")
	set.BoolVar(&flags.interactive, "tui", false, "browse the results in an interactive terminal UI")

	positional, code, ok := parseInterspersed(set, args)
	if !ok {
		return code
	}
	flags.positional, flags.envFiles, flags.globs, flags.excludes = positional, envFiles, globs, excludes

	if err := flags.validate(env); err != nil {
		return failUsage(env, err)
	}
	if flags.image != "" {
		flags.schema = image.Reference(flags.image)
	}
	plan, err := planLint(flags.lintInput)
	if err != nil {
		return failUsage(env, err)
	}

	jobs := planned(plan)
	report, err := batch.Run(ctx, jobs, plan.diagnostics, batch.Options{
		Workers:  flags.jobs,
		Validate: validate.Options{AllowUnknown: flags.allowUnknown},
		Naming:   naming.Spring{},

		LoadSchema: schemaLoader(ctx),
	})
	if err != nil {
		fmt.Fprintf(env.Stderr, "heimdall: interrupted: %s\n", err)
		return batch.ExitInvalid
	}

	if err := flags.present(env, report); err != nil {
		fmt.Fprintf(env.Stderr, "heimdall: cannot write output: %s\n", err)
		return batch.ExitInvalid
	}
	return report.ExitCode(flags.warningsAsErrors)
}

// planned pairs targets with schemas. Manifest failures leave no resolver and no targets.
func planned(plan lintPlan) []resolve.Resolved {
	if plan.resolver == nil {
		return nil
	}
	return resolve.All(plan.targets, plan.resolver)
}

func (f lintFlags) validate(env Environment) error {
	switch f.format {
	case "human", "json":
	default:
		return usagef("invalid --format %q (use human or json)", f.format)
	}
	switch f.color {
	case "auto", "always", "never":
	default:
		return usagef("invalid --color %q (use auto, always or never)", f.color)
	}
	if f.image != "" && (f.schema != "" || f.schemaDir != "" || f.manifestPath != "") {
		return usagef("--image cannot be combined with --schema, --schema-dir or --manifest")
	}
	if f.jobs < 0 {
		return usagef("--jobs must be zero or greater")
	}
	if f.interactive && f.format != "human" {
		return usagef("--tui cannot be combined with --format json")
	}
	if f.interactive && !isTerminal(env.Stdout) {
		return usagef("--tui needs an interactive terminal; drop it for plain output")
	}
	return nil
}

func (f lintFlags) present(env Environment, report batch.Report) error {
	switch {
	case f.format == "json":
		return render.JSON(env.Stdout, report, f.warningsAsErrors)
	case f.interactive:
		return tui.Run(report, f.warningsAsErrors)
	}
	return render.Human(env.Stdout, report, render.Options{
		Quiet: f.quiet, Color: f.colorEnabled(env), WarningsAsErrors: f.warningsAsErrors,
	})
}

// colorEnabled honors --color, NO_COLOR (https://no-color.org) and non-TTY output.
func (f lintFlags) colorEnabled(env Environment) bool {
	switch f.color {
	case "always":
		return true
	case "never":
		return false
	}
	return env.Getenv("NO_COLOR") == "" && env.Getenv("TERM") != "dumb" && isTerminal(env.Stdout)
}

func isTerminal(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
