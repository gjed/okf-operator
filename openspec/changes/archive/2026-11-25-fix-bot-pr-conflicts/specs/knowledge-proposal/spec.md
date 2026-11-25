# Spec Delta

## ADDED Requirements

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
