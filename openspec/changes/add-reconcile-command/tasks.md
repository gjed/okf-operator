# Tasks

## 1. Write reads plan version 2

- [x] 1.1 Add the plan version 2 types (`reconcile` object, `omitempty`) to `internal/plan`, make `write.DecodePlan` accept version 1 without and version 2 with a well-formed `reconcile` object, add `GitHubDiffer.Files` (contents API at `to`, text only, at most 64 KiB) and let the GitHub fake serve contents at a commit SHA; in `write`, fetch patches per distinct page base, send whole files for evidence `files` (new prompt section, neutralised, reason `file_unavailable`), no PR context for version 2; keep the `gate` and `review` commands on version 1 only; verify tests for: version 1 golden files and existing tests unchanged, per-base patches, whole-file evidence in a create prompt, oversized or missing file → `file_unavailable` and no model call, delimiter in a source file neutralised, malformed `reconcile` objects rejected, `gate` and `review` exit 1 on version 2

## 2. Propose reads plan version 2

- [ ] 2.1 Make `propose` take the PR flow for every version 2 plan (initial and `from` equal to `to` included), skip the source PR listing, write a reconcile PR body section and commit message with the cursor state, page reasons and deletion candidates, log each candidate, and advance the cursor with the existing compare-and-swap; verify tests for: initial version 2 plan opens the PR then creates the cursor, diverged cursor moved only after the PR update, failed PR call leaves the cursor, deletion candidates in the body and not in the commit

## 3. Reconcile command

- [ ] 3.1 Add `internal/reconcile` (tree at `to` through the recursive trees API, truncated tree → error, page selection from routing and provenance, compare per distinct base, deletion candidates, plan version 2, telemetry) and `internal/cli/reconcile.go` registered in `commands.go`; verify tests against `internal/github/githubtest` for: backfill with no cursor and no pages, stale page refreshed, deletion candidate never written or deleted, renamed path routed to its page, diverged cursor re-anchored only after the PR (and not on a failed run), truncated tree exits 1, unknown source exits 1, span and counters
- [ ] 3.2 Document `reconcile` in the README usage, `docs/reconcile.md` (plan version 2 contract), `docs/state.md` (re-anchoring) and `docs/write.md` (whole-file evidence), and verify the markdown lint passes
