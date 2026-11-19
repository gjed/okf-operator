# Tasks

## 1. Test fake

- [x] 1.1 Add to `internal/github/githubtest` the endpoints `propose` calls: `POST /git/blobs`, `POST /git/trees`, `GET /git/trees/{sha}?recursive=1`, `POST /git/commits`, `GET /git/commits/{sha}`, `GET /git/ref/{ref}`, `POST /git/refs`, `PATCH /git/refs/{ref}` (fast-forward check unless `force`), `GET`/`POST /pulls`, `PATCH /pulls/{n}`, `PUT /contents/{path}` (409 on a stale `sha`, 422 on an existing file without `sha`), backed by an in-memory object store; and verify `go test ./internal/github/...`

## 2. Cursor writer

- [x] 2.1 Implement `cursor.Writer.Advance` (content check against `from`, compare-and-swap `PUT` with the blob SHA, orphan `kb-state` when missing, skip when `from == to`), and verify `githubtest` tests for: file advanced, initial cursor on an existing branch, `kb-state` missing → orphan commit holding only the cursor, cursor not at `from` → error and no write, cursor changed between read and write (409) → error and the other value kept, branch created concurrently (422) → error; update `docs/state.md` and verify the markdown lint passes

## 3. Propose

- [x] 3.1 Implement report decoding (strict, version 1) and plan/report agreement checks, and verify table tests for: version 2, unknown key, each of `source`, `bundle`, `from`, `to` mismatched, a report path outside the plan, a plan page missing from the report
- [x] 3.2 Implement checkout verification against the base tree (blob SHA, symlinks, `.git` skipped, truncated tree, one-side files, changed-but-identical), and verify tests for: clean checkout, unreported modified file, unreported new file, deleted file, changed page identical to base, truncated tree
- [x] 3.3 Implement `propose.Run` (reads before writes, expected-head check, blob/tree/commit, ref create/fast-forward/force-reset, PR open or update, PR body with section marker and size cap, cursor last, result v1), and verify `githubtest` tests for: new PR opened and cursor advanced, existing bot PR gets a new commit and no second PR, stale branch from a closed PR reset, checkout at the wrong head → exit before any write, ref update fails → cursor not advanced and no PR, initial plan → cursor written and no PR or commit, all pages unchanged or dropped → no PR and cursor advanced, `from == to` → no write at all, cursor changed between read and write → error with the other value kept, only `changed` pages in the commit tree, PR body links every source PR and lists every page with reasons, body over 65 536 characters drops the oldest section
- [x] 3.4 Annotate the `propose` span and add the three `okf.propose.*` counters, and verify a test with in-memory span and metric exporters asserting names, attributes and values

## 4. CLI

- [x] 4.1 Add `internal/cli/propose.go` (flags, two `Provider`s with inline `Access` sharing one `Secrets`, wiring, result on stdout) and register it in `commands.go`, and verify tests for: exit 2 on a missing flag and on a short `--knowledge-base`, `--report -` from stdin, plan/report mismatch → exit 1 with no GitHub request, App mode token requests carrying only the knowledge repo with `contents` and `pull_requests` write and only the source repo with `contents`, `pull_requests` and `metadata` read, for one owner and for two
- [x] 4.2 Add a secrets test that runs `propose` at debug level in App mode with a GitHub error reply echoing the installation token, and verify the tokens, the JWT and the PEM appear in none of stderr, spans or stdout
- [x] 4.3 Add the `propose` README usage section (plan → checkout → write → propose order) and the `propose` rows of the `docs/github-app.md` permissions table, and verify the markdown lint passes

## 5. Validation and archive

- [x] 5.1 Verify `go build ./... && go vet ./... && go test -race ./...`, `make lint`, `pre-commit run --all-files` and `openspec validate --all --strict` pass
- [x] 5.2 Verify `docker build .` and `docker run --rm <image> propose -h` print the `propose` usage from the distroless image
- [x] 5.3 After approval, create a scratch knowledge repo under the maintainer's account, run `plan > plan.json`, `write --plan plan.json > report.json` and `propose` against it, record the opened PR URL and the `kb-state` cursor, then delete the repo after approval
- [x] 5.4 After approval, comment on [#5](https://github.com/gjed/okf-operator/issues/5) and [#8](https://github.com/gjed/okf-operator/issues/8) that recording declined changes moves to #8, and verify with `gh issue view`
- [x] 5.5 Run `openspec archive add-propose-command -y`, fix MD022 in the generated specs if flagged, and verify `openspec validate --all --strict` and the markdown lint pass
