# Proposal

## Why

`reconcile` cannot see a route that was widened to cover source files unchanged since a page's `source_sha`: it only routes the paths changed in `source_sha..to`, and provenance `source_paths` holds only the files of the last run. Such a page is skipped, or refreshed from the changed files only, until the newly covered files change ([#27](https://github.com/gjed/okf-operator/issues/27), follow-up of [#7](https://github.com/gjed/okf-operator/issues/7)). This belongs to the reconcile step of the plan, after [#26](https://github.com/gjed/okf-operator/pull/26).

## What Changes

- New tool-owned provenance key `routed_paths`: the sorted list of source files the page reflects at `source_sha`. It is optional: a page without it has an unknown routed set.
- `reconcile` adds `routed_paths` (every path routed to the page at `to`) to each `reconcile.pages` entry, and plans a page with the new reason `widened`, evidence `files`, when a path routed to it at `to` is absent from its recorded `routed_paths` and unchanged since its `source_sha`. Its `source_paths` are those new paths plus the routed paths that changed since `source_sha`. This also covers a page whose `source_sha` equals `to`.
- `write` stamps `routed_paths`: the entry's `routed_paths` for a reconcile page that carries one; otherwise the page's recorded set plus the page's `source_paths`, minus the files the plan reports removed or renamed away; a page without a recorded set keeps none, except a new page, which records its `source_paths`. A `routed_paths` the model writes is overwritten or removed.
- Pages stamped before this change are treated as unknown: `reconcile` plans them exactly as before #27, and the next `write` of a reconcile plan records their routed set at `to`. No backfill is forced.
- The gate's `provenance` rule does not require `routed_paths`; a wrong-typed value fails it, as for any provenance key.
- Telemetry: `okf.reconcile.pages` counts reason `widened` through `okf.reconcile.reason`, and the span gets `okf.reconcile.pages.widened`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `provenance`: the optional `routed_paths` key, tool-owned, absent versus empty in `Parse` and `Stamp`.
- `reconcile`: widened-route detection, reason `widened`, `routed_paths` in page entries, pages without a recorded set, telemetry.
- `page-write`: stamping `routed_paths` and validating it in reconcile plans.
- `auto-merge-gate`: the provenance rule's stance on `routed_paths`.

## Impact

- `internal/provenance`, `internal/plan` (reason constant, `routed_paths` field with `omitempty`), `internal/reconcile`, `internal/write`; tests in `internal/gate` only.
- Plan version 1 output, its golden file and its digests are unchanged. Plan version 2 gains an optional key, so a reconcile plan from an older release still decodes and digests the same; an older `write` rejects a plan carrying the key with an unknown-field error.
- No change to `change-plan`, `go.mod`, `api/`, `internal/controller` or `deploy/`.
- Docs: `docs/reconcile.md`, `docs/write.md`, README provenance list.
