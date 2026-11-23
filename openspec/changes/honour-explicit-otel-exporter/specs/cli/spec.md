## MODIFIED Requirements

### Requirement: Telemetry configuration

Trace and metric export SHALL be configured only through the standard `OTEL_*` environment variables. An exporter named explicitly through `OTEL_TRACES_EXPORTER` or `OTEL_METRICS_EXPORTER` (`otlp`, `prometheus`, `console` or `none`) SHALL be honoured whether or not an OTLP endpoint is set; an explicit `otlp` with no endpoint SHALL use the OpenTelemetry SDK default endpoint. For a signal with no exporter named and no OTLP endpoint configured, the binary SHALL export nothing and SHALL NOT fail or slow down because a collector is missing.

#### Scenario: No collector configured

- **WHEN** no `OTEL_*_EXPORTER` and no `OTEL_EXPORTER_OTLP_*` endpoint variable is set
- **THEN** subcommands run normally and make no network connection for telemetry

#### Scenario: Collector configured

- **WHEN** `OTEL_EXPORTER_OTLP_ENDPOINT` points at a reachable OTLP receiver
- **THEN** spans from the subcommand run reach the receiver with `service.name` `okf-operator` unless `OTEL_SERVICE_NAME` overrides it

#### Scenario: Explicit exporter without an endpoint

- **WHEN** `OTEL_METRICS_EXPORTER` is `prometheus` and no `OTEL_EXPORTER_OTLP_*` endpoint variable is set
- **THEN** the metrics are served in the Prometheus text format on the exporter's scrape port

#### Scenario: Explicit none with an endpoint

- **WHEN** `OTEL_TRACES_EXPORTER` is `none` and `OTEL_EXPORTER_OTLP_ENDPOINT` is set
- **THEN** no spans are exported
