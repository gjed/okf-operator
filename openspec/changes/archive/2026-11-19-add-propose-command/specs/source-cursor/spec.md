# Spec Delta

## ADDED Requirements

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
