# source-cursor Specification

## Purpose

Defines how the last processed commit of each source repository is stored, so that every run diffs from a durable cursor and never from the event that woke it.

## Requirements

### Requirement: Cursor storage

The cursor for source repo `owner/name` SHALL be the file `cursors/owner/name` on the `kb-state` branch of the knowledge repo. Its content SHALL be one full 40-character commit SHA followed by a newline.

#### Scenario: Cursor read

- **WHEN** `kb-state` holds `cursors/acme/api` with a valid SHA
- **THEN** `plan` for source `acme/api` uses that SHA as the start of the range

#### Scenario: Malformed cursor

- **WHEN** the cursor file content is not a 40-character hex SHA
- **THEN** `plan` fails with an error naming the file, and exits 1

### Requirement: Missing cursor anchors without planning

When no cursor exists for a source, or the `kb-state` branch does not exist, `plan` SHALL plan no pages, SHALL mark the plan as initial, and SHALL set the next cursor to the source HEAD.

#### Scenario: First run for a source

- **WHEN** `plan` runs for a source with no cursor file
- **THEN** the plan has `initial: true`, an empty page list, and `to` equal to the source default branch HEAD

### Requirement: Diverged cursor fails closed

When the cursor commit is not an ancestor of the source HEAD (force-push, history rewrite, or a missing commit), `plan` SHALL fail and SHALL NOT produce a plan.

#### Scenario: Force-pushed source

- **WHEN** the cursor SHA is not reachable from the source default branch HEAD
- **THEN** `plan` exits 1 with an error stating that the cursor diverged and that `reconcile` is required

### Requirement: Cursor at HEAD is a no-op

When the cursor equals the source HEAD, `plan` SHALL succeed with an empty range and no pages.

#### Scenario: Duplicate event

- **WHEN** a second webhook for an already processed merge triggers `plan`
- **THEN** the plan has `from` equal to `to`, no changed paths and no pages
