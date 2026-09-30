package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/whoisclebs/heimdall-lint/internal/batch"
	"github.com/whoisclebs/heimdall-lint/internal/diag"
)

func testReport() batch.Report {
	return batch.Report{
		General: []diag.Diagnostic{{Severity: diag.SeverityWarning, Code: diag.OrphanEnvFile, File: "envs/old.env", Message: "Environment file is not declared in heimdall.yaml."}},
		Files: []batch.FileResult{
			{Path: "envs/inventory-api.env", VariablesChecked: 4, Diagnostics: []diag.Diagnostic{
				{Severity: diag.SeverityError, Code: diag.UnknownVariable, Line: 3, Variable: "INVENTORY_TIMOUT",
					Message: "Unknown environment variable.", Suggestions: []string{"INVENTORY_TIMEOUT"}},
			}},
			{Path: "envs/reports-api.env", VariablesChecked: 31},
		},
	}
}

func sized(m model) model {
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return updated.(model)
}

func press(m model, key tea.KeyMsg) model {
	updated, _ := m.Update(key)
	return updated.(model)
}

func runes(r string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)} }

func TestViewBeforeSizeIsLoading(t *testing.T) {
	if got := newModel(testReport(), false).View(); !strings.Contains(got, "Loading") {
		t.Fatalf("view = %q", got)
	}
}

func TestInitialViewShowsListSummaryAndFirstDetail(t *testing.T) {
	view := sized(newModel(testReport(), false)).View()
	for _, want := range []string{"Heimdall Lint", "FAILED", "2 files", "envs/old.env", "envs/inventory-api.env", "envs/reports-api.env", "not declared in heimdall.yaml"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q\n%s", want, view)
		}
	}
}

func TestNavigationChangesDetail(t *testing.T) {
	m := sized(newModel(testReport(), false))
	m = press(m, tea.KeyMsg{Type: tea.KeyDown})
	if view := m.View(); !strings.Contains(view, "INVENTORY_TIMOUT") || !strings.Contains(view, "INVENTORY_TIMEOUT") {
		t.Fatalf("detail should show the inventory diagnostics\n%s", view)
	}
	m = press(m, runes("j"))
	if view := m.View(); !strings.Contains(view, "No problems found") {
		t.Fatalf("detail should show the passing file\n%s", view)
	}
	m = press(m, runes("j")) // clamps at the end
	if m.cursor != 2 {
		t.Fatalf("cursor = %d", m.cursor)
	}
	m = press(m, runes("g"))
	if m.cursor != 0 {
		t.Fatalf("cursor = %d", m.cursor)
	}
}

func TestFilterKeepsOnlyProblems(t *testing.T) {
	m := press(sized(newModel(testReport(), false)), runes("f"))
	if len(m.visible) != 2 || strings.Contains(m.View(), "envs/reports-api.env") {
		t.Fatalf("visible = %v\n%s", m.visible, m.View())
	}
	m = press(m, runes("f"))
	if len(m.visible) != 3 {
		t.Fatalf("visible = %v", m.visible)
	}
}

func TestQuitKeys(t *testing.T) {
	for _, key := range []tea.KeyMsg{runes("q"), {Type: tea.KeyEsc}, {Type: tea.KeyCtrlC}} {
		_, command := sized(newModel(testReport(), false)).Update(key)
		if command == nil {
			t.Errorf("key %v did not quit", key)
		} else if _, quit := command().(tea.QuitMsg); !quit {
			t.Errorf("key %v returned %T", key, command())
		}
	}
}

func TestPassedReport(t *testing.T) {
	report := batch.Report{Files: []batch.FileResult{{Path: "a.env", VariablesChecked: 1}}}
	view := sized(newModel(report, false)).View()
	if !strings.Contains(view, "PASSED") {
		t.Fatalf("view:\n%s", view)
	}
}

func TestEmptyReportDoesNotPanic(t *testing.T) {
	m := sized(newModel(batch.Report{}, false))
	m = press(m, runes("j"))
	m = press(m, runes("f"))
	if !strings.Contains(m.View(), "No files to show") {
		t.Fatalf("view:\n%s", m.View())
	}
}

func TestTinyTerminalDoesNotPanic(t *testing.T) {
	updated, _ := newModel(testReport(), false).Update(tea.WindowSizeMsg{Width: 10, Height: 3})
	_ = updated.(model).View()
}

func TestTruncateLeft(t *testing.T) {
	got := truncateLeft("/very/long/path/to/reports-api.env", 16)
	if len([]rune(got)) != 16 || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "reports-api.env") {
		t.Fatalf("got %q", got)
	}
	if truncateLeft("a.env", 16) != "a.env" {
		t.Fatal("short text must be untouched")
	}
}
