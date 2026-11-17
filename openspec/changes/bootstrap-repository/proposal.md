# Proposal

## Why

The repository is empty. Every later change (`plan`, `write`, `propose`, `gate`, `reconcile`, `controller`) needs the same base first: a Go module, a single binary with a subcommand dispatcher, CI, image and release automation, and the licence. Doing this once, in its own change, keeps each feature change focused on its own behaviour.

This belongs to Phase 2 of the plan (the writer tool). It comes before build-order step 1 (`plan`) and is required by release step 1 (the first public release after Phase 2).

## What Changes

- Go module `github.com/gjed/okf-operator`, binary `okf-operator` built from `cmd/okf-operator`.
- A subcommand dispatcher built on the standard library (`flag.FlagSet` per subcommand), with `version` and `help` only. Pipeline subcommands arrive in their own changes.
- Shared runtime plumbing the subcommands reuse: structured logging through `log/slog` (text or JSON), and OpenTelemetry SDK setup driven by the standard `OTEL_*` environment variables, disabled when no exporter is configured.
- Exit code contract: 0 success, 1 runtime failure, 2 usage error.
- Repository hygiene: Apache 2.0 `LICENSE`, `README.md`, `AGENTS.md`, `CONTRIBUTING.md`, `.gitignore`, `.golangci.yml`, `Makefile`, `.pre-commit-config.yaml`, `.markdownlint.yaml`, Renovate config.
- CI (`ci.yml`): lint, test and build on PRs and on `main`, with the docs-only skip used in `github-activity-exporter`.
- Release (`release.yml`, `.releaserc.js`, `package.json`): semantic-release on `main`.
- Image (`image.yml`, `Dockerfile`): multi-arch distroless image on GHCR, built on release tags.

## Capabilities

### New Capabilities

- `cli`: the `okf-operator` command line contract: subcommand dispatch, version reporting, help, exit codes, logging and telemetry flags shared by every subcommand.

### Modified Capabilities

None.

## Impact

- New repository content only, no existing users.
- Dependencies: the OpenTelemetry Go SDK and OTLP exporters. No CLI framework.
- GitHub: needs `gjed/okf-operator` to exist ([gjed/github-configs-public#17](https://github.com/gjed/github-configs-public/pull/17)). The release workflow needs the same release App secrets as the other gjed repos.
