# Proposal

## Why

`plan` and `write` leave updated pages in a local checkout, but nothing turns them into a reviewable knowledge PR or moves the source cursor, so every run re-plans the same range. `propose` closes the loop from merged source PR to knowledge PR.

This is Phase 2, build-order step 3 of the plan, and completes release step 1 together with `plan` and `write` ([#5](https://github.com/gjed/okf-operator/issues/5)).

## What Changes

- New `okf-operator propose` subcommand (`internal/propose`, `internal/cli/propose.go`). Inputs: `--plan <file>` (plan v1), `--report <file|->` (write report v1), `--knowledge-dir`, `--knowledge-repo` and `--knowledge-base <sha>` (the commit the checkout is at). Stdout is a versioned JSON result (PR number, URL, action, commit, cursor) that `gate` ([#4](https://github.com/gjed/okf-operator/issues/4)) can consume.
- Input checks before any write: plan and report must agree on `source`, `bundle`, `from` and `to`, list the same page paths, and the checkout must match the page-write "Propose input contract": every file under `--knowledge-dir` equals the `--knowledge-base` tree except the pages the report marks `changed`, which must differ. Only those pages are committed, read from `--knowledge-dir`.
- One bot branch per source, `okf/<owner>/<name>`, and at most one open bot PR per source. If one is open, the run adds a commit to it and appends a section to its body; `--knowledge-base` must equal the bot branch head. Otherwise the branch is reset to a new commit on the default branch head and a PR is opened.
- All GitHub writes go through the REST API (git data API for blobs, tree, commit and ref; pulls API). No git binary, so the distroless image works.
- The PR body links every merged source PR in `from..to` and lists pages changed, unchanged and dropped with reasons.
- Cursor write in `internal/cursor` (`Writer` next to `Reader`): compare-and-swap on `cursors/<owner>/<name>` on `kb-state`, an orphan commit when `kb-state` is missing. The cursor advances only after the branch update and the PR open or update succeed. An initial plan writes only the cursor. A run with no changed pages opens no PR and still advances the cursor.
- Two installation tokens per run, one per repo: knowledge repo `contents` and `pull_requests` write plus `metadata` read; source repo `contents`, `pull_requests` and `metadata` read (see design, "Access").
- OTel: the dispatcher's `propose` span plus counters `okf.propose.prs.opened`, `okf.propose.prs.updated` and `okf.propose.cursor.advances`.
- `githubtest` gains the git data, pulls, refs and contents-write endpoints.
- Declined changes are not recorded on `kb-state` in this change (see design, "Declined changes"); the issue gets a comment saying why.
- README `propose` usage section with the plan → checkout → write → propose order, `docs/state.md` and the `docs/github-app.md` permissions table.

## Capabilities

### New Capabilities

- `knowledge-proposal`: the `propose` subcommand, its inputs and input checks, the bot branch and PR lifecycle, the PR body, the ordering between PR and cursor, its JSON result and telemetry.

### Modified Capabilities

- `source-cursor`: adds how cursors are written and advanced (compare-and-swap, orphan `kb-state`, initial and no-change runs).
- `github-access`: adds the permissions `propose` requests.

## Impact

- New packages `internal/propose`; new `internal/cli/propose.go`; one line in `internal/cli/commands.go`; `internal/cursor` gains `Writer`.
- `internal/github/githubtest` gains endpoints. `internal/github/provider.go` is unchanged: `propose` builds two `Provider`s with one `Access` each.
- New public contracts, versioned: the `propose` result JSON v1, the bot branch name `okf/<owner>/<name>` and the PR body section marker.
- Overlap with [#4](https://github.com/gjed/okf-operator/issues/4): one line in `commands.go`, a README usage section and rows in the `docs/github-app.md` permissions table. Rebase only.
