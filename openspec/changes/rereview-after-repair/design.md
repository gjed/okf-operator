# Design

## Context

See proposal.md - Why. `Run` ends a run whose `propose` pushed nothing in `regate` when the controller remembers the plan behind the head. `regate` only gates, so it cannot restore an approval that the repair's dismissal removed.

## Decisions

### Track the move in the run, not in the state

The run records `repaired` where it moves the head: when update-branch answers a head other than the one it read, and when the regeneration commit is pushed. It does not infer the move from `gatedPlans` or from the head of a later run. A head that an earlier run moved and failed after is not re-reviewed by the next run: that run finds no move of its own, and the next push or source commit reviews it. Rejected: comparing the head with the remembered plan's head, which cannot tell a repair from a human push and would review whatever a person pushed.

### Reuse `gateAndApprove`

After a repair with nothing pushed, `Run` calls `gateAndApprove` on the repaired head instead of `regate`. The deterministic gate runs first, so a head that fails it dismisses the review App's approvals of it and costs no reviewer tokens, and only a head that passes is reviewed, gated again with the review and approved. No second path to maintain.

### Review the whole range

The plan is the one `gated` holds for the repaired head. After a regeneration that is the full-range plan of `propose.Repair`, which `Run` already stores; after update-branch it is the old head's plan carried over, because the new head only merges the default branch in. Building a narrower plan from the regenerated pages was rejected: the approval covers the whole head, so the review must too, and a plan that omits a changed page would make `review` fail that page with `not_in_plan` and the head never auto-merges.

### Token spend

No new cap. `review` already limits pages (`maxPages`) and tokens per page, and a repair yields one run and so one review. A repeatedly conflicting PR costs one review per repair, the same as one review per pushed update. Reviewing only the regenerated pages was rejected for the reason above.

### Reconcile PRs and a missing plan

A reconcile PR is never put in `gated`, so a repaired one finds no plan and goes to a human as before. The same holds for a PR whose plan is lost on restart: it is not gated, reviewed or approved until a run pushes to it. Persisting the plan on `kb-state` stays the upgrade path noted on `gatedPlans`; it is out of scope.

### Telemetry

The review and approval stages already have their spans and `okf.controller.prs.approved`. A repair re-review is told apart by an info log line naming the PR and head, so no attribute or metric is added.
