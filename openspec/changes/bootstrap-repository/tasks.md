# Tasks

## 1. Repository scaffolding

- [ ] 1.1 Add `LICENSE` (Apache 2.0), `.gitignore`, `.markdownlint.yaml`, `.pre-commit-config.yaml` (excluding the generated `.opencode/` OpenSpec skills) and the Renovate config following the sibling repo, and verify `pre-commit run --all-files` passes
- [ ] 1.2 Run `go mod init github.com/gjed/okf-operator` with the same `go`/`toolchain` lines as the sibling repo, and verify `go mod tidy` leaves no diff
- [ ] 1.3 Add `.golangci.yml` (v2, local-prefix `github.com/gjed/okf-operator`) and `Makefile` targets `build`, `test` and `lint`, and verify `make lint` runs on the empty module

## 2. CLI core

- [ ] 2.1 Implement the dispatcher, the command interface and exit-code mapping in `internal/cli`, and verify table tests for an unknown command (exit 2), no command (exit 2), a bad flag (exit 2) and a runtime error (exit 1)
- [ ] 2.2 Implement `help`, `-h` and `--help` at top level and per subcommand, and verify tests that assert every registered subcommand is listed and exit 0
- [ ] 2.3 Implement `version` with the ldflags and `ReadBuildInfo` fallback, and verify a test covering `dev` and an ldflags-injected version and commit
- [ ] 2.4 Implement `--log-format` and `--log-level` with `log/slog` writing to stderr, and verify tests that JSON output carries `time`/`level`/`msg` and that an invalid level exits 2
- [ ] 2.5 Implement OTel setup and shutdown in `internal/telemetry` through autoexport, forced to `none` without an endpoint, and verify a test that runs a command with no `OTEL_*` vars and no listener and finishes in under 1s with no dial attempt

## 3. Delivery pipelines

- [ ] 3.1 Add `ci.yml` (changes filter, lint, test, build) with SHA-pinned actions, and verify it passes on the bootstrap PR
- [ ] 3.2 Add a multi-stage distroless `Dockerfile` and `image.yml`, and verify `docker build --build-arg VERSION=0.0.0-test .` succeeds and the image's `version` prints `0.0.0-test`
- [ ] 3.3 Add `release.yml`, `.releaserc.js`, `package.json` and `pnpm-lock.yaml`, and verify `pnpm exec semantic-release --dry-run --no-ci` resolves the config locally

## 4. Documentation

- [ ] 4.1 Write `README.md` (purpose, status, build, usage of `version`), `CONTRIBUTING.md` and `AGENTS.md` (Conventional Commits, OpenSpec worktree workflow), and verify the markdown lint passes

## Workflow follow-up

- Archive the change in the same PR that implements it.
