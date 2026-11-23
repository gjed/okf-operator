# Spec Delta

## MODIFIED Requirements

### Requirement: Propose inputs

`propose` SHALL require `--plan <file>`, `--report <file|->` (`-` for stdin), `--knowledge-dir`, `--knowledge-repo` and `--knowledge-base <sha>` (the commit the checkout is at), and accept the GitHub App flags. A missing flag or a `--knowledge-base` that is not a full commit SHA SHALL exit 2. A plan that `write` rejects, a report whose `version` is not `3`, or a report with unknown keys SHALL exit 1, and the version error SHALL say to re-run `write`.

#### Scenario: Missing knowledge base

- **WHEN** `propose` runs without `--knowledge-base`
- **THEN** it exits 2 and the usage names the missing flag

#### Scenario: Report from stdin

- **WHEN** the user runs `okf-operator write --plan plan.json --knowledge-dir kb | okf-operator propose --plan plan.json --report - ...`
- **THEN** `propose` reads the report from stdin

#### Scenario: Report from an older write

- **WHEN** the report has `version: 1` or `version: 2`
- **THEN** `propose` exits 1 with an error naming the version, and makes no GitHub call

#### Scenario: Report with a newer version

- **WHEN** the report has `version: 4`
- **THEN** `propose` exits 1 with an error naming the version, and makes no GitHub call

### Requirement: Plan and report must agree

`propose` SHALL exit 1 before any GitHub call when the plan and the report differ in `source`, `bundle`, `from` or `to`, when the set of report page paths differs from the set of plan page paths, when a report page has a `result` other than `changed`, `created`, `restamped`, `unchanged` or `dropped`, or when a page is `restamped` and the plan is not a version 2 plan.

#### Scenario: Report from another range

- **WHEN** the report's `to` differs from the plan's `to`
- **THEN** `propose` exits 1 with an error naming the field, and makes no GitHub call

#### Scenario: Report lists a page outside the plan

- **WHEN** the report lists a path that is not a plan page path
- **THEN** `propose` exits 1 and makes no GitHub write

#### Scenario: Unknown result

- **WHEN** a report page has `result: "maybe"`
- **THEN** `propose` exits 1 naming the page and the result, and makes no GitHub call

#### Scenario: Restamped page of a version 1 plan

- **WHEN** the plan is version 1 and a report page is `restamped`
- **THEN** `propose` exits 1 naming the page, and makes no GitHub write

### Requirement: Input contract is verified, not trusted

Before any GitHub write for a plan that is not initial, `propose` SHALL compare every file under `--knowledge-dir`, except the top-level `.git`, with the recursive tree of `--knowledge-base`. Pages the report marks `changed` or `restamped` SHALL differ from the base, and pages it marks `created` SHALL follow "Created pages are new files". Every other path SHALL be present on both sides and byte-identical. A truncated tree or any other difference SHALL exit 1 naming the offending paths.

#### Scenario: Unreported file modified

- **WHEN** a file under `--knowledge-dir` differs from the base tree and the report does not mark it `changed` or `restamped`
- **THEN** `propose` exits 1 naming the file, and makes no GitHub write

#### Scenario: Unreported new file

- **WHEN** a file under `--knowledge-dir` is absent from the base tree and the report does not mark it `created`
- **THEN** `propose` exits 1 naming the file, and makes no GitHub write

#### Scenario: Changed page identical to base

- **WHEN** the report marks a page `changed` or `restamped` but its file equals the base tree's blob
- **THEN** `propose` exits 1 naming the page, and makes no GitHub write

### Requirement: Only changed pages are committed

`propose` SHALL commit only the pages the report marks `changed`, `restamped` or `created`, with their content read from `--knowledge-dir`, in one commit. A created page SHALL be added to the tree as a regular file with mode `100644`.

#### Scenario: Unchanged and dropped pages not committed

- **WHEN** the report has one `changed`, one `unchanged` and one `dropped` page
- **THEN** the commit touches only the changed page

#### Scenario: Created page committed

- **WHEN** the report has one `created` page
- **THEN** the commit's tree has that new path as a `100644` blob with the checkout's content

#### Scenario: Restamped page committed

- **WHEN** a version 2 plan's report has one `changed` and one `restamped` page
- **THEN** the one commit touches both pages, with the checkout's content

### Requirement: Cursor advances last

`propose` SHALL advance the source cursor from `from` to `to` only after the branch update and the PR open or update succeed. A failed branch update or PR call SHALL exit 1 with the cursor unchanged. An initial version 1 plan SHALL write only the cursor and SHALL open no PR. A run whose report has no `changed`, `created` or `restamped` page SHALL make no commit and open or update no PR, and SHALL advance the cursor. A version 1 plan with `from` equal to `to` SHALL change nothing.

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

#### Scenario: Only a restamped page

- **WHEN** a version 2 plan's report has one `restamped` page and every other page `unchanged`
- **THEN** a commit is made, the bot PR is opened or updated, and the cursor holds `to` only after that succeeded

### Requirement: Reconcile PR body section

The PR body section of a version 2 plan SHALL start with the range marker line, name the reconciled commit and the cursor state, list every page with its result, its reconcile reason and its drop reason, and list every deletion candidate with its `source_sha` and `source_paths`, stating that it was not deleted. `propose` SHALL log each deletion candidate at warn level and SHALL NOT commit, delete or move it.

#### Scenario: Reconcile section with a deletion candidate

- **WHEN** a version 2 plan with one changed page and one deletion candidate is proposed
- **THEN** the section lists the page with its reason and the candidate's path and source paths, and the candidate's file is not part of the commit

### Requirement: Re-stamped pages are listed apart

The PR body section of a version 2 plan SHALL list each page whose result is `restamped` apart from the other pages, under "Provenance refreshed, content unchanged", with its reconcile reason, and SHALL NOT list it with the other pages' results.

#### Scenario: Reconcile section with a restamped page

- **WHEN** a version 2 plan with one changed page and one restamped page is proposed
- **THEN** the changed page is listed under the pages and the restamped page under "Provenance refreshed, content unchanged", with its reconcile reason
