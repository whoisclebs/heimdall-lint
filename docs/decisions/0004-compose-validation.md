# 4. Compose files are validated per service, from the effective environment

## Status

Accepted

## Context

Operations already describe which environment each service receives in `docker-compose.yml` (`env_file`, `environment`). Validating loose `.env` files leaves a gap: what actually reaches a container is the merge of both.

## Decision

`heimdall lint compose.yaml` (or `--compose FILE`) builds, for every service that declares configuration, the environment Compose would give it: `env_file` entries in order, then `environment` on top, later definitions winning. Each service is one validation target, reported as `compose.yaml [service NAME]`.

- **Interpolation** follows Compose for `environment` values and `env_file` paths: `${VAR}`, `$VAR`, `${VAR:-default}`, `${VAR-default}`, `$$`; unset variables expand to empty. The process environment wins over a `.env` next to the compose file. `${VAR:?err}` and `${VAR:+alt}` are not evaluated and are validated as written. Values inside `env_file` files are not interpolated.
- **Schema per service**: `x-heimdall.schema` on the service, else `--schema-dir` (`<service>.schema.yaml`), else `--schema`.
- **Fail closed**: a service that declares configuration but resolves to no schema is an error (`HML010`), so unvalidated configuration cannot hide. `x-heimdall: {skip: true}` is the explicit opt-out (for example an off-the-shelf `postgres`). Services with no `env_file`/`environment` are ignored.
- The `x-heimdall` extension is decoded strictly: unknown keys, or `schema` together with `skip`, are errors (exit 3).
- Problems in individual `env_file` entries (missing required file, syntax error, duplicate inside one file) are reported against that file. Overriding a key across sources is normal Compose behavior and is not a duplicate. Entries carry no line numbers because they may come from several sources.

YAML anchors, aliases and merge keys are resolved (keys written in a mapping win over merged ones; earlier merge sources win over later), because real compose files build `environment` and whole services from anchors; without this, such services would be silently skipped.

Out of scope: `include`, `extends`, `profiles`, secrets/configs, and `env_file` `format`. `include` and `extends` are reported as a warning (`HML019`) rather than ignored silently.

## Consequences

The same schemas serve loose `.env` files and Compose services. Adoption on a compose file with many off-the-shelf services needs one `skip` per service; the alternative (silently ignoring services without a schema) would contradict the fail-closed principle.
