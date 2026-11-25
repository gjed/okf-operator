# Tasks

## 1. Review the repaired head

- [x] 1.1 In `internal/controller/runner.go`, record whether this run moved the bot PR head (update-branch or regeneration commit) and, when `propose` pushes nothing and the plan behind the head is remembered, run `gateAndApprove` instead of `regate`; verify with `go test ./internal/controller` covering update-branch and regeneration each reviewing the whole range once and approving the new head, an unrepaired run only gating, a repaired reconcile PR never reviewed, a repaired head without its plan not gated, and a failing gate dismissing without a review

## 2. Docs and archive

- [x] 2.1 Say in `docs/state.md` and `docs/controller.md` that a repaired head is reviewed and approved again; verify the markdown lint passes
- [x] 2.2 Validate and archive the change; verify with `openspec validate --all --strict`
