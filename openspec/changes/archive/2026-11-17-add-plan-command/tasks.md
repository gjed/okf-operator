# Tasks

## 1. Routing map

- [x] 1.1 Implement the v1 schema types, strict YAML decoding and `Load(path)` in `internal/routing`, and verify tests that unknown keys and `version: 2` fail
- [x] 1.2 Implement validation (duplicate source, bundle containment, `..`, non-`.md` targets, invalid globs) that collects every error, and verify table tests with one case per rule plus one multi-error file
- [x] 1.3 Implement matching with `doublestar` (exclude first, fan-out to several pages, sorted output), and verify table tests covering `**`, exclude-over-route and fan-out
- [x] 1.4 Add `examples/routing.yaml` and `docs/routing.md` describing the schema, and verify a test that loads the example without errors and the markdown lint passes

## 2. GitHub access

- [x] 2.1 Implement token selection (App settings, then `GITHUB_TOKEN`, else usage error) in `internal/github`, with the shared `--app-id` / `--app-private-key-file` flag binding and `GITHUB_API_URL` base URL, and verify tests for all three branches, including exit 2 when neither is set and when only one App setting is set
- [x] 2.2 Implement App JWT signing, installation lookup and narrowed installation tokens with an in-memory per-owner cache, and verify `httptest` tests that the request body carries only the requested repositories and permissions
- [x] 2.3 Add a redacting `slog` handler and a test that runs App mode at debug level and asserts the key PEM and the token never appear in captured logs or span attributes
- [x] 2.4 Document the required App permissions in `docs/github-app.md` and verify the markdown lint passes

## 3. Cursor

- [x] 3.1 Implement `cursor.Read` against the contents API on `kb-state`, with path `cursors/<owner>/<name>`, and verify `httptest` tests for found, file 404, branch 404 (both mean not found), malformed content (error) and a 500 (error, not "not found")
- [x] 3.2 Document the `kb-state` layout in `docs/state.md` and verify the markdown lint passes

## 4. Plan

- [x] 4.1 Define the plan JSON types (version 1) and deterministic encoding, and verify a golden-file test that a fixture plan encodes byte-identically on repeated runs
- [x] 4.2 Implement the compare-based `SourceDiffer` (ahead/identical/behind/diverged, 404 counted as diverged, renames with previous path, fail closed at 300 files), and verify tests against recorded compare responses in `test/fixtures/compare/`
- [x] 4.3 Implement `plan.Build`: source not onboarded, missing cursor (initial), cursor at HEAD, routed, unrouted and excluded paths, page state from the checkout, `--to` override. Verify one test per scenario in the `change-plan` and `source-cursor` specs, using fakes for the differ, cursor and checkout
- [x] 4.4 Log each unrouted path once at info level, and verify a test that captures the logs and asserts one line per unrouted path with the source repo
- [x] 4.5 Set the plan attributes on the dispatcher's `plan` root span (no second span) and add the four counters, and verify a test with the in-memory span and metric exporters that asserts names and attributes

## 5. CLI wiring

- [x] 5.1 Register `plan` in `internal/cli/commands.go` with its flags (`--source`, `--knowledge-repo`, `--knowledge-dir`, `--routing`, `--to`, `--app-id`, `--app-private-key-file`), and verify `okf-operator plan -h` lists them and a missing `--source` exits 2
- [x] 5.2 Add an end-to-end test that runs the built binary against an `httptest` GitHub fake (through `GITHUB_API_URL`) and a temp knowledge checkout, and verify that stdout matches the golden plan and the exit code is 0
- [x] 5.3 Document `plan` usage in `README.md` with a copy-pasteable `GITHUB_TOKEN` example against a public repo, and verify the command runs as written

## Workflow follow-up

- Archive the change in the same PR that implements it.
- Before the pilot: run `plan` with `--to` against 10 real merged PRs from the pilot repos, and check the routed pages by hand against the plan's expectations.
