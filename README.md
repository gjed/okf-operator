# okf-operator

okf-operator keeps an Open Knowledge Format (OKF) bundle repository in sync with merged pull requests in source repositories.

A merged pull request starts a run. The run diffs the source repository from the last processed commit to `HEAD`, routes the changed paths to knowledge pages, asks a language model for updated pages, stamps provenance, validates the result, and opens a pull request in the knowledge repository. Deterministic checks and an advisory model review decide whether the pull request merges automatically or waits for a person.

## Status

Pre-alpha. Only `version` and `help` exist. The pipeline stages and the controller mode are not implemented yet, and the command line may change without notice.

## Build

Requires Go 1.26 or newer. The module pins the `go1.27.4` toolchain, which the Go command downloads on first use.

```bash
make build        # writes ./okf-operator
make test         # go test ./...
```

Release builds set the version and commit through `-ldflags`:

```bash
make build VERSION=1.2.3
```

## Usage

```bash
okf-operator version
okf-operator help
okf-operator version -h
```

```text
okf-operator 1.2.3 (commit abc1234, go1.27.4)
```

A binary built without release metadata reports version `dev`.

Global flags come before the command name:

- `--log-format text|json` selects the log format (default `text`). Logs go to stderr.
- `--log-level debug|info|warn|error` sets the log level (default `info`).

Exit codes: `0` success, `1` runtime failure, `2` usage error.

### plan

`plan` reads the source cursor from the knowledge repo, diffs from it to the source default branch HEAD, routes the changed paths through `.kb/routing.yaml` in the knowledge checkout, and prints a JSON plan to stdout. It calls no model and writes nothing.

```bash
mkdir -p kb/.kb
cat > kb/.kb/routing.yaml <<'EOF'
version: 1
sources:
  - repo: cli/cli
    bundle: tools
    routes:
      - page: tools/gh/commands.md
        paths: ["pkg/cmd/**"]
EOF

GITHUB_TOKEN=$(gh auth token) okf-operator plan \
  --source cli/cli \
  --knowledge-repo gjed/okf-operator \
  --knowledge-dir ./kb \
  --to 0536d5e5288458c696ccbeac0e68d87128300069
```

`gjed/okf-operator` has no `kb-state` branch, so there is no cursor and the plan is initial: it plans no pages and anchors the range end at `--to`.

```json
{
  "version": 1,
  "source": "cli/cli",
  "bundle": "tools",
  "from": "",
  "to": "0536d5e5288458c696ccbeac0e68d87128300069",
  "initial": true,
  "changed_paths": [],
  "pages": [],
  "unrouted": [],
  "excluded": []
}
```

| Flag                                 | Description                                                                        |
| ------------------------------------ | ---------------------------------------------------------------------------------- |
| `--source owner/name`                | Source repo (required)                                                             |
| `--knowledge-repo owner/name`        | Knowledge repo holding the `kb-state` branch (required)                            |
| `--knowledge-dir path`               | Checkout of the knowledge repo default branch (required)                           |
| `--routing file`                     | Routing map (default `<knowledge-dir>/.kb/routing.yaml`)                           |
| `--to sha`                           | End of the range (full 40-character SHA) instead of the source default branch HEAD |
| `--app-id`, `--app-private-key-file` | GitHub App credentials, see [GitHub access](docs/github-app.md)                    |

`plan` exits 1 when the cursor diverged from the source history or the range lists 300 or more files; both need `reconcile`. See [Routing map](docs/routing.md) and [Operator state](docs/state.md).

### write

`write` reads a plan, asks an OpenAI-compatible model for the updated content of each planned page, or for a new page from its template when the plan says `create`, stamps provenance and writes the changed and created pages into the knowledge checkout. It prints a JSON report to stdout and never commits or pushes.

```bash
export GITHUB_TOKEN=$(gh auth token)
export OKF_MODEL_BASE_URL=http://localhost:11434/v1 OKF_MODEL=qwen3:8b

okf-operator plan --source cli/cli --knowledge-repo acme/kb --knowledge-dir ./kb |
  okf-operator write --plan - --knowledge-dir ./kb
```

| Flag                                 | Description                                                                    |
| ------------------------------------ | ------------------------------------------------------------------------------ |
| `--plan file\|-`                     | Plan JSON from `plan`, `-` for stdin (required)                                |
| `--knowledge-dir path`               | Knowledge repo checkout; changed and created pages are written here (required) |
| `--model-base-url url`               | Model API base URL (default `$OKF_MODEL_BASE_URL`)                             |
| `--model name`                       | Model name (default `$OKF_MODEL`)                                              |
| `--max-pages n`                      | Most update and create pages per run (default 10)                              |
| `--max-tokens-per-page n`            | Most tokens per page, all attempts together (default 16000)                    |
| `--app-id`, `--app-private-key-file` | GitHub App credentials, see [GitHub access](docs/github-app.md)                |

The model API key comes from `OKF_MODEL_API_KEY` only. A run over budget exits 1 before any model call. See [Writing pages](docs/write.md) for the profile, templates, provenance and the report.

### propose

`propose` commits the pages a `write` report marks `changed` or `created` to the bot branch `okf/<owner>/<name>` of the knowledge repo, opens the bot PR or adds the commit to the one already open, and then advances the source cursor on `kb-state`. It uses only the GitHub REST API, no git binary.

Run the stages in this order, because a bot PR that is still open holds earlier page changes the default branch lacks:

1. `plan` against a checkout of the knowledge repo.
1. Check out the bot branch if a bot PR for the source is open, otherwise the default branch.
1. `write` against that checkout.
1. `propose` with the commit the checkout is at.

```bash
okf-operator plan --source acme/api --knowledge-repo acme/kb --knowledge-dir ./kb > plan.json

branch=$(gh pr list -R acme/kb --head okf/acme/api --json headRefName --jq '.[0].headRefName // empty')
git -C kb fetch origin && git -C kb checkout --detach "origin/${branch:-main}"

okf-operator write --plan plan.json --knowledge-dir ./kb > report.json
okf-operator propose --plan plan.json --report report.json \
  --knowledge-repo acme/kb --knowledge-dir ./kb --knowledge-base "$(git -C kb rev-parse HEAD)"
```

| Flag                                 | Description                                                     |
| ------------------------------------ | --------------------------------------------------------------- |
| `--plan file`                        | Plan JSON from `plan` (required)                                |
| `--report file\|-`                   | Report JSON from `write`, `-` for stdin (required)              |
| `--knowledge-repo owner/name`        | Knowledge repo the PR is opened in (required)                   |
| `--knowledge-dir path`               | The checkout `write` ran against (required)                     |
| `--knowledge-base sha`               | Full commit SHA the checkout is at (required)                   |
| `--app-id`, `--app-private-key-file` | GitHub App credentials, see [GitHub access](docs/github-app.md) |

Before any write, `propose` checks that the plan and report describe the same range, that `--knowledge-base` is the head of the open bot PR or, with none open, of the default branch, and that every file in the checkout matches `--knowledge-base` except the changed pages and the created pages, which must be new regular files. Any mismatch exits 1 and changes nothing.

An initial plan only writes the cursor and skips the checkout checks. A report with no changed or created page opens no PR and still advances the cursor. `write` does not link a created page from the bundle `index.md`, and `gate` never auto-merges a PR that adds a page, so the reviewer adds the link. The cursor moves last: if the branch push or the PR call fails, it stays put. stdout gets a JSON result with the action (`opened`, `updated` or `none`), the PR number and URL, the commit and whether the cursor advanced. See [Operator state](docs/state.md) for the cursor rules.

### gate

`gate` decides whether a knowledge PR may auto-merge. It reads the PR, its changed files and their contents through the GitHub API, loads `.kb/policy.yaml` and `.kb/routing.yaml` from the PR's base branch (never from the head), and checks the PR against the plan of the run that produced it. It prints a JSON report to stdout and writes nothing.

```bash
GITHUB_TOKEN=$(gh auth token) okf-operator gate \
  --knowledge-repo acme/kb \
  --pr 12 \
  --plan plan.json
```

```json
{
  "version": 1,
  "pr": 12,
  "head_sha": "4444444444444444444444444444444444444444",
  "pass": false,
  "rules": [
    {"name": "provenance", "pass": true, "details": []},
    {"name": "routed_set", "pass": true, "details": []},
    {"name": "allowlist", "pass": true, "details": []},
    {"name": "change_shape", "pass": true, "details": []},
    {"name": "size", "pass": false, "details": ["product/services/api.md: 60 changed lines exceed limits.page_lines 50"]},
    {"name": "links", "pass": true, "details": []}
  ]
}
```

| Rule           | Passes when                                                                                                                    |
| -------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| `provenance`   | Every changed page has all five provenance fields and its `source_sha` is the plan's `to` and exists in the plan's source repo |
| `routed_set`   | Every changed file is one of the plan's pages                                                                                  |
| `allowlist`    | The page `type` and the plan's bundle are on the policy's auto-merge allowlist                                                 |
| `change_shape` | Every changed file is modified in place: no added, removed or renamed files, nothing under `.kb/`                              |
| `size`         | No page and not the whole PR exceed the policy's changed-line limits (added plus removed lines)                                |
| `links`        | Every relative or root-relative link in a changed page resolves to an existing file inside the bundle at the PR head           |

`pass` is `true` only when every rule passes. A failed gate still exits 0, so callers branch on `pass`. Exit 1 means the evaluation could not finish (an API failure, a missing or invalid policy, a plan that does not match the base routing map) and prints no report.

| Flag                                 | Description                                                         |
| ------------------------------------ | ------------------------------------------------------------------- |
| `--knowledge-repo owner/name`        | Knowledge repo the PR belongs to (required)                         |
| `--pr number`                        | Pull request number (required)                                      |
| `--plan file`                        | Plan JSON of the run that produced the PR, `-` for stdin (required) |
| `--app-id`, `--app-private-key-file` | GitHub App credentials, see [GitHub access](docs/github-app.md)     |

See [Auto-merge policy](docs/policy.md).

### Telemetry

Traces and metrics use the standard `OTEL_*` environment variables. With no `OTEL_EXPORTER_OTLP_*ENDPOINT` set, nothing is exported and no connection is made. Set `OTEL_EXPORTER_OTLP_ENDPOINT` to send telemetry to an OTLP receiver.

## Licence

Apache License 2.0. See [LICENSE](LICENSE).
