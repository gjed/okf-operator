# knowledge-proposal Specification

## Purpose

Defines the `propose` subcommand: given a plan, its write report and the knowledge checkout `write` changed, it verifies the input contract, commits the changed pages to one bot branch per source through the GitHub REST API, opens or updates the bot PR, and only then advances the source cursor.

## Requirements

### Requirement: Propose inputs

`propose` SHALL require `--plan <file>`, `--report <file|->` (`-` for stdin), `--knowledge-dir`, `--knowledge-repo` and `--knowledge-base <sha>` (the commit the checkout is at), and accept the GitHub App flags. A missing flag or a `--knowledge-base` that is not a full commit SHA SHALL exit 2. A plan or report whose `version` is not `1`, or with unknown keys, SHALL exit 1.

#### Scenario: Missing knowledge base

- **WHEN** `propose` runs without `--knowledge-base`
- **THEN** it exits 2 and the usage names the missing flag

#### Scenario: Report from stdin

- **WHEN** the user runs `okf-operator write --plan plan.json --knowledge-dir kb | okf-operator propose --plan plan.json --report - ...`
- **THEN** `propose` reads the report from stdin

### Requirement: Plan and report must agree

`propose` SHALL exit 1 before any GitHub call when the plan and the report differ in `source`, `bundle`, `from` or `to`, or when the set of report page paths differs from the set of plan page paths.

#### Scenario: Report from another range

- **WHEN** the report's `to` differs from the plan's `to`
- **THEN** `propose` exits 1 with an error naming the field, and makes no GitHub call

#### Scenario: Report lists a page outside the plan

- **WHEN** the report lists a path that is not a plan page path
- **THEN** `propose` exits 1 and makes no GitHub write

### Requirement: Input contract is verified, not trusted

Before any GitHub write, `propose` SHALL compare every file under `--knowledge-dir`, except the top-level `.git`, with the recursive tree of `--knowledge-base`. Pages the report marks `changed` SHALL differ from the base; every other path SHALL be present on both sides and byte-identical. A truncated tree or any other difference SHALL exit 1 naming the offending paths.

#### Scenario: Unreported file modified

- **WHEN** a file under `--knowledge-dir` differs from the base tree and the report does not mark it `changed`
- **THEN** `propose` exits 1 naming the file, and makes no GitHub write

#### Scenario: Changed page identical to base

- **WHEN** the report marks a page `changed` but its file equals the base tree's blob
- **THEN** `propose` exits 1 naming the page, and makes no GitHub write

### Requirement: Only changed pages are committed

`propose` SHALL commit only the pages the report marks `changed`, with their content read from `--knowledge-dir`.

#### Scenario: Unchanged and dropped pages not committed

- **WHEN** the report has one `changed`, one `unchanged` and one `dropped` page
- **THEN** the commit touches only the changed page

### Requirement: One bot PR per source

The bot branch for source `owner/name` SHALL be `okf/owner/name` in the knowledge repo, with at most one open PR from it into the default branch. When that PR is open, the new commit SHALL fast-forward the branch. Otherwise the branch SHALL be created or reset to a new commit on the default branch head and a PR SHALL be opened. All writes SHALL use the REST API; `propose` SHALL NOT need a git binary.

#### Scenario: New PR opened

- **WHEN** no bot PR is open for `acme/api` and the report has a changed page
- **THEN** branch `okf/acme/api` points at a new commit whose parent is the default branch head, one PR from it is open, and the result action is `opened`

#### Scenario: Existing bot PR updated

- **WHEN** a bot PR from `okf/acme/api` is open and `--knowledge-base` is its head
- **THEN** the branch fast-forwards to one new commit, no second PR is opened, the PR body gains a section for the new range, and the result action is `updated`

### Requirement: Checkout at the expected head

`--knowledge-base` SHALL equal the open bot PR's head, or the default branch head when no bot PR is open, and SHALL be the parent of the new commit. Otherwise `propose` SHALL exit 1 before any GitHub write, naming the branch and head to check out.

#### Scenario: Checkout behind the bot branch

- **WHEN** a bot PR is open and `--knowledge-base` is the default branch head
- **THEN** `propose` exits 1 naming `okf/acme/api` and its head, and makes no GitHub write

### Requirement: PR body

The PR body section for a run SHALL link every merged source PR that introduced a commit in `from..to`, and SHALL list the pages changed, unchanged and dropped, each dropped page with its reason. When the range has more commits than the compare API lists, the section SHALL say so. Each section SHALL start with a marker line naming the range. When appending a section would exceed the GitHub body limit, the oldest sections SHALL be removed and replaced with a note.

#### Scenario: Two source PRs in the range

- **WHEN** the range holds commits from merged source PRs 12 and 15
- **THEN** the section links both PRs and lists every report page with its result

### Requirement: Cursor advances last

`propose` SHALL advance the source cursor from `from` to `to` only after the branch update and the PR open or update succeed. A failed branch update or PR call SHALL exit 1 with the cursor unchanged. An initial plan SHALL write only the cursor and SHALL open no PR. A run whose report has no `changed` page SHALL make no commit and open or update no PR, and SHALL advance the cursor. A plan with `from` equal to `to` SHALL change nothing.

#### Scenario: Push fails

- **WHEN** the bot branch ref update fails
- **THEN** `propose` exits 1, no PR is opened and the cursor still holds `from`

#### Scenario: Initial plan

- **WHEN** the plan has `initial: true`
- **THEN** `propose` writes the cursor with `to`, opens no PR and makes no knowledge commit, and the result action is `none`

#### Scenario: Nothing changed

- **WHEN** every report page is `unchanged` or `dropped`
- **THEN** no PR is opened or updated and the cursor holds `to`

### Requirement: Propose result

On success, `propose` SHALL print one JSON document to stdout with `version: 1`, `source`, `bundle`, `from`, `to`, `action` (`opened`, `updated` or `none`), `pr` (number and URL, null when `action` is `none` and no bot PR is open), `commit` (empty when nothing was committed) and `cursor_advanced`.

#### Scenario: Opened PR in the result

- **WHEN** a run opens PR 7
- **THEN** stdout has `action: "opened"`, `pr.number: 7`, the PR URL and `cursor_advanced: true`

### Requirement: Propose telemetry

Each `propose` run SHALL annotate the dispatcher's `propose` span with the source repo, bundle, action, PR number and whether the cursor advanced, and SHALL record the counters `okf.propose.prs.opened`, `okf.propose.prs.updated` and `okf.propose.cursor.advances`, labelled by source repo and bundle. No token, page content or PR body SHALL become an attribute.

#### Scenario: Span and counters

- **WHEN** a run opens a PR with in-memory exporters installed
- **THEN** the `propose` span carries the action and PR number, `okf.propose.prs.opened` and `okf.propose.cursor.advances` are 1 and `okf.propose.prs.updated` is 0
