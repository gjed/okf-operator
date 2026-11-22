# Design

## Context

`plan` diffs the cursor on `kb-state` to the source HEAD with the compare API and routes the changed paths. `write` sends each page the compare patches of `from..to` for its `source_paths`, stamps `source_sha: to`, and `propose` commits the result to the bot branch and moves the cursor from the plan's `from` to its `to` with a compare-and-swap on the cursor blob. `plan` exits 1 on a diverged cursor (`ErrDiverged`) and on a compare listing 300 or more files (`ErrIncomplete`), and a first run only anchors the cursor.

Each generated page carries `source_sha` and `source_paths` in its frontmatter. `source_paths` are the paths that changed in the last run that wrote the page, not every routed path.

## Goals / Non-Goals

**Goals:**

- Bring every routed page of one source up to `to` without depending on the compare file list for page selection.
- Backfill a newly onboarded source, including pages for create routes.
- Report pages whose sources are gone, for a human.
- Re-anchor a diverged cursor, only after the reconcile PR is opened or updated.
- Reuse `write` and `propose` and keep `plan`'s version 1 output byte-identical.

**Non-Goals:**

- Deleting or moving pages.
- Several sources per run.
- Auto-merging reconcile PRs.
- A clone-based differ.

## Decisions

### Output: plan version 2 with a `reconcile` object

`reconcile` prints one plan document of `version: 2`. It has every version 1 key with the same meaning, plus `reconcile`:

- `cursor`: `missing`, `head`, `ancestor` or `diverged`, the state of the cursor relative to `to`.
- `pages`: one entry per plan page with `path`, `reason` (`backfill`, `stale` or `rename`), `evidence` (`hunks` or `files`) and `base` (the compare base for `hunks`, empty for `files`).
- `deletion_candidates`: `path`, `source_sha` and `source_paths` of each page whose sources are gone.

`from` is the cursor content `reconcile` read (empty when there is none), `initial` is true when there is none, and `to` is the reconciled commit. `changed_paths`, `unrouted` and `excluded` are empty: there is no range, and listing every unrouted file of the tree would be noise. `write` and `propose` read both versions through the same decoder, which checks that version 1 has no `reconcile` object and version 2 has one that covers exactly the plan pages. The Go field is `omitempty`, so `plan`'s output and golden file stay byte-identical.

Rejected:

- Several plan version 1 documents, one per page base, consumed unchanged. `propose` advances the cursor from the plan's `from`, so every document but the one whose `from` is the cursor would fail, the cursor can only move once, created pages and pages without provenance have no base at all, and the bot PR would get one section per document.
- A separate document type with its own `write` and `propose` code paths. It duplicates the budget, validation, provenance and verification rules that version 2 reuses unchanged.

### Page selection from the tree and provenance

The source tree at `to` comes from the recursive git trees API. A truncated listing exits 1: a partial tree would make routed pages look deleted. The tree's blob paths go through `routing.Source.Match`, which gives every page its routed paths at `to`.

The pages considered are every route target of the source, plus every `.md` page of the bundle whose provenance `source_repo` is the source. For each:

| Page state                                                             | Result                                                    |
| ---------------------------------------------------------------------- | --------------------------------------------------------- |
| Routed paths at `to`, page missing                                     | `create` (route `type`) or `missing`, `backfill`, `files` |
| Routed paths, no frontmatter or `source_sha`, or another `source_repo` | `update`, `backfill`, `files`                             |
| `source_sha` equals `to`                                               | Left out                                                  |
| `source_sha` not an ancestor of `to` (compare diverged or not found)   | `update`, `backfill`, `files`                             |
| `source_sha` an ancestor, compare lists 300 or more files              | `update`, `stale`, `files`, all routed paths              |
| `source_sha` an ancestor, no routed path changed since                 | Left out                                                  |
| `source_sha` an ancestor, routed paths changed                         | `update`, `stale` or `rename`, `hunks`, the changed paths |
| No routed path and no `source_paths` exists at `to`                    | Deletion candidate                                        |

For `hunks`, `source_paths` are the compare's changed paths of `base..to` (new and previous names, as `plan` routes them) that route to the page, so a deleted routed file is part of the evidence. `rename` means the compare reports a path of the page's provenance `source_paths` as the previous name of a renamed file; with no usable compare a renamed page is `backfill`. A frontmatter that is not a mapping, or a provenance key of the wrong type, exits 1 for a route target and is skipped with a log line for other bundle pages.

The compare is called once per distinct base and only refines a page that is already selected: it decides the evidence and leaves out pages with no routed change. It never fails the run on its size.

### Evidence for pages without a usable base: whole files

Pages with evidence `files` get the full content of each `source_paths` file at `to`, read through the contents API, inside the untrusted block and neutralised like patches. A file that is missing, not a regular file, not valid UTF-8, contains a NUL byte or is larger than 64 KiB drops the page with reason `file_unavailable` and no model call; the page is never sent partial evidence. The per-page token estimate and `--max-tokens-per-page`, `--max-pages` and the exit-before-any-model-call rule apply unchanged.

Rejected:

- Reporting such pages as needing a human and skipping them. Backfill, the main use, would then create and refresh nothing.
- Truncating large files. That is partial evidence the model cannot tell apart from the whole file.

### No source PR context for version 2

Version 2 prompts carry no PR titles or bodies. Each `hunks` page has its own range, often far longer than one merge; listing the PRs behind it costs one API call per commit, up to 250 per base, and pages with evidence `files` have no range at all. Rejected: PR context per base, for that cost and because most of it would be cut by the 16 KiB cap anyway.

### Re-anchoring through the existing compare-and-swap

`reconcile` sets `from` to the cursor it read and `initial` when there is none. `propose` then moves the cursor from `from` to `to` with the existing `cursor.Writer.Advance`, after the PR is opened or updated, or straight away when no page changed. A cursor moved by another run in between fails the run with the cursor kept, as for `plan`. No new cursor method is needed. A version 2 plan always takes the PR flow, also when the cursor is missing (backfill) or already at `to` (pages left behind by earlier runs); when `from` equals `to`, the cursor write is skipped. Rejected: an unconditional cursor write, which could overwrite a concurrent run.

### Deletion candidates go to a human

Candidates are listed in the `reconcile` output and in the reconcile section of the PR body, and `propose` logs each one at warn level. Nothing deletes them. When no page changed, no PR is opened and the candidates are only in the output and the log. Rejected: opening a PR with no file change just to carry the list, and deleting the page, which the issue forbids.

### `gate` and `review` refuse version 2 plans

The `auto-merge-gate` spec already requires `gate` to exit 1 on a plan of `version: 2`, and the `llm-review` spec takes plan version 1 only. Both got that for free from the version 1 only decoder; now that the shared decoder reads version 2, the two commands check the version themselves, and `internal/gate` and `internal/review` are unchanged. A reconcile PR is therefore never auto-merged: its body carries deletion candidates a merge would hide, and whole-file rewrites are riskier than hunk updates. A later `plan` run that updates the same bot PR passes a version 1 plan, but the reconcile pages' `source_sha` is the reconcile `to`, not that plan's `to`, so the provenance rule fails and the PR still goes to a human. Rejected: refusing only plans with deletion candidates, which would let whole-file backfills auto-merge.

### Source not in the routing map exits 1

`plan` succeeds with no pages for an unknown source because a webhook can fire for any repo. `reconcile` is run by hand for one named source, so an unknown source is a mistake and exits 1.

### Telemetry

`reconcile` annotates the dispatcher's `reconcile` span with the source, bundle, `from`, `to`, cursor state and counts, and adds `okf.reconcile.pages` (attribute `okf.reconcile.reason`), `okf.reconcile.deletion_candidates` and `okf.reconcile.re_anchors` (1 when the cursor diverged), labelled by source and bundle. A re-anchor is counted when it is planned; `propose`'s own telemetry shows whether the cursor moved.
