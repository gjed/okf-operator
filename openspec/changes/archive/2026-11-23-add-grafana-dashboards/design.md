# Design

## Context

Every stage records `okf.*` counters through the OpenTelemetry metrics API, and the controller adds histograms and a gauge. The operator exports through the standard `OTEL_*` variables, so the dashboards cannot assume a Prometheus scrape of the operator: they read what a collector or Prometheus's OTLP receiver stores. Metric names, units and attributes come from `internal/*/telemetry.go` and `docs/controller.md` on `main`.

## Goals / Non-Goals

**Goals:**

- One importable Grafana dashboard and one rule file that need no edit to run against a Prometheus datasource.
- Every query proven against the names and labels the binary exports.
- Thresholds and durations that are one edit away.

**Non-Goals:**

- New or renamed metrics, or any Go change.
- Packaging dashboards into the Helm chart ([#9](https://github.com/gjed/okf-operator/issues/9)).
- Trace panels, Loki panels, Grafana-managed alert rules.

## Decisions

### Datasource as a dashboard variable, not `__inputs`

The datasource is a `datasource` template variable named `DS_PROMETHEUS`, and panels reference `${DS_PROMETHEUS}`. `__inputs` is substituted only by the UI import flow, so a file-provisioned dashboard would keep a literal `${DS_PROMETHEUS}` UID and fail. A variable resolves in both, and Grafana picks the default Prometheus datasource without a prompt. No datasource UID is written anywhere.

### Alert rules as a Prometheus rule group

`dashboards/alerts/okf-operator.rules.yaml` is a plain `groups:` list. Prometheus, Thanos Ruler, Mimir, vmalert and the Prometheus Operator's `PrometheusRule` `spec` all load it, so a user wraps it in a `PrometheusRule` or mounts it as a rule file without translating anything. Grafana-managed provisioning was rejected: it names a datasource UID and a folder UID, which this change must not hard-code, and it ties the alerts to Grafana. Thresholds are literals in each `expr` and `for`, marked `tune`. A threshold held in a recording rule was rejected: a missing series turns the comparison into a silent no-op, and overriding it is the same one-line edit.

### Alert definitions

- `OkfOperatorRunFailures`: `sum(increase(okf_controller_run_failures_total[3h])) >= 2`, `for: 5m`. A counter series is created by its first failure, so Prometheus sees it start at 1 and `increase` never counts the first failure of a stage and mode pair. A new pair needs 3 failures to reach 2, a known pair needs 2. The default requeue interval is one hour, so a single failing `Source` fires within about two hours.
- `OkfOperatorCursorStale`: `max by (okf_source_repo) (okf_controller_cursor_staleness_seconds) > 14400`, `for: 15m`. The gauge is the time since a run last left the cursor at the source HEAD. A current `Source` saw-tooths up to the requeue interval, so the threshold (4 hours) is well above it. A growing gauge means merges that no run proposed or runs that fail. It does not distinguish the two, because the metrics do not.

### Per-run panels divide by PR-producing runs

No metric counts runs of `write` or `review`; their counters are added once per run, including zero. The only per-run denominator that is exact is `okf.propose.prs.opened` plus `okf.propose.prs.updated`, which `propose` adds with 0 or 1 once per run. "Pages changed per run" and "tokens per run" therefore divide by runs that opened or updated a PR. Tokens of failed runs stay in the numerator, so a failing writer shows as a spike instead of being hidden.

### Share of PRs auto-merged is approvals over PR-producing runs

The operator never sees the merge. It counts approvals by the review App (`okf.controller.prs.approved`) after the gate and review pass, and branch protection merges an approved PR. Every run that pushes to a PR is gated, reviewed and approved again, so approvals are counted per push, not per PR; dividing by `okf.propose.prs.opened` alone would exceed 100% for a PR updated after it opened. The panel is approved over opened plus updated PRs (the PR-producing runs used elsewhere) and says so. Reconcile PRs are never approved, so they lower the share.

### Variables: source and bundle, no knowledge repo

`okf.knowledge.repo` is a span attribute only; no metric carries it. A knowledge repo variable would filter nothing, so it is left out. `okf.bundle` is the nearest label. `Source` and `Bundle` filter the pipeline panels; the controller, gate and approval panels have no such attribute and their descriptions say so.

### Validation failures by source, gate failures by rule

`okf.write.validation_failures` has no rule attribute, so validation failures are split by source. `okf.gate.rule_results` has `okf.gate.rule` and `okf.gate.outcome` (`pass` or `fail`), and the panel filters on `fail`.

### Filter only on the values a panel needs

Pages changed counts `okf_write_result=~"changed|created"`. The pages-by-result panel groups by the label with no filter, so a result value added later appears without an edit.

### Translation contract

The queries use `UnderscoreEscapingWithSuffixes`: dots become underscores, a unit of `s` adds `_seconds`, counters add `_total`, annotation units such as `{page}` add nothing. That is the OpenTelemetry Collector `prometheus` exporter default (`add_metric_suffixes: true`) and the Prometheus OTLP receiver default. Dashboards do not select on `job` or `instance`, so any scrape job name works. `dashboards/README.md` states the contract and the temporality (cumulative) the `increase` and `rate` queries need.

## Risks / Trade-offs

- The operator ignores `OTEL_METRICS_EXPORTER` unless an OTLP endpoint is also set, so a direct Prometheus scrape of the operator needs both. The README documents the collector path and mentions this once. Changing `internal/telemetry` is out of scope.
- The controller keeps merge times and cursor staleness in memory, so both restart from nothing. The panels say so.
- The default OpenTelemetry histogram bounds (5 s to 10000 s) make the merge-to-PR quantiles coarse. A view with finer buckets needs a metrics change.
- The staleness entry of a `Source` is never pruned from the controller's memory, so a deleted `Source` keeps its growing staleness: its alert fires after the threshold and stays firing, and the panel keeps drawing it, until the controller restarts. Pruning it is a Go change and a follow-up; the README documents the effect.
- A `Source` shows in the staleness panel only after a run first leaves its cursor at HEAD, so a `Source` that never succeeds is invisible there and visible in the failure panel and alert.
