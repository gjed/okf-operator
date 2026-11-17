# Proposal

## Why

Every later stage depends on one decision: which knowledge pages a range of source commits affects. That decision is also the main cost control, because a change that routes nowhere must never reach the model. `plan` makes it deterministic, testable against real merged PRs, and free of model calls.

This is Phase 2, build-order step 1 of the plan, and it ships in release step 1 together with `write` and `propose`.

## What Changes

- New `okf-operator plan` subcommand. It reads the cursor for a source repo, diffs from the cursor to the source default branch HEAD, matches the changed paths against the routing map, and prints a versioned JSON plan to stdout.
- Routing map schema (`routing.yaml`, read from the knowledge repo checkout) with load-time validation. Each source repo is bound to exactly one bundle, and its routes may only target pages inside that bundle.
- Cursor format and location: one file per source repo on the `kb-state` branch of the knowledge repo. This change only reads it. Writing and advancing the cursor belong to `propose`.
- First-run behaviour: a source with no cursor plans nothing and only anchors the cursor at HEAD. Backfill is `reconcile`'s job.
- GitHub access for the CLI: a `GITHUB_TOKEN` for local use, or a GitHub App ID and private key that mint a short-lived installation token.
- OTel span and metrics for each plan run. A log line for every changed path that routes nowhere.
- Example `routing.yaml` under `examples/` and fixture-based tests built from recorded compare responses.

## Capabilities

### New Capabilities

- `routing`: the routing map schema, its validation rules and the matching from changed source paths to knowledge pages.
- `source-cursor`: where and how the last processed source commit is stored per source repo, and how a missing or diverged cursor is treated.
- `change-plan`: the `plan` subcommand, its inputs, its JSON output contract and its failure modes.
- `github-access`: how the CLI authenticates to GitHub (token or App installation token) and the minimum permissions it needs.

### Modified Capabilities

None. `cli` from `bootstrap-repository` is reused unchanged: `plan` registers as a subcommand under the existing contract.

## Impact

- Depends on `bootstrap-repository` being applied first.
- New dependencies: `github.com/google/go-github` (REST client), `github.com/bmatcuk/doublestar/v4` (`**` globs), `go.yaml.in/yaml/v3` (routing map), and `github.com/golang-jwt/jwt/v5` (App JWT, same as the sibling repo).
- Knowledge repos gain a documented `.kb/routing.yaml` schema and a `kb-state` branch layout. Both are public contracts from here on and are versioned.
