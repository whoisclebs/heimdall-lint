package render

import (
	"encoding/json"
	"io"

	"github.com/whoisclebs/heimdall-lint/internal/batch"
	"github.com/whoisclebs/heimdall-lint/internal/diag"
)

type jsonDiagnostic struct {
	Severity    string   `json:"severity"`
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	File        string   `json:"file,omitempty"`
	Line        int      `json:"line,omitempty"`
	Application string   `json:"application,omitempty"`
	Variable    string   `json:"variable,omitempty"`
	Message     string   `json:"message"`
	Expected    string   `json:"expected,omitempty"`
	Received    string   `json:"received,omitempty"`
	Suggestions []string `json:"suggestions,omitempty"`
}

type jsonFile struct {
	Path             string           `json:"path"`
	Application      string           `json:"application"`
	Schema           string           `json:"schema,omitempty"`
	Valid            bool             `json:"valid"`
	VariablesChecked int              `json:"variables_checked"`
	Diagnostics      []jsonDiagnostic `json:"diagnostics"`
}

type jsonSummary struct {
	Files            int `json:"files"`
	ValidFiles       int `json:"valid_files"`
	InvalidFiles     int `json:"invalid_files"`
	WarningFiles     int `json:"warning_files"`
	VariablesChecked int `json:"variables_checked"`
	Errors           int `json:"errors"`
	Warnings         int `json:"warnings"`
}

type jsonReport struct {
	Valid    bool             `json:"valid"`
	ExitCode int              `json:"exit_code"`
	Summary  jsonSummary      `json:"summary"`
	General  []jsonDiagnostic `json:"general"`
	Files    []jsonFile       `json:"files"`
}

func toJSONDiagnostics(diagnostics []diag.Diagnostic) []jsonDiagnostic {
	converted := make([]jsonDiagnostic, len(diagnostics))
	for i, d := range diagnostics {
		converted[i] = jsonDiagnostic{
			Severity: d.Severity.String(), Code: string(d.Code), Name: d.Code.Name(), File: d.File, Line: d.Line,
			Application: d.Application, Variable: d.Variable, Message: d.Message,
			Expected: d.Expected, Received: d.Received, Suggestions: d.Suggestions,
		}
	}
	return converted
}

// JSON writes the report as stable, indented JSON.
func JSON(w io.Writer, report batch.Report, warningsAsErrors bool) error {
	summary := report.Summary()
	document := jsonReport{
		Valid:    report.Passed(warningsAsErrors),
		ExitCode: report.ExitCode(warningsAsErrors),
		Summary: jsonSummary{
			Files: summary.Files, ValidFiles: summary.ValidFiles, InvalidFiles: summary.InvalidFiles,
			WarningFiles: summary.WarningFiles, VariablesChecked: summary.VariablesChecked,
			Errors: summary.Errors, Warnings: summary.Warnings,
		},
		General: toJSONDiagnostics(report.General),
		Files:   make([]jsonFile, len(report.Files)),
	}
	for i, file := range report.Files {
		document.Files[i] = jsonFile{
			Path: file.Path, Application: file.Application, Schema: file.SchemaPath,
			Valid: file.Errors() == 0, VariablesChecked: file.VariablesChecked,
			Diagnostics: toJSONDiagnostics(file.Diagnostics),
		}
	}
	return writeJSON(w, document)
}

func writeJSON(w io.Writer, document any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(document)
}
