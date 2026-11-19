# Design

## Context

Builds on `add-write-command`: the plan JSON v1 (`internal/plan`), the write report v1 (`write.Report`, `write.PageResult`) and the page-write "Propose input contract", `cursor.Reader`, `cursor.Path`, `cursor.Branch`, `cursor.ValidSHA`, `github.Provider` with `Access`, `github.Secrets` and the redacting handler, `plan.GitHubDiffer.Range` and `PullRequests`, and the dispatcher's per-command span. `propose` is the first command that writes to GitHub, so ordering, concurrency and token scope are the core of the design.

## Goals / Non-Goals

**Goals:**

- A reviewable knowledge PR per source with exactly the pages `write` changed, and a cursor that never runs ahead of a successful PR.
- Fail closed: an input that breaks the write contract, a checkout at the wrong commit or a concurrent run exits 1 without overwriting anything.
- No git binary: the image is `gcr.io/distroless/static`.

**Non-Goals:**

- Gates, labels, reviewers, auto-merge. That is `gate` ([#4](https://github.com/gjed/okf-operator/issues/4)).
- Checking out the knowledge repo. The caller provides `--knowledge-dir` at `--knowledge-base`; the controller ([#8](https://github.com/gjed/okf-operator/issues/8)) will do it there.
- Recording declined changes (below), backfill and divergence recovery (`reconcile`).

## Decisions

### Package layout

```text
internal/propose/        # Run(ctx, *plan.Plan, *write.Report, Deps) (*Result, error); checks, tree verify, PR body, result v1, telemetry
internal/cursor/         # + Writer{Clients, KnowledgeRepo}.Advance(ctx, source, from, to string, initial bool) error
internal/cli/propose.go  # flags, two Providers, wiring, result on stdout
```

`propose.Run` takes small interfaces (`Knowledge` for the git data and pulls calls, `SourcePRs` for the PR list, `CursorWriter`), so unit tests use fakes and the CLI tests run end to end against `githubtest`. The report decoder lives in `internal/propose` (strict, `DisallowUnknownFields`, version 1), mirroring `write.DecodePlan`; `internal/write` is not touched.

### Building on an open bot PR: require the checkout to be the bot branch

Options from the brief:

- (a) When a bot PR is open, `--knowledge-dir` must be a checkout of its branch, and `propose` fails if the branch head differs from the checkout's commit. Each run adds one commit; the cursor advances per run.
- (b) Never advance the cursor while a bot PR is open; each run re-plans the whole unmerged range from the default branch and force-pushes.

Chosen: (a). (b) re-spends model tokens on the whole unmerged range every run, grows without bound while the PR waits for review, throws away reviewer pushes to the bot branch on every force-push, and leaves the cursor behind for reasons unrelated to the source. (a) keeps one small commit per range in the PR history and lets a reviewer push fixes to the branch: the next run checks them out and builds on them, and a push between checkout and `propose` fails the run with "check out `okf/acme/api` at `<sha>`" instead of being overwritten.

Rule: expected head = open bot PR head SHA, else default branch head. `--knowledge-base` must equal it. The new commit's parent is `--knowledge-base`; an open PR's branch is updated with `force: false`, so a push to the branch after the check fails with 422 instead of being lost. With no open PR the ref is created, or updated with `force: true` when a stale branch from a merged or closed PR exists.

`plan`'s output does not depend on which of the two refs is checked out (bot PRs only change existing pages, never `.kb/`), so README documents: `plan` → check out the bot branch if a bot PR is open, else the default branch → `write` → `propose`.

### How propose learns the checkout commit: `--knowledge-base <sha>`

Options: a required `--knowledge-base` flag, or reading `.git/HEAD` and refs.

Chosen: the flag. `.git` handling would need loose refs, `packed-refs`, detached HEAD and `gitdir:` files of worktrees, and the controller will likely fetch a tarball with no `.git` at all. The flag is also not trusted: the tree verification below proves every unreported file matches it. It cannot catch a lie when the bot branch and the default branch differ only in pages this run changed, which is why the caller must pass the commit it actually checked out (`git rev-parse HEAD`), not the branch it meant to.

A third option, inferring the base from the expected head and relying on the tree comparison alone, was rejected for that same blind spot: a default-branch checkout whose only differences from the bot branch are pages this run rewrote would pass, and the bot PR's earlier edits to those pages would be silently reverted.

### Input contract verification

1. Decode both documents strictly; compare `source`, `bundle`, `from`, `to`; compare the sets of page paths. No network.
1. Read the base tree: `GET /git/trees/{base}?recursive=1`. `truncated: true` exits 1.
1. Walk `--knowledge-dir` through `os.Root`, skipping the top-level `.git`. For each regular file compute the git blob SHA (`sha1("blob <len>\x00" + content)`); for a symlink, of its target (tree mode `120000`). Directories and submodules (mode `160000`) are compared by presence only.
1. `changed` pages must differ from the base blob; every other path must match; a path on one side only fails. The error lists up to 20 offending paths.

`SIMPLIFIED:` ignored files cannot be told apart without git, so a checkout with build artefacts fails closed. File mode changes other than symlinks are not detected. Hashing the whole checkout is O(repo size) per run; knowledge repos are Markdown and small. Upgrade path: hash only the bundle directory and `.kb/` if repos grow.

### GitHub writes

All through the knowledge repo client:

1. `POST /git/blobs` per changed page (base64).
1. `POST /git/trees` with `base_tree` = the base commit's tree and one entry per changed page.
1. `POST /git/commits` with parent `--knowledge-base`. Message: `okf: update <bundle> from <source> <from7>..<to7>`, body listing the source PR references. Author and committer are left to GitHub, i.e. the App's bot identity or the token's user.
1. Ref: `PATCH /git/refs/heads/okf/<owner>/<name>` (`force: false`) for an open PR; otherwise `POST /git/refs`, falling back to `PATCH` with `force: true` on 422 "Reference already exists".
1. PR: `GET /pulls?state=open&head=<kbowner>:okf/<owner>/<name>&base=<default>`; `POST /pulls` or `PATCH /pulls/{n}` with the new body.

All reads (cursor, default branch, open PR, base tree, source PRs) happen before the first write, so a read failure never leaves a half-written run.

### PR body

Title: `okf: update <bundle> from <owner>/<name>`. Body: one intro line, then one section per run:

```text
<!-- okf:range <from>..<to> -->
### acme/api abc1234..def5678

Source PRs: https://github.com/acme/api/pull/12, https://github.com/acme/api/pull/15

- changed: `docs/a.md`
- unchanged: `docs/b.md`
- dropped: `docs/c.md` (validation_failed)
```

Source PRs come from `plan.GitHubDiffer.Range` (commit list) and `PullRequests`, unchanged. Above 250 commits the section says the list covers the first 250. An update appends a section to the current body. `SIMPLIFIED:` past 65 536 characters the oldest sections, found by the marker, are dropped and replaced by "N earlier ranges omitted, see the commit history"; the commit messages keep every range.

### Ordering and the cursor

Order: inputs → cursor read (must hold `from`, or be absent for an initial plan) → reads → commit → ref → PR → cursor write. Exit 1 at any step leaves the cursor at `from`. A failure after the PR call but before the cursor write leaves an open or updated PR and the old cursor: the next run re-plans the same range against the bot branch checkout and normally produces `NO_CHANGE` pages, so it converges without duplicates.

`cursor.Writer.Advance`:

- `GET /git/ref/heads/kb-state`. 404: orphan commit (blob, tree without base, commit without parents) and `POST /git/refs`; 422 exits 1.
- Otherwise `GET /contents/cursors/<owner>/<name>?ref=kb-state`, check the content against `from`, then `PUT /contents/...` with `branch: kb-state` and the blob `sha` (omitted when the file is new). GitHub answers 409 when the SHA is stale and 422 when a file appears that the request expected not to exist; both exit 1 and name the concurrent run as the likely cause.

The content check also runs before any knowledge write, so a stale plan fails before it opens a PR. `from == to` skips the cursor write.

### Access: two Providers

`github.Provider` mints one token per owner with one permission set. A source and knowledge repo of the same owner would therefore share a token with `contents: write` on the source repo. `propose` builds two `Provider`s sharing one `Secrets`, so each repo gets its own token and `provider.go` stays unchanged:

| Repo      | Contents | Pull requests | Metadata |
| --------- | -------- | ------------- | -------- |
| knowledge | write    | write         | read     |
| source    | read     | read          | read     |

The brief asked for `metadata` read only on the source repo. That is not enough: listing the range's commits (`GET /compare`) needs `contents` read and `GET /commits/{sha}/pulls` needs `pull_requests` read on a private source repo. Same set `write` already requests.

### Declined changes: deferred to #8

With (a) the cursor moves past a range as soon as its PR is opened or updated, so a bot PR that a human closes unmerged is never re-proposed by `plan`: its range is already behind the cursor. Recording declined SHAs therefore only matters for `reconcile` backfill and for showing why a range left no trace, both later work. Listing closed bot PRs on every run would add API calls and a `kb-state` format for no current reader. Left to [#8](https://github.com/gjed/okf-operator/issues/8), which already handles PR webhooks; a comment on #5 and #8 says so.

### Result JSON v1

```json
{"version":1,"source":"acme/api","bundle":"docs","from":"…","to":"…","action":"opened","pr":{"number":7,"url":"https://github.com/acme/kb/pull/7"},"commit":"…","cursor_advanced":true}
```

### Telemetry

`internal/propose/telemetry.go`, same shape as `internal/write/telemetry.go`: span attributes `okf.source.repo`, `okf.bundle`, `okf.propose.action`, `okf.propose.pr`, `okf.propose.cursor_advanced`, `okf.propose.pages.committed`; counters `okf.propose.prs.opened`, `okf.propose.prs.updated`, `okf.propose.cursor.advances`.

## Risks / Trade-offs

- [Checkout at the wrong commit] → `--knowledge-base` is compared with the expected head before any write, and the tree verification catches every unreported difference.
- \[Human push to the bot branch between checkout and `propose`\] → the run fails naming the new head; nothing is overwritten.
- [PR opened, cursor write fails] → exit 1; the next run updates the same PR, no duplicate.
- [Two runs for one source] → runs are serialised per knowledge repo by the controller; the cursor compare-and-swap and `force: false` ref update make a concurrent CLI run fail instead of interleaving.
- [Large repos] → whole-checkout hashing, see `SIMPLIFIED` above.

## Migration Plan

None: new subcommand, `kb-state` format unchanged.

## Open Questions

None blocking. The live run needs a scratch knowledge repo under the maintainer's account; it is created and deleted only with explicit approval.
