# dashboards Specification

## Purpose

Defines the Grafana dashboard and the Prometheus alert rules for okf-operator's OpenTelemetry metrics, and the name translation they depend on.

## Requirements

### Requirement: Grafana dashboard

`dashboards/grafana/okf-operator.json` SHALL be one Grafana dashboard with panels for webhook events received and dropped, runs queued, run failures by stage, pages changed per run, validation failures, gate failures by rule, time from source merge to knowledge PR, share of PRs auto-merged, tokens per run and cursor staleness. It SHALL query only metrics the operator emits and SHALL NOT require a metric to be added or renamed.

#### Scenario: Provisioned without edits

- **WHEN** the file is provisioned into a Grafana with a Prometheus datasource that stores the operator's metrics
- **THEN** the dashboard loads and every panel query runs without an error

#### Scenario: Metric names exist

- **WHEN** the operator exports its metrics through the Prometheus exporter
- **THEN** every metric name and label a panel or alert queries is present in the output

### Requirement: Datasource and filters

The dashboard SHALL select its datasource through a `DS_PROMETHEUS` datasource template variable and SHALL NOT contain a datasource UID. It SHALL offer `Source` and `Bundle` template variables that filter the panels whose metrics carry those attributes.

#### Scenario: No hard-coded datasource

- **WHEN** the dashboard JSON is searched for datasource references
- **THEN** each is `${DS_PROMETHEUS}`

#### Scenario: Source filter

- **WHEN** one `Source` is selected
- **THEN** the pipeline panels show only that `Source`'s series

### Requirement: Alert rules

`dashboards/alerts/okf-operator.rules.yaml` SHALL be a Prometheus rule group file with an alert for run failures above a threshold and an alert for a cursor that has stayed behind the source HEAD, the latter from the cursor staleness gauge. Thresholds, look-back windows and `for` durations SHALL be literals in the rule, each marked for tuning.

#### Scenario: Rules are valid

- **WHEN** `promtool check rules` runs on the file
- **THEN** it succeeds

#### Scenario: Run failures

- **WHEN** two or more failures are counted within three hours (the first failure of a stage and mode pair is not counted)
- **THEN** `OkfOperatorRunFailures` fires

#### Scenario: Stale cursor

- **WHEN** a `Source`'s cursor staleness stays above four hours for 15 minutes
- **THEN** `OkfOperatorCursorStale` fires for that source repo

### Requirement: Deleted Source does not keep the stale-cursor alert firing

A deleted `Source` SHALL NOT keep `OkfOperatorCursorStale` firing: the controller stops reporting its staleness, and the README SHALL NOT tell users to restart the controller or silence the alert after deleting a `Source`.

#### Scenario: Deleted Source

- **WHEN** a `Source` whose cursor staleness fired `OkfOperatorCursorStale` is deleted
- **THEN** the controller stops reporting its staleness and the alert resolves without a controller restart

### Requirement: Documented translation

`dashboards/README.md` SHALL document how to import the dashboard and load the rules, the OpenTelemetry to Prometheus name translation the queries rely on (dots to underscores, unit suffix, `_total`, resource attributes), and the collector settings they assume. The README SHALL link to it.

#### Scenario: Link from the README

- **WHEN** a reader opens the README Telemetry section
- **THEN** it links to `dashboards/README.md`
