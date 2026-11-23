# Proposal

## Why

The controller keeps a staleness entry per `Source` in memory and nothing removes it, so a deleted `Source` keeps reporting a growing `okf.controller.cursor.staleness`. `OkfOperatorCursorStale` fires for its source repo four hours later and cannot clear until the controller restarts ([#37](https://github.com/gjed/okf-operator/issues/37), found in the review of [#35](https://github.com/gjed/okf-operator/pull/35)). This belongs to the controller telemetry and the dashboards from the Grafana dashboards change, which documented the effect as a known limit.

## What Changes

- The controller drops a `Source`'s pending merge time and staleness entry when a reconcile finds the `Source` gone (NotFound). Any other read error leaves the entries alone and is returned as before.
- `okf.controller.cursor.staleness` keeps the `okf.source.repo` attribute and gains no `Source` attribute. Two `Source`s on one source repo still share a series; this is now documented instead of changed.
- `dashboards/README.md` loses the deleted-`Source` caveat (restart the controller or silence the alert) and says a deleted `Source` stops reporting. `docs/controller.md` says the same.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `controller`: new requirement, telemetry forgets a deleted `Source`.
- `dashboards`: new requirement, the stale-cursor alert resolves for a deleted `Source` without a restart.

## Impact

- `internal/controller` (`Tracker.forget`, `SourceReconciler.Reconcile`) and its tests; `dashboards/README.md`, `docs/controller.md`.
- No change to the exported metric names, attributes or units, the dashboard JSON, the alert rules, `api/`, `deploy/` or `go.mod`.
- A `Source` deleted and re-created under the same name starts with no staleness entry until a run reaches HEAD again, as after a restart.
