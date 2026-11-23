# Proposal

## Why

Without a dashboard there is no way to judge cost, drift or whether auto-merge has earned trust. The stages and the controller already emit the metrics ([#8](https://github.com/gjed/okf-operator/issues/8) settled the set); nothing reads them. This change belongs to the observability release step that follows the controller ([#10](https://github.com/gjed/okf-operator/issues/10)).

## What Changes

- New `dashboards/` directory, no Go change and no new or renamed metric:
  - `dashboards/grafana/okf-operator.json`: one Grafana dashboard with panels for webhook events received and dropped, runs queued, run failures by stage, pages changed per run, validation failures, gate failures by rule, source merge to knowledge PR time, share of PRs auto-merged, tokens per run and cursor staleness. The Prometheus datasource is a `DS_PROMETHEUS` datasource variable (no UID), and `Source` and `Bundle` template variables filter the panels whose metrics carry those attributes.
  - `dashboards/alerts/okf-operator.rules.yaml`: a Prometheus rule group with two alerts, run failures above a threshold and a cursor that stays behind the source HEAD, and `dashboards/alerts/okf-operator.rules.test.yaml`, the `promtool` unit tests of both.
  - `dashboards/README.md`: how to import both, the OpenTelemetry to Prometheus name translation the queries rely on, and the collector settings they assume.
- README links to `dashboards/README.md` from its Telemetry section.

## Capabilities

### New Capabilities

- `dashboards`: the Grafana dashboard, the alert rules and the translation contract they depend on.

### Modified Capabilities

None.

## Impact

- New `dashboards/` files only. Nothing is built into the image and nothing is packaged into a Helm chart ([#9](https://github.com/gjed/okf-operator/issues/9)).
- A panel whose metric carries too little to answer the question is left out or reduced, and the panel says why: the knowledge repo is not a metric attribute, validation failures have no rule attribute, and the gate, approval and merge-to-PR metrics carry no source attribute.
