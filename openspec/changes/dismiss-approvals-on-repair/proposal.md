# Proposal

## Why

A repair that moves a bot PR's head leaves the review App's approval of the old head in place ([#43](https://github.com/gjed/okf-operator/issues/43), raised in the review of [#42](https://github.com/gjed/okf-operator/pull/42)). After update-branch or a regeneration with no new source commits, the run ends in `regate`, which only runs the deterministic gate and never reviews or approves again, and `dismissStale` dismisses approvals of the head the run evaluated only. The old approval is cleared only if the knowledge repo enables "Dismiss stale pull request approvals when new commits are pushed", so without that setting a regenerated head with new model-written text can auto-merge on an approval of different content. The controller should not rely on a repo setting it cannot verify. This belongs to the controller run, in the step that repairs the bot PR before the checkout.

## What Changes

- Every run with an open bot PR dismisses, with the review App, every approving review the review App posted on a commit other than the PR's current head, through the existing dismissal path. It does so right after the head is settled (after update-branch or the regeneration commit) and before the checkout of that head, and the message names the commit. A run that fails after a repair moved the head, or whose dismissal failed, is therefore covered by the next run.
- Reviews by other users and the review App's approvals of the current head are left alone.
- The repaired head is not reviewed or approved again by that run: that is tracked in [#44](https://github.com/gjed/okf-operator/issues/44) and out of scope.
- The docs say the dismissal no longer depends on the repository setting.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `controller`: a repair dismisses the review App's approvals of the old head.

## Impact

- `internal/controller` (dismissal reason, call after the repair), the GitHub fake in `internal/github/githubtest` (review list and dismissal endpoints), and tests.
- No change to `api/`, `deploy/`, the gate or the CLI. The review App already has pull requests write, which the dismissal needs.
- Docs: `docs/state.md`, `docs/controller.md`.
