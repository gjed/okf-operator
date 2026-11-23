# Dashboards and alerts

A Grafana dashboard and two Prometheus alert rules for okf-operator, built on the metrics the pipeline stages and the controller already emit. They need no change to the operator.

| File                                  | What it is                                                                |
| ------------------------------------- | ------------------------------------------------------------------------- |
| `grafana/okf-operator.json`           | One Grafana dashboard                                                     |
| `alerts/okf-operator.rules.yaml`      | Prometheus rule group: `OkfOperatorRunFailures`, `OkfOperatorCursorStale` |
| `alerts/okf-operator.rules.test.yaml` | `promtool test rules` cases for the two alerts                            |

The dashboard has been checked against Grafana 12 and Prometheus 3. It uses only standard panels.

## Get the metrics into Prometheus

The operator exports through the standard `OTEL_*` variables, see [Telemetry](../README.md#telemetry). Nothing is exported until an exporter or an OTLP endpoint is set. Pick one path:

- **OpenTelemetry Collector.** Point the operator at the collector and let Prometheus scrape the collector's `prometheus` exporter.
- **Prometheus OTLP receiver.** Start Prometheus with `--web.enable-otlp-receiver` and point the operator at `http://<prometheus>:9090/api/v1/otlp/v1/metrics`.

```bash
# operator, collector path
OTEL_EXPORTER_OTLP_ENDPOINT=http://collector:4318

# operator, Prometheus receiver path
OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://prometheus:9090/api/v1/otlp/v1/metrics
OTEL_EXPORTER_OTLP_METRICS_PROTOCOL=http/protobuf
```

```yaml
# collector: receive OTLP, expose Prometheus
receivers:
  otlp:
    protocols:
      http:
exporters:
  prometheus:
    endpoint: 0.0.0.0:8889
    add_metric_suffixes: true # the default; the queries need the suffixes
service:
  pipelines:
    metrics:
      receivers: [otlp]
      exporters: [prometheus]
```

```yaml
# prometheus.yml, receiver path (Prometheus 3 default, shown for clarity)
otlp:
  translation_strategy: UnderscoreEscapingWithSuffixes
```

### Name translation the queries rely on

The queries use the names the collector's `prometheus` exporter and Prometheus's OTLP receiver produce by default, `UnderscoreEscapingWithSuffixes`:

| OpenTelemetry                                         | Prometheus                                                     | Rule                                                    |
| ----------------------------------------------------- | -------------------------------------------------------------- | ------------------------------------------------------- |
| `okf.controller.events.received` (counter, `{event}`) | `okf_controller_events_received_total`                         | dots become underscores, counters get `_total`          |
| `okf.write.tokens` (counter, `{token}`)               | `okf_write_tokens_total`                                       | annotation units in braces add no suffix                |
| `okf.controller.run.duration` (histogram, `s`)        | `okf_controller_run_duration_seconds_bucket`, `_sum`, `_count` | unit `s` becomes `_seconds`; histograms expand          |
| `okf.controller.cursor.staleness` (gauge, `s`)        | `okf_controller_cursor_staleness_seconds`                      | gauges get the unit suffix and no `_total`              |
| attribute `okf.source.repo`                           | label `okf_source_repo`                                        | attribute keys follow the same dots-to-underscores rule |
| attribute `okf.gate.outcome`                          | label `okf_gate_outcome`                                       | see the table below for every attribute used            |
| resource `service.name=okf-operator`                  | label `job` (OTLP receiver) or `target_info{service_name=...}` | not used by any query                                   |

If your collector or Prometheus keeps the dots (`NoUTF8EscapingWithSuffixes` or `NoTranslation`), the metric names are different and these queries return nothing. Change the strategy rather than the queries.

The queries do not select on `job` or `instance`, so any scrape job name works. They assume cumulative temporality, which is the OpenTelemetry SDK default: `increase` and `rate` return nothing for delta metrics unless the collector converts them.

Attributes the queries use:

| Label                                         | Metrics                                                                                                 |
| --------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| `okf_source_repo`                             | `okf_plan_*`, `okf_write_*`, `okf_propose_*`, `okf_review_*`, `okf_controller_cursor_staleness_seconds` |
| `okf_bundle`                                  | `okf_plan_*`, `okf_write_*`, `okf_propose_*`, `okf_review_*`                                            |
| `okf_write_result`                            | `okf_write_pages_total`                                                                                 |
| `okf_token_kind`                              | `okf_write_tokens_total`, `okf_review_tokens_total`                                                     |
| `okf_gate_rule`, `okf_gate_outcome`           | `okf_gate_rule_results_total` (`outcome` is `pass` or `fail`)                                           |
| `okf_controller_event`                        | `okf_controller_events_received_total`                                                                  |
| `okf_controller_reason`                       | `okf_controller_events_dropped_total`                                                                   |
| `okf_controller_trigger`                      | `okf_controller_runs_queued_total`                                                                      |
| `okf_controller_stage`, `okf_controller_mode` | `okf_controller_run_failures_total`                                                                     |

Scrape the operator directly only for debugging: `OTEL_METRICS_EXPORTER=prometheus` serves `/metrics` on port 9464, with no OTLP endpoint needed. The listener binds to `localhost` by default, so a scrape from outside the pod also needs `OTEL_EXPORTER_PROMETHEUS_HOST=0.0.0.0`.

## Import the dashboard

The datasource is the dashboard variable `DS_PROMETHEUS`, not a UID, so the file works unchanged in both flows. Grafana selects your default Prometheus datasource; change it with the **Prometheus** drop-down at the top.

In the UI, open **Dashboards**, **New**, **Import**, upload `grafana/okf-operator.json` and import. Or provision the file:

```yaml
# /etc/grafana/provisioning/dashboards/okf-operator.yaml
apiVersion: 1
providers:
  - name: okf-operator
    type: file
    options:
      path: /var/lib/grafana/dashboards/okf-operator
```

Copy `grafana/okf-operator.json` into that path.

The `Source` and `Bundle` variables filter the panels whose metrics carry those attributes. The knowledge repo is not a metric attribute, so there is no variable for it; `Bundle` is the nearest label.

## Panels

| Panel                                                                | Query basis                                                                                                  | Limit                                                                                                                         |
| -------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------- |
| Webhook events received, dropped; runs queued; run failures by stage | `okf_controller_*_total`                                                                                     | No source attribute, so the variables do not filter them                                                                      |
| Pages by write result; pages changed per run                         | `okf_write_pages_total`, divided by PR-producing runs for the per-run view                                   | Per run means per run that opened or updated a PR, see below                                                                  |
| Validation failures                                                  | `okf_write_validation_failures_total`                                                                        | No rule attribute: split by source, not by rule                                                                               |
| Gate failures by rule                                                | `okf_gate_rule_results_total{okf_gate_outcome="fail"}`                                                       | No source attribute                                                                                                           |
| Source merge to knowledge PR                                         | `histogram_quantile` over `okf_controller_merge_to_pr_seconds_bucket`                                        | Merge times are in memory; default histogram bounds make the quantiles coarse                                                 |
| Share of PRs auto-merged                                             | `okf_controller_prs_approved_total` over `okf_propose_prs_opened_total` plus `okf_propose_prs_updated_total` | Approval, not the merge, which the operator does not see; counts each push to a PR as a run; reconcile PRs are never approved |
| Tokens per run                                                       | `okf_write_tokens_total`, `okf_review_tokens_total`, divided by PR-producing runs                            | Tokens of failed runs are in the numerator only                                                                               |
| Cursor staleness                                                     | `okf_controller_cursor_staleness_seconds`                                                                    | Appears after a run first reaches HEAD; ends when the `Source` is deleted; restarts with the controller                       |

No metric counts `write` or `review` runs, so "per run" divides by `okf_propose_prs_opened_total` plus `okf_propose_prs_updated_total`, which `propose` adds with 0 or 1 once per run. "Pages changed" counts `okf_write_result` values `changed` and `created`; the pages-by-result panel shows every value present.

## Load the alert rules

`alerts/okf-operator.rules.yaml` is a plain rule group file. Add it to Prometheus:

```yaml
# prometheus.yml
rule_files:
  - /etc/prometheus/rules/okf-operator.rules.yaml
```

For the Prometheus Operator, put the same `groups:` list under `spec` of a `PrometheusRule`:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: okf-operator
spec:
  groups:
    # contents of alerts/okf-operator.rules.yaml, from `- name: okf-operator` down
```

Thanos Ruler, Mimir (`mimirtool rules load`) and vmalert read the file as it is. Plain rule groups were chosen over Grafana-managed alert provisioning because the latter needs a datasource UID and a folder UID, and ties the alerts to Grafana.

| Alert                    | Fires when                                                                      | Default                         |
| ------------------------ | ------------------------------------------------------------------------------- | ------------------------------- |
| `OkfOperatorRunFailures` | Failed runs in the look-back window exceed the threshold                        | 2 or more in 3 hours, `for: 5m` |
| `OkfOperatorCursorStale` | A Source's cursor has been behind the source HEAD for longer than the threshold | 4 hours, `for: 15m`             |

Both thresholds, both look-back windows and both `for` durations are literals in the rule, marked `tune`. Edit them in the file, or patch them in your deployment tooling. The default requeue interval of a `Source` is one hour and the staleness gauge saw-tooths up to it while the Source is current, so keep the staleness threshold well above your requeue interval.

`OkfOperatorRunFailures` uses `increase`, which does not count the first failure of a stage and mode pair: the counter series is created by that failure, so Prometheus sees it start at 1. A pair that has never failed needs 3 failures in the window to reach the default threshold of 2, a pair that failed before needs 2.

`OkfOperatorCursorStale` cannot tell merges no run proposed from runs that keep failing: both leave the cursor behind HEAD. Read the run failure panel and the `Source` status for the cause.

A deleted `Source` stops reporting: the controller drops its staleness entry when it sees the `Source` gone, so its series ends and `OkfOperatorCursorStale` resolves once Prometheus marks the series stale. Staleness is labelled by `okf_source_repo` only, so two `Source`s on the same source repo share one series and only one of them is drawn; deleting one of them leaves the series with the other.

Check the rules and their tests with `promtool`:

```bash
promtool check rules alerts/okf-operator.rules.yaml
promtool test rules alerts/okf-operator.rules.test.yaml
```
