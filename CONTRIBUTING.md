# Contributing

Usage is in the [README](README.md). This page covers working on the code.

## Prerequisites

- Go 1.26 or newer (the module pins the `go1.27.4` toolchain)
- [golangci-lint](https://golangci-lint.run/usage/install/) v2 (CI uses v2.15.0)
- [pre-commit](https://pre-commit.com/)
- Node.js 22.14+ and [pnpm](https://pnpm.io/) 12, only for the release tooling in `package.json`
- Docker, to build the image
- [Helm](https://helm.sh/docs/intro/install/) 4, to lint and render the chart in `deploy/chart`

## Build

```bash
make build
```

## Test

```bash
go test -race ./...
```

## Lint

```bash
make lint
```

## Pre-commit

```bash
pre-commit run --all-files
```

The hooks check YAML, whitespace and line endings, and lint Markdown with `markdownlint-cli2`. The generated `.opencode/` directory is excluded.

## Helm chart

The chart lives in `deploy/chart`. Its `crds/` directory is generated: `make generate` writes the CRDs to `deploy/crds` and copies them there, so never edit either copy by hand.

```bash
helm lint --strict deploy/chart
helm template okf-operator deploy/chart --namespace okf-operator
```

`Chart.yaml` keeps placeholder versions; the release workflow sets them.

## Release tooling

`package.json` and `pnpm-lock.yaml` drive semantic-release. Install with the frozen lockfile:

```bash
pnpm install --frozen-lockfile
```

Do not hand-edit `pnpm-lock.yaml`. Renovate updates it.

## Changes and specs

Behaviour changes are proposed and specified with [OpenSpec](https://github.com/Fission-AI/OpenSpec) under [`openspec/`](openspec/). See [AGENTS.md](AGENTS.md) for the workflow.

## Commits

Use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/). The commit type decides the release, see below.

## CI

| Job             | Trigger                   | Description                                                           |
| --------------- | ------------------------- | --------------------------------------------------------------------- |
| `changes`       | every PR + push to `main` | Skips lint, test and build for docs-only PRs                          |
| `lint`          | every PR + push to `main` | golangci-lint, `go mod tidy` and generated-file checks                |
| `test`          | every PR + push to `main` | `go test -race`, JUnit XML upload, coverage PR comment                |
| `build`         | every PR + push to `main` | `go build ./...`                                                      |
| `chart`         | PR touching `deploy/**`   | `helm lint`, `helm template`, chart CRD copy check                    |
| `release`       | push to `main`            | semantic-release: version tag + GitHub release                        |
| `image`         | release published         | Multi-arch image to `ghcr.io/gjed/okf-operator`                       |
| `publish-chart` | release published         | Helm chart, same version as the image, to `oci://ghcr.io/gjed/charts` |

## Release

Releases are automated. Merging to `main` lets semantic-release pick the next version from the commit types:

| Commit prefix                 | Version bump |
| ----------------------------- | ------------ |
| `feat:`                       | minor        |
| `fix:`, `perf:`               | patch        |
| `feat!:` / `BREAKING CHANGE:` | major        |
| `chore:`, `docs:`, `test:`    | no release   |
