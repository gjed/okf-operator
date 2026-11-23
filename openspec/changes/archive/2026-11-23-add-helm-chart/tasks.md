# Tasks

## 1. CRDs from one source

- [x] 1.1 Make `make generate` copy the CRDs into `deploy/chart/crds/`; verify with `make generate && git diff --exit-code` and `diff -r deploy/crds deploy/chart/crds`

## 2. Chart

- [x] 2.1 Add `deploy/chart/` with `Chart.yaml`, `values.yaml`, helpers, ServiceAccount, Deployment and webhook Service with the raw manifests' invariants; verify with `helm lint deploy/chart` and `helm template` showing `Recreate`, one replica, the security contexts, the `/tmp` volume, probes, ports, args and no `OTEL_EXPORTER_OTLP_ENDPOINT` by default
- [x] 2.2 Add the RBAC templates: ClusterRole and binding, leader-election Role, per-namespace Secret Roles by `resourceNames`; verify with `helm template` that no rule has a wildcard or a Secret `list` or `watch`, and that an entry without names fails
- [x] 2.3 Add the optional Ingress and NetworkPolicy; verify with `helm template` with both enabled, and that a missing host fails

## 3. CI and publication

- [x] 3.1 Add `.github/workflows/chart.yml` and skip Helm templates in the `check-yaml` hook; verify with `actionlint`, `pre-commit run --all-files` and the workflow's commands run locally
- [x] 3.2 Add the chart publish job to `.github/workflows/image.yml`; verify with `actionlint` and `helm package deploy/chart --version 0.0.0-test --app-version 0.0.0-test`

## 4. Cluster proof

- [x] 4.1 Install on a kind cluster with no App credentials; verify the Pod is Ready, a sample `Source` is `Ready=False` with reason `CredentialsFailed`, a signed webhook answers 202 and an unsigned one 401
- [x] 4.2 Verify least privilege with `kubectl auth can-i --as=system:serviceaccount:<ns>:<sa>`: a listed Secret is readable, an unlisted Secret and `secrets` list are denied, and only `sources/status` is writable among the CRDs

## 5. Docs

- [x] 5.1 Remove `deploy/controller`; document the install, CRD upgrades, the values and the NetworkPolicy limits in `README.md` and `docs/controller.md`, and add the `chart` job and chart publication to the `CONTRIBUTING.md` tables; verify the Markdown lint passes
