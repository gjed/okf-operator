# cli Specification

## Purpose

Defines the `okf-operator` command line contract that every pipeline stage and the controller mode share: subcommand dispatch, help, version, exit codes, logging and telemetry.

## Requirements

### Requirement: Subcommand dispatch

The binary SHALL take the subcommand as its first argument and run only that subcommand. Each subcommand SHALL parse its own flags.

#### Scenario: Known subcommand runs

- **WHEN** the user runs `okf-operator version`
- **THEN** the version subcommand runs and the process exits 0

#### Scenario: Unknown subcommand

- **WHEN** the user runs `okf-operator frobnicate`
- **THEN** the process writes `unknown command "frobnicate"` and the list of subcommands to stderr, and exits 2

#### Scenario: No subcommand

- **WHEN** the user runs `okf-operator` with no arguments
- **THEN** the process writes usage to stderr and exits 2

### Requirement: Help output

`okf-operator help`, `-h` and `--help` SHALL print usage that lists every subcommand with a one-line description. `okf-operator <subcommand> -h` SHALL print that subcommand's flags.

#### Scenario: Top-level help

- **WHEN** the user runs `okf-operator --help`
- **THEN** usage listing every registered subcommand is printed to stdout and the process exits 0

#### Scenario: Subcommand help

- **WHEN** the user runs `okf-operator version -h`
- **THEN** the flags of `version` are printed and the process exits 0

### Requirement: Version reporting

`okf-operator version` SHALL print the release version, the git commit and the Go version the binary was built with. A binary built without release metadata SHALL report version `dev`.

#### Scenario: Release build

- **WHEN** a binary built with version `1.2.3` and commit `abc1234` runs `version`
- **THEN** stdout contains `1.2.3`, `abc1234` and the Go version

#### Scenario: Development build

- **WHEN** a binary built with plain `go build` runs `version`
- **THEN** stdout reports version `dev`

### Requirement: Exit codes

Every subcommand SHALL exit 0 on success, 1 on a runtime failure and 2 on a usage error such as an unknown flag or a missing required argument.

#### Scenario: Bad flag

- **WHEN** the user passes an undefined flag to a subcommand
- **THEN** the error and that subcommand's usage go to stderr and the process exits 2

### Requirement: Structured logging

Every subcommand SHALL log to stderr. The global `--log-format` flag selects `text` (the default) or `json`, and `--log-level` selects `debug`, `info` (the default), `warn` or `error`. Command results go to stdout, never mixed with logs.

#### Scenario: JSON logs

- **WHEN** a subcommand runs with `--log-format json`
- **THEN** every stderr line is a JSON object with at least `time`, `level` and `msg`

#### Scenario: Invalid log level

- **WHEN** the user passes `--log-level verbose`
- **THEN** the process reports the allowed values and exits 2

### Requirement: Telemetry configuration

Trace and metric export SHALL be configured only through the standard `OTEL_*` environment variables, and an exporter named through `OTEL_TRACES_EXPORTER` or `OTEL_METRICS_EXPORTER` SHALL be honoured whether or not an OTLP endpoint is set. With no exporter named and no endpoint configured, the binary SHALL export nothing and SHALL NOT fail or slow down because a collector is missing.

#### Scenario: No collector configured

- **WHEN** no `OTEL_*_EXPORTER` and no `OTEL_EXPORTER_OTLP_*` endpoint variable is set
- **THEN** subcommands run normally and make no network connection for telemetry

#### Scenario: Collector configured

- **WHEN** `OTEL_EXPORTER_OTLP_ENDPOINT` points at a reachable OTLP receiver
- **THEN** spans from the subcommand run reach the receiver with `service.name` `okf-operator` unless `OTEL_SERVICE_NAME` overrides it

#### Scenario: Explicit exporter without an endpoint

- **WHEN** `OTEL_METRICS_EXPORTER` is `prometheus` and no `OTEL_EXPORTER_OTLP_*` endpoint variable is set
- **THEN** the metrics are served in the Prometheus text format on the exporter's scrape port

#### Scenario: Explicit otlp without an endpoint

- **WHEN** `OTEL_TRACES_EXPORTER` is `otlp` and no `OTEL_EXPORTER_OTLP_*` endpoint variable is set
- **THEN** spans are exported to the OpenTelemetry SDK default endpoint

#### Scenario: Explicit none with an endpoint

- **WHEN** `OTEL_TRACES_EXPORTER` is `none` and `OTEL_EXPORTER_OTLP_ENDPOINT` is set
- **THEN** no spans are exported
