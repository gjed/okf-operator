# Proposal

## Why

The controller has no deployable unit: `deploy/controller` is a sample the operator copies and edits, and the image is the only release artifact ([#9](https://github.com/gjed/okf-operator/issues/9), follow-up of [#8](https://github.com/gjed/okf-operator/issues/8)). A generic Helm chart, published next to the image under the same version, lets any cluster install the controller with deployment-specific values kept outside this repository. This is release step 3 of the plan, after the controller ([#8](https://github.com/gjed/okf-operator/issues/8)).

## What Changes

- New chart `deploy/chart/`: CRDs, a one-replica `Recreate` Deployment, a ServiceAccount with least-privilege RBAC, the webhook Service, an optional Ingress and an optional NetworkPolicy.
- RBAC as the raw manifests had it: a ClusterRole reading only the `okf.dev` CRDs, writing `sources/status` and creating or patching events; a leader-election Role on leases in the release namespace; Secrets readable by `resourceNames` only, from a values list of `{namespace, names}` that renders one Role and RoleBinding per namespace.
- Secrets are referenced by name only. The chart never creates a Secret that holds a credential.
- `make generate` also writes the CRDs into `deploy/chart/crds/`, and a new `chart` workflow fails a PR whose chart CRDs differ from `deploy/crds`.
- New `chart` workflow (PRs touching `deploy/**`): `helm lint`, `helm template` with default values and with Ingress and NetworkPolicy enabled, and the CRD drift check.
- The `image` workflow also packages the chart with the release version as chart version and app version, and pushes it to `oci://ghcr.io/gjed/charts`.
- `deploy/controller` is removed: the chart replaces it, and `helm template` renders the same manifests for a cluster without Helm.
- Docs: install section, values reference, CI and release tables.

## Capabilities

### New Capabilities

- `helm-chart`: the chart's workloads, RBAC, network exposure, secret handling, CRD sourcing and publication.

### Modified Capabilities

- `controller`: the "Deployment manifests" requirement now points at the chart and the CRD copy that `make generate` writes.

## Impact

- `deploy/chart/` (new), `deploy/controller/` (removed), `Makefile`, `.pre-commit-config.yaml` (Helm templates are not YAML), `.github/workflows/chart.yml` (new), `.github/workflows/image.yml`.
- No change to Go code, `api/` or the CRDs themselves. `.github/workflows/ci.yml` is untouched; its existing `make generate` drift check also covers the chart CRD copy.
- Docs: `README.md`, `docs/controller.md`, `CONTRIBUTING.md`.
- Anyone applying `deploy/controller` by hand moves to `helm install`, or to `helm template | kubectl apply -f -`.
