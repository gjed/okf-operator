# Spec Delta

## MODIFIED Requirements

### Requirement: Deployment manifests

`deploy/` SHALL hold the CRDs, the Helm chart (see the `helm-chart` capability) and the sample resources. `make generate` SHALL regenerate the CRDs and deepcopy code with a pinned `controller-gen`, write the CRDs to both `deploy/crds` and `deploy/chart/crds`, and CI SHALL fail when it leaves a diff.

#### Scenario: Generated files up to date

- **WHEN** `make generate` runs on a clean checkout
- **THEN** `git diff --exit-code` passes
