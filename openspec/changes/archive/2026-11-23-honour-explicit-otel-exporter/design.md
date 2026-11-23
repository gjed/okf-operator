# Design

## Context

autoexport builds the exporter from `OTEL_TRACES_EXPORTER` and `OTEL_METRICS_EXPORTER` and falls back to OTLP when they are unset, which would dial `localhost:4318` in every run. Bootstrap avoided that by forcing `none` whenever no OTLP endpoint was set, at the cost of overwriting an explicit value.

## Decisions

- Force `none` per signal only when `OTEL_<SIGNAL>_EXPORTER` is empty and neither `OTEL_EXPORTER_OTLP_ENDPOINT` nor the signal-specific endpoint is set. This is the smallest change that keeps the no-variable default and the endpoint-only default.
- An explicit `otlp` with no endpoint exports to the SDK default `http://localhost:4318`. Rejected: treating it as `none`, because a user who names an exporter has asked for it, and silently dropping it is the bug being fixed. The cost is a failed export attempt when no collector listens, which the batch processor and periodic reader log and do not turn into a run failure.
- The comma-separated form (`OTEL_METRICS_EXPORTER=otlp,prometheus`) is passed through to autoexport untouched.
- No change to the Helm chart: it still sets `OTEL_EXPORTER_OTLP_ENDPOINT` only when `otlpEndpoint` is set.

## Risks

- A deployment that already sets `OTEL_*_EXPORTER=otlp` (or the default `console`) without an endpoint used to export nothing and now exports. That is the documented intent of those variables.
