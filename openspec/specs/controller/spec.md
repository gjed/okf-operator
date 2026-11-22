# controller Specification

## Purpose

Defines `okf-operator controller`: the Kubernetes operator that runs the pipeline for `KnowledgeBase` and `Source` resources, woken by GitHub webhooks and a periodic requeue.

## Requirements

### Requirement: Controller subcommand

`okf-operator controller` SHALL run a controller-runtime manager that reconciles `Source` objects of group `okf.dev`, version `v1alpha1`. It SHALL serve health probes and the webhook receiver, and SHALL use leader election unless `--leader-elect=false` is passed. It SHALL never shell out to run a pipeline stage.

#### Scenario: Subcommand registered

- **WHEN** the user runs `okf-operator help`
- **THEN** `controller` is listed with a one-line description

#### Scenario: Bad flag

- **WHEN** the user runs `okf-operator controller --frobnicate`
- **THEN** the error and the controller flags go to stderr and the process exits 2

### Requirement: Custom resources

`KnowledgeBase` SHALL name the knowledge repo, the writer and review App Secrets, the writer and reviewer model endpoints and names, optional model API key Secrets and the webhook Secret. `Source` SHALL name the source repo, a `KnowledgeBase` in its namespace, a requeue interval (default `1h`) and a reconcile interval (default `168h`, `0s` disables). Secrets SHALL be referenced by name only.

#### Scenario: Defaults

- **WHEN** a `Source` is created without intervals
- **THEN** it is requeued hourly and reconciled weekly

### Requirement: Source status

`Source` status SHALL carry a `Ready` condition, the last run time and mode, the last cursor seen, the last PR, the last error and the last reconcile time. The controller SHALL never read the cursor from status.

#### Scenario: Successful run status

- **WHEN** a run of a `Source` succeeds and `propose` opened PR 12
- **THEN** its `Ready` condition is `True`, `lastPR.number` is 12, `lastCursor` is the commit `propose` moved the cursor to, and `lastError` is empty

#### Scenario: Failed run status

- **WHEN** a run fails in `write`
- **THEN** `Ready` is `False` with reason `WriteFailed`, `lastError` holds the redacted error, a Warning event is recorded, and the cursor on `kb-state` is unchanged

### Requirement: Level-triggered runs

A webhook SHALL only enqueue a `Source`. Every run SHALL plan from the `kb-state` cursor to the source HEAD, and every reconcile SHALL return a requeue after the smaller of the requeue interval and the time to the next reconcile run.

#### Scenario: Periodic requeue without webhook

- **WHEN** a `Source` with `requeueInterval: 1m` exists and no webhook arrives
- **THEN** the controller runs it again after about one minute

### Requirement: Webhook receiver

`POST /webhook` SHALL verify `X-Hub-Signature-256` with HMAC-SHA256 and a constant-time comparison against each `KnowledgeBase` webhook secret, SHALL reject bodies over 5 MiB with 413, and SHALL answer 401 and enqueue nothing when the header is missing or matches no secret. It SHALL drop events from a matching knowledge base's knowledge repo and from the writer or review App's bot user.

#### Scenario: Oversized body

- **WHEN** the body is larger than 5 MiB
- **THEN** the response is 413 and nothing is enqueued

#### Scenario: Bad signature

- **WHEN** the signature header is missing or wrong
- **THEN** the response is 401 and nothing is enqueued

#### Scenario: Knowledge repo event

- **WHEN** a signed `push` comes from the knowledge repo itself
- **THEN** nothing is enqueued and `okf.controller.events.dropped` counts reason `knowledge_repo`

#### Scenario: Own bot event

- **WHEN** a signed event's sender is the writer App's bot user
- **THEN** nothing is enqueued and the drop reason is `bot`

### Requirement: Enqueued events

The receiver SHALL enqueue only for `push` to the default branch and `pull_request` closed and merged into the default branch, and only the `Source`s whose repository is the event's repository and whose knowledge base's secret matched.

#### Scenario: Signed merge event

- **WHEN** a `pull_request` `closed` event with `merged: true` for `acme/api`, signed with the secret of knowledge base `kb-a`, arrives and `Source`s of `acme/api` exist for `kb-a` and `kb-b`
- **THEN** only the `kb-a` `Source` is enqueued and the response is 202

### Requirement: Run order

A run SHALL materialise the knowledge checkout from the GitHub tarball API at the head of the open bot PR, else of the default branch, load the routing map from the default branch, and call in order `plan.Build` (or `reconcile.Build` when the reconcile interval is due), `write.Run` and `propose.Run`.

#### Scenario: Open bot PR

- **WHEN** a bot PR for the source is open
- **THEN** the checkout is its head and `propose` gets that head as the knowledge base

#### Scenario: Weekly reconcile

- **WHEN** a `Source`'s reconcile interval has passed since its last reconcile
- **THEN** the run calls `reconcile.Build`, not `plan.Build`, records `lastReconcileTime`, and stops after `propose`

### Requirement: Gate, review and approval

For a version 1 plan whose `propose` opened or updated the PR, a run SHALL call `gate.Evaluate` without a review, and only if that passes `review.Run`, `gate.Evaluate` with the review result, and `approve.Run`.

#### Scenario: Deterministic gate fails

- **WHEN** the gate without review fails
- **THEN** no reviewer model call is made and nothing is approved

### Requirement: Serialised runs per knowledge repository

Runs of `Source`s that share a knowledge repo SHALL never overlap. A `Source` whose knowledge repo is busy SHALL be requeued.

#### Scenario: Two Sources of one knowledge repo

- **WHEN** two `Source`s of the same knowledge repo are reconciled at once
- **THEN** one run finishes before the other starts

### Requirement: Dismiss stale approvals

A run that pushes nothing SHALL gate the bot PR head again, without a review, when that head is the one the controller last pushed and it still holds the plan behind it. When a gate run fails on head H, the controller SHALL dismiss, with the review App, every approving review the review App posted on commit H.

#### Scenario: Gate fails on an approved head

- **WHEN** the review App approved head H, and a later run that pushes nothing gates H again and the gate fails
- **THEN** that approval is dismissed through `PUT /repos/{owner}/{repo}/pulls/{n}/reviews/{id}/dismissals`, and nothing is reviewed or approved

### Requirement: Secrets never leak

App private keys, App JWTs, installation tokens, webhook secrets and model API keys SHALL NOT appear in logs, Kubernetes events, `Source` status or span attributes.

#### Scenario: Error echoing a token

- **WHEN** a GitHub error response quotes the installation token
- **THEN** the token is redacted from the log line, the event, `lastError` and the span

### Requirement: Controller telemetry

The controller SHALL emit `okf.controller.events.received`, `okf.controller.events.dropped` (by reason), `okf.controller.runs.queued`, `okf.controller.run.duration`, `okf.controller.run.failures` (by stage), `okf.controller.merge_to_pr`, `okf.controller.prs.approved` and `okf.controller.cursor.staleness`, and SHALL wrap each run in a `controller run` span.

#### Scenario: Failed stage counted

- **WHEN** a run fails in `propose`
- **THEN** `okf.controller.run.failures` is incremented with stage `propose` and the `controller run` span has error status

### Requirement: Deployment manifests

`deploy/` SHALL hold the CRDs, RBAC limited to the CRDs, the named Secrets, Events and leases, and a sample Deployment. `make generate` SHALL regenerate the CRDs and deepcopy code with a pinned `controller-gen`, and CI SHALL fail when it leaves a diff.

#### Scenario: Generated files up to date

- **WHEN** `make generate` runs on a clean checkout
- **THEN** `git diff --exit-code` passes
