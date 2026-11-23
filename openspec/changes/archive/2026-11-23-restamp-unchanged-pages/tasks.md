# Tasks

## 1. Write re-stamps unchanged reconcile pages

- [x] 1.1 Add the `restamped` result, report version 3 and the re-stamp of unchanged pages of version 2 plans in `internal/write`, with the `okf.write.result=restamped` counter value and the `okf.write.pages.restamped` span attribute; verify with `go test ./internal/write` covering `NO_CHANGE` and a same-content reply for stale, widened and backfill pages, body byte for byte, other keys, key order and comments kept, an identical re-stamp reported `unchanged`, a version 1 plan leaving an unchanged page untouched, an update staying `changed`, all-or-nothing on a model failure, and the counter and span attribute

## 2. Propose commits re-stamped pages

- [x] 2.1 Accept report version 3 only, accept `restamped` for version 2 plans only, commit re-stamped pages with the changed ones and list them apart in the reconcile PR body, in `internal/propose`; verify with `go test ./internal/propose ./internal/cli` covering a version 2 report and an unknown version rejected, an unknown result rejected, `restamped` on a version 1 plan rejected before any write, a re-stamped-only run opening a PR and moving the cursor after it, the body section, and a re-stamped page identical to the base rejected

## 3. Gate and review

- [x] 3.1 Pass provenance-only pages in `internal/review` without a model call; verify with `go test ./internal/review` covering a provenance-only page, a changed custom frontmatter key still reviewed, and no source compare for the page
- [x] 3.2 Pin the gate's treatment of frontmatter-only pages; verify with `go test ./internal/gate` for a re-stamped page passing every rule and one stamped at another commit failing the provenance rule

## 4. End to end and docs

- [x] 4.1 Add a test with GitHub and model fakes where a reconcile plan holds one stale and one widened page and the model answers `NO_CHANGE` for both: the first run re-stamps both, the second reconcile plans nothing and makes no model call; verify with `go test ./internal/reconcile`
- [x] 4.2 Document the `restamped` result, report version 3 and the decisions in `docs/write.md`, `docs/reconcile.md` and the README; verify the markdown lint passes
