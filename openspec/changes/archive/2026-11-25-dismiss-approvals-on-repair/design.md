# Design

## Context

See proposal.md - Why. The controller's `dismissStale` dismisses the review App's approvals of one commit, and the run calls it when a gate run on a head fails. A repair moves the head before the gate sees the new one, and `dismissStale` is not called for the old head.

## Decisions

### Dismiss every superseded head, on every run

A repair moves the head in one place (update-branch in the checkout stage, the regeneration push in the repair stage) and a later step can fail before a dismissal that only compared the old and new head of this run. The next run would then read the moved head as the PR head and never dismiss the old approval. So the run dismisses the review App's approvals of every commit other than the current head, whenever a bot PR is open. The review App approves only the head it evaluated, so an approval of any other commit describes content that is no longer the PR's, whether a repair, a human push or an earlier failed run left it there. The cost is one list-reviews call per run with an open PR.

### Dismiss before the checkout of the moved head

The dismissal is its own stage, right after the head is settled and before the tarball of the new head is downloaded, so a failing checkout, routing fetch or write cannot leave the old approval in place. For a conflicting PR it runs after the regeneration push and before the checkout of the new head. The checkout of the PR head is split from finding and syncing the PR for this.

### Reuse the dismissal path, with a reason

`dismissStale` and the `Dismiss` stage step take the sentence that names the commit, so the message says the bot branch moved off that commit instead of that a gate run failed. `dismissExcept` shares the list-and-dismiss code with `dismissStale` and takes a commit to keep instead of one to dismiss.

### Failure fails the run

A failed dismissal fails the run at the `dismiss` stage, as a dismissal after a failed gate does, and the next run dismisses again because the approval is still on a commit that is not the head. Rejected: ignoring the error, which would leave an approval silently in place.

### No re-review

The repaired head is gated, reviewed and approved only when a later run pushes to it. Reviewing it without a new source commit is [#44](https://github.com/gjed/okf-operator/issues/44).
