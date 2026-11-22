# reconcile Specification

## Purpose

Defines the `reconcile` subcommand: for one source repo, it plans every routed knowledge page against the source tree at one commit, without calling a model, reports pages whose sources are gone, and prints a plan of version 2 that `write` and `propose` consume.

## Requirements

### Requirement: Reconcile inputs

`reconcile` SHALL require `--source owner/name`, `--knowledge-repo owner/name` and `--knowledge-dir <path>`, the checkout `write` will use: the open bot PR's branch for the source, otherwise the default branch. It SHALL accept `--routing <file>` (default `<knowledge-dir>/.kb/routing.yaml`), `--to <sha>` (a full 40-character commit SHA, default the source default branch HEAD) and the GitHub App flags.

#### Scenario: Missing source

- **WHEN** `reconcile` runs without `--source`
- **THEN** it exits 2 and the usage names the missing flag

### Requirement: One onboarded source per run

`reconcile` SHALL reconcile exactly one source per run. A source absent from the routing map SHALL exit 1 without a plan.

#### Scenario: Source not onboarded

- **WHEN** `reconcile` runs for a repo absent from the routing map
- **THEN** it exits 1 and prints no plan

### Requirement: Source state from the tree at the end commit

`reconcile` SHALL list the source repo's files at `to` with the recursive git trees API and route every blob path through the routing map. A truncated tree listing SHALL exit 1 without a plan. Page selection SHALL NOT depend on a compare file list, so a source with any number of changed files can be reconciled.

#### Scenario: Truncated tree

- **WHEN** the trees API reports the source tree at `to` as truncated
- **THEN** `reconcile` exits 1 with an error naming the truncated tree and prints no plan

### Requirement: Pages considered

`reconcile` SHALL consider every route target of the source and every `.md` page of the bundle whose provenance `source_repo` is the source. A route target whose frontmatter is malformed SHALL exit 1.

#### Scenario: Malformed route target

- **WHEN** a route target's frontmatter is not a YAML mapping
- **THEN** `reconcile` exits 1 naming the page

### Requirement: Backfill without a usable base

A routed page that does not exist SHALL be planned as `create` when its route has a `type`, otherwise as `missing`. An existing routed page without frontmatter, without `source_sha`, with another `source_repo`, or whose `source_sha` has diverged from `to` SHALL be planned as `update`. These pages SHALL have reason `backfill`, evidence `files` and as `source_paths` their routed paths at `to`.

#### Scenario: Backfill without cursor or pages

- **WHEN** the source has no cursor, the knowledge checkout has none of its pages, one route has `type: concept` and another has no `type`, and both match files at `to`
- **THEN** the plan has `initial: true`, the typed route's page with state `create` and the other with state `missing`, both with reason `backfill`, evidence `files` and the routed paths at `to`

### Requirement: Stale and renamed pages

An existing routed page whose `source_sha` is an ancestor of `to` and whose routed paths changed since SHALL be planned as `update` with evidence `hunks`, `base` its `source_sha`, and as `source_paths` the changed paths of `base..to`, new and previous names, that route to it. Its reason SHALL be `rename` when the compare reports a path of its provenance `source_paths` as the previous name of a renamed file, otherwise `stale`.

#### Scenario: Page behind the source

- **WHEN** a page's `source_sha` is an ancestor of `to` and a path routed to it changed since
- **THEN** the page is planned as `update` with reason `stale`, evidence `hunks`, `base` its `source_sha` and that path in `source_paths`

#### Scenario: Renamed source path

- **WHEN** a page's provenance lists `src/old.go`, which was renamed since its `source_sha` to `src/new.go`, both routed to the page
- **THEN** the page is planned with reason `rename` and `src/new.go` and `src/old.go` in `source_paths`

### Requirement: Oversized compare falls back to whole files

When the compare of a stale page's `base..to` lists 300 or more files, the page SHALL be planned with reason `stale`, evidence `files` and all its routed paths at `to`, and the run SHALL NOT fail.

#### Scenario: Base far behind

- **WHEN** a page's `source_sha` is an ancestor of `to` and the compare lists 300 files
- **THEN** the page is planned with evidence `files` and `reconcile` exits 0

### Requirement: No rollback

`reconcile` SHALL exit 1 without a plan when the cursor, or the `source_sha` of a page it considers, is a descendant of `to`, so it never moves the cursor or a page back to an older commit.

#### Scenario: End commit older than the cursor

- **WHEN** `--to` names an ancestor of the cursor
- **THEN** `reconcile` exits 1 and prints no plan

### Requirement: Current pages are left out

A page whose `source_sha` equals `to`, or whose routed paths did not change since its `source_sha`, SHALL NOT be planned unless it is planned as `widened`.

#### Scenario: Page already at the end commit

- **WHEN** a page's `source_sha` equals `to` and every path routed to it at `to` is in its `routed_paths`
- **THEN** the page is not planned

### Requirement: Deletion candidates

An existing page whose routes match no file at `to` and whose provenance `source_paths` are all absent at `to` SHALL be reported as a deletion candidate with its `path`, `source_sha` and `source_paths`. It SHALL NOT be planned, and no command SHALL delete or move it.

#### Scenario: Routed paths gone

- **WHEN** every file routed to an existing page was deleted from the source
- **THEN** the page is listed under `reconcile.deletion_candidates`, is not a plan page, and stays in the checkout after `write` and `propose`

### Requirement: Plan version 2 output

`reconcile` SHALL print one JSON plan document with `version: 2` holding every key of plan version 1 and an object `reconcile` with `cursor` (`missing`, `head`, `ancestor` or `diverged`), `pages` and `deletion_candidates`. `from` SHALL be the cursor read from `kb-state`, empty when there is none, `initial` SHALL be true exactly when there is none, and `changed_paths`, `unrouted` and `excluded` SHALL be empty.

#### Scenario: Diverged cursor

- **WHEN** the cursor has diverged from `to`
- **THEN** the plan has `from` equal to the cursor, `initial: false` and `reconcile.cursor: "diverged"`

### Requirement: Reconcile page entries

`reconcile.pages` SHALL hold one entry per plan page with its `path`, `reason` (`backfill`, `stale`, `rename` or `widened`), `evidence`, `base` (empty for evidence `files`) and `routed_paths`, the paths routed to the page at `to`. All lists SHALL be sorted, and the same inputs SHALL produce byte-identical output.

#### Scenario: Repeated run

- **WHEN** `reconcile` runs twice with identical inputs
- **THEN** both stdout outputs are byte-identical

#### Scenario: Routed paths in an entry

- **WHEN** a stale page's route matches `src/a.go` and `src/b.go` at `to` and only `src/b.go` changed
- **THEN** its entry has `routed_paths: [src/a.go, src/b.go]` and the plan page has `source_paths: [src/b.go]`

### Requirement: Plan version 1 unchanged

`plan` SHALL keep printing version 1 documents without a `reconcile` key.

#### Scenario: Plan output unchanged

- **WHEN** `plan` runs on the inputs of its golden file
- **THEN** its output is byte-identical to the golden file

### Requirement: Reconcile telemetry

Each `reconcile` run SHALL annotate the dispatcher's `reconcile` span with the source repo, bundle, `from`, `to`, the cursor state and the counts of planned pages per reason, `widened` included, and of deletion candidates. It SHALL add the counters `okf.reconcile.pages` (attribute `okf.reconcile.reason`), `okf.reconcile.deletion_candidates` and `okf.reconcile.re_anchors` (1 when the cursor diverged), labelled by source repo and bundle.

#### Scenario: Span and counters

- **WHEN** a reconcile run plans one `stale` page and one deletion candidate for a diverged cursor with in-memory exporters installed
- **THEN** the `reconcile` span carries those counts and the counters record 1 stale page, 1 deletion candidate and 1 re-anchor

#### Scenario: Widened page counted

- **WHEN** a reconcile run plans one `widened` page with in-memory exporters installed
- **THEN** the span has `okf.reconcile.pages.widened` 1 and `okf.reconcile.pages` records 1 with `okf.reconcile.reason` `widened`

### Requirement: Widened routes

An existing routed page with a recorded `routed_paths`, a `source_sha` that equals `to` or is an ancestor of it, and a path routed to it at `to` that is absent from `routed_paths` and unchanged since `source_sha` SHALL be planned as `update` with reason `widened`, evidence `files` and no `base`. Its `source_paths` SHALL be those new paths plus the paths routed to it at `to` that changed since `source_sha`. Reason `widened` SHALL take precedence over `stale` and `rename`.

#### Scenario: Route widened to an unchanged file

- **WHEN** a page records `routed_paths: [src/a.go]`, its route now also matches `src/b.go`, and only `src/a.go` changed since its `source_sha`
- **THEN** the page is planned with reason `widened`, evidence `files` and `source_paths: [src/a.go, src/b.go]`

#### Scenario: Route widened at the end commit

- **WHEN** a page's `source_sha` equals `to`, it records `routed_paths: [src/a.go]` and its route now also matches `src/b.go`
- **THEN** the page is planned with reason `widened`, evidence `files` and `source_paths: [src/b.go]`

#### Scenario: New file added under the route

- **WHEN** a page records `routed_paths: [src/a.go]` and `src/b.go`, routed to it, was added since its `source_sha`
- **THEN** the page is planned with reason `stale`, evidence `hunks` and `src/b.go` in `source_paths`

### Requirement: Pages without routed paths

A page without `routed_paths` SHALL have an unknown routed set: `reconcile` SHALL NOT plan it as `widened` and SHALL plan it as it did before routed paths existed.

#### Scenario: Page stamped before routed paths existed

- **WHEN** a page has no `routed_paths`, its `source_sha` equals `to` and its route now matches more files than its `source_paths`
- **THEN** the page is not planned
