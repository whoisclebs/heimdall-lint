// Package batch validates many environment files against their schemas.
//
// Only the independent per-file work runs concurrently. Schemas are loaded
// once, before the pool starts, and are read-only afterwards; each worker
// writes only to its own slot of the result slice, so no locks are needed.
package batch

import (
	"cmp"
	"context"
	"errors"
	"os"
	"runtime"
	"slices"
	"sync"

	"github.com/whoisclebs/heimdall-lint/internal/diag"
	"github.com/whoisclebs/heimdall-lint/internal/envfile"
	"github.com/whoisclebs/heimdall-lint/internal/fsutil"
	"github.com/whoisclebs/heimdall-lint/internal/naming"
	"github.com/whoisclebs/heimdall-lint/internal/resolve"
	"github.com/whoisclebs/heimdall-lint/internal/schema"
	"github.com/whoisclebs/heimdall-lint/internal/validate"
)

type Options struct {
	// Workers <= 0 means min(NumCPU, number of files).
	Workers  int
	Validate validate.Options
	Naming   naming.Strategy
	// LoadSchema resolves a schema path; nil means schema.Load. It lets the
	// caller serve schemas from places other than files (an image).
	LoadSchema func(path string) (*schema.Schema, error)
}

type FileResult struct {
	Path             string
	Application      string
	SchemaPath       string
	VariablesChecked int
	Diagnostics      []diag.Diagnostic
}

func (r FileResult) Errors() int   { return diag.Count(r.Diagnostics, diag.SeverityError) }
func (r FileResult) Warnings() int { return diag.Count(r.Diagnostics, diag.SeverityWarning) }

// Report is the complete outcome of a run. General holds problems that belong
// to the contract or the environment as a whole rather than to one file.
type Report struct {
	Files   []FileResult
	General []diag.Diagnostic
}

type Summary struct {
	Files, ValidFiles, InvalidFiles, WarningFiles int
	VariablesChecked, Errors, Warnings            int
}

func (r Report) Summary() Summary {
	summary := Summary{
		Files:    len(r.Files),
		Errors:   diag.Count(r.General, diag.SeverityError),
		Warnings: diag.Count(r.General, diag.SeverityWarning),
	}
	for _, file := range r.Files {
		summary.VariablesChecked += file.VariablesChecked
		summary.Errors += file.Errors()
		summary.Warnings += file.Warnings()
		if file.Errors() == 0 {
			summary.ValidFiles++
		} else {
			summary.InvalidFiles++
		}
		if file.Warnings() > 0 {
			summary.WarningFiles++
		}
	}
	return summary
}

// Exit codes.
const (
	ExitOK       = 0
	ExitInvalid  = 1
	ExitUsage    = 2
	ExitContract = 3
)

// ExitCode maps the report to the process exit code. A broken schema or
// manifest wins over invalid environments: results built on a broken contract
// cannot be trusted.
func (r Report) ExitCode(warningsAsErrors bool) int {
	all := slices.Clone(r.General)
	for _, file := range r.Files {
		all = append(all, file.Diagnostics...)
	}
	for _, d := range all {
		if d.Code.IsContractFailure() {
			return ExitContract
		}
	}
	summary := r.Summary()
	if summary.Errors > 0 || (warningsAsErrors && summary.Warnings > 0) {
		return ExitInvalid
	}
	return ExitOK
}

// Passed reports whether the environment may start.
func (r Report) Passed(warningsAsErrors bool) bool { return r.ExitCode(warningsAsErrors) == ExitOK }

// Run validates every job. extra are diagnostics gathered earlier (discovery,
// manifest) that are merged into the report. It returns ctx.Err() if cancelled.
func Run(ctx context.Context, jobs []resolve.Resolved, extra []diag.Diagnostic, options Options) (Report, error) {
	if options.Naming == nil {
		options.Naming = naming.Spring{}
	}
	if options.LoadSchema == nil {
		options.LoadSchema = schema.Load
	}

	schemas, schemaDiagnostics := loadSchemas(jobs, options)
	results := make([]FileResult, len(jobs))
	work(ctx, len(jobs), workerCount(options.Workers, len(jobs)), func(index int) {
		results[index] = validateJob(jobs[index], schemas[jobs[index].SchemaPath])
	})
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}

	slices.SortFunc(results, func(a, b FileResult) int {
		return cmp.Or(cmp.Compare(a.Application, b.Application), cmp.Compare(a.Path, b.Path))
	})
	general := slices.Concat(extra, schemaDiagnostics)
	diag.Sort(general)
	return Report{Files: results, General: general}, nil
}

func workerCount(requested, jobs int) int {
	if requested <= 0 {
		requested = runtime.NumCPU()
	}
	return max(1, min(requested, jobs))
}

// work calls do(0..count-1) on a bounded pool and returns when all are done or
// ctx is cancelled.
func work(ctx context.Context, count, workers int, do func(index int)) {
	indexes := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range indexes {
				do(index)
			}
		}()
	}
feed:
	for index := range count {
		select {
		case indexes <- index:
		case <-ctx.Done():
			break feed
		}
	}
	close(indexes)
	wg.Wait()
}

type loadedSchema struct {
	validator *validate.Validator
	// failure is set when the schema cannot be used.
	failure *diag.Diagnostic
}

// loadSchemas loads each distinct schema once. Problems that concern the
// schema itself are returned once, not once per environment file.
func loadSchemas(jobs []resolve.Resolved, options Options) (map[string]loadedSchema, []diag.Diagnostic) {
	paths := map[string]bool{}
	for _, job := range jobs {
		if job.Err == nil {
			paths[job.SchemaPath] = true
		}
	}

	loaded := make(map[string]loadedSchema, len(paths))
	var general []diag.Diagnostic
	for _, path := range slices.Sorted(func(yield func(string) bool) {
		for p := range paths {
			if !yield(p) {
				return
			}
		}
	}) {
		contract, err := options.LoadSchema(path)
		var invalid *schema.Error
		var missing *schema.NotFoundError
		switch {
		case err == nil:
			loaded[path] = loadedSchema{validator: validate.New(contract, options.Naming, options.Validate)}
		case errors.As(err, &missing):
			loaded[path] = loadedSchema{failure: &diag.Diagnostic{
				Severity: diag.SeverityError, Code: diag.SchemaNotFound,
				Message: "Schema not found.", Expected: path,
			}}
		case errors.As(err, &invalid):
			loaded[path] = loadedSchema{failure: &diag.Diagnostic{
				Severity: diag.SeverityError, Code: diag.InvalidSchema,
				Message: "Environment file was not validated because its schema is invalid.", Expected: path,
			}}
			for _, problem := range invalid.Problems {
				general = append(general, diag.Diagnostic{
					Severity: diag.SeverityError, Code: diag.InvalidSchema, File: path,
					Variable: problem.Variable, Message: problem.Message,
				})
			}
		}
	}
	return loaded, general
}

func validateJob(job resolve.Resolved, schemaEntry loadedSchema) FileResult {
	result := FileResult{Path: job.Path, Application: job.Application, SchemaPath: job.SchemaPath}
	fail := func(d diag.Diagnostic) FileResult {
		d.File, d.Application = job.Path, job.Application
		result.Diagnostics = []diag.Diagnostic{d}
		return result
	}

	if job.Err != nil {
		return fail(diag.Diagnostic{
			Severity: diag.SeverityError, Code: diag.SchemaNotFound,
			Message: "Cannot determine the schema for this file: " + job.Err.Error() + ".",
		})
	}
	if schemaEntry.failure != nil {
		return fail(*schemaEntry.failure)
	}

	if job.Env != nil {
		validated := schemaEntry.validator.Validate(job.Path, job.Application, *job.Env)
		result.Diagnostics, result.VariablesChecked = validated.Diagnostics, validated.VariablesChecked
		return result
	}

	data, err := fsutil.ReadFile(job.Path, fsutil.MaxEnvBytes)
	if err != nil {
		message := "Cannot read environment file: " + err.Error() + "."
		if errors.Is(err, os.ErrNotExist) {
			message = "Environment file not found."
		}
		return fail(diag.Diagnostic{Severity: diag.SeverityError, Code: diag.EnvFileUnreadable, Message: message})
	}

	validated := schemaEntry.validator.Validate(job.Path, job.Application, envfile.Parse(data))
	result.Diagnostics, result.VariablesChecked = validated.Diagnostics, validated.VariablesChecked
	return result
}
