# Proposal

## Why

`plan` fails closed on a diverged cursor and on ranges of 300 or more files, and points to `reconcile`, which does not exist yet. A source with no cursor is never backfilled: its first `plan` only anchors the cursor. Pages also drift when a route changes, a source file is renamed or deleted, or a page was dropped by an earlier run, because `plan` only looks at the files changed since the cursor ([#7](https://github.com/gjed/okf-operator/issues/7)).

## What Changes

- New subcommand `okf-operator reconcile --source owner/name --knowledge-repo owner/name --knowledge-dir <path> [--routing <file>] [--to <sha>]`, one source per run. It lists the source tree at `to` (default the source HEAD) with the recursive git trees API, routes every path through the routing map, reads each page's provenance from the knowledge checkout, and prints a plan JSON document of version `2`. A truncated tree exits 1. It calls no model and writes nothing.
- Pages are selected from the tree and provenance, never from a compare file list: a routed page that does not exist is `create` (route with `type`) or `missing`, reason `backfill`; an existing page without a usable `source_sha` is reason `backfill`; a page whose `source_sha` is an ancestor of `to` and whose routed paths changed since is reason `stale`, or `rename` when one of its `source_paths` no longer exists. Pages at `to`, or with no routed change since their `source_sha`, are left out.
- Pages whose routes and `source_paths` match nothing at `to` are reported as deletion candidates. No file is ever deleted.
- Plan version `2` is plan version `1` plus a `reconcile` object: the cursor state, a per-page `reason`, `evidence` (`hunks` or `files`) and `base`, and the deletion candidates. `plan` keeps printing version `1`, byte for byte.
- `write` accepts plan version `1` or `2`. For a version 2 plan it fetches the compare patches of each page from that page's `base` to `to`, and for pages with evidence `files` it sends the whole source files at `to` as untrusted data: text only, at most 64 KiB per file, otherwise the page is dropped with the new reason `file_unavailable`. Version 2 prompts carry no source PR context. Budget, validation, provenance and the working-tree rules are unchanged.
- `propose` accepts plan version `1` or `2`. A version 2 plan goes through the PR flow even when the cursor is missing or already at `to`, its PR body section is a reconcile section that lists the deletion candidates, and the cursor moves from the `from` the plan read to `to` through the existing compare-and-swap, only after the PR is opened or updated. A diverged cursor is re-anchored this way.
- `gate` and `review` keep exiting 1 on a version 2 plan, as their specs already require (plan version 1 only), so a reconcile PR always goes to a human. Since the shared plan decoder now reads version 2, both commands check the version themselves.
- Telemetry: the dispatcher's `reconcile` span gets the range, cursor state and counts; counters `okf.reconcile.pages` (by `okf.reconcile.reason`), `okf.reconcile.deletion_candidates` and `okf.reconcile.re_anchors`.

## Capabilities

### New Capabilities

- `reconcile`: the `reconcile` subcommand, page selection from the tree at `to`, deletion candidates, plan version 2 and its telemetry.

### Modified Capabilities

- `page-write`: plan version 2 input, per-page compare base, whole-file evidence and the `file_unavailable` reason.
- `knowledge-proposal`: plan version 2 input, reconcile PR body section, cursor rules for reconcile plans.
- `source-cursor`: a diverged cursor is re-anchored by `reconcile` followed by `propose`.

## Impact

- New `internal/reconcile` and `internal/cli/reconcile.go`; changes to `internal/plan` (version 2 types, `GitHubDiffer.Files`), `internal/write`, `internal/propose`, `internal/cli/gate.go` and `internal/cli/review.go` (version check only), `prompts/page.md`, `prompts/create.md`, the GitHub fake.
- `plan` output and its golden file are unchanged. A `write` or `propose` from an older release rejects a version 2 plan with a version error.
- No change to `internal/gate`, `internal/review`, the `auto-merge-gate` and `llm-review` specs, `routing` or `provenance`.
- Docs: README usage, `docs/reconcile.md`, `docs/state.md`, `docs/write.md`.
