# Tasks

## 1. Shared prompt plumbing

- [x] 1.1 Move the delimiter neutralisation to `prompts.Neutralise` in `prompts/untrusted.go`, make `write`'s `neutralise` call it, add `prompts/review-system.md` and `prompts/review.md` with their embeds, and verify `go test ./prompts/... ./internal/write/...` passes unchanged

## 2. Review core

- [x] 2.1 Implement `internal/review`: result types (version 1), `Encode`, strict `Decode`, `Run` reading the PR through `gate.Reader` and hunks through `plan.GitHubDiffer.Range`, prompt rendering with provenance alignment, strict reply parsing, budget, and `GateInput`. Verify tests against `githubtest` and an httptest model: supported change passes; unsupported sentence fails and is listed; prose, missing key and contradicting replies fail with `reply_invalid`; model HTTP 500 returns an error and no result; a page that tries to instruct the reviewer reaches the model neutralised and is judged by the reply only; missing patch, removed page, page outside the plan and over-budget page fail without a call
- [x] 2.2 Add review telemetry and verify a test with in-memory span and metric exporters that asserts the attributes and the `okf.review.pages` and `okf.review.tokens` points

## 3. Review CLI

- [x] 3.1 Add `internal/cli/review.go` with the flags, reviewer model settings (no writer fallback), two narrowed providers, and register it. Verify `review -h`, usage errors (missing `--pr`, writer-only model settings exit 2), an end-to-end run printing the result, App-mode token scopes, and a secrets test proving the reviewer key, the writer key, `GITHUB_TOKEN` and App tokens never reach stdout, stderr or spans

## 4. Gate integration

- [x] 4.1 Add `gate.Review`, the `review` rule after `links`, `gate.Decode`, and `--review <file>` in `internal/cli/gate.go`. Verify tests: failing deterministic rule plus passing review gives `pass: false`; all deterministic rules passing plus failing review gives `pass: false`; review for another head SHA or PR exits 1; two runs with the same review are byte-identical

## 5. Approve and the review App

- [x] 5.1 Add `CredentialNames` and `LoadNamed` to `internal/github` with `Load` unchanged in behaviour, and verify the existing `internal/github` tests pass plus a test for the review names
- [ ] 5.2 Add the pull request reviews endpoint to `githubtest`, `internal/approve`, `internal/cli/approve.go`, and register it. Verify tests: approval posted on the head SHA; declines for a failed gate, a missing review rule, a hand-edited `pass`, and a moved head; report for another PR exits 1; writer-only credentials exit 2; App-mode token scope; review App secrets never logged
- [ ] 5.3 Add `deploy/review-app-manifest.json`, document it and `approve` in `docs/github-app.md`, and verify a test that parses the manifest and checks its permissions

## 6. Docs and archive

- [ ] 6.1 Document `review`, `gate --review` and `approve` in `README.md`, and verify the markdown lint passes
- [ ] 6.2 Archive the change with `openspec archive add-llm-review -y`, and verify `openspec validate --all --strict` passes
