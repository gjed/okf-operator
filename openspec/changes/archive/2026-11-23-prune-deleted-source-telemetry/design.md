# Design

## Context

`Tracker` holds `merges` and `caughtUp`, both keyed by Source `namespace/name`. The staleness gauge callback observes every `caughtUp` entry, labelled by the Source's repository. `Reconcile` returns on a NotFound read of the `Source` without touching the tracker.

## Decisions

- **Prune in `Reconcile`, on NotFound.** A `Source` deletion enqueues a reconcile through the controller's watch, so the reconcile that reads NotFound is the one place that sees every deletion, with no finalizer and no extra watch. If the controller is down during the deletion, the restart starts with empty maps, so nothing leaks.
- **One unexported `Tracker.forget`** drops the key from both maps under the mutex. `merges` is pruned too: a deleted `Source`'s pending merge time would otherwise sit in memory for ever.
- **Keep `okf.source.repo` as the only attribute.** The dashboard's `Source` variable, its staleness panel and `OkfOperatorCursorStale` (`max by (okf_source_repo)`) all key on that label, and every other per-source metric uses it. A `Source` attribute would change the series identity and force edits to the panel, the alert and its tests for a case that is rare: two `Source`s on one repo. The cost is that those two share one series, the SDK keeps one of the two observations, and the series stays while either exists. That is documented rather than fixed.

## Risks

- A transient NotFound from a stale cache drops an entry that the next run that reaches HEAD records again. The gauge has a gap until then, and `OkfOperatorCursorStale` cannot fire from it.
