# Tasks

## 1. Controller forgets deleted Sources

- [x] 1.1 Add `Tracker.forget` and call it from `SourceReconciler.Reconcile` on NotFound; verify with `go test ./internal/controller -run TestDeletedSourceLeavesStalenessGauge`, which collects the staleness gauge through an in-memory reader, shows both Sources' series before the delete, and shows the deleted one gone and the other kept after `Reconcile` sees NotFound, with its pending merge dropped

## 2. Docs

- [x] 2.1 Remove the deleted-`Source` caveat from `dashboards/README.md`, fix the staleness panel row, document the shared per-repo series in it and in `docs/controller.md`; verify with the markdown lint (`pre-commit run --all-files --config ~/.config/pre-commit/.pre-commit-config.yaml`)
