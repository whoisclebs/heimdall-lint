package render

import (
	"bufio"
	"fmt"
	"io"
	"slices"

	"github.com/whoisclebs/heimdall-lint/internal/batch"
	"github.com/whoisclebs/heimdall-lint/internal/diag"
)

type Options struct {
	// Quiet prints only problems and the summary.
	Quiet            bool
	Color            bool
	WarningsAsErrors bool
}

// Human writes the report for people. Passing files stay compact; failures get
// full detail.
func Human(w io.Writer, report batch.Report, options Options) error {
	out := bufio.NewWriter(w)
	printer := humanPrinter{out: out, style: style{color: options.Color}}

	fmt.Fprintln(out, printer.style.bold("Heimdall Lint"))
	fmt.Fprintln(out)
	printer.general(report.General)
	for _, file := range report.Files {
		printer.file(file, options.Quiet)
	}
	printer.summary(report.Summary(), report.Passed(options.WarningsAsErrors), report.Summary().Warnings > 0)
	return out.Flush()
}

type humanPrinter struct {
	out   *bufio.Writer
	style style
}

func (p humanPrinter) general(diagnostics []diag.Diagnostic) {
	var files []string
	for _, d := range diagnostics {
		if !slices.Contains(files, d.File) {
			files = append(files, d.File)
		}
	}
	for _, file := range files {
		fmt.Fprintln(p.out, clean(file))
		fmt.Fprintln(p.out)
		for _, d := range diagnostics {
			if d.File == file {
				p.diagnostic(d)
			}
		}
	}
}

func (p humanPrinter) file(file batch.FileResult, quiet bool) {
	errors, warnings := file.Errors(), file.Warnings()
	if quiet && errors == 0 && warnings == 0 {
		return
	}

	fmt.Fprintln(p.out, clean(file.Path))
	switch {
	case errors > 0:
		fmt.Fprintln(p.out, "  "+p.style.error("FAIL"))
	case warnings > 0:
		fmt.Fprintln(p.out, "  "+p.style.warning("WARN"))
	default:
		fmt.Fprintln(p.out, "  "+p.style.pass("PASS"))
		fmt.Fprintf(p.out, "  %d %s checked\n\n", file.VariablesChecked, plural(file.VariablesChecked, "variable", "variables"))
		return
	}
	if file.SchemaPath != "" {
		fmt.Fprintf(p.out, "  %s\n", p.style.dim("schema: "+clean(file.SchemaPath)))
	}
	fmt.Fprintln(p.out)
	for _, d := range file.Diagnostics {
		p.diagnostic(d)
	}
}

func (p humanPrinter) diagnostic(d diag.Diagnostic) {
	label := p.style.error("ERROR")
	if d.Severity == diag.SeverityWarning {
		label = p.style.warning("WARNING")
	}
	subject := d.Variable
	if subject == "" {
		subject = d.Code.Name()
	}
	where := string(d.Code)
	if d.Line > 0 {
		where = fmt.Sprintf("%s, line %d", d.Code, d.Line)
	}
	fmt.Fprintf(p.out, "  %s %s %s\n\n", label, p.style.bold(clean(subject)), p.style.dim("("+where+")"))

	fmt.Fprintf(p.out, "    %s\n", clean(d.Message))
	p.block("Expected:", d.Expected)
	p.block("Received:", d.Received)
	if len(d.Suggestions) > 0 {
		heading := "Did you mean:"
		if d.SuggestionKind == diag.Use {
			heading = "Use:"
		}
		fmt.Fprintf(p.out, "\n    %s\n", heading)
		for _, suggestion := range d.Suggestions {
			fmt.Fprintf(p.out, "      %s\n", clean(suggestion))
		}
	}
	fmt.Fprintln(p.out)
}

func (p humanPrinter) block(heading, content string) {
	if content != "" {
		fmt.Fprintf(p.out, "\n    %s\n      %s\n", heading, clean(content))
	}
}

func (p humanPrinter) summary(s batch.Summary, passed, hasWarnings bool) {
	fmt.Fprintln(p.out, p.style.bold("Summary"))
	fmt.Fprintln(p.out)
	fmt.Fprintf(p.out, "  Files discovered:     %d\n", s.Files)
	fmt.Fprintf(p.out, "  Files valid:          %d\n", s.ValidFiles)
	fmt.Fprintf(p.out, "  Files invalid:        %d\n", s.InvalidFiles)
	fmt.Fprintf(p.out, "  Files with warnings:  %d\n\n", s.WarningFiles)
	fmt.Fprintf(p.out, "  Variables checked:    %d\n", s.VariablesChecked)
	fmt.Fprintf(p.out, "  Errors:               %d\n", s.Errors)
	fmt.Fprintf(p.out, "  Warnings:             %d\n\n", s.Warnings)

	switch {
	case !passed:
		fmt.Fprintln(p.out, p.style.error("Environment validation failed."))
	case hasWarnings:
		fmt.Fprintln(p.out, p.style.warning("Environment validation passed with warnings."))
	default:
		fmt.Fprintln(p.out, p.style.pass("Environment validation passed."))
	}
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

// Diagnostics writes only the diagnostic blocks, exactly as Human prints them.
// Interactive views use it so the wording never diverges from plain output.
func Diagnostics(w io.Writer, diagnostics []diag.Diagnostic, color bool) error {
	out := bufio.NewWriter(w)
	printer := humanPrinter{out: out, style: style{color: color}}
	for _, d := range diagnostics {
		printer.diagnostic(d)
	}
	return out.Flush()
}
