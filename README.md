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

### Telemetry

Traces and metrics use the standard `OTEL_*` environment variables. With no `OTEL_EXPORTER_OTLP_*ENDPOINT` set, nothing is exported and no connection is made. Set `OTEL_EXPORTER_OTLP_ENDPOINT` to send telemetry to an OTLP receiver.

## Licence

Apache License 2.0. See [LICENSE](LICENSE).
