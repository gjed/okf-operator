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

A run SHALL repair the open bot PR, if any, then materialise the knowledge checkout from the GitHub tarball API at the head of the open bot PR as repaired, else of the default branch, load the routing map from the default branch, and call in order `plan.Build` (or `reconcile.Build` when the reconcile interval is due), `write.Run` and `propose.Run`.

#### Scenario: Open bot PR

- **WHEN** a clean bot PR for the source is open
- **THEN** the checkout is its head and `propose` gets that head as the knowledge base

#### Scenario: Behind bot PR

- **WHEN** the bot PR is `behind`
- **THEN** the controller updates the branch, and the checkout and `propose`'s knowledge base are the new head

#### Scenario: Conflicting bot PR

- **WHEN** the bot PR is `dirty`
- **THEN** the controller pushes the two-parent commit that regenerates the conflicting pages, and the checkout and `propose`'s knowledge base are that commit

#### Scenario: Weekly reconcile

- **WHEN** a `Source`'s reconcile interval has passed since its last reconcile
- **THEN** the run calls `reconcile.Build`, not `plan.Build`, records `lastReconcileTime`, and stops after `propose`

### Requirement: Bot PR repair

A run SHALL read the merge state of the open bot PR before the checkout and repair the PR when it is `behind` (update-branch) or `dirty` (regeneration of the conflicting pages). The repair SHALL use the writer App's knowledge-repo token. It SHALL fail the run, with the stage `checkout` for the merge-state read and update-branch and the stage `repair` for a regeneration, when it cannot complete.

#### Scenario: Update-branch fails

- **WHEN** update-branch answers 422
- **THEN** the run fails at the stage `checkout` and nothing else is written

#### Scenario: Regeneration refused

- **WHEN** the repair refuses a PR whose body omits its earlier ranges
- **THEN** the run fails at the stage `repair` and the cursor is unchanged

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

### Requirement: Deleted Source telemetry

When a reconcile finds that its `Source` no longer exists, the controller SHALL drop that `Source`'s pending merge time and cursor staleness entry, so `okf.controller.cursor.staleness` stops reporting it, and SHALL leave the entries of other `Source`s unchanged. A read error other than not found SHALL leave the entries unchanged.

#### Scenario: Deleted Source stops reporting staleness

- **WHEN** two `Source`s have reported `okf.controller.cursor.staleness`, one is deleted and the controller reconciles it
- **THEN** the next collection of the gauge has no series for the deleted `Source`'s repo and still has the series of the other `Source`

### Requirement: Deployment manifests

`deploy/` SHALL hold the CRDs, the Helm chart (see the `helm-chart` capability) and the sample resources. `make generate` SHALL regenerate the CRDs and deepcopy code with a pinned `controller-gen`, write the CRDs to both `deploy/crds` and `deploy/chart/crds`, and CI SHALL fail when it leaves a diff.

#### Scenario: Generated files up to date

- **WHEN** `make generate` runs on a clean checkout
- **THEN** `git diff --exit-code` passes

### Requirement: Approvals of superseded heads are dismissed

On every run with an open bot PR, once the PR head is settled (after update-branch or after the regeneration commit) and before the checkout of that head, the controller SHALL dismiss, with the review App, every approving review the review App posted on a commit other than that head. The message SHALL name the commit. The run SHALL NOT review or approve the head. Reviews by other users and the review App's approvals of the head SHALL be left alone.

#### Scenario: Behind PR updated

- **WHEN** the review App approved head H and the run updates the branch off H
- **THEN** that approval is dismissed through `PUT /repos/{owner}/{repo}/pulls/{n}/reviews/{id}/dismissals`, a human's approval of H stays, and nothing is reviewed or approved

#### Scenario: Conflicting PR regenerated

- **WHEN** the review App approved head H and the run pushes the regeneration commit
- **THEN** that approval is dismissed and no review is posted

#### Scenario: Approval of the current head

- **WHEN** the review App approved the head the run builds on
- **THEN** that approval is not dismissed

### Requirement: Dismissals are not lost

A failed dismissal SHALL fail the run at the stage `dismiss`, and a run that fails after it moved the bot PR head SHALL NOT leave the approvals of the old head in place: the dismissal SHALL come before the checkout of the moved head, and the next run SHALL dismiss what is left.

#### Scenario: A stage after the move fails

- **WHEN** the checkout of the moved head fails after update-branch
- **THEN** the approval of the old head was already dismissed

#### Scenario: Dismissal fails

- **WHEN** the dismissal fails after the head moved, and the next run finds the moved head as the PR head
- **THEN** the next run dismisses the approval of the old head without repairing again
