package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/whoisclebs/heimdall-lint/internal/compose"
	"github.com/whoisclebs/heimdall-lint/internal/diag"
	"github.com/whoisclebs/heimdall-lint/internal/discovery"
	"github.com/whoisclebs/heimdall-lint/internal/manifest"
	"github.com/whoisclebs/heimdall-lint/internal/resolve"
)

const defaultSchemaFile = "env.schema.yaml"

// lintInput is the raw, already-parsed command line of `heimdall lint`.
type lintInput struct {
	positional     []string
	envFiles       []string
	schema         string
	schemaDir      string
	manifestPath   string
	composePath    string
	all            bool
	recursive      bool
	globs          []string
	excludes       []string
	followSymlinks bool
}

func (in lintInput) discoveryOptions() discovery.Options {
	return discovery.Options{Recursive: in.recursive, Globs: in.globs, Excludes: in.excludes, FollowSymlinks: in.followSymlinks}
}

func (in lintInput) usesDiscoveryFlags() bool {
	return in.recursive || len(in.globs) > 0 || len(in.excludes) > 0 || in.followSymlinks
}

// lintPlan is what discovery and resolution decided, before any validation.
type lintPlan struct {
	targets  []discovery.Target
	resolver resolve.Resolver
	// diagnostics gathered while planning (manifest problems, empty directories, orphans).
	diagnostics []diag.Diagnostic
}

// planLint runs discovery and schema resolution. It returns a usageError for
// invalid invocations; problems with the inputs themselves become diagnostics.
func planLint(in lintInput) (lintPlan, error) {
	if err := in.discoveryOptions().ValidatePatterns(); err != nil {
		return lintPlan{}, usageError{message: err.Error()}
	}
	if in.schema != "" && in.schemaDir != "" {
		return lintPlan{}, usagef("--schema and --schema-dir cannot be used together")
	}

	if compose := in.composeFile(); compose != "" {
		return planFromCompose(in, compose)
	}
	if in.manifestPath != "" || in.shouldUseDefaultManifest() {
		return planFromManifest(in)
	}
	if in.usesDiscoveryFlags() && !in.all {
		return lintPlan{}, usagef("--recursive, --glob, --exclude and --follow-symlinks require --all")
	}

	plan := lintPlan{}
	switch {
	case in.all:
		if len(in.envFiles) > 0 {
			return lintPlan{}, usagef("--env cannot be combined with --all")
		}
		roots := in.positional
		if len(roots) == 0 {
			roots = []string{"."}
		}
		plan.targets, plan.diagnostics = discovery.Directories(roots, in.discoveryOptions())
	default:
		files := append(append([]string{}, in.envFiles...), in.positional...)
		if len(files) == 0 {
			return lintPlan{}, usagef("nothing to validate: pass an environment file, use --all DIR, or run in a directory with %s", manifest.DefaultFileName)
		}
		plan.targets = discovery.Files(files)
	}

	resolver, err := in.schemaResolver()
	if err != nil {
		return lintPlan{}, err
	}
	plan.resolver = resolver
	return plan, nil
}

// shouldUseDefaultManifest is true for a bare `heimdall lint` next to a heimdall.yaml.
func (in lintInput) shouldUseDefaultManifest() bool {
	bare := len(in.positional) == 0 && len(in.envFiles) == 0 && !in.all && in.schema == "" && in.schemaDir == "" && !in.usesDiscoveryFlags()
	return bare && fileExists(manifest.DefaultFileName)
}

func (in lintInput) schemaResolver() (resolve.Resolver, error) {
	switch {
	case in.schema != "":
		return resolve.Fixed(in.schema), nil
	case in.schemaDir != "":
		return resolve.Convention{Dir: in.schemaDir}, nil
	case fileExists(defaultSchemaFile):
		return resolve.Fixed(defaultSchemaFile), nil
	}
	return nil, usagef("no schema: use --schema FILE, --schema-dir DIR, a %s manifest, or create ./%s", manifest.DefaultFileName, defaultSchemaFile)
}

func planFromManifest(in lintInput) (lintPlan, error) {
	conflicting := len(in.positional) > 0 || len(in.envFiles) > 0 || in.all || in.schema != "" || in.schemaDir != "" || in.usesDiscoveryFlags()
	if conflicting {
		return lintPlan{}, usagef("a manifest already describes the environment; it cannot be combined with files, --all, --schema, --schema-dir or discovery flags")
	}

	path := in.manifestPath
	if path == "" {
		path = manifest.DefaultFileName
	}
	loaded, err := manifest.Load(path)
	var invalid *manifest.Error
	if errors.As(err, &invalid) {
		return lintPlan{diagnostics: manifestDiagnostics(invalid)}, nil
	}

	targets, diagnostics := discovery.FromManifest(loaded)
	schemas := resolve.ByApplication{}
	for _, app := range loaded.Applications {
		schemas[app.Name] = app.SchemaPath
	}
	return lintPlan{targets: targets, resolver: schemas, diagnostics: diagnostics}, nil
}

func manifestDiagnostics(err *manifest.Error) []diag.Diagnostic {
	diagnostics := make([]diag.Diagnostic, len(err.Problems))
	for i, problem := range err.Problems {
		diagnostics[i] = diag.Diagnostic{Severity: diag.SeverityError, Code: diag.InvalidManifest, File: err.Path, Message: problem + "."}
	}
	return diagnostics
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// composeFile returns the compose file to use: --compose, or a single
// positional argument that is named like one.
func (in lintInput) composeFile() string {
	if in.composePath != "" {
		return in.composePath
	}
	if !in.all && len(in.envFiles) == 0 && len(in.positional) == 1 && compose.IsComposeFile(in.positional[0]) {
		return in.positional[0]
	}
	return ""
}

// planFromCompose validates each service's effective environment. Services that
// declare configuration but have no schema are errors, unless they opt out with
// x-heimdall.skip; services with no configuration at all are ignored.
func planFromCompose(in lintInput, path string) (lintPlan, error) {
	positionalFiles := len(in.positional) > 0 && in.composePath != ""
	if positionalFiles || len(in.envFiles) > 0 || in.all || in.manifestPath != "" || in.usesDiscoveryFlags() {
		return lintPlan{}, usagef("a compose file describes the environment by itself; it cannot be combined with other files, --all, --manifest or discovery flags")
	}
	fallback, err := in.composeFallbackResolver()
	if err != nil {
		return lintPlan{}, err
	}

	project, err := compose.Load(path, os.LookupEnv)
	var invalid *compose.Error
	if errors.As(err, &invalid) {
		return lintPlan{diagnostics: composeDiagnostics(invalid)}, nil
	}

	plan := lintPlan{}
	if project.HasInclude {
		plan.diagnostics = append(plan.diagnostics, unsupported(project.Path, "", "include is not interpreted: services defined in included files are not validated."))
	}
	overrides := map[string]string{}
	for _, service := range project.Services {
		if service.Extends && !service.Skip {
			plan.diagnostics = append(plan.diagnostics, unsupported(project.Path, service.Name,
				fmt.Sprintf("service %q uses extends, which is not interpreted: environment inherited through it is not validated.", service.Name)))
		}
		if service.Skip || !service.HasEnvironment() {
			continue
		}
		environment, diagnostics := compose.BuildEnvironment(service)
		plan.diagnostics = append(plan.diagnostics, diagnostics...)
		plan.targets = append(plan.targets, discovery.Target{
			Path: fmt.Sprintf("%s [service %s]", project.Path, service.Name), Application: service.Name, Env: &environment,
		})
		if service.SchemaPath != "" {
			overrides[service.Name] = service.SchemaPath
		}
	}
	plan.resolver = resolve.Overrides{ByApplication: overrides, Default: fallback}
	return plan, nil
}

// composeFallbackResolver is used for services without x-heimdall.schema. Unlike
// files, having no schema source at all is not a usage error here: each
// service is then reported individually.
func (in lintInput) composeFallbackResolver() (resolve.Resolver, error) {
	switch {
	case in.schema != "":
		return resolve.Fixed(in.schema), nil
	case in.schemaDir != "":
		return resolve.Convention{Dir: in.schemaDir}, nil
	}
	return nil, nil
}

func composeDiagnostics(err *compose.Error) []diag.Diagnostic {
	diagnostics := make([]diag.Diagnostic, len(err.Problems))
	for i, problem := range err.Problems {
		diagnostics[i] = diag.Diagnostic{Severity: diag.SeverityError, Code: diag.InvalidManifest, File: err.Path, Message: problem + "."}
	}
	return diagnostics
}

func unsupported(file, application, message string) diag.Diagnostic {
	return diag.Diagnostic{Severity: diag.SeverityWarning, Code: diag.UnsupportedConstruct, File: file, Application: application, Message: message}
}
