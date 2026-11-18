# Tasks

## 1. Provenance

- [x] 1.1 Implement `provenance.Fields`, `Parse` and `Stamp` on a `yaml.Node` (replace in place, append missing keys in fixed order, preserve other keys and the body), and verify table tests for: no frontmatter, malformed YAML, wrong key type, model-written provenance overwritten, custom key kept in position, `Stamp` then `Parse` round trip with a byte-identical body

## 2. Model client

- [x] 2.1 Implement `internal/model` (chat completions request, `temperature: 0`, `max_tokens`, optional bearer key, reply text, finish reason and usage, 5-minute timeout, redacted bounded error body), and verify `httptest` tests for: request shape, no `Authorization` header without a key, `finish_reason: length`, missing `usage`, HTTP 500 and 401 errors

## 3. Source data

- [x] 3.1 Add `GitHubDiffer.Patches` (one compare call, keyed by new and previous filename, absent when omitted, empty patch with `changes > 0` treated as absent), and verify tests against recorded compare fixtures in `test/fixtures/compare/` for: full patch, omitted patch, rename, `changes == 0`; and verify `go test ./internal/plan/` keeps the plan golden file unchanged
- [x] 3.2 Add PR listing for the range's commits (dedupe, merged only, sorted, 4 KiB body cap, warning above 250 commits) and the "PRs for a commit" endpoint in `githubtest`, and verify tests for dedupe, unmerged filtering, truncation and the warning

## 4. Write

- [x] 4.1 Add `prompts/` (`system.txt`, `page.tmpl`, `embed.go`) and prompt assembly with delimiter neutralisation, and verify a golden test of the rendered prompt and a test that a PR body containing the closing marker yields exactly one closing marker
- [x] 4.2 Implement plan decoding (strict, version 1), page path checks, profile and template loading, and style-reference selection, and verify tests for: version 2 rejected, `../` and `.kb/` paths rejected, symlink escape rejected, missing profile and template allowed, deterministic reference order with same-type first
- [x] 4.3 Implement the minimal `Validator` (YAML mapping, non-empty `type`, non-empty body, not a reserved filename) behind an interface, and verify table tests per rule
- [x] 4.4 Implement `write.Run`: budget preflight, one call per update page, response contract (`NO_CHANGE`, fence unwrapping), one retry within the page budget, unchanged detection ignoring provenance, stamping, buffered all-or-nothing writes through `os.Root`, report v1. Verify tests against an `httptest` model server and `githubtest` for: page updated, explicit no-change, same content returned, invalid page retried once then dropped, retry succeeds, budget exceeded with zero model requests, token budget exceeded with zero model requests, PR body instructing an edit to another page leaves every non-planned file byte-identical, missing or truncated patch drops the page with no model request, create and missing pages dropped, model HTTP 500 mid-run leaves the checkout untouched, empty plan, byte-identical report on repeat
- [x] 4.5 Annotate the `write` span and add the three `okf.write.*` counters, and verify a test with in-memory span and metric exporters asserting names, attributes and values

## 5. CLI

- [ ] 5.1 Add `Env.Stdin`, `internal/cli/write.go` (flags, model config with env fallback, inline `github.Access` for the source repo only, shared `Secrets` holding the model key) and register it in `commands.go`, and verify tests for: exit 2 on missing `--knowledge-dir` and missing model base URL, `--plan -` from stdin, App mode token request carrying only the source repo with `contents`, `metadata` and `pull_requests` read
- [ ] 5.2 Add a secrets test that runs `write` at debug level in App mode with a model key, a 401 reply echoing the key and `GITHUB_TOKEN` set, and verify the key, the PEM and the tokens appear in none of stderr, spans or stdout, and that no model request carries a GitHub token
- [ ] 5.3 Add `docs/write.md` (flags, model config, profile and templates, report v1, working-tree contract) and a `write` usage section in `README.md`, and verify the markdown lint passes

## 6. Validation and archive

- [ ] 6.1 Verify `go build ./... && go vet ./... && go test -race ./...`, `make lint` and `openspec validate --all --strict` pass
- [ ] 6.2 Verify one live `plan | write` run against a real public source repo, a temp knowledge dir with a hand-written `.kb/routing.yaml` and a real OpenAI-compatible endpoint, recording the page diff and the report
- [ ] 6.3 Run `openspec archive add-write-command -y`, fix MD022 in the generated specs if flagged, and verify `openspec validate --all --strict` and the markdown lint pass
