# Proposal

## Why

`plan` decides which knowledge pages a source range affects, but nothing produces the pages yet. `write` is the stage that turns routed source changes into updated OKF pages with tool-stamped provenance, under a hard spend budget and with source content treated as untrusted.

This is Phase 2, build-order step 2 of the plan. It ships in release step 1 together with `plan` and `propose` ([#5](https://github.com/gjed/okf-operator/issues/5)).

## What Changes

- New `okf-operator write` subcommand. It reads a plan v1 JSON document (`--plan <file|->`), fetches the diff hunks for each planned page's `source_paths` only, makes one model call per page, validates the result, stamps provenance and writes the changed pages in place under `--knowledge-dir`. Stdout is a versioned JSON report.
- Model client (`internal/model`): one client for the OpenAI-compatible chat completions API at a configurable base URL. Config: `--model-base-url` / `OKF_MODEL_BASE_URL`, `--model` / `OKF_MODEL`, and `OKF_MODEL_API_KEY` (env only, optional). No native provider clients. The client never receives GitHub credentials and returns text only.
- Provenance package (`internal/provenance`) with `Fields`, `Parse` and `Stamp`. `write` stamps `source_repo`, `source_sha`, `source_paths`, `updated_at` and `generated_by` after the model returns and overwrites any of those keys the model wrote. [#4](https://github.com/gjed/okf-operator/issues/4)'s gate imports it.
- Per-file patches: a new `GitHubDiffer.Patches` method returns compare API patches for the requested paths. A missing or truncated patch drops the page; it is never sent partial. `plan.ChangedPath`, `Changes` and the plan JSON v1 golden files do not change.
- Source PR context: titles and bodies of the PRs that introduced the commits in `from..to`, placed with the hunks in a delimited untrusted block of the prompt.
- Prompts in `prompts/`, embedded with `go:embed`.
- Knowledge repo config read from the checkout: `.kb/profile.md` (writing guidance) and `.kb/templates/<type>.md` (page skeleton per concept type), both optional for updates. Two style-reference pages are picked deterministically from the same bundle.
- Per-page validation, one retry with the validator error, then drop.
- Budget: `--max-pages` and `--max-tokens-per-page`, checked before the first model call. Over budget exits 1 with no model call.
- All-or-nothing writes: pages are written only after every page has been processed. A run that exits non-zero leaves the checkout untouched.
- OTel: the dispatcher's `write` span plus `okf.write.*` counters for tokens, pages by result and validation failures.
- `state: create` and `state: missing` pages are reported as dropped. Page creation moves to a new issue (see design).
- README usage section and `docs/write.md` (report and profile contract).

## Capabilities

### New Capabilities

- `page-write`: the `write` subcommand, its inputs, budget, validation, routed-set enforcement, the JSON report and the working-tree contract that `propose` consumes.
- `provenance`: the provenance frontmatter fields, how they are parsed and stamped, and the guarantee that the tool, not the model, sets them.
- `model-client`: the OpenAI-compatible model endpoint configuration, request shape, credential isolation and secret redaction.

### Modified Capabilities

- `github-access`: adds the permissions `write` requests (read-only `contents`, `metadata` and `pull_requests` on the source repo).

## Impact

- New packages `internal/model`, `internal/provenance`, `internal/write`; new `internal/cli/write.go`; one line in `internal/cli/commands.go`; `Env` gains `Stdin`.
- `internal/plan/github.go` gains `Patches`. `internal/github/githubtest` gains the "PRs for a commit" endpoint. `internal/github/provider.go` and `redact.go` are unchanged: the model API key is registered with the same `github.Secrets`.
- New `prompts/` directory embedded in the binary.
- New public contracts, versioned: the `write` report JSON v1, `.kb/profile.md` and `.kb/templates/<type>.md`, and the provenance keys.
- Dependency: the OKF validator. `github.com/skosovsky/okf` is a Go module (`validator.ValidateSource`, v0.2.7) but the repository has no license file, so it is not redistributable in an Apache 2.0 binary. See design, "Page validation": this needs a decision before implementation.
- Overlap with [#4](https://github.com/gjed/okf-operator/issues/4): one line in `commands.go` and a README usage section. `internal/provenance` API is frozen once this proposal is approved.
