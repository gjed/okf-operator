# Design

## Context

See proposal.md - Why. `accept` in `internal/write` reports a `NO_CHANGE` reply, and a reply equal to the current page apart from provenance, as `unchanged` and never writes the file. For a reconcile page that is the wrong outcome: `reconcile` chooses pages by their provenance, so a page whose provenance does not move is chosen again.

## Goals / Non-Goals

**Goals:** a page the model judged current stops being planned; one stamp path (`provenance.Stamp`) for every provenance write; version 1 behaviour unchanged.

**Non-Goals:** changing how `reconcile` picks pages, changing `gate`'s rules, letting reconcile PRs auto-merge, re-stamping pages `write` did not send to the model.

## Decisions

### Re-stamp in write, for reconcile plans only

`write` already holds the stamped current page: `accept` computes `Stamp(current, fields)` to detect "same content". A job of a version 2 plan keeps that output when the page is unchanged and reports `restamped`. A page is re-stamped only when the stamp changes the file; if the bytes are equal (same second, same values) it stays `unchanged` and nothing is written. The fields are the ones a changed page gets, so `source_paths` and `routed_paths` follow the plan entry. Rejected: re-stamping version 1 pages too. A `plan` run's range is the cursor's, and the next `plan` starts after it, so an unchanged page costs nothing again; stamping it would add a frontmatter-only commit to nearly every plan run for no benefit. The issue's default is reconcile only, and the spec says so explicitly.

### Report version 3, propose accepts only 3

The `restamped` result is new vocabulary and `propose` decodes strictly, so the version moves to 3 as it did for `created`. `propose` accepts version 3 only and says "re-run write with this release". Accepting version 2 as well would be harmless to decode (a subset of version 3) but keeps a dead path alive: reports are produced and consumed in the same run, by one binary, and the controller runs both stages in process. Version 1 plan reports stay version 3 too; the version describes the document, not the plan. A `restamped` result for a version 1 plan fails `Agree` before any write, because `write` never produces it there.

### Propose commits re-stamped pages in the same PR

A re-stamped page is a modified file, so it goes into the same commit as changed pages: `Verify` already requires a changed page to exist and differ from the base, which is exactly the re-stamp contract. The reconcile section of the PR body lists them apart under "Provenance refreshed, content unchanged", with the reconcile reason, so a reviewer sees at once which pages have a text change. Rejected: committing them to a separate branch or PR (a second PR per source breaks "one bot PR per source"), and leaving them uncommitted (the cursor would pass `to` while the pages stay stale, so the next `reconcile` would plan them again). The cursor rule is unchanged: it advances after the PR call, and a run with only `unchanged` and `dropped` pages makes no commit and no PR.

### Gate unchanged

A re-stamped page is `modified`, its diff is a few lines, and its `source_sha` is the plan's `to`, so change-shape, size and the provenance rule pass without a code change; tests pin it. Created pages stay human-only (change-shape). Whether a PR auto-merges is decided before the rules: `gate` and `review` reject version 2 plans and the controller never gates a reconcile PR, so a re-stamped page in a reconcile PR is merged by a human like every other page of it. The issue's "auto-merge-eligible like any modified page" therefore holds for the rules, and nothing here changes who merges a reconcile PR. Making reconcile PRs gateable is a separate decision with its own risks.

### Review passes a provenance-only page without a model call

`review` aligns the provenance of both sides before prompting. When the aligned pages are equal and the page is not new, no statement changed, so there is nothing to support: the page passes, no model call is made and the source range is not needed for it. The reason `provenance_only` is logged at info level. It is not a `reason` in the result: the result's `reason` field means "the page did not get a model verdict" and the gate treats any reason as a failure, so a passing page keeps an empty reason and the result schema stays at version 1. Any change outside the provenance keys, a custom frontmatter key included, differs after alignment and is reviewed as before.

## Risks / Trade-offs

- \[A model that wrongly answers `NO_CHANGE` now stamps the page current, so it is not retried by the next reconcile\] → the same page was already kept as is by the old behaviour; the difference is that its provenance says it was checked at `to`, which is true. The re-stamp is reviewable in the PR.
- [A frontmatter-only commit adds PR noise] → listed apart in the PR body; one small diff per page.
- [Stamp re-encodes the frontmatter] → the same encoder writes every changed page today; comments, key order and values are kept, and a test pins the bytes.
- [Mixed-version binaries] → `propose` fails with a version error that names the fix.

## Migration Plan

Upgrade `write` and `propose` together; a report written by the old `write` is rejected with a message telling the user to re-run `write`. Pages already stale stay as they are until the next reconcile that the model judges current re-stamps them. Rollback: an older binary rejects a version 3 report and still reads re-stamped pages, whose frontmatter is ordinary provenance.
