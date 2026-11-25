# Proposal

## Why

A bot PR whose branch conflicts with, or falls behind, the knowledge repo's default branch is never repaired ([#41](https://github.com/gjed/okf-operator/issues/41)). Another PR that edits the same page merges first, and every later run checks out the open bot PR's head, commits on it and fast-forwards the branch, so the conflict stays. Nothing reads `mergeable_state` or calls update-branch, and `gate` can approve a head that cannot merge. The cursor has already moved past the PR's range, so the PR's changes come back only through a weekly reconcile that always goes to a human. A PR that is only behind, under branch protection that requires up-to-date branches, is never updated either. This belongs to the propose step and to the controller run that wraps it, before the gate.

## What Changes

- At the start of a run with an open bot PR the controller reads the PR's `mergeable_state` through REST. GitHub answers `unknown` while it computes the state, so the read retries a few times with a bounded backoff and then treats the state as unknown and carries on as today.
- `behind` (no conflict): the controller calls `PUT /repos/{owner}/{repo}/pulls/{n}/update-branch` with `expected_head_sha` set to the head the run read, waits for the branch to move and continues the run on the new head. No model call. A 422 (the head moved, or the update is no longer clean) fails the run before anything is written.
- `dirty` (conflict): the controller regenerates the conflicting pages on top of the default branch head. A page conflicts when both the bot branch and the default branch changed it since their merge base and now differ; this is decided from the git data API trees, with no git binary. `write` runs for only those pages over the PR's whole source range, read from the `<!-- okf:range FROM..TO -->` markers of the PR body, on top of the default branch's version of the page, so a page a human edited is the base the model updates. The result is pushed as a commit whose parents are the default branch head and the PR head, with `force: false`: the PR keeps its number and review history. Bot pages that do not conflict keep the bot branch's content, default-branch changes that do not conflict keep the default's. The run then continues on the new head as for any open PR.
- A PR that cannot be regenerated (a reconcile section, ranges the body no longer lists in full, a conflicting path that is not a page of the plan) fails the run with a message naming the reason.
- `gate` gets a rule `mergeable` that fails when the PR's `mergeable_state` is `dirty`, so the review App never approves a PR that cannot merge, and the controller's existing stale-approval dismissal covers it. `approve` requires the rule in the report.
- The cursor never moves backwards: neither the update-branch nor the regeneration touches it.
- A new counter `okf.propose.prs.repaired`, attribute `okf.propose.repair` = `update_branch` or `regenerate`, counts each repair. The dashboard gets a panel for it.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `knowledge-proposal`: merge-state read, update-branch, conflict regeneration, repair telemetry.
- `auto-merge-gate`: the `mergeable` rule and the report's rule order.
- `review-approval`: `approve` requires the `mergeable` rule.
- `controller`: run order with the merge-state read, update-branch and regeneration.
- `source-cursor`: a repair never moves the cursor.
- `dashboards`: a panel for repairs.

## Impact

- `internal/propose` (merge state, update-branch, conflict detection and regeneration, telemetry), `internal/gate` (rule, PR reader), `internal/approve` (required rules), `internal/controller` (run order, stage), `internal/github` (merge-state polling), and the GitHub fake in `internal/github/githubtest`.
- Gate report: a seventh deterministic rule. A report from before this change lacks `mergeable`, so `approve` answers `report_incomplete` for it; reports are produced and consumed in the same run, so no deployed pair mixes them.
- The writer App's knowledge-repo token already has `contents` and `pull_requests` write, which update-branch needs. No change to `api/`, `deploy/` or the CRDs.
- Docs: `docs/state.md`, `docs/controller.md`, `docs/policy.md`, README, `dashboards/README.md`.
