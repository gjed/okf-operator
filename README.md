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

| Flag                                 | Description                                                     |
| ------------------------------------ | --------------------------------------------------------------- |
| `--source owner/name`                | Source repo (required)                                          |
| `--knowledge-repo owner/name`        | Knowledge repo holding the `kb-state` branch (required)         |
| `--knowledge-dir path`               | Checkout of the knowledge repo default branch (required)        |
| `--routing file`                     | Routing map (default `<knowledge-dir>/.kb/routing.yaml`)        |
| `--to sha`                           | End of the range instead of the source default branch HEAD      |
| `--app-id`, `--app-private-key-file` | GitHub App credentials, see [GitHub access](docs/github-app.md) |

`plan` exits 1 when the cursor diverged from the source history or the range lists 300 or more files; both need `reconcile`. See [Routing map](docs/routing.md) and [Operator state](docs/state.md).

### Telemetry

Traces and metrics use the standard `OTEL_*` environment variables. With no `OTEL_EXPORTER_OTLP_*ENDPOINT` set, nothing is exported and no connection is made. Set `OTEL_EXPORTER_OTLP_ENDPOINT` to send telemetry to an OTLP receiver.

## Licence

Apache License 2.0. See [LICENSE](LICENSE).
