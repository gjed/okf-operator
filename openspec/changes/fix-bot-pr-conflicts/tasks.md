# Tasks

## 1. Propose: merge state and update-branch

- [ ] 1.1 Add the bounded merge-state read and update-branch to `internal/propose` and `internal/github`, and extend the GitHub fake with merge states, the update-branch endpoint and merge bases; verify with `go test ./internal/propose ./internal/github/...` covering `clean`, `behind`, `dirty`, `unknown` resolving after retries, `unknown` after every retry, `expected_head_sha` on the request, a 422 failing with no other write, a head that never moves, and the `okf.propose.prs.repaired` counter

## 2. Propose: conflict regeneration

- [ ] 2.1 Add range parsing of the PR body, conflict detection from three trees and the two-parent regeneration commit to `internal/propose`; verify with `go test ./internal/propose` covering exactly the pages both sides changed being picked, identical changes not conflicting, the commit's parents and tree, a human-edited page as the model's base, a dropped regeneration keeping the default's page, the refusals (omitted ranges, non-contiguous ranges, reconcile section, conflicting non-page path), a moved branch failing, and the cursor untouched

## 3. Gate

- [ ] 3.1 Add the `mergeable` rule, the merge state to the PR reader and the rule to `approve`'s required set; verify with `go test ./internal/gate ./internal/approve ./cmd/...` covering a `dirty` PR failing, `behind`, `clean` and `unknown` passing, the report order, the golden reports and a report without the rule yielding `report_incomplete`

## 4. Controller

- [ ] 4.1 Read the merge state, update the branch and regenerate in the run before the checkout, carry the plan behind the new head, and add the `repair` stage; verify with `go test ./internal/controller` covering a behind PR, a conflicting PR repaired with a model stub, a refused repair failing the run, the cursor never moving backwards, and a dirty PR's approval being dismissed by the gate

## 5. Docs, dashboard and archive

- [ ] 5.1 Document the repairs in `docs/state.md`, `docs/controller.md`, `docs/policy.md`, the README and `dashboards/README.md`, and add the repairs panel to the dashboard; verify the markdown lint passes and the dashboard JSON parses
- [ ] 5.2 Validate and archive the change; verify with `openspec validate --all --strict`
