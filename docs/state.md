# Operator state

`okf-operator` keeps its state in the knowledge repo, on a branch named `kb-state`. The branch holds no pages and is never merged into the default branch.

## Layout

```text
kb-state
└── cursors/
    └── <owner>/
        └── <name>     # cursor for source repo <owner>/<name>
declined/
└── <owner>/
    └── <name>         # declined ranges of source repo <owner>/<name>
```

## Cursors

A cursor is the last source commit the operator processed for one source repo. Every run diffs from the cursor to the source default branch HEAD, never from the event that triggered it, so duplicate and out-of-order webhooks are harmless.

- Path: `cursors/<owner>/<name>`, no extension. GitHub owner and repo names cannot contain `/`, so the path is unambiguous.
- Content: one full 40-character lowercase hex commit SHA followed by a newline.
- One file per source, so updating one source never rewrites another, and `git log cursors/<owner>/<name>` is that source's processing history.

```bash
gh api 'repos/acme/kb/contents/cursors/acme/api?ref=kb-state' --jq .content | base64 -d
```

`plan` only reads cursors. `propose` writes and advances them. The controller writes only `declined/`.

## How plan treats the cursor

| Cursor                                      | Result                                                                            |
| ------------------------------------------- | --------------------------------------------------------------------------------- |
| File or `kb-state` branch missing (404)     | Initial plan: `initial: true`, no pages, `to` is the source HEAD                  |
| Knowledge repo missing or not readable      | Exit 1, so a wrong `--knowledge-repo` or lost access never looks like a first run |
| Equal to the end commit                     | Empty plan: `from` equals `to`, no changed paths                                  |
| Ancestor of the end commit                  | Normal plan of the range                                                          |
| Not an ancestor (force-push, rewrite, gone) | Exit 1: cursor diverged, `reconcile` required                                     |
| Malformed content                           | Exit 1: the error names the file                                                  |
| Any other API error                         | Exit 1, so an outage never looks like a first run                                 |

A source with no cursor is never backfilled by `plan`. The first run only anchors the range end, and backfill is `reconcile`'s job.

## How propose advances the cursor

`propose` moves the cursor from the plan's `from` to its `to`, and only after the knowledge PR branch and the PR itself were updated successfully.

| Case                                          | Result                                                                 |
| --------------------------------------------- | ---------------------------------------------------------------------- |
| Cursor holds `from`                           | Rewritten with `to`, conditional on the file's blob SHA                |
| Initial plan, no cursor file                  | Created with `to`; no knowledge PR                                     |
| Initial plan, `kb-state` missing              | `kb-state` created by a parentless commit holding only the cursor file |
| `from` equals `to`                            | Nothing written                                                        |
| Cursor differs from `from`, or exists already | Exit 1 before any write: another run moved it, re-run `plan`           |
| Cursor changed between read and write         | GitHub answers 409 or 422, exit 1; the other run's cursor is kept      |

Each advance is one commit on `kb-state`: `okf: advance cursor for <owner>/<name> to <sha>`.

The cursor moves when `propose` pushes, not when the PR merges, so it is ahead of the knowledge repo's default branch for as long as a bot PR is open. It never moves backwards. When the open bot PR falls behind the default branch or conflicts with it, the controller repairs the PR before the run builds on it, and neither repair reads or writes the cursor: see [Repairing a bot PR](#repairing-a-bot-pr).

## Repairing a bot PR

The controller reads the open bot PR's `mergeable_state` at the start of every run. GitHub computes it lazily and answers `unknown` meanwhile, so the read is repeated up to five times with a doubling delay from 500 ms (about 7.5 s); a state still `unknown` is treated like `clean`, and the run carries on as before.

| `mergeable_state` | What the run does                                                                                                                                                    |
| ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `behind`          | `PUT /repos/{owner}/{repo}/pulls/{n}/update-branch` with `expected_head_sha` set to the head the run read; no model call. The run continues on the new head.         |
| `dirty`           | The conflicting pages are regenerated on top of the default branch head and pushed as a commit with two parents, the default branch head and the PR head. See below. |
| anything else     | Nothing: the run checks out the PR head as before.                                                                                                                   |

A 422 from update-branch means the head moved after the run read it, or the branch no longer merges cleanly. The run fails before it writes anything else, and the next run reads the state again.

For a `dirty` PR the run reads the trees of the default branch head, the PR head and their merge base with the git data API, so no git binary is needed. A page conflicts when both the bot branch and the default branch changed it since the merge base and the two versions differ; two identical changes are not a conflict, and two edits to different lines of one page are, because only the model can merge prose. For those pages only, `write` runs over the PR's whole source range, read from the `<!-- okf:range FROM..TO -->` markers of the PR body, in a checkout of the default branch head. A page a human edited on the default branch is regenerated like any other: the human's version is the page the model updates, and the new provenance records the regeneration. The commit keeps the bot branch's version of every page only the bot branch changed and the default branch's version of every page only the default branch changed, and it fast-forwards the bot branch (`force: false`), so the PR keeps its number and review history. A conflicting page that `write` leaves unchanged or drops keeps the default branch's version. The PR body gains a "Conflict repair" note that lists the regenerated pages.

The repair refuses, and the run fails at the `repair` stage with the PR number and the reason, when it cannot know the range: the body records omitted earlier ranges, holds a reconcile section, or its ranges do not chain. It also refuses when a conflicting path is not a page the range routes. Resolve such a PR by hand or close it; closing it records its ranges in `declined/` and the next run starts a new branch.

Every run with an open bot PR also dismisses every approval the review App posted on a commit other than the PR's current head (`PUT /repos/{owner}/{repo}/pulls/{n}/reviews/{id}/dismissals`), with the message "okf-operator: the bot branch moved off `<sha>`." It does so as soon as the head is settled, after update-branch or the regeneration commit and before the checkout of the new head, so a repair that fails afterwards, or a failed dismissal, is cleared by the next run. Approvals by anyone else and the review App's approval of the current head are left alone. Nothing is reviewed or approved again: the repaired head is gated, reviewed and approved only when a later run pushes to it. This does not depend on the knowledge repo's "Dismiss stale pull request approvals" setting, which stays recommended for everything else.

After a repair the run goes on as usual: it checks out the repaired head, plans from the cursor and, if the source has moved on, `propose` adds the new range on top.

## Re-anchoring

A diverged cursor, or a source with no cursor that needs a backfill, goes through `reconcile`, `write` and `propose`; see [Reconciling a source](reconcile.md). `reconcile` reads the cursor and writes it into its plan as `from` (empty and `initial: true` when there is none), and `reconcile.cursor` says how it relates to `to`. `propose` then moves the cursor from that `from` to `to` with the same compare-and-swap as above. There is no unconditional cursor write.

| `reconcile.cursor` | Cursor after a successful `propose`                                          |
| ------------------ | ---------------------------------------------------------------------------- |
| `missing`          | Created with `to` (and `kb-state` with it), after the bot PR if there is one |
| `diverged`         | Re-anchored: rewritten with `to`, after the bot PR is opened or updated      |
| `ancestor`         | Moved to `to`, like a `plan` run                                             |
| `head`             | Not written; the bot PR still carries the refreshed pages                    |

Unlike an initial `plan`, an initial reconcile plan takes the full `propose` path: checkout checks, commit and bot PR first, cursor last. A run that fails before the cursor write leaves the cursor where it was, so a diverged cursor stays diverged and `plan` keeps exiting 1 until a reconcile run succeeds. A cursor moved by another run after `reconcile` read it fails `propose`; re-run `reconcile`.

## Declined ranges

When a bot PR from `okf/<owner>/<name>` is closed without merging, the next controller run of that source records the source ranges of its body in `declined/<owner>/<name>`. Each `<!-- okf:range FROM..TO -->` marker `propose` wrote becomes one line:

```text
<pr-number> <from or -> <to>
```

`-` stands for the empty start of an initial range. Lines are sorted by PR number, each PR is recorded once, and the write is a compare-and-swap on the file, like the cursor; a missing `kb-state` is created the same way. The file is bookkeeping for `reconcile` and for people: it never blocks a later proposal of the same range and never moves the cursor. The controller looks at the 30 most recently updated closed bot PRs of the source on each run.

```bash
gh api 'repos/acme/kb/contents/declined/acme/api?ref=kb-state' --jq .content | base64 -d
```
