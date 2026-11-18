# Tasks

## 1. Policy

- [x] 1.1 Implement the v1 schema, `Version`, `DefaultPath`, `Parse`, `Load` and strict decoding in `internal/policy`, and verify tests that an unknown key and `version: 2` fail and that a policy without `auto_merge` loads with empty lists
- [x] 1.2 Implement `Validate` (empty or duplicate type, invalid or duplicate bundle, missing or non-positive limits) collecting every error, and verify table tests with one case per rule plus one multi-error policy
- [x] 1.3 Add `examples/policy.yaml` and `docs/policy.md`, and verify a test that parses the example without errors and the markdown lint passes

## 2. Test fake

- [x] 2.1 Add `SetPull`, `SetPullFiles` and `SetCommit` to `internal/github/githubtest` with the pulls, pull files and any-SHA commit endpoints, and verify `go test ./internal/github/...` passes unchanged and a new test reads each endpoint through go-github

## 3. Gate core

- [x] 3.1 Define the report types (version 1), `Finalize` and the deterministic encoder in `internal/gate`, and verify a golden-file test that a fixture report encodes byte-identically on repeated runs and a test that appending a passing entry to a failing report keeps `pass` false
- [x] 3.2 Implement the GitHub `PRReader`: PR head and base, base ref resolved once, paginated files with the fail-closed count check, contents at a SHA. Verify tests against `githubtest`, including a count mismatch exiting with an error
- [x] 3.3 Implement the base-branch config load (policy and routing at the base commit, plan source and bundle checked against routing), reading the plan with the CLI's existing `readPlan` (`write.DecodePlan`: version 1, unknown fields rejected), and verify tests for a bad plan version, a missing policy and a bundle mismatch
- [x] 3.4 Implement the `routed_set` rule, and verify a table test with a passing and a failing case against `githubtest`
- [x] 3.5 Implement the `change_shape` rule, and verify a table test with a passing case and failing cases for added, removed, renamed and `.kb/` files
- [x] 3.6 Implement the `size` rule, and verify a table test with a passing case, a page over `page_lines` and a PR over `pr_lines`
- [x] 3.7 Implement link extraction and the `links` rule, and verify a table test with a passing case, a broken link, a link escaping the bundle, and ignored external, fragment-only and code-span links
- [x] 3.8 Add a test where the PR head modifies `.kb/policy.yaml` to allow itself, and verify the gate reads the base policy, `change_shape` fails and `pass` is false

## 4. Rules depending on B1

- [ ] 4.1 Rebase onto `origin/main` with `internal/provenance` from [#3](https://github.com/gjed/okf-operator/issues/3) merged, or stop and report, and verify `go build ./...` passes after the rebase
- [ ] 4.2 Implement the `provenance` rule with `internal/provenance` and the `CommitChecker`, and verify a table test with a passing case, a missing field, a foreign `source_repo`, an unknown SHA (404 or 422 "No commit found" fails the rule) and a 500 (fails the run)
- [ ] 4.3 Implement the `allowlist` rule with the page `type` read at the base commit through `internal/provenance`, and verify a table test with a passing case, a disallowed type, a disallowed bundle, an empty allowlist and a changed `type`

## 5. Evaluation and telemetry

- [x] 5.1 Implement `gate.Evaluate` running every rule in the fixed order, and verify a test that one failing rule still reports all six and a deterministic-output test that two runs on the same fake give byte-identical JSON
- [x] 5.2 Set the gate attributes on the dispatcher span and add `okf.gate.rule_results`, and verify a test with the in-memory span and metric exporters that asserts names, attributes and one data point per rule

## 6. CLI wiring

- [x] 6.1 Add `internal/cli/gate.go` with `--knowledge-repo`, `--pr`, `--plan` and the App flags, two inline `github.Access` providers, and register it in `internal/cli/commands.go`. Verify `okf-operator gate -h` lists the flags, a missing `--pr` exits 2, a failing gate exits 0 with `pass: false`, and an App-mode test asserts the two token requests carry only the specified repositories and permissions
- [x] 6.2 Add an end-to-end test that runs the built binary against `githubtest` with a plan on stdin, and verify stdout matches the golden report and the exit code is 0
- [x] 6.3 Document `gate` in `README.md`, and verify the markdown lint passes

## Workflow follow-up

- Archive the change in the same PR that implements it.
- Later: run `gate` against a real PR in a public sandbox repo whose base branch has `.kb/policy.yaml` and `.kb/routing.yaml`, with a hand-written plan, and record the JSON report and the exit code.
