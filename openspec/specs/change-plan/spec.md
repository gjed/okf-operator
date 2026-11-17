# change-plan Specification

## Purpose

Defines the `plan` subcommand: given a source repo, it decides which commit range to process and which knowledge pages that range affects, without calling a model. Its JSON output is the input contract for `write`.

## Requirements

### Requirement: Plan inputs

`plan` SHALL require `--source owner/name`, `--knowledge-repo owner/name` and `--knowledge-dir <path>`. It SHALL accept `--routing <file>` to override the routing map path, and `--to <sha>` to plan up to a given commit instead of the source default branch HEAD.

#### Scenario: Missing required flag

- **WHEN** `plan` runs without `--source`
- **THEN** it exits 2 and the usage names the missing flag

#### Scenario: Explicit end commit

- **WHEN** `plan` runs with `--to <sha>`
- **THEN** the range ends at that SHA and the source HEAD is not looked up

### Requirement: Source not in routing map

When the source repo has no entry in the routing map, `plan` SHALL succeed with no pages and SHALL log that the source is not onboarded.

#### Scenario: Unknown source

- **WHEN** `plan` runs for a repo absent from `routing.yaml`
- **THEN** it exits 0, the plan lists no pages, and the log carries `source not onboarded`

### Requirement: JSON plan output

`plan` SHALL write one JSON document to stdout with `version: 1`, `source`, `bundle`, `from`, `to`, `initial`, `changed_paths`, `pages`, `unrouted` and `excluded`. Each page SHALL carry `path`, `state` (`update`, `create` or `missing`), `type` when known, and the sorted `source_paths` that routed to it.

#### Scenario: Routed change

- **WHEN** the range changes `deploy/values.yaml`, which routes to `infra/pipelines/deploy.md`, an existing page
- **THEN** `pages` holds one entry with path `infra/pipelines/deploy.md`, state `update` and source path `deploy/values.yaml`

### Requirement: Deterministic output

For the same routing map, cursor, end commit and knowledge checkout, `plan` SHALL produce byte-identical output. All lists SHALL be sorted.

#### Scenario: Repeated run

- **WHEN** `plan` runs twice with identical inputs
- **THEN** both stdout outputs are byte-identical

### Requirement: Renames route both paths

A renamed source file SHALL be treated as a change to both its previous and its new path for routing.

#### Scenario: File moved out of routed globs

- **WHEN** `src/api/handler.go`, routed to a page, is renamed to `legacy/handler.go`, which is unrouted
- **THEN** the page is planned with source path `src/api/handler.go`, and `legacy/handler.go` is listed as unrouted

### Requirement: Unrouted paths are logged, not planned

Every changed path that is neither routed nor excluded SHALL appear in `unrouted` and SHALL be logged once at info level with the source repo and the path.

#### Scenario: Nothing routed

- **WHEN** every changed path in the range is unrouted
- **THEN** `pages` is empty, `unrouted` lists every path, and the command exits 0

### Requirement: Oversized range fails closed

When the source compare cannot return the complete list of changed files for the range, `plan` SHALL fail instead of planning from a partial list.

#### Scenario: Range above the API limit

- **WHEN** the range contains more changed files than the compare API returns
- **THEN** `plan` exits 1 with an error stating that the range is incomplete and `reconcile` is required

### Requirement: Plan telemetry

Each `plan` run SHALL emit one span carrying the source repo, the range, and the counts of changed paths, routed pages, unrouted paths and excluded paths. It SHALL also record those counts as metrics labelled by source repo and bundle.

#### Scenario: Span attributes

- **WHEN** a plan run finishes with an OTLP collector configured
- **THEN** the collector receives a span named `plan` with the source repo and the four counts as attributes
