# Design

## Context

The repository is empty. Tooling conventions (Makefile targets, golangci-lint v2, semantic-release, distroless image, Renovate) follow `github.com/gjed/github-activity-exporter` so that both repos are maintained the same way. See proposal.md for the motivation.

## Goals / Non-Goals

**Goals:**

- A binary that later changes extend by registering one subcommand each, without touching the dispatcher.
- Logging and telemetry set up once, before any subcommand runs.
- CI, release and image pipelines working from the first merge, so the first feature change ships through them.

**Non-Goals:**

- Any pipeline subcommand, the controller mode, CRDs or a Helm chart. Each gets its own change.
- A config file format. Subcommands take flags and environment variables until a change needs more.

## Decisions

### Standard library dispatcher instead of cobra or urfave/cli

A `map[string]command` keyed by name, where each command owns a `flag.FlagSet` and a `Run(ctx, args) error`. Global flags (`--log-format`, `--log-level`) are parsed from a top-level FlagSet before the subcommand name.

- Why: five or six subcommands with a few flags each do not justify a framework dependency. The sibling repo uses no CLI framework either.
- Alternative: cobra, which gives shell completion and nested commands. Neither is needed now, and switching later only touches `cmd/`.

### Errors map to exit codes in one place

A usage error is wrapped in a sentinel type, and `main` maps it to exit 2. Any other error maps to 1. Subcommands return errors and never call `os.Exit`, so they stay testable.

### Version through ldflags, with a `debug.ReadBuildInfo` fallback

`-X main.version`/`-X main.commit` are set by the Dockerfile and the Makefile, matching the sibling repo. When they are absent, `version` reads VCS info from `runtime/debug.ReadBuildInfo()` so that `go install` builds still report a commit.

### OpenTelemetry through autoexport

`go.opentelemetry.io/contrib/exporters/autoexport` builds the trace and metric exporters from the standard `OTEL_*` variables and returns a no-op exporter when `OTEL_TRACES_EXPORTER`/`OTEL_METRICS_EXPORTER` are `none` or no endpoint is set.

- Why: it covers the spec's "no collector, no failure" requirement and both gRPC and HTTP without writing code for each protocol.
- Alternative: wire the otlptrace/otlpmetric exporters by hand as the sibling repo does. That means more code for the same behaviour.
- `SIMPLIFIED:` autoexport defaults to OTLP when nothing is set. Bootstrap forces `none` unless an `OTEL_EXPORTER_OTLP_*ENDPOINT` variable is present. The ceiling is that an exporter configured only through `OTEL_TRACES_EXPORTER=console` is ignored. The upgrade path is to honour the explicit `OTEL_*_EXPORTER` value when it is set.

### Layout

```text
cmd/okf-operator/        # main, dispatcher, version
internal/cli/            # command interface, global flags, exit-code mapping
internal/telemetry/      # slog + OTel setup and shutdown
```

`internal/` is used everywhere because the public API surface is the CLI and, later, the CRDs. Packages move to `pkg/` only when an outside consumer asks for them.

### Release and image

The pipelines are copied from the sibling repo and adapted: `release.yml` runs semantic-release with the gjed release App token, and `image.yml` builds `linux/amd64,linux/arm64` when a release is published and pushes `ghcr.io/gjed/okf-operator`. Actions are pinned by SHA, as in the sibling repo.

## Risks / Trade-offs

- [Release App not installed on the new repo] → The first `release.yml` run fails. Install the gjed release App on `okf-operator` once [gjed/github-configs-public#17](https://github.com/gjed/github-configs-public/pull/17) is applied.
- [Copied workflows drift from the sibling repo] → Accepted. Renovate keeps the action pins current in both repos.
- [Go version] → `go.mod` pins the same `go`/`toolchain` lines as the sibling repo, and Renovate bumps them.
