# Spec Delta

## MODIFIED Requirements

### Requirement: Grafana dashboard

`dashboards/grafana/okf-operator.json` SHALL be one Grafana dashboard with panels for webhook events received and dropped, runs queued, run failures by stage, pages changed per run, validation failures, gate failures by rule, time from source merge to knowledge PR, share of PRs auto-merged, tokens per run, bot PR repairs by kind and cursor staleness. It SHALL query only metrics the operator emits and SHALL NOT require a metric to be added or renamed.

#### Scenario: Provisioned without edits

- **WHEN** the file is provisioned into a Grafana with a Prometheus datasource that stores the operator's metrics
- **THEN** the dashboard loads and every panel query runs without an error

#### Scenario: Metric names exist

- **WHEN** the operator exports its metrics through the Prometheus exporter
- **THEN** every metric name and label a panel or alert queries is present in the output

#### Scenario: Repairs panel

- **WHEN** a run updates a behind bot PR and another regenerates a conflicting one
- **THEN** the repairs panel shows one `update_branch` and one `regenerate` for their source repos
