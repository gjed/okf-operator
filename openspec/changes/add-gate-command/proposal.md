# Proposal

## Why

Auto-merge of a knowledge PR must be decided by rules that give the same answer every time for the same PR, not by a model. `gate` evaluates those rules and prints a machine-readable verdict that the controller and CI branch on. The advisory LLM review ([#6](https://github.com/gjed/okf-operator/issues/6)) is a later input that can only block.

This is release step 2 of the plan.

## What Changes

- New `.kb/policy.yaml` v1 schema in `internal/policy`: an auto-merge allowlist of concept types and bundles (empty by default, so nothing auto-merges until configured) and per-page and per-PR diff line limits. Strict decoding and an all-errors `Validate`, same shape as `internal/routing`. `examples/policy.yaml` and `docs/policy.md` document it.
- New `okf-operator gate --knowledge-repo owner/name --pr <number> --plan <file|->` subcommand. It reads the PR, its files and their contents through the GitHub API, with no local checkout.
- `policy.yaml` and `routing.yaml` are read from the PR's base branch, never from the head. A PR that touches `.kb/` fails the change-shape rule.
- Six deterministic rules, each reported separately: provenance, routed set, allowlist, change shape, size, links.
- A versioned JSON report on stdout (`version`, `pr`, `head_sha`, `pass`, `rules[]` with `name`, `pass`, `details`). `pass` is the conjunction of every rule, so a rule added later, such as the LLM review, can only turn `pass` into `false`.
- OTel: attributes on the dispatcher's `gate` span and an `okf.gate.rule_results` counter by rule and outcome.
- In App mode, two narrowed installation tokens: the knowledge repo with read-only `contents`, `metadata` and `pull_requests`, and the plan's source repo with read-only `contents` and `metadata`.

Out of scope: the LLM review ([#6](https://github.com/gjed/okf-operator/issues/6)), merging the PR, posting statuses or comments, and page creation (`gate` fails any added page).

## Capabilities

### New Capabilities

- `gate-policy`: the `.kb/policy.yaml` schema, its location on the base branch, its strict decoding and validation rules.
- `auto-merge-gate`: the `gate` subcommand, its inputs, the six rules, the JSON report contract, its exit codes and its telemetry.

### Modified Capabilities

- `github-access`: adds the permissions `gate` requests in App mode.

## Impact

- Depends on [#12](https://github.com/gjed/okf-operator/pull/12) (merged): `internal/plan` JSON v1, `internal/routing`, `internal/github`.
- The provenance rule and the allowlist's page `type` lookup depend on `internal/provenance` from [#3](https://github.com/gjed/okf-operator/issues/3) (lane B1). Every other rule is built first. No second frontmatter parser is written: `internal/provenance` gains an exported `Type` reader on top of its existing frontmatter split.
- `internal/github/githubtest` gains fake endpoints for pull requests and pull request files. `internal/github` itself does not change.
- One added line in `internal/cli/commands.go` and a `gate` section in `README.md`. Both overlap with B1 and are resolved by rebase.
- `.kb/policy.yaml` becomes a public, versioned contract. The gate report JSON is versioned from v1.
- No new module dependencies are planned. Link extraction uses the standard library (see design).
