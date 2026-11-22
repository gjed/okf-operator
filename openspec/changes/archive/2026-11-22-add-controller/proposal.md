# Proposal

## Why

Every pipeline stage runs as a standalone subcommand, but nothing runs them when a source PR merges. The Kubernetes runtime was chosen instead of Argo Events and Workflows: one Deployment, no event bus, no JetStream storage ([#8](https://github.com/gjed/okf-operator/issues/8)). This change belongs to the controller release step that follows the pipeline stages ([#4](https://github.com/gjed/okf-operator/issues/4), [#5](https://github.com/gjed/okf-operator/issues/5), [#7](https://github.com/gjed/okf-operator/issues/7)).

Two follow-ups land with it because the controller owns the PR lifecycle: a bot PR closed without merging is recorded as declined on `kb-state`, and an approval the review App posted is dismissed when a later gate run fails on the same head.

## What Changes

- New subcommand `okf-operator controller`: a controller-runtime manager, level-triggered. GitHub webhooks only enqueue a `Source`; a periodic `RequeueAfter` covers lost events.
- New CRDs in group `okf.dev`, version `v1alpha1`: `KnowledgeBase` (knowledge repo, writer and review App Secrets, writer and reviewer model endpoints, model names and API key Secrets, webhook Secret) and `Source` (source repo, knowledge base reference, requeue interval, reconcile interval, weekly by default). Secrets are referenced by name only. `Source` status carries a `Ready` condition and the last run, last cursor seen, last PR and last error. The cursor stays on `kb-state`; status is never read back as the cursor.
- Webhook receiver on `POST /webhook`: verifies `X-Hub-Signature-256` against each knowledge base's webhook secret in constant time, caps the body at 5 MiB, drops events from the knowledge repo and from the writer and review Apps' bot users, and enqueues only the `Source`s of the matching knowledge base and repository for `push` to the default branch and `pull_request` closed and merged into the default branch.
- One run, in-process through the package APIs: materialise the knowledge checkout from the GitHub tarball API at an exact SHA (the bot branch when a bot PR is open, else the default branch), `plan.Build` (or `reconcile.Build` when the reconcile interval is due), `write.Run`, `propose.Run`, then for a version 1 plan that pushed a commit: deterministic `gate.Evaluate`, `review.Run`, `gate.Evaluate` with the review, `approve.Run`. A failing gate dismisses the review App's approvals of that head.
- Runs are serialised per knowledge repository with an in-process lock; a `Source` whose knowledge repo is busy is requeued.
- A bot PR on `okf/<owner>/<name>` closed without merging has its source ranges recorded in `declined/<owner>/<name>` on `kb-state`. Bookkeeping only.
- Raw manifests under `deploy/`: CRDs, RBAC limited to the CRDs, named Secrets, Events and leases, a sample Deployment. `make generate` regenerates CRDs and deepcopy code with a pinned `controller-gen`; CI fails on a diff. Controller tests run against a pinned envtest.
- Telemetry: `okf.controller.*` metrics (events received and dropped by reason, runs queued, run duration, failures by stage, merge-to-PR latency, PRs approved, cursor staleness) and a `controller run` span per run.

## Capabilities

### New Capabilities

- `controller`: the `controller` subcommand, CRDs, webhook receiver, run order, serialisation, approval dismissal, manifests and telemetry.

### Modified Capabilities

- `source-cursor`: declined ranges of bot PRs closed without merging are recorded on `kb-state`.
- `github-access`: the controller reads App credentials from Kubernetes Secrets, keeps the writer and review identities apart, and resolves each App's bot login.

## Impact

- New `api/v1alpha1`, `internal/controller`, `internal/cli/controller.go`, `deploy/` manifests, `docs/controller.md`.
- `internal/github` gains an in-memory private key option and an App slug lookup.
- No change to `internal/{plan,write,propose,reconcile,provenance,gate,review,approve,routing,cursor,model}`.
- New dependencies: controller-runtime and the Kubernetes client libraries.
- No Helm chart ([#9](https://github.com/gjed/okf-operator/issues/9)) and no dashboards ([#10](https://github.com/gjed/okf-operator/issues/10)).
