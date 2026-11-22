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

### Requirement: Cursor advance is compare-and-swap

`propose` SHALL advance a cursor from the plan's `from` to its `to` only if the cursor file still holds `from`, or, for an initial plan, does not exist. The write SHALL be conditional on the file's current blob SHA, so a cursor changed by another run between read and write fails the run (exit 1) instead of being overwritten. `plan` SHALL keep only reading cursors.

#### Scenario: Cursor moved by another run

- **WHEN** another run advances `cursors/acme/api` after `propose` read it and before it wrote it
- **THEN** `propose` exits 1 and the other run's cursor is kept

#### Scenario: Cursor no longer at the plan start

- **WHEN** the cursor on `kb-state` differs from the plan's `from`
- **THEN** `propose` exits 1 before any GitHub write

### Requirement: Missing kb-state is created

When the `kb-state` branch does not exist, `propose` SHALL create it with an orphan commit holding only the cursor file. If the branch appears concurrently, the run SHALL exit 1.

#### Scenario: kb-state missing

- **WHEN** `propose` advances a cursor and the knowledge repo has no `kb-state` branch
- **THEN** `kb-state` is created by a commit with no parent holding only `cursors/acme/api`, with `to` and a newline

### Requirement: Reconcile re-anchors a diverged cursor

A diverged cursor SHALL be re-anchored only by `reconcile` followed by `write` and `propose`: the version 2 plan carries the diverged cursor as `from` and the reconciled commit as `to`, and `propose` moves the cursor from `from` to `to` with the same compare-and-swap as for any plan, after the bot PR is opened or updated. A run that fails before that point SHALL leave the cursor unchanged, and a cursor moved by another run after `reconcile` read it SHALL fail `propose`.

#### Scenario: Re-anchor after a force-push

- **WHEN** the source default branch was force-pushed, `plan` exits 1 with "reconcile required", and `reconcile`, `write` and `propose` then succeed
- **THEN** the cursor holds the reconciled `to`, and the next `plan` diffs from it

#### Scenario: Reconcile run fails

- **WHEN** `write` or `propose` fails during a reconcile run
- **THEN** the cursor still holds the diverged commit
