# Design

## Context

See proposal.md - Why. The controller ships as an image plus sample manifests in `deploy/controller` (a Deployment, a Service, RBAC) and generated CRDs in `deploy/crds`. The raw manifests carry invariants in comments: one replica with `Recreate` (the leader serves the webhook and reports Ready, so a rolling update never becomes Ready), a hardened Pod, and Secrets readable by name only.

## Goals / Non-Goals

**Goals:** a chart a cluster can install from GHCR with `helm install`; the raw manifests' invariants kept and not overridable; no deployment-specific value in the repo; a stale chart CRD fails CI.

**Non-Goals:** a values schema, autoscaling, a PodDisruptionBudget, a ServiceMonitor (the controller pushes OTLP and has no scrape endpoint), creating Secrets or Namespaces, signing the chart.

## Decisions

### CRDs: a generated copy in `crds/`, checked for drift

`make generate` runs `controller-gen` into `deploy/crds` and copies the two files to `deploy/chart/crds/`. The `chart` workflow runs `diff -r deploy/crds deploy/chart/crds`, and the existing `make generate && git diff --exit-code` step in `ci.yml` covers both copies without any edit to it. Rejected: a second `controller-gen` output path (it cannot write one run's output to two places), a symlink (Helm packaging and Windows checkouts handle symlinks unevenly), and CRDs as templates (a templated CRD is no longer the generated file).

Helm applies `crds/` on install only and never on upgrade or deletion. The docs say so and give `helm show crds ... | kubectl apply --server-side -f -` for upgrades. Rejected: moving CRDs into `templates/` with a keep policy, which removes the "never hand-edit, always generated" property.

### The raw manifests are removed

`deploy/controller` would duplicate every invariant and drift from the chart. `helm template` renders the same objects for a cluster without Helm, so no path is lost. The sample `KnowledgeBase` stays in `deploy/samples`. Rejected: keeping both, which doubles the surface a RBAC or hardening change must touch.

### Fixed invariants, configurable the rest

The strategy, replica count, security contexts, probes, ports and the `/tmp` volume are not values: overriding any of them breaks leader election, the webhook or the hardening. `tmpSizeLimit`, resources, scheduling fields, the image, extra environment and extra controller arguments are values.

### Secrets: a list of `{namespace, names}`

`secrets` renders one Role and RoleBinding per entry, with `get` on the listed `resourceNames`. An entry without names fails the render, because a Role rule with empty `resourceNames` grants every Secret in the namespace. `namespace` defaults to the release namespace. The chart cannot grant access to a namespace that does not exist; the install fails there and the operator creates the namespace first.

### NetworkPolicy by CIDR, with the API server explicit

A NetworkPolicy cannot match host names, so `api.github.com` and the model endpoint can only be limited by address. GitHub publishes its ranges at `https://api.github.com/meta`, and they change, so the chart cannot embed them; `networkPolicy.egress.https.cidrs` defaults to `0.0.0.0/0` on TCP 443, which makes the policy work as installed and holds egress to DNS, the API server and HTTPS until the operator narrows it. The Kubernetes API server is a separate rule (`kubeApi.cidrs`, ports 443 and 6443), because narrowing the HTTPS CIDRs to GitHub would otherwise cut the controller off from the cluster. DNS goes to `kube-dns` in `kube-system` by default. A model endpoint inside the cluster, on a port other than 443, or an OTLP collector needs `extraRules`. Rejected: a default that lists GitHub's ranges (stale within weeks), and a FQDN-based policy (CNI-specific CRDs).

The policy is off by default: it needs a CNI that enforces it and values the chart cannot guess.

### Image tag and version

`Chart.yaml` holds `version: 0.0.0` and `appVersion: "0.0.0"`. The `image` workflow packages the chart with `--version` and `--app-version` from the release tag without its `v`, the same version `docker/metadata-action` gives the image, so the default image tag (`.Chart.AppVersion`) names the image published in the same run. The chart job `needs` the image job so a chart never points at an image that failed to build. Packaging a release does not edit the repository, which avoids a version-bump commit on `main`.

### Workflow permissions

`image.yml` had `packages: write` for the whole workflow. Both jobs now declare their own `contents: read` and `packages: write`, with the workflow default reduced to `contents: read`. The new `chart.yml` declares `permissions: {}` and `contents: read` for its job.

### pre-commit

`check-yaml` cannot parse Helm templates. The Kubernetes-manifest hook skips `deploy/chart/templates/`; `helm lint` and `helm template` check those files instead.
