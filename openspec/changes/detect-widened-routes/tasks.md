# Tasks

## 1. Provenance records the routed set

- [x] 1.1 Add the optional `routed_paths` key to `internal/provenance` (`Fields.RoutedPaths`, nil when absent or null, removed by `Stamp` when nil) and to write's provenance-key exception; verify with `go test ./internal/provenance` covering round trip with and without the key, absent versus empty, wrong type and repeated key rejected, and a model-written value removed for an unknown set

## 2. Write stamps the routed set

- [x] 2.1 Add reason `widened` and the optional `routed_paths` entry key to `internal/plan`, validate them in `write.DecodePlan`, and stamp `routed_paths` in `write` from the entry or carried forward; verify with `go test ./internal/plan ./internal/write` that the plan golden file and a version 2 plan digest without the key are unchanged, the entry's set is stamped, a model-written value is overwritten, a reply that drops the key is accepted, the version 1 carry-forward drops removed and renamed-away paths, a page without the set stays without it, a create records its `source_paths`, and unsorted `routed_paths` fail before any call

## 3. Reconcile detects widened routes

- [x] 3.1 Plan pages with reason `widened` in `internal/reconcile`, add `routed_paths` to every entry, count the reason in telemetry; verify with `go test ./internal/reconcile` covering a route widened to an unchanged file (ancestor and equal `source_sha`), an added file staying `stale`, a page without `routed_paths` planned as before, and the span and counter for `widened`

## 4. Gate and docs

- [ ] 4.1 Add gate provenance rule tests for pages with, without and with a wrong-typed `routed_paths`; verify with `go test ./internal/gate`
- [ ] 4.2 Document `routed_paths`, reason `widened` and the migration in `docs/reconcile.md`, `docs/write.md` and the README, remove the documented limit, and verify the markdown lint passes
