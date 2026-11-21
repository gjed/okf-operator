# Spec Delta

## ADDED Requirements

### Requirement: Created pages are new files

Each page the report marks `created` SHALL be a regular file under `--knowledge-dir`, not a symlink, and SHALL be absent from the `--knowledge-base` tree. Otherwise `propose` SHALL exit 1 naming the page before any GitHub write.

#### Scenario: Created page already in the base

- **WHEN** the report marks a page `created` but the base tree has that path
- **THEN** `propose` exits 1 naming the page, and makes no GitHub write

#### Scenario: Created page is a symlink

- **WHEN** the report marks a page `created` and that path under `--knowledge-dir` is a symlink
- **THEN** `propose` exits 1 naming the page, and makes no GitHub write

## MODIFIED Requirements

### Requirement: Propose inputs

`propose` SHALL require `--plan <file>`, `--report <file|->` (`-` for stdin), `--knowledge-dir`, `--knowledge-repo` and `--knowledge-base <sha>` (the commit the checkout is at), and accept the GitHub App flags. A missing flag or a `--knowledge-base` that is not a full commit SHA SHALL exit 2. A plan whose `version` is not `1`, a report whose `version` is not `2`, or either with unknown keys, SHALL exit 1.

#### Scenario: Missing knowledge base

- **WHEN** `propose` runs without `--knowledge-base`
- **THEN** it exits 2 and the usage names the missing flag

#### Scenario: Report from stdin

- **WHEN** the user runs `okf-operator write --plan plan.json --knowledge-dir kb | okf-operator propose --plan plan.json --report - ...`
- **THEN** `propose` reads the report from stdin

#### Scenario: Report from an older write

- **WHEN** the report has `version: 1`
- **THEN** `propose` exits 1 with an error naming the version, and makes no GitHub call

### Requirement: Input contract is verified, not trusted

Before any GitHub write for a plan that is not initial, `propose` SHALL compare every file under `--knowledge-dir`, except the top-level `.git`, with the recursive tree of `--knowledge-base`. Pages the report marks `changed` SHALL differ from the base, and pages it marks `created` SHALL follow "Created pages are new files". Every other path SHALL be present on both sides and byte-identical. A truncated tree or any other difference SHALL exit 1 naming the offending paths.

#### Scenario: Unreported file modified

- **WHEN** a file under `--knowledge-dir` differs from the base tree and the report does not mark it `changed`
- **THEN** `propose` exits 1 naming the file, and makes no GitHub write

#### Scenario: Unreported new file

- **WHEN** a file under `--knowledge-dir` is absent from the base tree and the report does not mark it `created`
- **THEN** `propose` exits 1 naming the file, and makes no GitHub write

#### Scenario: Changed page identical to base

- **WHEN** the report marks a page `changed` but its file equals the base tree's blob
- **THEN** `propose` exits 1 naming the page, and makes no GitHub write

### Requirement: Only changed pages are committed

`propose` SHALL commit only the pages the report marks `changed` or `created`, with their content read from `--knowledge-dir`. A created page SHALL be added to the tree as a regular file with mode `100644`.

#### Scenario: Unchanged and dropped pages not committed

- **WHEN** the report has one `changed`, one `unchanged` and one `dropped` page
- **THEN** the commit touches only the changed page

#### Scenario: Created page committed

- **WHEN** the report has one `created` page
- **THEN** the commit's tree has that new path as a `100644` blob with the checkout's content

### Requirement: PR body

The PR body section for a run SHALL link every merged source PR that introduced a commit in `from..to`, and SHALL list the pages changed, created, unchanged and dropped, each dropped page with its reason. When the range has more commits than the compare API lists, the section SHALL say so. Each section SHALL start with a marker line naming the range. When appending a section would exceed the GitHub body limit, the oldest sections SHALL be removed and replaced with a note.

#### Scenario: Two source PRs in the range

- **WHEN** the range holds commits from merged source PRs 12 and 15
- **THEN** the section links both PRs and lists every report page with its result

### Requirement: Cursor advances last

`propose` SHALL advance the source cursor from `from` to `to` only after the branch update and the PR open or update succeed. A failed branch update or PR call SHALL exit 1 with the cursor unchanged. An initial plan SHALL write only the cursor and SHALL open no PR. A run whose report has no `changed` or `created` page SHALL make no commit and open or update no PR, and SHALL advance the cursor. A plan with `from` equal to `to` SHALL change nothing.

#### Scenario: Push fails

- **WHEN** the bot branch ref update fails
- **THEN** `propose` exits 1, no PR is opened and the cursor still holds `from`

#### Scenario: Initial plan

- **WHEN** the plan has `initial: true`
- **THEN** `propose` writes the cursor with `to`, opens no PR and makes no knowledge commit, and the result action is `none`

#### Scenario: Initial plan while a bot PR is open

- **WHEN** the plan has `initial: true`, a bot PR for the source is open and `--knowledge-base` is not its head
- **THEN** `propose` writes the cursor and reads neither the knowledge branches nor the checkout

#### Scenario: Nothing changed

- **WHEN** every report page is `unchanged` or `dropped`
- **THEN** no PR is opened or updated and the cursor holds `to`
