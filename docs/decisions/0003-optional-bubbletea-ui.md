# 3. The Bubble Tea UI is opt-in and read-only

## Status

Accepted

## Context

Heimdall's main habitat is a one-shot container in Docker Compose and CI, where output must be plain, deterministic and stable. Operators looking at a large batch interactively benefit from browsing results instead of scrolling.

## Decision

`heimdall lint --tui` opens a Bubble Tea results browser (file list plus scrollable diagnostics, filter to problems only). It renders the same `batch.Report` and the same diagnostic text (`render.Diagnostics`) as the plain output, never validates anything itself, and the process exit code is computed from the report, not from the UI. It refuses to start without a TTY or together with `--format json`.

The default stays plain text. Interactivity is never inferred from a TTY, so a script that gains a terminal does not change behavior.

## Consequences

The TUI adds Bubble Tea and lipgloss to the build. They are imported only from `internal/tui`, so the core packages stay free of them.
