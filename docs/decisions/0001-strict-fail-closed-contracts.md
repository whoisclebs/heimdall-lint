# 1. Contracts are decoded strictly and fail closed

## Status

Accepted

## Context

Heimdall is a gate: a wrongly accepted configuration reaches production. A schema or manifest that is silently misread (a typo such as `requird: true`, a field from a newer version, an out-of-range `default`) would weaken the gate without anyone noticing.

## Decision

- Schema and manifest are decoded with unknown fields rejected. `version` is read first, so a future version reports "unsupported version" instead of a confusing unknown-field error.
- Rules that are reserved for later (`required_if`, `requires`, `conflicts_with`) are therefore rejected today rather than ignored.
- A schema is rejected if it is self-contradictory: a `default` that fails its own type check, `min > max`, `required` together with `default`, a `replacement` that is not declared, and so on. All problems are reported in one pass.
- A broken schema or manifest exits with code 3, distinct from "the environment is invalid" (1), because results computed against a broken contract cannot be trusted.
- The one YAML dependency is `go.yaml.in/yaml/v3`; the standard library has no YAML parser, and writing one is a worse trade than importing this one.

## Consequences

Adding a schema field is a version bump, and old binaries refuse new schemas loudly. That is the intended behavior for a gate.
