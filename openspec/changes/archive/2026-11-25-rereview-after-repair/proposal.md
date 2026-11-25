# Proposal

## Why

A bot PR head that a repair moved is never reviewed or approved again ([#44](https://github.com/gjed/okf-operator/issues/44)). Since [#45](https://github.com/gjed/okf-operator/pull/45) the controller dismisses the review App's approvals of the old head right after update-branch or a regeneration. A run that then pushes nothing ends in `regate`, which runs only the deterministic gate, so the PR stays without an approval and waits for a human until a new source commit triggers a full run. On a quiet source that can be a long time, and every conflict repair turns an auto-mergeable bot PR into a human-only one.

## What Changes

- When a run's repair moved the bot PR head, by update-branch (`behind`) or by the regeneration commit (`dirty`), and `propose` then pushes nothing, the run gates the repaired head, reviews it, gates it with the review and approves it, as a run that pushed would, instead of only gating it again. The review runs over the plan of the PR's whole range: the one remembered for the head, which after a regeneration is the full range of the repair and after update-branch the old head's plan. It is not narrowed to the regenerated pages, because the approval covers the whole head.
- A run that moved the head and whose `propose` pushes new commits is unchanged: that path already reviews and approves, once.
- A run that repaired nothing and pushes nothing still only gates the head again. It never reviews or approves.
- A reconcile PR is never reviewed or approved, repaired or not: the controller keeps no plan for it.
- A repaired head whose plan the controller no longer holds (after a restart) is neither gated, reviewed nor approved, as before.
- A repaired head that fails the deterministic gate has the review App's approvals of it dismissed and is not reviewed, so it costs no reviewer tokens.
- At most one review per repair. No new cap: the existing page and per-page token limits apply. No new metric.
- The docs no longer say a repaired head is not reviewed again.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `controller`: the dismissal of a superseded head's approvals is followed by a review and approval of the repaired head.

## Impact

- `internal/controller` (`runner.go`: track whether this run moved the head, choose between `gateAndApprove` and `regate`) and its tests; a `SetMergeStates` helper in `internal/github/githubtest`.
- No change to `api/`, `deploy/`, the gate, the review, the approval or the CLI. The review and approval stages are reused as they are.
- Docs: `docs/state.md`, `docs/controller.md`.
