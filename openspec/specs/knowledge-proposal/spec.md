# knowledge-proposal Specification

## Purpose

Defines the `propose` subcommand: given a plan, its write report and the knowledge checkout `write` changed, it verifies the input contract, commits the changed pages to one bot branch per source through the GitHub REST API, opens or updates the bot PR, and only then advances the source cursor.

## Requirements

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

### Requirement: One bot PR per source

The bot branch for source `owner/name` SHALL be `okf/owner/name` in the knowledge repo, with at most one open PR from it into the default branch. When that PR is open, the new commit SHALL fast-forward the branch. Otherwise the branch SHALL be created or reset to a new commit on the default branch head and a PR SHALL be opened. All writes SHALL use the REST API; `propose` SHALL NOT need a git binary.

#### Scenario: New PR opened

- **WHEN** no bot PR is open for `acme/api` and the report has a changed page
- **THEN** branch `okf/acme/api` points at a new commit whose parent is the default branch head, one PR from it is open, and the result action is `opened`

#### Scenario: Existing bot PR updated

- **WHEN** a bot PR from `okf/acme/api` is open and `--knowledge-base` is its head
- **THEN** the branch fast-forwards to one new commit, no second PR is opened, the PR body gains a section for the new range, and the result action is `updated`

### Requirement: Checkout at the expected head

For a plan that is not initial, `--knowledge-base` SHALL equal the open bot PR's head, or the default branch head when no bot PR is open, and SHALL be the parent of the new commit. Otherwise `propose` SHALL exit 1 before any GitHub write, naming the branch and head to check out.

#### Scenario: Checkout behind the bot branch

- **WHEN** a bot PR is open and `--knowledge-base` is the default branch head
- **THEN** `propose` exits 1 naming `okf/acme/api` and its head, and makes no GitHub write

### Requirement: PR body

The PR body section for a run SHALL link every merged source PR that introduced a commit in `from..to`, and SHALL list the pages changed, created, unchanged and dropped, each dropped page with its reason. When the range has more commits than the compare API lists, the section SHALL say so. Each section SHALL start with a marker line naming the range. When appending a section would exceed the GitHub body limit, the oldest sections SHALL be removed and replaced with a note.

#### Scenario: Two source PRs in the range

- **WHEN** the range holds commits from merged source PRs 12 and 15
- **THEN** the section links both PRs and lists every report page with its result

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

### Requirement: Created pages are new files

Each page the report marks `created` SHALL be a regular file under `--knowledge-dir`, not a symlink, and SHALL be absent from the `--knowledge-base` tree. Otherwise `propose` SHALL exit 1 naming the page before any GitHub write.

#### Scenario: Created page already in the base

- **WHEN** the report marks a page `created` but the base tree has that path
- **THEN** `propose` exits 1 naming the page, and makes no GitHub write

#### Scenario: Created page is a symlink

- **WHEN** the report marks a page `created` and that path under `--knowledge-dir` is a symlink
- **THEN** `propose` exits 1 naming the page, and makes no GitHub write

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

### Requirement: Re-stamped pages are listed apart

The PR body section of a version 2 plan SHALL list each page whose result is `restamped` apart from the other pages, under "Provenance refreshed, content unchanged", with its reconcile reason, and SHALL NOT list it with the other pages' results.

#### Scenario: Reconcile section with a restamped page

- **WHEN** a version 2 plan with one changed page and one restamped page is proposed
- **THEN** the changed page is listed under the pages and the restamped page under "Provenance refreshed, content unchanged", with its reconcile reason

### Requirement: Bot PR merge state

The controller SHALL read the `mergeable_state` of the open bot PR through `GET /repos/{owner}/{repo}/pulls/{n}` at the start of a run, before the knowledge checkout is made. While GitHub answers `unknown` the read SHALL be retried at most four more times with a doubling delay starting at 500 ms, and a state that is still `unknown` SHALL be treated as unknown: the run SHALL carry on as if no state had been read. Only `behind` and `dirty` SHALL trigger a repair. No git binary SHALL be used.

#### Scenario: State resolves after retries

- **WHEN** the first two reads of the PR answer `unknown` and the third answers `behind`
- **THEN** the run treats the PR as `behind`

#### Scenario: State never resolves

- **WHEN** every read answers `unknown`
- **THEN** the run makes five reads, repairs nothing and carries on on the PR's head

#### Scenario: Clean PR

- **WHEN** the PR is `clean`
- **THEN** no update-branch call and no regeneration happens

### Requirement: Behind PR is updated

For a PR whose state is `behind`, the controller SHALL call `PUT /repos/{owner}/{repo}/pulls/{n}/update-branch` with `expected_head_sha` set to the head the run read, make no model call for it, wait until the PR's head differs from the old head (bounded as the merge-state read is), and check out and plan on the new head. A 422 answer, or a head that does not move, SHALL fail the run before any other GitHub write.

#### Scenario: Behind PR

- **WHEN** the bot PR is `behind` and the default branch has a commit the branch lacks
- **THEN** one update-branch request carries the old head as `expected_head_sha`, no model call is made for it and the run continues with the new head as the knowledge base

#### Scenario: Head moved

- **WHEN** another push moved the bot branch after the run read its head and update-branch answers 422
- **THEN** the run fails and the cursor is unchanged

### Requirement: Conflicting pages are found from the git trees

For a PR whose state is `dirty`, the controller SHALL read, with the REST git data and compare APIs only, the trees of the default branch head D, the PR head H and their merge base B. A path is changed by a side when its entry (mode and blob, or absence) differs between B and that side's commit. A path SHALL conflict when both sides changed it and its entries at D and H differ.

#### Scenario: Pages changed on both sides

- **WHEN** the bot branch and the default branch both changed `docs/a.md` differently, only the bot branch changed `docs/b.md`, and only the default branch changed `docs/c.md`
- **THEN** exactly `docs/a.md` conflicts

#### Scenario: Same change on both sides

- **WHEN** both sides changed a page to identical content
- **THEN** the page is not a conflict

### Requirement: Conflicting PR is regenerated

When pages conflict, the controller SHALL run `plan.Build` over the range `FROM..TO` of the PR in a checkout of D, where `FROM` is the start of the first `<!-- okf:range FROM..TO -->` marker of the PR body and `TO` the end of the last, and `write` for only the conflicting pages. The page's content at D SHALL be the base the model updates. A repair SHALL NOT read or write the cursor.

#### Scenario: Whole range

- **WHEN** the PR body holds the ranges `A..B` and `B..C` and `docs/a.md` conflicts
- **THEN** `write` runs for `docs/a.md` over `A..C`

### Requirement: Regeneration commit

The controller SHALL create a commit whose parents are D and H and whose tree is D's tree, with H's entry or deletion for each path only the bot branch changed, and the regenerated content of each conflicting page that `write` reported `changed` or `created`; any other conflicting page keeps D's version. A `dirty` PR with no conflicting page SHALL get the same commit without a model call.

#### Scenario: Conflicting page regenerated

- **WHEN** only `docs/a.md` conflicts, the bot branch alone changed `docs/b.md` and the default branch alone changed `docs/c.md`
- **THEN** `write` runs for `docs/a.md` only, over the PR's whole range; the new commit has parents D and H; `docs/a.md` holds the regenerated page, `docs/b.md` the bot branch's version and `docs/c.md` the default branch's

#### Scenario: Human-edited page

- **WHEN** a human edited a page on the default branch after the bot branch was created and the bot branch edited it too
- **THEN** the model receives the human's version of the page as the current page, and the regenerated page carries the new provenance

#### Scenario: Regeneration dropped

- **WHEN** `write` reports a conflicting page `unchanged` or `dropped`
- **THEN** the page keeps the default branch's version in the new commit and the PR body note says so

### Requirement: Regeneration is published without force

The controller SHALL move the bot branch to the regeneration commit with `force: false`, append a note to the PR body listing the regenerated pages with their results, and continue the run on the new head, so the PR keeps its number and review history.

#### Scenario: Branch moved during the repair

- **WHEN** the bot branch moved after the repair read its head
- **THEN** moving the branch is refused as not a fast-forward, the run fails and the cursor is unchanged

### Requirement: Regeneration refusals

The repair SHALL fail the run, with a message naming the PR and the reason and before any write, when the PR body's ranges are not contiguous, when it records omitted earlier ranges, when it has no range, when it holds a section written by `reconcile`, or when a conflicting path is not a page of the range's plan.

#### Scenario: Omitted ranges

- **WHEN** the PR body holds a note that earlier ranges were omitted
- **THEN** the run fails, nothing is written and the message names the PR

#### Scenario: Reconcile section

- **WHEN** the PR body holds a section written by `reconcile`
- **THEN** the run fails and nothing is written

### Requirement: Repair telemetry

Each update-branch call and each conflict regeneration SHALL add 1 to the counter `okf.propose.prs.repaired`, labelled by source repo and by the attribute `okf.propose.repair` with the value `update_branch` or `regenerate`. The counters `okf.propose.prs.opened` and `okf.propose.prs.updated` SHALL NOT count a repair.

#### Scenario: Counter on regeneration

- **WHEN** a run regenerates a conflicting PR
- **THEN** `okf.propose.prs.repaired` is 1 with `okf.propose.repair=regenerate`
