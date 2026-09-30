# 5. Schemas can be read from a local image, through the Docker CLI

## Status

Accepted

## Context

An environment contract is only useful if it matches the application version that will read the environment. Keeping schemas in a separate repository lets them drift from the images they describe. The image itself is the natural carrier: application version and environment contract ship together.

## Decision

`--image REF` (and `image://REF` wherever a schema path is accepted, including both arguments of `diff`) reads `/env.schema.yaml` from the image, or the path in the `io.heimdall.schema` label.

- It shells out to the Docker CLI: `image inspect` (label), `create`, `cp`, `rm`. There is no Docker SDK dependency.
- **Nothing in the image runs.** The container is only created, never started (`--entrypoint` is set to a path that does not exist, `--network none`), and always removed afterwards, including on failure or cancellation.
- **Nothing is pulled.** `--pull never`: the image must already be present locally, so no network access is introduced and Heimdall stays local-first. A missing image is reported as such.
- The reference is validated before Docker is invoked (no leading `-`, no whitespace or shell metacharacters), arguments are passed as an argv list with no shell, the copied stream is size-capped, and only a single regular file no larger than the schema limit is accepted.
- Failures map onto the existing model: a missing schema in the image is `HML010` (exit 1), an unusable image or an invalid embedded schema is a contract failure (`HML009`, exit 3).
- The core packages do not depend on Docker. Schema loading goes through a `LoadSchema` hook in `batch`, and only the CLI wires in the image-aware loader.

## Consequences

Docker is required only when `--image` is used. Because the loader is a hook, a future source (an OCI registry client, an artifact store) can be added without touching validation.
