# Spec Delta

## ADDED Requirements

### Requirement: Widened routes

An existing routed page with a recorded `routed_paths`, a `source_sha` that equals `to` or is an ancestor of it, and a path routed to it at `to` that is absent from `routed_paths` and unchanged since `source_sha` SHALL be planned as `update` with reason `widened`, evidence `files` and no `base`. Its `source_paths` SHALL be those new paths plus the paths routed to it at `to` that changed since `source_sha`.

#### Scenario: Route widened to an unchanged file

- **WHEN** a page records `routed_paths: [src/a.go]`, its route now also matches `src/b.go`, and only `src/a.go` changed since its `source_sha`
- **THEN** the page is planned with reason `widened`, evidence `files` and `source_paths: [src/a.go, src/b.go]`

#### Scenario: Route widened at the end commit

- **WHEN** a page's `source_sha` equals `to`, it records `routed_paths: [src/a.go]` and its route now also matches `src/b.go`
- **THEN** the page is planned with reason `widened`, evidence `files` and `source_paths: [src/b.go]`

#### Scenario: New file added under the route

- **WHEN** a page records `routed_paths: [src/a.go]` and `src/b.go`, routed to it, was added since its `source_sha`
- **THEN** the page is planned with reason `stale`, evidence `hunks` and `src/b.go` in `source_paths`

### Requirement: Widened routes wait for deletions

Reason `widened` SHALL take precedence over `stale` and `rename`, except when a path routed to the page was removed or renamed away since `source_sha`. The page SHALL then be planned as `stale` or `rename` with evidence `hunks`, and its entry's `routed_paths` SHALL omit the new paths, so a later run plans them as `widened`.

#### Scenario: Reflected file removed while the route widened

- **WHEN** a page records `routed_paths: [src/a.go, src/c.go]`, `src/c.go` was removed since its `source_sha`, and its route now also matches the unchanged `src/b.go`
- **THEN** the page is planned with reason `stale`, evidence `hunks` and `source_paths: [src/c.go]`, and its entry has `routed_paths: [src/a.go]`

### Requirement: Pages without routed paths

A page without `routed_paths` SHALL have an unknown routed set: `reconcile` SHALL NOT plan it as `widened` and SHALL plan it as it did before routed paths existed.

#### Scenario: Page stamped before routed paths existed

- **WHEN** a page has no `routed_paths`, its `source_sha` equals `to` and its route now matches more files than its `source_paths`
- **THEN** the page is not planned

## MODIFIED Requirements

### Requirement: Current pages are left out

A page whose `source_sha` equals `to`, or whose routed paths did not change since its `source_sha`, SHALL NOT be planned unless it is planned as `widened`.

#### Scenario: Page already at the end commit

- **WHEN** a page's `source_sha` equals `to` and every path routed to it at `to` is in its `routed_paths`
- **THEN** the page is not planned

### Requirement: Reconcile page entries

`reconcile.pages` SHALL hold one entry per plan page with its `path`, `reason` (`backfill`, `stale`, `rename` or `widened`), `evidence`, `base` (empty for evidence `files`) and `routed_paths`, the paths routed to the page at `to` less the new paths of a widened route that waits for a deletion. All lists SHALL be sorted, and the same inputs SHALL produce byte-identical output.

#### Scenario: Repeated run

- **WHEN** `reconcile` runs twice with identical inputs
- **THEN** both stdout outputs are byte-identical

#### Scenario: Routed paths in an entry

- **WHEN** a stale page's route matches `src/a.go` and `src/b.go` at `to` and only `src/b.go` changed
- **THEN** its entry has `routed_paths: [src/a.go, src/b.go]` and the plan page has `source_paths: [src/b.go]`

### Requirement: Reconcile telemetry

Each `reconcile` run SHALL annotate the dispatcher's `reconcile` span with the source repo, bundle, `from`, `to`, the cursor state and the counts of planned pages per reason, `widened` included, and of deletion candidates. It SHALL add the counters `okf.reconcile.pages` (attribute `okf.reconcile.reason`), `okf.reconcile.deletion_candidates` and `okf.reconcile.re_anchors` (1 when the cursor diverged), labelled by source repo and bundle.

#### Scenario: Span and counters

- **WHEN** a reconcile run plans one `stale` page and one deletion candidate for a diverged cursor with in-memory exporters installed
- **THEN** the `reconcile` span carries those counts and the counters record 1 stale page, 1 deletion candidate and 1 re-anchor

#### Scenario: Widened page counted

- **WHEN** a reconcile run plans one `widened` page with in-memory exporters installed
- **THEN** the span has `okf.reconcile.pages.widened` 1 and `okf.reconcile.pages` records 1 with `okf.reconcile.reason` `widened`
