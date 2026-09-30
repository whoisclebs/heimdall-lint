package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/whoisclebs/heimdall-lint/internal/batch"
	"github.com/whoisclebs/heimdall-lint/internal/image"
	"github.com/whoisclebs/heimdall-lint/internal/manifest"
	"github.com/whoisclebs/heimdall-lint/internal/naming"
	"github.com/whoisclebs/heimdall-lint/internal/render"
	"github.com/whoisclebs/heimdall-lint/internal/resolve"
	"github.com/whoisclebs/heimdall-lint/internal/schema"
	"github.com/whoisclebs/heimdall-lint/internal/suggest"
)

// schemaSource is a loadable schema with the label users know it by.
type schemaSource struct {
	label string
	path  string
}

func inspect(ctx context.Context, args []string, env Environment) int {
	var schemaPath, schemaDir, manifestPath, application, imageName string
	set := newFlagSet("inspect", "NAME", env)
	set.StringVar(&schemaPath, "schema", "", "schema `file`")
	set.StringVar(&imageName, "image", "", "read the schema from a local Docker `image`")
	set.StringVar(&schemaDir, "schema-dir", "", "`directory` of <name>.schema.yaml files")
	set.StringVar(&manifestPath, "manifest", "", "manifest `file` (default: ./heimdall.yaml when it exists)")
	set.StringVar(&application, "application", "", "only look in this `application` (or schema name)")

	positional, code, ok := parseInterspersed(set, args)
	if !ok {
		return code
	}
	if len(positional) != 1 {
		return failUsage(env, usagef("inspect takes exactly one variable name"))
	}
	name := positional[0]
	if imageName != "" {
		if schemaPath != "" || schemaDir != "" || manifestPath != "" {
			return failUsage(env, usagef("--image cannot be combined with --schema, --schema-dir or --manifest"))
		}
		schemaPath = image.Reference(imageName)
	}

	sources, err := inspectSources(schemaPath, schemaDir, manifestPath)
	if err != nil {
		return failUsage(env, err)
	}
	if application != "" {
		sources = slices.DeleteFunc(sources, func(source schemaSource) bool { return source.label != application })
		if len(sources) == 0 {
			return failUsage(env, usagef("no application or schema named %q", application))
		}
	}

	var entries []render.InspectEntry
	var declared []string
	for _, source := range sources {
		contract, err := schemaLoader(ctx)(source.path)
		if err != nil {
			printSchemaError(env, source.path, err)
			return batch.ExitContract
		}
		declared = append(declared, contract.Names()...)
		if variable, found := contract.Variables[name]; found {
			entries = append(entries, render.InspectEntry{Source: source.label + ": " + source.path, Variable: variable})
		}
	}

	if len(entries) == 0 {
		fmt.Fprintf(env.Stderr, "heimdall: %s is not declared in any schema\n", name)
		if result := suggest.New(declared, naming.Spring{}).Suggest(name); len(result.Suggestions) > 0 {
			fmt.Fprintf(env.Stderr, "\nDid you mean:\n  %s\n", strings.Join(result.Suggestions, "\n  "))
		}
		return batch.ExitInvalid
	}
	if err := render.Inspect(env.Stdout, name, entries); err != nil {
		return batch.ExitInvalid
	}
	return batch.ExitOK
}

func inspectSources(schemaPath, schemaDir, manifestPath string) ([]schemaSource, error) {
	switch {
	case schemaPath != "":
		return []schemaSource{{label: schemaPath, path: schemaPath}}, nil
	case schemaDir != "":
		return schemaDirSources(schemaDir)
	case manifestPath != "":
		return manifestSources(manifestPath)
	case fileExists(manifest.DefaultFileName):
		return manifestSources(manifest.DefaultFileName)
	case fileExists(defaultSchemaFile):
		return []schemaSource{{label: defaultSchemaFile, path: defaultSchemaFile}}, nil
	}
	return nil, usagef("no schema: use --schema, --schema-dir or --manifest, or run where %s or %s exists", manifest.DefaultFileName, defaultSchemaFile)
}

func schemaDirSources(dir string) ([]schemaSource, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*"+resolve.SchemaSuffix))
	if err != nil || len(paths) == 0 {
		return nil, usagef("no %s files in %s", "*"+resolve.SchemaSuffix, dir)
	}
	slices.Sort(paths)
	sources := make([]schemaSource, len(paths))
	for i, path := range paths {
		sources[i] = schemaSource{label: strings.TrimSuffix(filepath.Base(path), resolve.SchemaSuffix), path: path}
	}
	return sources, nil
}

func manifestSources(path string) ([]schemaSource, error) {
	loaded, err := manifest.Load(path)
	var invalid *manifest.Error
	if errors.As(err, &invalid) {
		return nil, usagef("%s: %s", path, strings.Join(invalid.Problems, "; "))
	}
	sources := make([]schemaSource, len(loaded.Applications))
	for i, app := range loaded.Applications {
		sources[i] = schemaSource{label: app.Name, path: app.SchemaPath}
	}
	return sources, nil
}

// printSchemaError explains why a schema cannot be used.
func printSchemaError(env Environment, path string, err error) {
	var invalid *schema.Error
	var missing *schema.NotFoundError
	switch {
	case errors.As(err, &missing):
		fmt.Fprintf(env.Stderr, "heimdall: schema not found: %s\n", path)
	case errors.As(err, &invalid):
		fmt.Fprintf(env.Stderr, "heimdall: invalid schema %s\n", path)
		for _, problem := range invalid.Problems {
			if problem.Variable != "" {
				fmt.Fprintf(env.Stderr, "  %s: %s\n", problem.Variable, problem.Message)
			} else {
				fmt.Fprintf(env.Stderr, "  %s\n", problem.Message)
			}
		}
	default:
		fmt.Fprintf(env.Stderr, "heimdall: %s: %s\n", path, err)
	}
}
