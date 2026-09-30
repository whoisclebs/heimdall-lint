// Package discovery finds environment files. It knows nothing about schemas.
package discovery

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/whoisclebs/heimdall-lint/internal/diag"
	"github.com/whoisclebs/heimdall-lint/internal/envfile"
	"github.com/whoisclebs/heimdall-lint/internal/manifest"
)

// Target is an environment to validate: usually a file, or an environment
// already assembled elsewhere (a Compose service), in which case Path is only
// a label and Env holds the content.
type Target struct {
	Path        string
	Application string
	Env         *envfile.File
}

type Options struct {
	Recursive bool
	// Globs replaces DefaultIncludes when set.
	Globs          []string
	Excludes       []string
	FollowSymlinks bool
}

// DefaultIncludes are the file names treated as environment files.
var DefaultIncludes = []string{"*.env", ".env", ".env.*"}

// DefaultExcludes are files that are clearly auxiliary. They always apply
// during directory discovery; name a file explicitly to validate it anyway.
var DefaultExcludes = []string{
	".env.example", ".env.sample", ".env.template", ".env.backup", ".env.bak", ".env.orig",
	"*.example.env", "*.sample.env", "*.template.env", "*.backup.env", "*.bak.env",
	"*.env.example", "*.env.sample", "*.env.template", "*.env.backup", "*.env.bak", "*.env.orig",
	"*.bak", "*.orig", "*.swp", "*~",
}

// ValidatePatterns rejects malformed glob patterns up front.
func (o Options) ValidatePatterns() error {
	for _, pattern := range slices.Concat(o.Globs, o.Excludes) {
		if _, err := path.Match(pattern, ""); err != nil {
			return fmt.Errorf("invalid pattern %q", pattern)
		}
	}
	return nil
}

// ApplicationName derives the application from a file name:
// reports-api.env -> reports-api, .env.staging -> staging, and a bare
// .env -> the name of its directory.
func ApplicationName(envPath string) string {
	base := filepath.Base(envPath)
	switch {
	case base == ".env":
		return filepath.Base(filepath.Dir(absolute(envPath)))
	case strings.HasPrefix(base, ".env."):
		return strings.TrimPrefix(base, ".env.")
	}
	return strings.TrimSuffix(base, ".env")
}

func absolute(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// Files turns explicitly named files into targets, dropping repeats.
func Files(paths []string) []Target {
	var targets []Target
	seen := map[string]bool{}
	for _, p := range paths {
		clean := filepath.Clean(p)
		if seen[clean] {
			continue
		}
		seen[clean] = true
		targets = append(targets, Target{Path: clean, Application: ApplicationName(clean)})
	}
	return targets
}

// Directories walks roots and returns the matching environment files sorted by
// path, plus diagnostics for roots that cannot be searched.
func Directories(roots []string, options Options) ([]Target, []diag.Diagnostic) {
	includes := options.Globs
	if len(includes) == 0 {
		includes = DefaultIncludes
	}
	walker := walker{options: options, includes: includes, excludes: slices.Concat(DefaultExcludes, options.Excludes), visited: map[string]bool{}}

	var diagnostics []diag.Diagnostic
	for _, root := range roots {
		info, err := os.Stat(root)
		switch {
		case err != nil:
			diagnostics = append(diagnostics, problem(root, diag.EnvFileUnreadable, "Cannot read directory."))
			continue
		case !info.IsDir():
			diagnostics = append(diagnostics, problem(root, diag.EnvFileUnreadable, "Not a directory. Pass files without --all."))
			continue
		}
		before := len(walker.found)
		walker.walk(root, root, true)
		if len(walker.found) == before {
			diagnostics = append(diagnostics, problem(root, diag.NoEnvFiles, "No environment files found."))
		}
	}

	slices.Sort(walker.found)
	walker.found = slices.Compact(walker.found)
	targets := make([]Target, len(walker.found))
	for i, found := range walker.found {
		targets[i] = Target{Path: found, Application: ApplicationName(found)}
	}
	return targets, diagnostics
}

func problem(file string, code diag.Code, message string) diag.Diagnostic {
	return diag.Diagnostic{Severity: diag.SeverityError, Code: code, File: file, Message: message}
}

type walker struct {
	options  Options
	includes []string
	excludes []string
	visited  map[string]bool // real directory paths, to break symlink cycles
	found    []string
}

func (w *walker) walk(root, dir string, isRoot bool) {
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		if w.visited[real] {
			return
		}
		w.visited[real] = true
	}

	entries, err := os.ReadDir(dir) // sorted by name: deterministic
	if err != nil {
		return
	}
	for _, entry := range entries {
		full := filepath.Join(dir, entry.Name())
		info, ok := w.resolve(full, entry)
		if !ok {
			continue
		}
		switch {
		case info.IsDir():
			if w.options.Recursive && !strings.HasPrefix(entry.Name(), ".") {
				w.walk(root, full, false)
			}
		case info.Mode().IsRegular() && w.selected(root, full):
			w.found = append(w.found, full)
		}
	}
}

// resolve returns the entry's info, skipping symlinks unless opted in.
func (w *walker) resolve(full string, entry os.DirEntry) (os.FileInfo, bool) {
	if entry.Type()&os.ModeSymlink != 0 {
		if !w.options.FollowSymlinks {
			return nil, false
		}
		info, err := os.Stat(full)
		return info, err == nil
	}
	info, err := entry.Info()
	return info, err == nil
}

func (w *walker) selected(root, full string) bool {
	relative, err := filepath.Rel(root, full)
	if err != nil {
		relative = full
	}
	relative = filepath.ToSlash(relative)
	base := path.Base(relative)
	return matchesAny(w.includes, base, relative) && !matchesAny(w.excludes, base, relative)
}

func matchesAny(patterns []string, base, relative string) bool {
	for _, pattern := range patterns {
		if ok, _ := path.Match(pattern, base); ok {
			return true
		}
		if ok, _ := path.Match(pattern, relative); ok {
			return true
		}
	}
	return false
}

// FromManifest expands the manifest's env entries into targets and reports
// manifest-level problems: patterns matching nothing, one file claimed by two
// applications, and (as warnings) environment files present next to declared
// ones but not declared.
func FromManifest(m *manifest.Manifest) ([]Target, []diag.Diagnostic) {
	var targets []Target
	var diagnostics []diag.Diagnostic
	owner := map[string]string{}

	for _, app := range m.Applications {
		for _, pattern := range app.EnvPatterns {
			paths, matched := expand(pattern)
			if !matched {
				diagnostics = append(diagnostics, diag.Diagnostic{
					Severity: diag.SeverityError, Code: diag.InvalidManifest, File: m.Path, Application: app.Name,
					Message:  fmt.Sprintf("Application %q: env pattern matches no files.", app.Name),
					Expected: pattern,
				})
				continue
			}
			for _, p := range paths {
				if other, claimed := owner[p]; claimed && other != app.Name {
					diagnostics = append(diagnostics, diag.Diagnostic{
						Severity: diag.SeverityError, Code: diag.InvalidManifest, File: m.Path, Application: app.Name,
						Message: fmt.Sprintf("File %s is assigned to both %q and %q.", p, other, app.Name),
					})
					continue
				}
				if _, claimed := owner[p]; claimed {
					continue
				}
				owner[p] = app.Name
				targets = append(targets, Target{Path: p, Application: app.Name})
			}
		}
	}

	slices.SortFunc(targets, func(a, b Target) int { return strings.Compare(a.Path, b.Path) })
	return targets, append(diagnostics, orphans(m, owner)...)
}

// expand returns the files a pattern refers to. A literal path is always
// returned, so a missing file is reported later as unreadable rather than
// silently skipped.
func expand(pattern string) ([]string, bool) {
	if !strings.ContainsAny(pattern, "*?[") {
		return []string{filepath.Clean(pattern)}, true
	}
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil, false
	}
	slices.Sort(matches)
	return matches, true
}

func orphans(m *manifest.Manifest, declared map[string]string) []diag.Diagnostic {
	directories := map[string]bool{}
	for p := range declared {
		directories[filepath.Dir(p)] = true
	}

	var diagnostics []diag.Diagnostic
	for _, dir := range slices.Sorted(func(yield func(string) bool) {
		for d := range directories {
			if !yield(d) {
				return
			}
		}
	}) {
		found, _ := Directories([]string{dir}, Options{})
		for _, target := range found {
			if _, isDeclared := declared[target.Path]; !isDeclared {
				diagnostics = append(diagnostics, diag.Diagnostic{
					Severity: diag.SeverityWarning, Code: diag.OrphanEnvFile, File: target.Path,
					Message: fmt.Sprintf("Environment file is not declared in %s.", filepath.Base(m.Path)),
				})
			}
		}
	}
	return diagnostics
}
