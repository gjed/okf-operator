# Design

## Context

See proposal.md - Why. `reconcile` selects a page with a usable `source_sha` only from the paths changed in `source_sha..to`. Provenance `source_paths` is the page's last-run evidence, not its routed set. `write` sees only the plan: it loads neither the routing map nor the source tree.

## Goals / Non-Goals

**Goals:** detect a route widened to unchanged files; keep plan version 1 byte-identical; keep pages stamped before this change working without a forced backfill.

**Non-Goals:** detecting a narrowed route (a page that covers fewer files is not stale), detecting a route widened before a page first gets `routed_paths`, changing `plan`.

## Decisions

### A list, not a digest

`routed_paths` is the full sorted list of source files the page reflects at `source_sha`. With a list, `reconcile` knows which routed files are new and sends only those, plus the routed files that changed since `source_sha`, as whole-file evidence. A digest only says that the set differs, so every routed file would go as evidence, which for a broad route can push the page over `--max-tokens-per-page`. Rejected: digest, for that reason; reusing `source_paths`, whose meaning (the run's evidence) the gate, the review and the deletion candidates already depend on.

### The set means "files the page reflects", not "files routed"

A page reflects the files its runs have sent the model. Recording what the page has seen, rather than what the route matched at the time, keeps the field correct when `write` cannot know the route: a page created by `plan` from its changed files only records those, and a later `reconcile` plans the unchanged routed files as `widened`, which is what a page that never saw them needs.

### Reconcile carries the set; write carries it forward otherwise

`reconcile` already routes the whole tree at `to`, so each `reconcile.pages` entry gets `routed_paths`, the routed set at `to`, and `write` stamps it. Every reconcile evidence ends with the page reflecting that set: `backfill` and oversized `stale` send all routed files, `widened` sends the new and changed ones on top of the recorded set, and `stale`/`rename` only happen when nothing routed is new. For a version 1 plan, or an older version 2 plan without the key, `write` stamps the recorded set plus the page's `source_paths`, minus the files the plan reports removed or renamed away. Rejected: having `write` load the routing map and call the trees API at `to`, which adds a GitHub call and a routing dependency to every write for a value `reconcile` already has.

`plan` and its version 1 output are untouched. Version 2 gains `routed_paths` with `omitempty`, so a version 2 plan without it encodes and digests as before; the version stays 2 because the key is optional and `gate` and `review` bind digests of version 1 only. An older `write` rejects a plan carrying the key with an unknown-field error, which is acceptable since `reconcile` and `write` ship in one binary.

### Widened selection

For a page with a recorded set whose `source_sha` equals `to` or is an ancestor of it: new = routed at `to` minus recorded minus the paths changed since `source_sha`. A non-empty new set plans the page as `widened`, evidence `files`, `source_paths` new plus routed-and-changed. A file added since `source_sha` is changed, so it stays `stale` with hunks, which already carry its full content. `widened` wins over `stale` and `rename` because a page can have one evidence kind, and whole files cover the hunks. Diverged and oversized compares keep their reasons, since both already send every routed file.

### Unknown for old pages

A page without `routed_paths` is planned exactly as before #27. Treating it as an empty set would plan every existing page as `widened` on the first run, a forced backfill of the whole bundle. The set is recorded by the next write of the page from a reconcile plan, or carried forward once known. A page that only ever goes through `plan` stays unknown until it is created anew or reconciled.

### Gate does not require the field

Requiring `routed_paths` would fail the gate for every version 1 update of an old page, which correctly stays unknown. The rule keeps rejecting a wrong-typed value through `Parse`, as for any provenance key. `internal/gate` only gains tests.

## Risks / Trade-offs

- [A route widened before an old page records its set is never detected] → the first reconcile write records the routed set at `to`; documented in `docs/reconcile.md`.
- \[`write` carries the set forward from the plan's `changed_paths`, which start at the cursor, not at the page's `source_sha`\] → the set can miss files added in between, which only makes a later `reconcile` plan them as `widened`; it can keep files deleted in between, which no rule reads as present.
- \[The model replies `NO_CHANGE` to a widened page\] → the page keeps its `source_sha` and set and is planned again by the next `reconcile`, as a `stale` page is today.
- [Larger frontmatter] → one path per routed file; the routes are page-scoped, so the list is bounded by what one page covers.

## Migration Plan

No step. Old pages have no `routed_paths` and behave as before. Rollback: an older binary ignores `routed_paths` in pages (unknown keys are kept as other frontmatter) and rejects a new version 2 plan that carries the key.
