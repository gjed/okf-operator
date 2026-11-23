# Tasks

## 1. Honour an explicit exporter

- [x] 1.1 In `internal/telemetry`, force `none` per signal only when `OTEL_<SIGNAL>_EXPORTER` is empty and no OTLP endpoint is set, and drop the `SIMPLIFIED:` note; verify with `go test ./internal/telemetry` covering the unset case still forced to `none` (with and without an endpoint left alone) and an explicit `prometheus`, `console`, `otlp` and `none` left as is with and without an endpoint

## 2. Docs

- [x] 2.1 Update the README Telemetry section and `dashboards/README.md` (drop the OTLP-endpoint workaround); verify the markdown lint passes
