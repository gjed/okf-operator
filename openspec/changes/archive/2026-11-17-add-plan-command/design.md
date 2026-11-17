# Design

## Context

Builds on `bootstrap-repository` (dispatcher, exit codes, logging, telemetry). `plan` is the first stage that talks to GitHub and the first consumer of knowledge repo config, so it fixes three public contracts: the `routing.yaml` schema, the `kb-state` cursor layout and the plan JSON. See the specs for the required behaviour.

## Goals / Non-Goals

**Goals:**

- Pure planning: no model calls, no writes to any repository.
- Package boundaries that `write`, `propose`, `reconcile` and the controller reuse unchanged.

**Non-Goals:**

- Advancing or writing the cursor. That is `propose`.
- Fetching diff hunks for the model. That is `write`.
- Webhook intake and the controller.
- Page templates and the OKF profile. `plan` only knows whether a route declares a `type`.

## Decisions

### Package layout

```text
internal/routing/    # schema, Load(), Validate(), Match(changedPaths) -> routes
internal/cursor/     # Read(ctx, kbRepo, source) -> (sha, found, err); path format
internal/plan/       # Build(ctx, deps) -> Plan; JSON types (version 1)
internal/github/     # client construction: token or App installation tokens, redacting slog handler
internal/cli/plan.go # plan Command, registered in commands.go: flags -> plan.Build -> JSON on stdout
```

`plan.Build` takes interfaces (`SourceDiffer`, `CursorReader`, `PageChecker`), so tests run against recorded fixtures with no network.

### CLI wiring and exit codes

`plan` is a `cli.Command` in `internal/cli/commands.go`. `cmd/okf-operator` and the dispatcher do not change, and the global flags stay `--log-format` and `--log-level`.

- Flags are parsed with `parseFlags`. A missing required flag, a missing credential or an incomplete App setting prints the error and the `plan` flags to stderr and returns `cli.UsageError`, so the process exits 2.
- Routing, cursor and compare failures are plain errors and exit 1.
- The App flags (`--app-id`, `--app-private-key-file`) are `plan` flags, registered by a helper in `internal/github` so later commands bind the same flags and env fallbacks.

### GitHub API base URL

The REST base URL is `GITHUB_API_URL` when set, otherwise `https://api.github.com/`. GitHub Actions sets this variable already. It is also the seam the end-to-end test uses to point the built binary at an `httptest` fake.

### YAML decoding

The routing map is decoded with `go.yaml.in/yaml/v3`, the maintained fork of `gopkg.in/yaml.v3` that is already in `go.sum` through the OTel exporters. `Decoder.KnownFields(true)` gives strict decoding.

### Changed paths from the GitHub compare API, not a git clone

`GET /repos/{owner}/{repo}/compare/{cursor}...{to}` returns the merge-base status (`ahead`, `identical`, `behind`, `diverged`) and the changed files with `status` and `previous_filename`.

- `ahead` → plan the files. `identical` → empty plan. `behind` or `diverged` → fail with the "reconcile required" error from the `source-cursor` spec. A 404 on the cursor commit is treated as diverged.
- Why: no clone, no git binary in a distroless image, one request per run. Ranges are small because runs follow merges.
- Alternative: a blobless clone plus `git diff --name-status`. It has no file cap and can be reused by `write` for hunks, but it needs git in the image (or go-git, which does not support partial clones) and disk per run.
- `SIMPLIFIED:` the compare response lists at most 300 files for the whole comparison. When it returns 300, `plan` fails closed, as the spec requires. The ceiling is that ranges with 300 or more changed files always fall to `reconcile`. The upgrade path is the clone-based differ behind the same `SourceDiffer` interface, which `write` may need anyway for large hunks.

### Cursor read through the contents API

`GET /repos/{kb}/contents/cursors/{owner}/{name}?ref=kb-state`. Both a 404 for the file and a 404 for the branch mean "no cursor". Any other error fails the run, so an outage never looks like a first run.

- The path is `cursors/<owner>/<name>`, with no extension. GitHub owner and repo names cannot contain `/`, so the mapping is unambiguous.
- One file per source, not one shared file. `propose` will update cursors for different sources without rewriting the others, and `git log` on a file is that source's processing history.

### Routing map schema (version 1)

```yaml
version: 1
sources:
  - repo: acme/api
    bundle: product
    exclude: ["**/secrets/**", "**/*.pem"]
    routes:
      - page: product/services/api.md
        paths: ["api/**", "cmd/server/**"]
      - page: product/services/billing.md
        type: Service          # allows `create` when the page does not exist
        paths: ["billing/**"]
```

- Strict decoding: unknown keys fail loading, so a typo cannot silently disable a route.
- Validation runs at load time and collects every error before failing, so one run reports all problems in the file.
- Globs use `github.com/bmatcuk/doublestar/v4` (`**`). Patterns are validated at load time with `doublestar.ValidatePattern`.
- Bundle containment is checked on the `path.Clean`-ed target: it must start with `<bundle>/`, end in `.md`, and contain no `..`.

### Page state from the local checkout

`--knowledge-dir` is a checkout of the knowledge repo default branch. `plan` stats `<dir>/<page>` to choose `update` versus `create`/`missing`. It reads nothing else from the checkout except `routing.yaml`.

- Why a local checkout instead of the API: `write` and `propose` need the checkout anyway, the controller clones once per run, and tests use a temp dir.

### End commit

Without `--to`, `plan` resolves the source default branch HEAD with one `GET /repos/{owner}/{repo}` and one `GET /repos/{owner}/{repo}/commits/{default_branch}`. The webhook's merge SHA is never used as the end of the range. It is only a trigger, which keeps duplicate and out-of-order events harmless.

### GitHub App authentication

The App JWT is signed with `golang-jwt/jwt/v5` (RS256, 9-minute expiry). The installation for each repo owner is found with `GET /repos/{owner}/{repo}/installation`. The token is minted with `POST /app/installations/{id}/access_tokens`, with `repositories` and `permissions` narrowed to what the command needs. Tokens are cached in memory per owner for the run only.

- Setting only one of the App ID and the private key file is a usage error that names the missing one.

- `internal/github` provides a `slog.Handler` wrapper that replaces every registered secret (the key PEM, the App JWT, each installation token and `GITHUB_TOKEN`) with `[REDACTED]` in messages and attribute values. `plan` wraps `env.Logger` with it before the first GitHub call, and each token is registered as it is minted. No secret is ever set as a span attribute or wrapped into an error.

- Alternative: `bradleyfalzon/ghinstallation`. It is a fine library but cannot narrow permissions per token without extra plumbing. The hand-rolled path is about 80 lines, and the sibling repo already does the same.

### Plan JSON

```json
{
  "version": 1,
  "source": "acme/api",
  "bundle": "product",
  "from": "<sha or empty when initial>",
  "to": "<sha>",
  "initial": false,
  "changed_paths": [{"path": "...", "status": "modified", "previous_path": ""}],
  "pages": [{"path": "product/services/api.md", "state": "update", "type": "", "source_paths": ["api/x.go"]}],
  "unrouted": ["README.md"],
  "excluded": ["deploy/secrets/token.yaml"]
}
```

Lists are sorted. The struct field order is fixed, and `encoding/json` with `SetIndent("", "  ")` gives byte-identical output.

### Telemetry

The dispatcher already opens one root span per subcommand, named after it, so `plan` gets its `plan` span for free. `plan.Build` does not open a second one: it sets its attributes on `trace.SpanFromContext(ctx)`. The span attributes are `okf.source.repo`, `okf.bundle`, `okf.plan.from`, `okf.plan.to` and the four counts below. The counters are `okf.plan.changed_paths`, `okf.plan.routed_pages`, `okf.plan.unrouted_paths` and `okf.plan.excluded_paths`, with the attributes `okf.source.repo` and `okf.bundle`. Names stay in the `okf.` namespace so they don't collide with semconv.

## Risks / Trade-offs

- [The compare API's 300-file cap] → Fail closed and point to `reconcile`. Measure how often it happens in the pilot before building the clone differ.
- [Squash merges and rebases on the source default branch are fine, but a force-push to it is not] → The diverged-cursor error makes this loud. `reconcile` re-anchors the cursor.
- \[The `routing.yaml` v1 schema becomes a public contract\] → The explicit `version` field and strict decoding let v2 change the shape without silent misreads.
- [Org-wide App installations hand out tokens with broad repository access] → Narrowing `repositories` per token bounds each run even when the installation is wide.

## Open Questions

- Whether `plan` should also emit diff hunk byte counts so the `write` budget can be checked early. Deferred to the `write` change. It only adds a field under the existing `version: 1` contract.
