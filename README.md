<a id="readme-top"></a>

<!-- PROJECT SHIELDS -->
[![Go][go-shield]][go-url]
[![Docker][docker-shield]][docker-url]
[![Static binary][static-shield]][go-url]

<!-- PROJECT LOGO -->
<br />
<div align="center">
  <h3 align="center">Heimdall Lint</h3>

  <p align="center">
    The guardian at the gate: validate environment files against declarative contracts before your containers start.
    <br />
    <a href="#usage"><strong>Explore the usage guide »</strong></a>
    <br />
    <br />
    <a href="examples">View Examples</a>
    &middot;
    <a href="docs/decisions">Design Decisions</a>
    &middot;
    <a href="#roadmap">Roadmap</a>
  </p>
</div>

<!-- TABLE OF CONTENTS -->
<details>
  <summary>Table of Contents</summary>
  <ol>
    <li>
      <a href="#about-the-project">About The Project</a>
      <ul>
        <li><a href="#built-with">Built With</a></li>
      </ul>
    </li>
    <li>
      <a href="#getting-started">Getting Started</a>
      <ul>
        <li><a href="#prerequisites">Prerequisites</a></li>
        <li><a href="#installation">Installation</a></li>
        <li><a href="#quick-start">Quick Start</a></li>
      </ul>
    </li>
    <li>
      <a href="#usage">Usage</a>
      <ul>
        <li><a href="#single-file-validation">Single File Validation</a></li>
        <li><a href="#batch-validation">Batch Validation</a></li>
        <li><a href="#directory-discovery">Directory Discovery</a></li>
        <li><a href="#manifest">Manifest</a></li>
        <li><a href="#schema-resolution">Schema Resolution</a></li>
        <li><a href="#schema">Schema</a></li>
        <li><a href="#cli">CLI</a></li>
        <li><a href="#docker">Docker</a></li>
        <li><a href="#compose-files">Compose Files</a></li>
        <li><a href="#schema-inside-the-image">Schema Inside The Image</a></li>
        <li><a href="#docker-compose">Docker Compose</a></li>
        <li><a href="#spring-boot">Spring Boot</a></li>
        <li><a href="#did-you-mean">Did You Mean</a></li>
        <li><a href="#exit-codes">Exit Codes</a></li>
        <li><a href="#diagnostic-codes">Diagnostic Codes</a></li>
        <li><a href="#security">Security</a></li>
      </ul>
    </li>
    <li><a href="#roadmap">Roadmap</a></li>
    <li><a href="#contributing">Contributing</a></li>
    <li><a href="#license">License</a></li>
    <li><a href="#contact">Contact</a></li>
    <li><a href="#acknowledgments">Acknowledgments</a></li>
  </ol>
</details>

<!-- ABOUT THE PROJECT -->
## About The Project

Heimdall is the guardian at the gate. Heimdall Lint is a small, local-first CLI that validates environment files (`.env`) against declarative contracts **before your containers start**.

It catches the mistakes that otherwise surface in production: a required variable that was never set, `API_TIMOUT` instead of `API_TIMEOUT`, `DATABASE_PORT=banana`, an enum typo, a Spring property (`api.timeout`) used where an environment variable is required, a deprecated variable that is still configured, a `.env` nobody validates.

It is built for the case where Operations owns dozens or hundreds of `.env` files: one command validates a file, a folder, a tree, or an environment described by a manifest, in parallel, with deterministic output.

```text
   many .env files ─▶ discovery ─▶ schema resolution ─▶ parallel validation
                                                              │
                                              aggregated, sorted diagnostics
                                                       ┌──────┴──────┐
                                                     PASS           FAIL
                                              applications      deploy
                                                 can start      blocked
```

It is one static binary with no network access, no services and a small dependency set (a YAML parser, plus Bubble Tea for the optional UI).

<p align="right">(<a href="#readme-top">back to top</a>)</p>

### Built With

* [![Go][go-shield]][go-url]
* [Bubble Tea](https://github.com/charmbracelet/bubbletea) and [Lip Gloss](https://github.com/charmbracelet/lipgloss) for the optional interactive UI
* [go.yaml.in/yaml/v3](https://github.com/yaml/go-yaml) for schemas and manifests
* [![Docker][docker-shield]][docker-url]

The core uses the standard library for everything else. The UI libraries are imported only from `internal/tui`.

<p align="right">(<a href="#readme-top">back to top</a>)</p>

<!-- GETTING STARTED -->
## Getting Started

### Prerequisites

* Go 1.26 or newer (to build from source)
* Docker with Compose (optional: image, Compose example and `make docker-test`)

### Installation

1. Clone the repository
   ```sh
   git clone https://github.com/whoisclebs/heimdall-lint.git
   cd heimdall-lint
   ```
2. Build the binary
   ```sh
   make build          # writes bin/heimdall
   ```
3. Or build the Docker image
   ```sh
   make docker         # tags heimdall-lint
   ```

### Quick Start

```sh
go build -o bin/heimdall ./cmd/heimdall          # or: make build
export PATH="$PWD/bin:$PATH"

cd examples/basic
heimdall lint production.env      # PASS, exit 0
heimdall lint broken.env          # FAIL, exit 1, with suggestions
```

Every directory under `examples/` has a `run.sh` you can execute (set `HEIMDALL=/path/to/heimdall` if it is not on your `PATH`).

<p align="right">(<a href="#readme-top">back to top</a>)</p>

<!-- USAGE EXAMPLES -->
## Usage

### Single File Validation

```bash
heimdall lint production.env                       # uses ./env.schema.yaml
heimdall lint --schema reports-api.schema.yaml --env reports-api.env
```

```text
Heimdall Lint

production.env
  PASS
  7 variables checked
```

A failure shows what failed, what was expected, what was received, and how to fix it:

```text
broken.env
  FAIL

  ERROR DATABASE_PORT (HML003, line 2)

    Value has the wrong type.

    Expected:
      port number between 1 and 65535

    Received:
      "banana"

  ERROR API_TIMOUT (HML002, line 4)

    Unknown environment variable.

    Did you mean:
      API_TIMEOUT
```

### Batch Validation

```bash
heimdall lint --all /etc/company/envs --schema-dir /etc/company/schemas
```

Heimdall discovers the files, resolves each file's schema, validates them on a bounded worker pool (`--jobs N`, default: number of CPUs; `--jobs 1` for serial), and prints one aggregated, deterministically ordered report. Output never depends on which goroutine finished first: running the same command twice, with any `--jobs`, produces identical output.

Passing files stay compact; failures get full detail. `--quiet` prints only problems and the summary. `--format json` emits machine-readable output for CI.

### Directory Discovery

| Flag | Meaning |
|---|---|
| `--all [DIR...]` | Validate every environment file in the directories (default `.`). |
| `--recursive` | Descend into subdirectories. Hidden directories such as `.git` are skipped. |
| `--glob PATTERN` | Only files matching the pattern. Default: `*.env`, `.env`, `.env.*`. |
| `--exclude PATTERN` | Skip files matching the pattern (repeatable). Matched against the file name and the path relative to the root. |
| `--follow-symlinks` | Follow symbolic links. **Off by default**; cycles are detected when on. |

Auxiliary files are excluded by default and cannot be included by accident: `.env.example`, `.env.template`, `.env.sample`, `.env.backup`, `*.example.env`, `*.backup.env`, `*.bak`, `*.orig`, `*.swp`, `*~` and similar. To validate one of them anyway, name it explicitly.

Discovery that finds nothing is an error, not a pass.

### Manifest

`heimdall.yaml` declares applications, their schemas and their environment files. It is optional; with it, a bare `heimdall lint` validates the whole environment.

```yaml
version: 1

applications:
  reports-api:
    schema: schemas/reports-api.schema.yaml
    env:
      - envs/reports-api-*.env       # literal paths and globs (no "**")

  scheduler:
    schema: schemas/scheduler.schema.yaml
    env:
      - envs/scheduler.env
```

Paths are relative to the manifest. The manifest doubles as an inventory:

- an env pattern that matches nothing is an error;
- a file claimed by two applications is an error;
- an environment file that sits next to declared files but is **not declared** is reported as a warning (`HML012`), so forgotten files do not go unvalidated. Heimdall never modifies or removes anything.

Use `--manifest path/to/heimdall.yaml` to point elsewhere. A manifest cannot be combined with files, `--all`, `--schema` or `--schema-dir`.

### Schema Resolution

Discovery and resolution are separate steps. Exactly one strategy applies, in this order:

1. **Manifest**: each file uses its application's schema.
2. **`--schema FILE`**: every file uses the same schema (for `reports-api-01.env`, `-02.env`, ... sharing one contract).
3. **`--schema-dir DIR`**: `name.env` is validated against `DIR/name.schema.yaml`.
4. **Default**: `./env.schema.yaml`.

A file whose schema cannot be found is never skipped:

```text
envs/search-api.env
  FAIL

  ERROR schema_not_found (HML010)

    Schema not found.

    Expected:
      schemas/search-api.schema.yaml
```

The application name comes from the file name: `reports-api.env` → `reports-api`, `.env.staging` → `staging`, a bare `.env` → its directory name.

### Schema

```yaml
version: 1

variables:
  DATABASE_PORT:
    type: port
    required: true
    description: Database port

  QUEUE_PROVIDER:
    type: enum
    required: true
    values: [KAFKA, RABBITMQ, SQS, NATS]

  API_TIMEOUT:
    type: duration
    default: 5s
    min: 100ms
    max: 30s

  API_BASE_URL:
    type: url
    required: true
    schemes: [https]

  API_KEY:
    type: string
    required: true
    secret: true

  LEGACY_API_URL:
    type: string
    deprecated: true
    replacement: API_BASE_URL
```

**Types**

| Type | Accepts |
|---|---|
| `string` | Any text. |
| `integer` | Base-10 integer. `min`/`max` supported. |
| `float` | Decimal number (no `NaN`, `Inf`, hex). `min`/`max` supported. |
| `boolean` | `true` or `false`, case-insensitive. `yes`, `1`, `on` are rejected: Spring silently reads them as `false`. |
| `enum` | One of `values`, case-sensitive. |
| `url` | Absolute URL. `schemes` restricts it. Opaque URLs such as `jdbc:postgresql://db/app` are accepted for non-web schemes. |
| `duration` | Go syntax: `5s`, `100ms`, `1h30m`. `min`/`max` supported. |
| `hostname` | RFC 1123 name (underscores allowed, as in Docker service names) or an IP address. |
| `port` | Integer 1–65535; `min`/`max` may narrow it. |
| `ip` | IPv4 or IPv6 address. |
| `cidr` | CIDR prefix such as `10.0.0.0/8`. |

**Rules**: `required`, `default`, `min`, `max`, `values`, `schemes`, `secret`, `deprecated`, `replacement`, `description`, plus rules between variables:

```yaml
REDIS_ENABLED: {type: boolean, default: false}
REDIS_URL:
  type: url
  required_if: {REDIS_ENABLED: true}   # all conditions must hold (AND)
  conflicts_with: [MEMCACHED_URL]      # never set together
TLS_CERT:
  type: string
  requires: [TLS_KEY]                  # if TLS_CERT is set, TLS_KEY must be too
```

- `required_if` compares the **effective** value (the file's value, or the `default` when absent), since that is what the application will see. Booleans compare case-insensitively. It cannot be combined with `required`.
- `requires` and `conflicts_with` consider a variable *set* when it is present and non-empty. A conflicting pair is reported once.
- Every variable named in a rule must be declared, and `required_if` values must be valid for the referenced variable's type.
- Diagnostics: `HML016` required_by_condition, `HML017` missing_dependency, `HML018` conflicting_variables.

**Semantics worth knowing**

- A missing variable with a `default` is valid. Heimdall never edits your files.
- A `required` variable that is set but empty is reported as missing.
- An optional non-`string` variable that is set but empty is a type error.
- Duplicate keys are an error (Docker keeps the last one; Heimdall says so).
- Variables not in the schema are errors (`--allow-unknown` turns that off).
- Deprecated variables are **warnings**; they do not fail the run unless `--warnings-as-errors`.
- Schemas are decoded strictly and fail closed: unknown fields, unknown types, an invalid `default`, `min > max`, `required` with `default`, or an undeclared `replacement` reject the schema. Rules between variables are described below.

### CLI

```text
heimdall lint [FILE...]            validate files against env.schema.yaml
heimdall lint --all DIR            validate every environment file in DIR
heimdall lint compose.yaml         validate each service of a Compose file
heimdall lint                      validate the environment in ./heimdall.yaml
heimdall inspect NAME              describe an environment variable
heimdall diff OLD NEW              compare two schema versions (files or image://REF)
heimdall schema generate SOURCE    build a schema from Spring Boot metadata
heimdall version
```

`lint` flags: `--env`, `--schema`, `--schema-dir`, `--manifest`, `--compose`, `--image`, `--all`, `--recursive`, `--glob`, `--exclude`, `--follow-symlinks`, `--jobs`, `--format human|json`, `--quiet`, `--color auto|always|never`, `--warnings-as-errors`, `--allow-unknown`, `--tui`. Flags may appear before or after positional arguments.

Color is used only on a terminal, respects `NO_COLOR`, and is never needed to understand a message.

**`inspect`** describes a variable from the schema (never from an environment file). Secret defaults are redacted. With several schemas loaded (manifest or `--schema-dir`), every match is shown; narrow with `--application NAME`.

```text
$ heimdall inspect API_TIMEOUT
API_TIMEOUT

Type:           duration
Required:       false
Default:        5s
Minimum:        100ms
Maximum:        30s

Description:
Maximum time to wait for the upstream service
```

**`diff`** tells Operations what a release changes in the contract. Changes that require action (a new required variable without a default, a variable that became required, a type change) are marked.

```text
$ heimdall diff env-v1.yaml env-v2.yaml
Environment contract changes

ADDED

  API_READ_TIMEOUT  [action required]
    type: duration
    required: true

REMOVED

  LEGACY_API_URL

CHANGED

  API_TIMEOUT
    default: 5s -> 10s
```

**Interactive UI.** `heimdall lint --all DIR --tui` opens a [Bubble Tea](https://github.com/charmbracelet/bubbletea) browser: a file list with status, a scrollable diagnostics pane, `f` to show only problems, `q` to quit. It shows the same report as the plain output and the exit code is unchanged. It is opt-in and needs a terminal; it never activates on its own. See [docs/decisions/0003](docs/decisions/0003-optional-bubbletea-ui.md).

### Docker

The image is built `FROM scratch` around a static binary and runs as a non-root user.

```bash
docker build -t heimdall-lint .

# one file
docker run --rm -v "$PWD:/workspace:ro" heimdall-lint \
  lint --env /workspace/production.env --schema /workspace/env.schema.yaml

# a directory, one schema per application
docker run --rm \
  -v /opt/company/envs:/envs:ro -v /opt/company/schemas:/schemas:ro \
  heimdall-lint lint --all /envs --schema-dir /schemas

# a manifest (the working directory in the image is /workspace)
docker run --rm -v /opt/company/config:/workspace:ro heimdall-lint lint
```

### Compose Files

`heimdall lint` can read a Compose file and validate what each service will actually receive: its `env_file` entries in order, then `environment` on top (later definitions win, as in Compose).

```sh
heimdall lint compose.yaml --schema-dir schemas
```

```text
compose.yaml [service inventory-api]
  FAIL

  ERROR DATABASE_PORT (HML003)

    Value has the wrong type.
    ...
```

- A file named `compose.y[a]ml`, `docker-compose.y[a]ml` (or with a suffix such as `compose.prod.yaml`) is recognized automatically; otherwise use `--compose FILE`.
- Each service is a target. Its schema is `x-heimdall.schema` on the service, else `<service>.schema.yaml` in `--schema-dir`, else `--schema`.
- `${VAR}`, `$VAR`, `${VAR:-default}`, `${VAR-default}` and `$$` are interpolated in `environment` values and `env_file` paths, using the process environment and a `.env` next to the compose file.
- A service that declares configuration but has no schema is an **error**. Opt out explicitly, for example for an off-the-shelf database:

```yaml
services:
  reports-api:
    env_file: envs/reports-api.env
    x-heimdall:
      schema: schemas/reports-api.schema.yaml

  postgres:
    image: postgres
    environment: {POSTGRES_PASSWORD: ${DB_PASSWORD}}
    x-heimdall: {skip: true}
```

Services with no `env_file` or `environment` are ignored. YAML anchors, aliases and merge keys (`<<: *common`) are resolved. `include`, `extends` and `profiles` are not interpreted; `include` and `extends` are reported as a warning (`HML019`) so nothing is skipped silently. See [docs/decisions/0004](docs/decisions/0004-compose-validation.md).

### Schema Inside The Image

The contract can travel with the application version. Bake the schema into the application image:

```dockerfile
COPY env.schema.yaml /env.schema.yaml
# optional: a different location
LABEL io.heimdall.schema=/etc/app/env.schema.yaml
```

Then validate an environment against the exact version you are about to deploy:

```sh
heimdall lint --image company/reports-api:2026.10.0 production.env
heimdall diff image://company/reports-api:2026.9.0 image://company/reports-api:2026.10.0
heimdall inspect API_TIMEOUT --image company/reports-api:2026.10.0
```

The same `production.env` can pass against one version and fail against the next, which is the point: a new required variable is caught before the new image starts. Failure blocks show `schema: image://...` so it is clear which version imposed the contract.

`--image` needs the Docker CLI and an image that is **already present locally**. Nothing is pulled and nothing in the image is executed: Heimdall creates a container without starting it (`--network none`), copies the schema file out, and removes the container. It cannot be combined with `--schema`, `--schema-dir` or `--manifest`. See [docs/decisions/0005](docs/decisions/0005-schema-from-local-images.md).

### Docker Compose

One Heimdall container validates the whole environment; the applications wait for it.

```yaml
services:
  heimdall:
    image: company/heimdall-lint:latest
    volumes:
      - /opt/company/envs:/envs:ro
      - /opt/company/schemas:/schemas:ro
    command: [lint, --all, /envs, --schema-dir, /schemas]

  reports-api:
    image: company/reports-api:latest
    depends_on:
      heimdall:
        condition: service_completed_successfully
    env_file:
      - /opt/company/envs/reports-api.env
```

If any file is invalid, Heimdall exits 1, `service_completed_successfully` is never met, and no application starts. A runnable version is in [`examples/docker-compose`](examples/docker-compose); `make docker-test` proves both outcomes.

### Spring Boot

Spring Boot binds `spring.datasource.url` to the environment variable `SPRING_DATASOURCE_URL`. Schemas always use the environment variable name, and Heimdall recognizes the mistake of using the property name:

```text
  ERROR spring.datasource.url (HML002, line 1)

    Unknown environment variable. Spring property notation detected.

    Use:
      SPRING_DATASOURCE_URL
```

Property notation is detected through a `naming.Strategy`; the core has no Spring knowledge. See [`examples/spring-boot`](examples/spring-boot). 
**Generate a schema from your application.** With `spring-boot-configuration-processor` on the classpath, Spring writes `META-INF/spring-configuration-metadata.json` at build time. Heimdall can turn it into a schema:

```sh
heimdall schema generate app.jar --prefix api. --output reports-api.schema.yaml
# alias: heimdall spring import app.jar
```

`SOURCE` can be a jar (plain or Spring Boot executable), a directory such as `target/classes`, or the JSON file itself. Types, defaults, descriptions and deprecations are imported; enum values come from Spring's value hints.

- Variable names follow Spring's **canonical** rule: dots become underscores and **dashes are removed**, so `api.base-url` becomes `API_BASEURL`, not `API_BASE_URL`.
- Spring does not say what is mandatory, so nothing is `required`. Review the file and add `required: true` where needed.
- A default that cannot be expressed in the schema (for example a bare number for a duration) is dropped with a warning on stderr rather than emitting an invalid schema; the output is always re-validated by Heimdall's own schema parser.
- Indexed and wildcard properties (`a[0].b`) have no single variable and are skipped, as are names that collide after conversion; each is reported as a warning.
- Only the application's own metadata is read (nested jars in `BOOT-INF/lib` are not opened), entries are size-limited, and an existing `--output` file is never overwritten without `--force`.

### Did You Mean

Unknown variables get suggestions from a layered engine:

1. **Notation**: `api.timeout` / `api-timeout` map to `API_TIMEOUT` when the match is unambiguous; a case-only difference is called out as such.
2. **Damerau-Levenshtein** distance, where a transposition (`TIEMOUT`) counts as one edit.
3. **Token similarity** over `_`-separated parts, which helps long names (`QUEUE_PROVIDR_TIMEOUT`).
4. **Score and thresholds**: a weighted score decides between one suggestion, up to three, or none.

A plausibility gate runs before scoring, so an unrelated variable is never suggested: `DATABASE_PORT` does not suggest `DATABASE_HOST`. Rationale in [docs/decisions/0002](docs/decisions/0002-gated-suggestions.md).

### Exit Codes

| Code | Meaning |
|---|---|
| `0` | Valid. Warnings alone do not fail (unless `--warnings-as-errors`). |
| `1` | At least one environment file is invalid, a schema for a file is missing, or discovery found nothing. |
| `2` | Usage error: bad flags or arguments. |
| `3` | A schema or manifest is invalid. Takes precedence over `1`: results built on a broken contract cannot be trusted. |

### Diagnostic Codes

| Code | Name |
|---|---|
| HML001 | missing_required_variable |
| HML002 | unknown_variable |
| HML003 | invalid_type |
| HML004 | invalid_enum |
| HML005 | below_minimum |
| HML006 | above_maximum |
| HML007 | deprecated_variable (warning) |
| HML008 | duplicate_variable |
| HML009 | invalid_schema |
| HML010 | schema_not_found |
| HML011 | invalid_manifest |
| HML012 | orphan_env_file (warning) |
| HML013 | invalid_env_syntax |
| HML014 | env_file_unreadable |
| HML015 | no_env_files |
| HML016 | required_by_condition |
| HML017 | missing_dependency |
| HML018 | conflicting_variables |
| HML019 | unsupported_construct (warning) |

Human and JSON output are rendered from the same diagnostics.

### Security

- **Secrets**: a variable with `secret: true` is shown as `[REDACTED]` everywhere: errors, JSON, batch output, `inspect`, `diff`. Redaction happens where the diagnostic is created, so no renderer can forget it. Parser and syntax errors never echo values, and enum suggestions are skipped for secrets. This is tested explicitly, including through the built binary.
- **Terminal escapes**: control characters in file names, variable names and values are escaped before printing, so a hostile `.env` cannot drive your terminal.
- **Inputs are untrusted**: files are size-limited and must be regular files (no devices or FIFOs); YAML is decoded strictly into fixed scalar types, so nested structures and alias bombs are rejected before anything is expanded (and `yaml.v3` enforces its own alias limit); the three parsers are fuzzed.
- **Symlinks** are not followed unless `--follow-symlinks` is given.
- **Read-only**: Heimdall never writes to or deletes anything, and works on read-only mounts.
- **Paths in a manifest** are resolved relative to it and may point elsewhere (a manifest is operator-authored, like command-line flags). Do not run Heimdall on a manifest you do not trust with more access than you would give its author.
- **Fail closed**: anything Heimdall cannot interpret (schema, manifest, env file, missing schema) is a failure, never a pass.

<p align="right">(<a href="#readme-top">back to top</a>)</p>

<!-- ROADMAP -->
## Roadmap

- [x] V1: single-file, directory, recursive, glob and exclude discovery
- [x] V1: manifest (`heimdall.yaml`) with multiple `.env` files and globs per application
- [x] V1: schema resolution by `--schema`, `--schema-dir` and manifest
- [x] V1: concurrent validation with deterministic output
- [x] V1: unknown-variable detection, "Did you mean?", Spring property detection
- [x] V1: `inspect`, `diff`, human and JSON output, exit codes
- [x] V1: Docker image and Docker Compose example, with integration tests
- [x] Optional Bubble Tea results browser (`--tui`)
- [x] V1.1: import Spring Boot configuration metadata (`heimdall schema generate`, `heimdall spring import`)
- [x] V1.1: `required_if`, `requires`, `conflicts_with`
- [x] V1.2: validate `docker-compose.yml` directly, per service (`env_file` + `environment`)
- [x] V1.3: schema embedded in the Docker image, version-aware validation (`--image`, `image://`)
- [ ] V2: broader environment contracts (ports, volumes, healthchecks, release policies)

Out of scope: a web UI, a central server, secret-manager integrations, auto-fix.

<p align="right">(<a href="#readme-top">back to top</a>)</p>

<!-- CONTRIBUTING -->
## Contributing

Contributions are welcome. Please keep changes small and tested, and record significant design choices in [`docs/decisions`](docs/decisions).

```sh
make check         # go vet + go test -race ./...
make docker-test   # builds the image and runs the Compose scenarios (needs Docker)
```

Layout:

```text
cmd/heimdall        entry point
internal/diag       structured diagnostics and codes
internal/envfile    .env parser
internal/schema     schema loading and per-type value checks
internal/validate   schema × environment → diagnostics (pure)
internal/suggest    "did you mean" engine
internal/naming     framework naming strategies (Spring)
internal/spring     Spring Boot metadata importer (schema generate)
internal/manifest   heimdall.yaml
internal/compose    Compose files: env_file, environment, interpolation
internal/image      reads a schema from a local Docker image
internal/discovery  finds environment files
internal/resolve    picks the schema for each file
internal/batch      worker pool, aggregation, exit codes
internal/contractdiff  schema comparison
internal/render     human and JSON output
internal/tui        optional Bubble Tea UI
internal/cli        argument handling
test/e2e            runs the real binary
test/docker         Docker Compose integration test
```

The domain (`validate`, `schema`, `suggest`) is testable without the CLI, the filesystem or Docker. Run a fuzzer with e.g. `go test ./internal/envfile -run '^$' -fuzz FuzzParse -fuzztime 30s`. Design decisions are recorded in [`docs/decisions`](docs/decisions).

1. Create a branch (`git checkout -b feature/short-name`)
2. Run `make check` (and `make docker-test` if you touched Docker or Compose behavior)
3. Commit your changes and open a pull request

<p align="right">(<a href="#readme-top">back to top</a>)</p>

<!-- LICENSE -->
## License

No license has been chosen yet. Until one is added, all rights are reserved by the author.

<p align="right">(<a href="#readme-top">back to top</a>)</p>

<!-- CONTACT -->
## Contact

whoisclebs - [github.com/whoisclebs](https://github.com/whoisclebs)

Project Link: [https://github.com/whoisclebs/heimdall-lint](https://github.com/whoisclebs/heimdall-lint)

<p align="right">(<a href="#readme-top">back to top</a>)</p>

<!-- ACKNOWLEDGMENTS -->
## Acknowledgments

* [Best-README-Template](https://github.com/othneildrew/Best-README-Template)
* [Charm](https://charm.sh) for Bubble Tea and Lip Gloss
* [NO_COLOR](https://no-color.org) convention
* [Spring Boot relaxed binding](https://docs.spring.io/spring-boot/reference/features/external-config.html) rules for environment variable names

<p align="right">(<a href="#readme-top">back to top</a>)</p>

<!-- MARKDOWN LINKS & IMAGES -->
[go-shield]: https://img.shields.io/badge/Go-1.26-00ADD8?style=for-the-badge&logo=go&logoColor=white
[go-url]: https://go.dev
[docker-shield]: https://img.shields.io/badge/Docker-scratch_image-2496ED?style=for-the-badge&logo=docker&logoColor=white
[docker-url]: https://www.docker.com
[static-shield]: https://img.shields.io/badge/binary-static-2ea44f?style=for-the-badge
