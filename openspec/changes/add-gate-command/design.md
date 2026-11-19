# Design

## Context

`plan` ([#12](https://github.com/gjed/okf-operator/pull/12)) fixed the plan JSON, the routing map and GitHub access. `write` ([#3](https://github.com/gjed/okf-operator/issues/3), lane B1) adds `internal/provenance`, which owns the provenance field names and the frontmatter parsing. `gate` is the first command that reads a pull request and the first consumer of `policy.yaml`. See the specs for the required behaviour.

## Goals / Non-Goals

**Goals:**

- A verdict that depends only on the PR head, the base commit, the plan and the policy.
- Packages that the controller calls directly, with the CLI as a thin wrapper.

**Non-Goals:**

- The LLM review ([#6](https://github.com/gjed/okf-operator/issues/6)), merging, commit statuses and PR comments.
- Gating page creation. Added pages always fail `change_shape`.
- A frontmatter parser. Both frontmatter reads go through `internal/provenance`.

## Decisions

### Package layout

```text
internal/policy/    # schema v1, Parse, Load, Validate, Version, DefaultPath
internal/gate/      # Evaluate(ctx, deps) -> Report; rules; report JSON; telemetry
internal/cli/gate.go # flags -> two Providers -> gate.Evaluate -> JSON on stdout
```

`gate.Evaluate` takes a `PRReader` interface (PR head and base, changed files, file contents at a ref) and a `CommitChecker` interface (source commit exists), so the rules are tested without a network and the GitHub implementations are tested against `githubtest`.

### Policy schema (version 1)

```yaml
version: 1
auto_merge:
  types: [Service, Runbook]
  bundles: [product]
limits:
  page_lines: 200
  pr_lines: 600
```

- Same pattern as `internal/routing`: the version is checked before strict decoding, `KnownFields(true)`, and `Validate` collects every error.
- `Parse([]byte)` is the entry point `gate` uses, because the file comes from the API. `Load(path)` exists for symmetry and local checks.
- `types` and `bundles` are independent lists: a page passes when its type is listed and the plan's bundle is listed. Per-bundle type lists can come in v2 without breaking v1.
- Limits have no defaults. A policy without limits is a configuration error, not "unlimited".
- A missing `.kb/policy.yaml` on the base branch fails the run (exit 1). An empty allowlist is the safe default, a missing file is a misconfiguration worth a loud error.

### Reading through the API

One `GET /repos/{kb}/pulls/{n}` gives `head.sha`, `base.ref` and `base.repo`. The base ref is resolved to a commit once with `GetCommitSHA1`, and both `.kb/` files are read at that SHA. The files come from `GET /pulls/{n}/files`, paginated at 100 per page. Head contents are read with the contents API at `head.sha`.

- The base branch tip is used, not `base.sha` from the PR, because the tip is the policy that applies when the PR merges.
- The files API lists at most 3000 files. When the listed count differs from the PR's `changed_files`, the run fails closed (exit 1).
- A PR whose head repo differs from the base repo (a fork) is still read by the head SHA, which the base repo serves.

### Two providers, not an edit to `internal/github`

`github.Access` carries one permission set for all its repos, and the issue asks for `pull_requests: read` on the knowledge repo only. `internal/cli/gate.go` builds two `github.Provider`s:

- knowledge: `Access{Repos: [kb], Permissions: contents, metadata, pull_requests read}`
- source: `Access{Repos: [plan.source], Permissions: contents, metadata read}`

The source repo is the plan's `source`. A page whose `source_repo` is a different repo fails `provenance` without an API call, so the source token never needs a repo the run did not declare. When both repos share an owner, the run mints two tokens for that owner, each narrowed.

### Rule details

- `provenance`: parses the page at the head with `internal/provenance`, then `GetCommitSHA1(source_repo, source_sha)`. Each unique SHA is looked up once. GitHub answers an unknown 40-hex SHA with 422 "No commit found for SHA", not 404 (checked against `cli/cli`), so both fail the rule. Any other status fails the run.
- `routed_set`: set membership against `plan.pages[].path`.
- `allowlist`: the bundle is checked once. The page `type` is read from the page at the base commit with `provenance.Type`, a small reader added to `internal/provenance` on top of its existing frontmatter split, because B1 exposes only the five provenance keys and `skosovsky/okf` is not linked (unlicensed). A head `type` different from the base `type`, a missing type and a page absent at the base all fail the rule.
- `change_shape`: files API `status` must be `modified`. Any path under `.kb/` fails, whatever its status.
- `size`: per file `additions + deletions` from the files API, and their sum.
- `links`: extracted with a standard-library scan of the page body after the frontmatter (split by `provenance.Parse`; a page whose frontmatter fails to parse is scanned whole and already fails `provenance`), for inline links `[text](target)`, images `![alt](target)` and reference definitions `[id]: target`, skipping fenced and inline code. Each unique target is resolved with `path.Join(path.Dir(page), target)`, must stay under `<bundle>/`, and is checked with the contents API at `head.sha` (404 fails the rule, other errors fail the run).
- `SIMPLIFIED:` link extraction is a regex scan, not a CommonMark parser. It misses autolinks and HTML `<a href>`, and can misread exotic nesting. Upgrade path: `goldmark`'s AST walker behind the same function, if the pilot shows misses.
- A rule that cannot be evaluated because the page failed to parse reports that page in its details and fails. It does not abort the run.

### Report JSON

```json
{
  "version": 1,
  "pr": 42,
  "head_sha": "<sha>",
  "pass": false,
  "rules": [
    {"name": "provenance", "pass": true, "details": []},
    {"name": "routed_set", "pass": true, "details": []},
    {"name": "allowlist", "pass": false, "details": ["product/services/api.md: type \"Service\" is not allowed"]},
    {"name": "change_shape", "pass": true, "details": []},
    {"name": "size", "pass": true, "details": []},
    {"name": "links", "pass": true, "details": []}
  ]
}
```

- Fixed rule order, sorted details, fixed struct field order, the same encoder settings as `plan.Encode`. No timestamps, no durations.
- `pass` is computed from `rules` by one function, `Report.Finalize`, which the later review entry also goes through. A review can therefore append a failing entry and turn `pass` to `false`, and has no way to set it to `true`.

### Telemetry

The dispatcher opens the `gate` span. `gate` sets `okf.knowledge.repo`, `okf.gate.pr`, `okf.gate.head_sha`, `okf.source.repo`, `okf.bundle` and `okf.gate.pass` on it, and adds to the `okf.gate.rule_results` counter with `okf.gate.rule` and `okf.gate.outcome`, mirroring `internal/plan/telemetry.go`.

### Test fake

`internal/github/githubtest` gains `SetPull(repo, number, head, baseRef)` and `SetPullFiles(repo, number, files)` and serves `GET /repos/{owner}/{repo}/pulls/{n}` and `/pulls/{n}/files`. Its commit handler also answers any SHA registered with a new `SetCommit(repo, sha)`. All additions are additive. `internal/github` is not touched.

## Decisions for approval

### Exit code on a failed gate

| Option                      | Effect                                                                                                                                       |
| --------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| **A. exit 0, `pass=false`** | The controller and CI branch on the JSON. Exit 1 keeps one meaning: the evaluation did not finish, so there is no report.                    |
| B. exit 1                   | A shell `&&` chain stops on a failed gate, but exit 1 then means both "failed gate" and "broken run", and callers must check stdout to tell. |

Pick: **A**. The specs are written for A.

### How the size limit counts lines

| Option                                            | Effect                                                                                                                                                       |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **A. `additions + deletions` from the files API** | Always present, one call, no truncation. A modified line counts twice (one removed, one added), same as `git diff --stat`.                                   |
| B. count `+`/`-` lines in the `patch` field       | Same numbers when a patch exists, but GitHub omits `patch` for large diffs, so the rule would need a fail-closed branch for the case it most needs to catch. |

Pick: **A**. The specs are written for A.

## Risks / Trade-offs

- \[`provenance.Type` extends B1's package\] → It reuses the package's split and mapping helpers, so there is still one frontmatter reader for gate. `internal/write` keeps its own private splitter; consolidating it is out of scope.
- [A force-push to the PR during the run] → Every head read uses the recorded `head_sha`, so the verdict describes one commit. The caller must merge with that SHA (`sha` on the merge API) to avoid merging a later head.
- \[Base branch moves between `gate` and merge\] → The verdict holds for the recorded base commit. The controller re-runs `gate` when it requeues.
- \[`policy.yaml` v1 becomes a public contract\] → Explicit `version` and strict decoding, as with `routing.yaml`.

## Open Questions

- The live run against a real PR is deferred: it needs a public sandbox repo whose base branch has `.kb/policy.yaml` and `.kb/routing.yaml` and an open PR that modifies a page. It is tracked as a workflow follow-up in `tasks.md`.
