# Proposal

## Why

When the model answers `NO_CHANGE` for a reconcile page, `write` reports it `unchanged` and leaves the file alone, so its provenance never moves. A `stale` page keeps its old `source_sha` and a `widened` page ([#28](https://github.com/gjed/okf-operator/pull/28)) never records its new `routed_paths`: every later `reconcile` plans the same pages again and pays a model call for each, for no change ([#29](https://github.com/gjed/okf-operator/issues/29), review thread on [#28](https://github.com/gjed/okf-operator/pull/28#discussion_r4658268385)). This belongs to the reconcile step of the plan, after [#28](https://github.com/gjed/okf-operator/pull/28).

## What Changes

- `write` re-stamps a page of a version 2 plan that the model leaves unchanged (`NO_CHANGE`, or the current page returned unchanged apart from provenance): it refreshes the provenance keys through `provenance.Stamp` and keeps the body, the other frontmatter keys, their order and the comments. The page is reported under the new result `restamped`. A re-stamp that would not change the file is reported `unchanged`.
- A version 1 plan (`plan` runs) keeps today's behaviour: an unchanged page is untouched and reported `unchanged`.
- The write report `version` becomes `3`. `propose` accepts version 3 only and fails with a message that tells the user to re-run `write`; it rejects a `restamped` result on a version 1 plan.
- `propose` commits a `restamped` page like a changed one, as part of the same bot PR and commit. The reconcile PR body lists such pages apart, under "Provenance refreshed, content unchanged". The cursor moves as before: after the PR call, and a run with only unchanged and dropped pages still makes no commit.
- `write` counts `restamped` in the existing `okf.write.pages` counter (`okf.write.result=restamped`) and in a new span attribute `okf.write.pages.restamped`. No new metric.
- `review` passes a page whose head differs from its base only in provenance, with no model call, and logs the reason `provenance_only`.
- `gate` is unchanged: a re-stamped page is a modified page that carries the plan's `to`, so change-shape, size and the provenance rule apply to it as to any page. Auto-merge eligibility is still decided by the plan version, and a reconcile PR still always goes to a human; this change does not alter that.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `page-write`: re-stamping in reconcile plans, the `restamped` result, report version 3, the propose input contract and telemetry.
- `provenance`: re-stamping keeps the body, the other keys, their order and the comments.
- `knowledge-proposal`: report version 3, `restamped` accepted for reconcile plans only, commits and PR body section, nothing-changed rule.
- `auto-merge-gate`: a frontmatter-only modified page passes the deterministic rules.
- `llm-review`: a provenance-only page passes without a model call.
- `reconcile`: a re-stamped page is not planned again.

## Impact

- `internal/write` (result, report version, re-stamp, telemetry), `internal/propose` (report version, result check, verify, PR body), `internal/review` (provenance-only pages); tests in `internal/gate`, `internal/reconcile` and `internal/cli`.
- Report version 3: a `propose` from an older release rejects the new report with a version error, and this `propose` rejects a version 2 report. `write` and `propose` ship in one binary and the controller runs both in process, so no deployed pair mixes versions.
- No change to `internal/plan`, `internal/provenance`, `internal/gate`, `go.mod`, `api/` or `deploy/`. Dashboards that read `okf.write.result` see a fifth value, `restamped`.
- Docs: `docs/write.md`, `docs/reconcile.md`, README.
