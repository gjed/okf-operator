# Spec Delta

## MODIFIED Requirements

### Requirement: Propose inputs

`propose` SHALL require `--plan <file>`, `--report <file|->` (`-` for stdin), `--knowledge-dir`, `--knowledge-repo` and `--knowledge-base <sha>` (the commit the checkout is at), and accept the GitHub App flags. A missing flag or a `--knowledge-base` that is not a full commit SHA SHALL exit 2. A plan that `write` rejects as input, a report whose `version` is not `2`, or a report with unknown keys, SHALL exit 1.

#### Scenario: Missing knowledge base

- **WHEN** `propose` runs without `--knowledge-base`
- **THEN** it exits 2 and the usage names the missing flag

#### Scenario: Report from stdin

- **WHEN** the user runs `okf-operator write --plan plan.json --knowledge-dir kb | okf-operator propose --plan plan.json --report - ...`
- **THEN** `propose` reads the report from stdin

#### Scenario: Report from an older write

- **WHEN** the report has `version: 1`
- **THEN** `propose` exits 1 with an error naming the version, and makes no GitHub call

### Requirement: Cursor advances last

`propose` SHALL advance the source cursor from `from` to `to` only after the branch update and the PR open or update succeed. A failed branch update or PR call SHALL exit 1 with the cursor unchanged. An initial version 1 plan SHALL write only the cursor and SHALL open no PR. A run whose report has no `changed` or `created` page SHALL make no commit and open or update no PR, and SHALL advance the cursor. A version 1 plan with `from` equal to `to` SHALL change nothing.

#### Scenario: Push fails

- **WHEN** the bot branch ref update fails
- **THEN** `propose` exits 1, no PR is opened and the cursor still holds `from`

#### Scenario: Initial plan

- **WHEN** a version 1 plan has `initial: true`
- **THEN** `propose` writes the cursor with `to`, opens no PR and makes no knowledge commit, and the result action is `none`

#### Scenario: Initial plan while a bot PR is open

- **WHEN** a version 1 plan has `initial: true`, a bot PR for the source is open and `--knowledge-base` is not its head
- **THEN** `propose` writes the cursor and reads neither the knowledge branches nor the checkout

#### Scenario: Nothing changed

- **WHEN** every report page is `unchanged` or `dropped`
- **THEN** no PR is opened or updated and the cursor holds `to`

## ADDED Requirements

### Requirement: Reconcile plans take the PR flow

A version 2 plan SHALL take the checkout checks, the commit and the PR open or update even when it is initial or its `from` equals `to`. Afterwards `propose` SHALL create the cursor with `to` when the plan is initial, move it from `from` to `to` when they differ, and leave it when they are equal, with the same compare-and-swap and only after the PR succeeded. It SHALL NOT list source PRs for a version 2 plan.

#### Scenario: Backfill opens a PR before creating the cursor

- **WHEN** an initial version 2 plan's report has a `created` page
- **THEN** the bot PR is opened first and the cursor is created with `to` after it

#### Scenario: Diverged cursor re-anchored

- **WHEN** a version 2 plan's `from` is a diverged cursor and its report has a `changed` page
- **THEN** the cursor holds `to` only after the bot PR is opened or updated, and still holds `from` when the PR call fails

### Requirement: Reconcile PR body section

The PR body section of a version 2 plan SHALL start with the range marker line, name the reconciled commit and the cursor state, list every page with its result, its reconcile reason and its drop reason, and list every deletion candidate with its `source_sha` and `source_paths`, stating that it was not deleted. `propose` SHALL log each deletion candidate at warn level and SHALL NOT commit, delete or move it.

#### Scenario: Reconcile section with a deletion candidate

- **WHEN** a version 2 plan with one changed page and one deletion candidate is proposed
- **THEN** the section lists the page with its reason and the candidate's path and source paths, and the candidate's file is not part of the commit
