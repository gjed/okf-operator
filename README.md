# okf-operator

okf-operator keeps an Open Knowledge Format (OKF) bundle repository in sync with merged pull requests in source repositories.

A merged pull request starts a run. The run diffs the source repository from the last processed commit to `HEAD`, routes the changed paths to knowledge pages, asks a language model for updated pages, stamps provenance, validates the result, and opens a pull request in the knowledge repository. Deterministic checks and an advisory model review decide whether the pull request merges automatically or waits for a person.

## Status

Pre-alpha. The pipeline stages and the controller mode exist, and the command line and the custom resources may change without notice.

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

| Flag                                 | Description                                                                             |
| ------------------------------------ | --------------------------------------------------------------------------------------- |
| `--source owner/name`                | Source repo (required)                                                                  |
| `--knowledge-repo owner/name`        | Knowledge repo holding the `kb-state` branch (required)                                 |
| `--knowledge-dir path`               | Checkout `write` will use: the open bot PR's branch, else the default branch (required) |
| `--routing file`                     | Routing map (default `<knowledge-dir>/.kb/routing.yaml`)                                |
| `--to sha`                           | End of the range (full 40-character SHA) instead of the source default branch HEAD      |
| `--app-id`, `--app-private-key-file` | GitHub App credentials, see [GitHub access](docs/github-app.md)                         |

`plan` exits 1 when the cursor diverged from the source history or the range lists 300 or more files; both need [`reconcile`](#reconcile). See [Routing map](docs/routing.md) and [Operator state](docs/state.md).

### reconcile

`reconcile` plans every routed page of one source against the source tree at one commit instead of the range since the cursor. It backfills a newly onboarded source, recovers from a diverged cursor or an oversized range, and refreshes pages that fell behind. It lists the tree with the git trees API, routes every file, reads each page's provenance from the knowledge checkout, and prints a plan of `version: 2` that `write` and `propose` take like a `plan` output. It calls no model and writes nothing.

```bash
GITHUB_TOKEN=$(gh auth token) okf-operator reconcile \
  --source cli/cli \
  --knowledge-repo gjed/okf-operator \
  --knowledge-dir ./kb \
  --to 0536d5e5288458c696ccbeac0e68d87128300069
```

| Flag                                 | Description                                                                              |
| ------------------------------------ | ---------------------------------------------------------------------------------------- |
| `--source owner/name`                | Source repo, one per run (required)                                                      |
| `--knowledge-repo owner/name`        | Knowledge repo holding the `kb-state` branch (required)                                  |
| `--knowledge-dir path`               | Checkout `write` will use: the open bot PR's branch, else the default branch (required)  |
| `--routing file`                     | Routing map (default `<knowledge-dir>/.kb/routing.yaml`)                                 |
| `--to sha`                           | Commit to reconcile at (full 40-character SHA) instead of the source default branch HEAD |
| `--app-id`, `--app-private-key-file` | GitHub App credentials, see [GitHub access](docs/github-app.md)                          |

Each planned page carries a reason (`backfill`, `stale`, `rename` or `widened`) and its evidence for `write`: the compare patches from the page's own `source_sha`, or the whole source files at `to` when the page has no usable base or its route now covers files missing from its provenance `routed_paths`. Pages whose routes and `source_paths` match nothing at `to` are reported as deletion candidates and never deleted. Run `write` and `propose` on the plan as for `plan`, against the same checkout; `propose` opens or updates the bot PR, lists the deletion candidates in it, and only then moves the cursor from the one `reconcile` read to `to`, which re-anchors a diverged cursor. `gate` and `review` refuse reconcile plans, so a reconcile PR always goes to a human. A truncated tree or a source missing from the routing map exits 1. See [Reconciling a source](docs/reconcile.md).

### write

`write` reads a plan, asks an OpenAI-compatible model for the updated content of each planned page, or for a new page from its template when the plan says `create`, stamps provenance and writes the changed and created pages into the knowledge checkout. For a reconcile plan, a page the model leaves unchanged only has its provenance refreshed. It prints a JSON report to stdout and never commits or pushes.

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

`propose` commits the pages a `write` report marks `changed`, `created` or `restamped` to the bot branch `okf/<owner>/<name>` of the knowledge repo, opens the bot PR or adds the commit to the one already open, and then advances the source cursor on `kb-state`. It uses only the GitHub REST API, no git binary.

Run the stages in this order, because a bot PR that is still open holds earlier page changes, including created pages, that the default branch lacks. `plan` and `write` must see the same checkout: a page created in the open bot PR is an `update` there but a `create` on the default branch, and `write` refuses a create whose file exists.

1. Check out the bot branch if a bot PR for the source is open, otherwise the default branch.
1. `plan` against that checkout.
1. `write` against the same checkout.
1. `propose` with the commit the checkout is at.

The bot branch's `.kb/routing.yaml` is as old as the bot PR, so routes added on the default branch since then would be missed while the cursor still advances past their changes. Pass the default branch's routing map with `--routing`, as below.

```bash
branch=$(gh pr list -R acme/kb --head okf/acme/api --json headRefName --jq '.[0].headRefName // empty')
git -C kb fetch origin && git -C kb checkout --detach "origin/${branch:-main}"
git -C kb show origin/main:.kb/routing.yaml > routing.yaml

okf-operator plan --source acme/api --knowledge-repo acme/kb --knowledge-dir ./kb --routing routing.yaml > plan.json
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

Before any write, `propose` checks that the plan and report describe the same range, that `--knowledge-base` is the head of the open bot PR or, with none open, of the default branch, and that every file in the checkout matches `--knowledge-base` except the changed and restamped pages and the created pages, which must be new regular files. Any mismatch exits 1 and changes nothing.

An initial plan only writes the cursor and skips the checkout checks. A report with no changed, created or restamped page opens no PR and still advances the cursor. A restamped page, a reconcile page the model left unchanged whose provenance was refreshed, is committed with the rest and listed apart in the PR body. `write` does not link a created page from the bundle `index.md`, and `gate` never auto-merges a PR that adds a page, so the reviewer adds the link. The cursor moves last: if the branch push or the PR call fails, it stays put. stdout gets a JSON result with the action (`opened`, `updated` or `none`), the PR number and URL, the commit and whether the cursor advanced. See [Operator state](docs/state.md) for the cursor rules.

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
  "version": 2,
  "knowledge_repo": "acme/kb",
  "pr": 12,
  "head_sha": "4444444444444444444444444444444444444444",
  "base_sha": "3333333333333333333333333333333333333333",
  "plan_digest": "sha256:bf42e82d4fbd46a95a7c2496e05d6fc0a52caec15ab89e991b3a75eb3d326576",
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
| `review`       | Only with `--review`: the LLM review passed for the same PR head (see [review](#review))                                       |

`pass` is `true` only when every rule passes. A failed gate still exits 0, so callers branch on `pass`. Exit 1 means the evaluation could not finish (an API failure, a missing or invalid policy, a plan that does not match the base routing map) and prints no report.

| Flag                                 | Description                                                         |
| ------------------------------------ | ------------------------------------------------------------------- |
| `--knowledge-repo owner/name`        | Knowledge repo the PR belongs to (required)                         |
| `--pr number`                        | Pull request number (required)                                      |
| `--plan file`                        | Plan JSON of the run that produced the PR, `-` for stdin (required) |
| `--review file`                      | `review` result for the same PR head, added as the `review` rule    |
| `--app-id`, `--app-private-key-file` | GitHub App credentials, see [GitHub access](docs/github-app.md)     |

A `--review` result for another knowledge repo, PR, head SHA, base SHA or plan (`plan_digest`) exits 1. The review can turn `pass` into `false`, never into `true`.

See [Auto-merge policy](docs/policy.md).

### review

`review` is the advisory LLM review. For each changed Markdown page of a knowledge PR it sends a reviewer model the page at the base, the page at the head and the source diff of the page's planned source paths, and asks whether every changed statement is supported by that diff. Pull request titles and bodies are not sent. It prints a JSON result to stdout and writes nothing.

```bash
GITHUB_TOKEN=$(gh auth token) \
OKF_REVIEW_MODEL_BASE_URL=http://localhost:11434/v1 \
OKF_REVIEW_MODEL=qwen2.5:7b \
okf-operator review --knowledge-repo acme/kb --pr 12 --plan plan.json > review.json
```

```json
{
  "version": 1,
  "knowledge_repo": "acme/kb",
  "pr": 12,
  "head_sha": "4444444444444444444444444444444444444444",
  "base_sha": "3333333333333333333333333333333333333333",
  "plan_digest": "sha256:bf42e82d4fbd46a95a7c2496e05d6fc0a52caec15ab89e991b3a75eb3d326576",
  "model": "qwen2.5:7b",
  "pass": false,
  "pages": [
    {"path": "product/services/api.md", "pass": false, "reason": "", "unsupported": ["Requests are retried three times."]}
  ],
  "tokens": {"prompt": 1840, "completion": 22}
}
```

A page fails with a `reason` and no model call when it is removed (`page_removed`), not in the plan (`not_in_plan`), missing a source patch (`patch_unavailable`) or over its budget (`over_budget`). A reply that is not exactly `{"verdict": "supported"|"unsupported", "unsupported": [...]}` fails the page with `reply_invalid`. `pass` is `true` only when at least one page was reviewed and every page passed. A failed review exits 0; a model or GitHub error exits 1 and prints nothing.

The reviewer model is configured apart from the writer and never falls back to the `OKF_MODEL_*` settings. Use a different model from the writer where you can.

| Flag                                 | Description                                                                           |
| ------------------------------------ | ------------------------------------------------------------------------------------- |
| `--knowledge-repo owner/name`        | Knowledge repo the PR belongs to (required)                                           |
| `--pr number`                        | Pull request number (required)                                                        |
| `--plan file`                        | Plan JSON of the run that produced the PR, `-` for stdin (required)                   |
| `--review-model-base-url url`        | OpenAI-compatible API base URL of the reviewer (default `$OKF_REVIEW_MODEL_BASE_URL`) |
| `--review-model name`                | Reviewer model name (default `$OKF_REVIEW_MODEL`)                                     |
| `--max-tokens-per-page n`            | Most model tokens for the review of one page (default 16000)                          |
| `--app-id`, `--app-private-key-file` | GitHub App credentials, see [GitHub access](docs/github-app.md)                       |

The reviewer API key comes only from `OKF_REVIEW_MODEL_API_KEY`.

### approve

`approve` posts the approving review that branch protection counts. GitHub does not let a PR's author approve it, so `approve` signs in as a separate review-only App and reads none of the operator's credentials. It approves only when the gate report passes, contains every deterministic rule and a passing `review` rule, and the PR head is still the report's `head_sha`, and it approves that commit. The knowledge repo must dismiss stale approvals when new commits are pushed, or an approval keeps counting after `propose` pushes a new head; see [Dismiss stale approvals](docs/github-app.md#dismiss-stale-approvals).

```bash
okf-operator gate --knowledge-repo acme/kb --pr 12 --plan plan.json --review review.json \
  | OKF_REVIEW_GITHUB_APP_ID=67890 OKF_REVIEW_GITHUB_APP_PRIVATE_KEY_FILE=review.pem \
    okf-operator approve --knowledge-repo acme/kb --pr 12 --report -
```

```json
{"version": 1, "pr": 12, "head_sha": "4444444444444444444444444444444444444444", "approved": false, "reason": "head_moved"}
```

`reason` is `gate_failed`, `report_incomplete`, `review_missing`, `head_moved` or `base_moved` (the base branch tip is no longer the `base_sha` the gate evaluated) when nothing was posted. Either way it exits 0. A report for another repo or PR, an invalid report or a GitHub error exits 1.

| Flag                                               | Description                                                             |
| -------------------------------------------------- | ----------------------------------------------------------------------- |
| `--knowledge-repo owner/name`                      | Knowledge repo the PR belongs to (required)                             |
| `--pr number`                                      | Pull request number (required)                                          |
| `--report file`                                    | `gate` report, `-` for stdin (required)                                 |
| `--review-app-id`, `--review-app-private-key-file` | Review App credentials, see [Review App](docs/github-app.md#review-app) |

### controller

`controller` runs the pipeline as a Kubernetes operator for `KnowledgeBase` and `Source` resources (`okf.dev/v1alpha1`). A GitHub webhook only queues the matching `Source`; every run checks out the knowledge repo from the GitHub tarball API, runs `plan` (or `reconcile`, weekly by default), `write` and `propose` in-process, then `gate`, `review` and `approve` for the PR it pushed. A periodic requeue covers lost webhooks, and runs of one knowledge repo never overlap.

```bash
helm install okf-operator oci://ghcr.io/gjed/charts/okf-operator \
  --version 1.2.3 --namespace okf-operator --create-namespace
kubectl apply -f deploy/samples/knowledgebase.yaml   # after replacing the placeholders
```

The Helm chart is published with each release, under the same version as the image. See [Controller](docs/controller.md) for the install values, the resources, Secrets, webhook events, run order and metrics.

### Telemetry

Traces and metrics use the standard `OTEL_*` environment variables. With no `OTEL_TRACES_EXPORTER`, `OTEL_METRICS_EXPORTER` or `OTEL_EXPORTER_OTLP_*ENDPOINT` set, nothing is exported and no connection is made. Set `OTEL_EXPORTER_OTLP_ENDPOINT` to send telemetry to an OTLP receiver. An exporter named through `OTEL_TRACES_EXPORTER` or `OTEL_METRICS_EXPORTER` is honoured with or without an endpoint: `OTEL_METRICS_EXPORTER=prometheus` serves `/metrics` on port 9464, and `otlp` with no endpoint exports to the SDK default `http://localhost:4318`.

A Grafana dashboard and two Prometheus alert rules for the metrics live in [`dashboards/`](dashboards/README.md), with the name translation they assume and how to import them.

## Licence

Apache License 2.0. See [LICENSE](LICENSE).
