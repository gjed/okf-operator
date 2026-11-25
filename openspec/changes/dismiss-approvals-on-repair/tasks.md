# Tasks

## 1. Dismiss on repair

- [ ] 1.1 Dismiss the review App's approvals of the old head right after a repair moves the head, with a message that names the repair, in `internal/controller`, and add review list and dismissal endpoints to the GitHub fake; verify with `go test ./internal/controller ./internal/github/...` covering update-branch and regeneration dismissing only the review App's approval of the old head, a human's approval and another commit's approval staying, an approval of the current head staying, a PR that was not repaired losing nothing, no review being posted, and the message naming the repair

## 2. Docs and archive

- [ ] 2.1 Document the dismissal in `docs/state.md` and `docs/controller.md`; verify the markdown lint passes
- [ ] 2.2 Validate and archive the change; verify with `openspec validate --all --strict`
