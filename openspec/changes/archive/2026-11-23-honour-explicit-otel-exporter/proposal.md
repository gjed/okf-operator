# Proposal

## Why

`telemetry.Setup` forces `OTEL_TRACES_EXPORTER` and `OTEL_METRICS_EXPORTER` to `none` unless an OTLP endpoint is set, which overwrites an exporter the user named. `OTEL_METRICS_EXPORTER=prometheus` alone serves nothing and `console` is ignored, so a Prometheus scrape of the operator, the simplest way to use the dashboards without a collector, needs a fake OTLP endpoint ([#38](https://github.com/gjed/okf-operator/issues/38)). The bootstrap design recorded this as a `SIMPLIFIED:` ceiling with the upgrade path "honour the explicit value when it is set". This change belongs to the observability release step that follows the dashboards ([#35](https://github.com/gjed/okf-operator/pull/35)).

## What Changes

- `internal/telemetry`: `Setup` forces `none` for a signal only when `OTEL_<SIGNAL>_EXPORTER` is unset or empty and no OTLP endpoint is set. Any explicit value (`prometheus`, `console`, `otlp`, `none`) is left to autoexport. With no `OTEL_*` variable at all nothing changes: nothing is exported and no collector is contacted.
- An explicit `otlp` with no endpoint is honoured: the SDK default endpoint (`http://localhost:4318`) is used. Naming `otlp` is a request to export.
- The `SIMPLIFIED:` note on `Setup` is removed.
- The README and `dashboards/README.md` state the new behaviour, and the dashboards README drops the workaround that sets an OTLP endpoint to any value.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `cli`: the telemetry requirement no longer says export needs an endpoint; it says nothing is exported unless an exporter or an endpoint is configured, and that an explicitly named exporter is honoured.

## Impact

- `internal/telemetry` and its test, plus the two READMEs above. No metric, flag, CRD or chart change. The `dashboards` capability text is unchanged.
