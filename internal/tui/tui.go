// Package tui is the interactive results browser (Bubble Tea). It renders the
// same batch.Report the plain renderers use and never validates anything.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/whoisclebs/heimdall-lint/internal/batch"
	"github.com/whoisclebs/heimdall-lint/internal/diag"
	"github.com/whoisclebs/heimdall-lint/internal/render"
)

// Run shows the report until the user quits. The process exit code is decided
// by the caller from the report, never by what happens in the UI.
func Run(report batch.Report, warningsAsErrors bool) error {
	program := tea.NewProgram(newModel(report, warningsAsErrors), tea.WithAltScreen())
	_, err := program.Run()
	return err
}

type status int

const (
	statusPass status = iota
	statusWarn
	statusFail
)

// entry is one row of the list: a file, or a group of general problems.
type entry struct {
	title       string
	detail      string // dimmed second line: variables checked, schema
	status      status
	diagnostics []diag.Diagnostic
}

func (e entry) hasProblems() bool { return e.status != statusPass }

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	dimStyle      = lipgloss.NewStyle().Faint(true)
	selectedStyle = lipgloss.NewStyle().Bold(true).Reverse(true)
	paneStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240"))
	statusGlyph   = map[status]string{
		statusPass: lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("✓"),
		statusWarn: lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render("!"),
		statusFail: lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("✗"),
	}
)

const (
	minListWidth = 24
	maxListWidth = 44
	chromeHeight = 4 // header, summary line, footer, spacing
)

type model struct {
	entries      []entry
	visible      []int // indexes into entries
	cursor       int
	onlyProblems bool
	detail       viewport.Model
	summary      batch.Summary
	passed       bool
	width        int
	height       int
	ready        bool
}

func newModel(report batch.Report, warningsAsErrors bool) model {
	m := model{
		entries: buildEntries(report),
		summary: report.Summary(),
		passed:  report.Passed(warningsAsErrors),
		detail:  viewport.New(0, 0),
	}
	m.refilter()
	return m
}

func buildEntries(report batch.Report) []entry {
	var entries []entry
	var groupOrder []string
	byFile := map[string][]diag.Diagnostic{}
	for _, d := range report.General {
		if _, seen := byFile[d.File]; !seen {
			groupOrder = append(groupOrder, d.File)
		}
		byFile[d.File] = append(byFile[d.File], d)
	}
	for _, file := range groupOrder {
		entries = append(entries, entry{
			title: file, detail: "configuration", status: statusOf(byFile[file]), diagnostics: byFile[file],
		})
	}
	for _, file := range report.Files {
		entries = append(entries, entry{
			title:       file.Path,
			detail:      fmt.Sprintf("%d variables", file.VariablesChecked),
			status:      statusOf(file.Diagnostics),
			diagnostics: file.Diagnostics,
		})
	}
	return entries
}

func statusOf(diagnostics []diag.Diagnostic) status {
	switch {
	case diag.Count(diagnostics, diag.SeverityError) > 0:
		return statusFail
	case diag.Count(diagnostics, diag.SeverityWarning) > 0:
		return statusWarn
	}
	return statusPass
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height, m.ready = message.Width, message.Height, true
		m.layout()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(message)
	}
	return m, nil
}

func (m model) handleKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "home", "g":
		m.moveTo(0)
	case "end", "G":
		m.moveTo(len(m.visible) - 1)
	case "f":
		m.onlyProblems = !m.onlyProblems
		m.refilter()
		m.layout()
	default:
		var command tea.Cmd
		m.detail, command = m.detail.Update(key)
		return m, command
	}
	return m, nil
}

func (m *model) move(delta int) { m.moveTo(m.cursor + delta) }
func (m *model) moveTo(index int) {
	if len(m.visible) == 0 {
		return
	}
	m.cursor = max(0, min(index, len(m.visible)-1))
	m.refreshDetail()
}

// refilter recomputes the visible rows, keeping the cursor in range.
func (m *model) refilter() {
	m.visible = nil
	for index, e := range m.entries {
		if !m.onlyProblems || e.hasProblems() {
			m.visible = append(m.visible, index)
		}
	}
	m.cursor = max(0, min(m.cursor, len(m.visible)-1))
}

func (m model) listWidth() int {
	return max(minListWidth, min(maxListWidth, m.width/3))
}

func (m *model) layout() {
	paneHeight := max(3, m.height-chromeHeight)
	m.detail.Width = max(10, m.width-m.listWidth()-6)
	m.detail.Height = max(1, paneHeight-2)
	m.refreshDetail()
}

func (m *model) refreshDetail() {
	m.detail.SetContent(m.detailText())
	m.detail.GotoTop()
}

func (m model) selected() (entry, bool) {
	if len(m.visible) == 0 {
		return entry{}, false
	}
	return m.entries[m.visible[m.cursor]], true
}

func (m model) detailText() string {
	selected, ok := m.selected()
	if !ok {
		return dimStyle.Render("Nothing to show.")
	}
	var text strings.Builder
	text.WriteString(titleStyle.Render(selected.title) + "\n\n")
	if len(selected.diagnostics) == 0 {
		text.WriteString(statusGlyph[statusPass] + " No problems found. " + dimStyle.Render(selected.detail))
		return text.String()
	}
	_ = render.Diagnostics(&text, selected.diagnostics, true)
	return text.String()
}

func (m model) View() string {
	if !m.ready {
		return "Loading…"
	}
	paneHeight := max(3, m.height-chromeHeight)
	list := paneStyle.Width(m.listWidth()).Height(paneHeight - 2).Render(m.listView(paneHeight - 2))
	detail := paneStyle.Width(m.detail.Width + 2).Height(paneHeight - 2).Render(m.detail.View())

	return strings.Join([]string{
		titleStyle.Render("Heimdall Lint") + "  " + m.verdict(),
		dimStyle.Render(m.summaryLine()),
		lipgloss.JoinHorizontal(lipgloss.Top, list, detail),
		dimStyle.Render(m.help()),
	}, "\n")
}

func (m model) verdict() string {
	if m.passed {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true).Render("PASSED")
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true).Render("FAILED")
}

func (m model) summaryLine() string {
	return fmt.Sprintf("%d files · %d valid · %d invalid · %d errors · %d warnings",
		m.summary.Files, m.summary.ValidFiles, m.summary.InvalidFiles, m.summary.Errors, m.summary.Warnings)
}

func (m model) help() string {
	filter := "f only problems"
	if m.onlyProblems {
		filter = "f show all"
	}
	return "↑/↓ select · pgup/pgdn scroll · " + filter + " · q quit"
}

// listView renders the rows, scrolled so the cursor stays visible.
func (m model) listView(height int) string {
	if len(m.visible) == 0 {
		return dimStyle.Render("No files to show.")
	}
	first := max(0, min(m.cursor-height/2, len(m.visible)-height))
	last := min(len(m.visible), first+height)

	width := m.listWidth() - 2
	lines := make([]string, 0, height)
	for position := first; position < last; position++ {
		e := m.entries[m.visible[position]]
		line := statusGlyph[e.status] + " " + truncateLeft(e.title, width-2)
		if position == m.cursor {
			line = selectedStyle.Render(lipgloss.NewStyle().Width(width).Render(line))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// truncateLeft keeps the end of a path, which is the informative part.
func truncateLeft(text string, width int) string {
	runes := []rune(text)
	if width <= 1 || len(runes) <= width {
		return text
	}
	return "…" + string(runes[len(runes)-width+1:])
}
