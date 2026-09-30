# 2. "Did you mean?" uses a plausibility gate before scoring

## Status

Accepted

## Context

A bad suggestion is worse than none. A plain edit-distance threshold suggests `DATABASE_HOST` for `DATABASE_PORT` (two characters differ in a 13-character name), which would send an operator in the wrong direction.

## Decision

Suggestions are layered:

1. **Notation mapping** (`naming.Strategy`, Spring relaxed binding by default): `api.timeout` and `api-timeout` map to `API_TIMEOUT` when exactly one declared variable matches. Case-only differences are reported as such.
2. **Plausibility gate**: a candidate must differ from the input by at most one edit per six characters (Damerau-Levenshtein, so a transposition counts once), and when both names have the same number of `_`-separated tokens, every token that differs must itself look like a typo of its counterpart. Short tokens must match exactly.
3. **Score** (edit similarity, token similarity, common prefix, length similarity; weights in one place) ranks the survivors. At or above the high threshold one suggestion is shown (or more, only if several tie); above the medium threshold up to three; below it none.

Enum values use a looser gate and a lower minimum score, since they form a short closed list (`KAFKAA` suggests `KAFKA`).

## Consequences

The gate, not the score, is what prevents absurd suggestions, so tuning the weights cannot make them appear. The tunables live at the top of `internal/suggest/suggest.go`.
