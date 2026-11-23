# helm-chart Specification

## Purpose

A generic Helm chart that deploys the controller with least-privilege access, so a cluster installs a released version without copying manifests and keeps every deployment-specific value outside this repository.

## Requirements

### Requirement: Controller workload

The chart SHALL deploy the controller as one replica with the `Recreate` strategy, running `--log-format json controller` as a non-root user with a read-only root filesystem, no added capabilities, privilege escalation off and the `RuntimeDefault` seccomp profile. It SHALL mount a size-limited `emptyDir` at `/tmp`, probe `/healthz` and `/readyz` on the `probes` port and serve the webhook on the `webhook` port 8080. These settings SHALL NOT be configurable through values.

#### Scenario: Default render

- **WHEN** the chart is rendered with default values
- **THEN** the Deployment has `replicas: 1`, `strategy.type: Recreate`, the hardened security contexts, the `/tmp` volume with a `sizeLimit`, and both probes

### Requirement: Image selection

The chart SHALL run `ghcr.io/gjed/okf-operator` by default, with a tag that defaults to the chart's app version and can be overridden through values.

#### Scenario: Tag follows the app version

- **WHEN** the chart is rendered with no `image.tag`
- **THEN** the container image tag equals the chart's app version

### Requirement: Optional telemetry endpoint

The chart SHALL set `OTEL_EXPORTER_OTLP_ENDPOINT` on the container only when an endpoint is configured in values.

#### Scenario: No endpoint configured

- **WHEN** the chart is rendered with default values
- **THEN** the container has no `OTEL_EXPORTER_OTLP_ENDPOINT` variable

### Requirement: Least-privilege RBAC

The chart SHALL grant the ServiceAccount, cluster-wide, `get`, `list` and `watch` on `knowledgebases` and `sources` of `okf.dev`, `get`, `update` and `patch` on `sources/status`, and `create` and `patch` on events. It SHALL grant leases in the release namespace through a Role. It SHALL NOT use wildcard verbs, resources or API groups, nor grant any write on the CRDs other than `sources/status`.

#### Scenario: CRD access

- **WHEN** the chart is installed
- **THEN** the ServiceAccount can `get` `sources` and `update` `sources/status`, and cannot `update` `sources` or `delete` `knowledgebases`

### Requirement: Secrets readable by name only

The chart SHALL grant `get` on Secrets only through `resourceNames`, one Role and RoleBinding per namespace of the `secrets` values list, and SHALL NOT grant `list` or `watch` on Secrets. A list entry with no names SHALL fail the render.

#### Scenario: Listed and unlisted Secrets

- **WHEN** `secrets` lists namespace `team-a` with the name `kb-app`
- **THEN** the ServiceAccount can `get` the Secret `kb-app` in `team-a`, and cannot `get` another Secret there or `list` Secrets

#### Scenario: Entry without names

- **WHEN** a `secrets` entry has an empty `names` list
- **THEN** rendering fails instead of granting access to every Secret in the namespace

### Requirement: No credentials in the chart

The chart SHALL NOT create a Secret and SHALL NOT take a credential value in its values. Secrets are referenced by name through the KnowledgeBase resources.

#### Scenario: Render creates no Secret

- **WHEN** the chart is rendered with every optional feature enabled
- **THEN** the output holds no `Secret` resource

### Requirement: Webhook exposure

The chart SHALL create a Service for the webhook port. When `ingress.enabled` is true it SHALL create an Ingress for `/webhook` with the configured class, host, annotations and, when a TLS Secret name is set, TLS; it SHALL fail the render when no host is set.

#### Scenario: Ingress enabled

- **WHEN** the chart is rendered with `ingress.enabled`, a host and a class
- **THEN** the Ingress routes that host's `/webhook` to the webhook Service

### Requirement: Optional egress-limiting NetworkPolicy

When `networkPolicy.enabled` is true the chart SHALL create a NetworkPolicy for the controller Pods that allows ingress on the webhook port and egress only to DNS, to the Kubernetes API server, to TCP 443 on the configured CIDRs and to any extra rules from values. It SHALL document that a NetworkPolicy cannot match host names, so the GitHub API and the model endpoint are limited by CIDR.

#### Scenario: Policy rendered

- **WHEN** the chart is rendered with `networkPolicy.enabled`
- **THEN** the policy lists the Ingress and Egress policy types and its egress rules are DNS, the API server and the configured CIDRs

### Requirement: CRDs come from the generated source

The chart's `crds/` directory SHALL hold the same CRDs `make generate` writes to `deploy/crds`, written by the same command. CI SHALL fail a pull request in which the two differ.

#### Scenario: Stale chart CRD

- **WHEN** a pull request changes `deploy/crds` but not `deploy/chart/crds`
- **THEN** the `chart` workflow fails

### Requirement: Chart CI

A `chart` workflow SHALL run on pull requests that touch `deploy/**` or the workflow, and SHALL run `helm lint`, render the chart with default values and with Ingress and NetworkPolicy enabled, and check the CRD copy.

#### Scenario: Broken template

- **WHEN** a pull request breaks a chart template
- **THEN** the `chart` workflow fails

### Requirement: Chart publication

On a published release the `image` workflow SHALL package the chart with the chart version and app version set to the release tag without its leading `v`, and push it to `oci://ghcr.io/gjed/charts`. The chart in the repository SHALL keep a placeholder version.

#### Scenario: Release v1.2.3

- **WHEN** release `v1.2.3` is published
- **THEN** the chart `okf-operator` version `1.2.3` with app version `1.2.3` is pushed to GHCR next to image tag `1.2.3`
