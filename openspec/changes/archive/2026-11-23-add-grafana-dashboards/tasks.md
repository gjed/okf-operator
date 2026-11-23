# Tasks

## 1. Metric names

- [x] 1.1 Export the operator's metrics through the Prometheus exporter and confirm every name and label the panels and rules query; verify by scraping `/metrics` from the `plan` binary and from the controller, write, propose, gate, review and reconcile recorders and checking each queried name and label is present

## 2. Dashboard

- [x] 2.1 Add `dashboards/grafana/okf-operator.json` with the `DS_PROMETHEUS` variable, `Source` and `Bundle` variables and the panels of the spec; verify with `python3 -m json.tool` and by provisioning it into Grafana with a Prometheus datasource scraping the exported metrics, then running every panel query through the Grafana HTTP API without an error

## 3. Alerts

- [x] 3.1 Add `dashboards/alerts/okf-operator.rules.yaml` with the two alerts; verify with `promtool check rules` and a `promtool test rules` case per alert that fires and one that stays quiet

## 4. Docs

- [x] 4.1 Add `dashboards/README.md` (import, rules, translation contract, collector settings, tuning) and link it from the README; verify `pre-commit run --all-files` passes
