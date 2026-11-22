# Design

## Context

`plan`, `reconcile`, `write`, `propose`, `gate`, `review` and `approve` are library packages with thin CLI wrappers. The CLI wrappers read a knowledge checkout from disk, credentials from flags and environment variables, and pass JSON files between stages. The controller runs the same packages in one process, with credentials from Kubernetes Secrets and a checkout it builds itself. The image is distroless, with no git binary.

## Goals / Non-Goals

**Goals:**

- One Deployment that keeps every `Source` in sync, woken by webhooks and by a periodic requeue.
- The same stage order and safety rules as the documented CLI flow.
- The writer App never sees the review App's credentials, and the reverse.
- Telemetry that issue #10's dashboards can use.

**Non-Goals:**

- A Helm chart (#9), dashboards (#10), a Job per run, multi-cluster or sharded controllers.
- Changing the stage packages.

## Decisions

### API: `okf.dev/v1alpha1`, `KnowledgeBase` and `Source`

Both kinds are namespaced. A `Source` refers to a `KnowledgeBase` in its own namespace by name, so a namespace is the unit of tenancy and RBAC. Secrets are referenced by name only, with fixed keys: an App Secret holds `app-id` and `private-key`, a model key Secret holds `api-key`, the webhook Secret holds `secret`. Fixed keys keep the spec small and the manifests obvious.

`Source.status` holds `observedGeneration`, a `Ready` condition (`True` after a successful run, `False` with the failed stage as reason and the redacted error as message), `lastRunTime`, `lastRunMode` (`plan` or `reconcile`), `lastCursor`, `lastPR` (number and URL), `lastError` and `lastReconcileTime`. `lastCursor` is the commit the cursor held after the run as `propose` reported it. It is informational: every run reads the cursor from `kb-state`.

`spec.reconcileInterval` defaults to `168h` (weekly). A run is a reconcile run when the interval has passed since `lastReconcileTime`, or since the `Source` was created when it never reconciled; `0s` disables it. An interval rather than a cron schedule needs no cron parser and survives restarts through status. The first reconcile happens one interval after creation: the first `plan` run anchors the cursor, and an immediate backfill stays an explicit `reconcile` run from the CLI.

Rejected: cluster-scoped `KnowledgeBase`. Secrets are namespaced, so a cluster-scoped object would need a namespace in every Secret reference and cluster-wide Secret access.

### Level-triggered: webhooks only enqueue

The webhook handler never runs the pipeline. It sends a generic event for each matching `Source` through a channel source; the reconciler always diffs from the `kb-state` cursor to the source HEAD, so duplicate, lost and reordered events are harmless. Every reconcile returns `RequeueAfter` of the smaller of `spec.requeueInterval` (default `1h`) and the time to the next reconcile run.

### Webhook receiver

`POST /webhook` on its own listener (`--webhook-bind-address`, default `:8080`).

1. The body is read through a 5 MiB cap; a larger body answers 413. GitHub caps payloads at 25 MiB, but `push` and `pull_request` payloads the controller acts on are far smaller, and a cap bounds memory before the signature is checked.
1. `X-Hub-Signature-256` is checked with HMAC-SHA256 against the webhook secret of every `KnowledgeBase`, with `hmac.Equal`. No match, or a missing or malformed header, answers 401 and enqueues nothing. Several knowledge bases may share one App and one secret.
1. Dropped with 202 and a reason: the repository is the knowledge repo of a matching knowledge base (`knowledge_repo`); the sender is the writer or review App's bot user (`bot`); the event is not one below (`ignored_event`); no `Source` matches (`no_source`).
1. Enqueued: `push` to `refs/heads/<default branch>`, and `pull_request` with action `closed`, `merged: true` and base the default branch. Every other event, `ping` included, enqueues nothing.
1. The `Source`s enqueued are those whose `spec.repository` equals the event's repository (case-insensitive) and whose knowledge base is one whose secret matched.

The bot logins come from `GET /app` with each App's JWT and are cached in memory by the reconciler after a run; before the first run nothing is dropped as `bot`, which is harmless because enqueueing is idempotent.

### Run order

1. Read the writer App and writer model key Secrets.
1. Record declined bot PRs (below).
1. Find the open bot PR (`okf/<owner>/<name>` into the default branch). The checkout SHA is its head, else the default branch head.
1. Materialise the checkout from `GET /repos/{owner}/{repo}/tarball/{sha}` into a per-run temp directory, removed after the run. Entries are confined to the directory, only regular files, directories and symlinks are kept, and the archive is capped at 256 MiB uncompressed.
1. The routing map is always the default branch's `.kb/routing.yaml`, read with the contents API at the default branch head, as the README asks of CLI users with `--routing`. With no bot PR open it is the same file as in the checkout.
1. `plan.Build`, or `reconcile.Build` when the reconcile interval is due.
1. `write.Run` against the checkout, `propose.Run` with the checkout SHA as `--knowledge-base`.
1. Only for a version 1 plan whose `propose` opened or updated the PR: `gate.Evaluate` without the review. If it fails, dismiss stale approvals and stop.
1. Read the review App and reviewer model key Secrets. `review.Run`, then `gate.Evaluate` with `review.GateInput()`. If it fails, dismiss stale approvals and stop; otherwise `approve.Run` with the review App.

The deterministic gate runs first so a PR that can never auto-merge (a created page, a type off the allowlist) costs no reviewer tokens; the second gate run is the one `approve` consumes, and it is bound to repo, PR, head, base and plan digest like the CLI flow. A reconcile plan stops after `propose`: `gate` and `review` refuse version 2 plans, so a reconcile PR always goes to a human. A run whose `propose` changed nothing does not gate: the PR head was produced by an earlier plan.

`review.Run` reads GitHub with the writer App, as the `review` command does; the review App is installed on the knowledge repo only and cannot read source repos. The review App is used only for `approve` and for dismissals.

SIMPLIFIED: the pipeline runs in-process, so the controller Pod holds both Apps' keys and both model keys. Upgrade path: a Job per run, with the review stages in a second Job, when each step needs its own Secrets.

### Serialisation per knowledge repository

Each run takes a non-blocking lock keyed by the lower-cased knowledge repo `owner/name`, held by the reconciler in memory. A `Source` whose knowledge repo is busy is requeued after 15 seconds instead of blocking a worker. Two `KnowledgeBase`s naming one repo share the lock. Leader election (on by default) keeps one active replica, so the in-memory lock is enough. The cursor compare-and-swap in `propose` still fails a run that loses a race.

Rejected: one workqueue keyed by knowledge base. controller-runtime queues reconcile requests per object; a second queue would duplicate its retry and rate-limit logic.

### Declined ranges

At the start of a run, the controller lists the 30 most recently updated closed PRs from `okf/<owner>/<name>`. For each one closed without merging whose number is not yet recorded, it parses the `<!-- okf:range FROM..TO -->` markers of its body and appends one line per range to `declined/<owner>/<name>` on `kb-state`:

```text
<pr-number> <from or -> <to>
```

Lines are sorted by PR number, then range order. The write is a compare-and-swap on the file's blob SHA, and creates `kb-state` with a parentless commit when it is missing, like the cursor. Ranges omitted from a long PR body are not recorded. Recording never blocks a later proposal of the same range.

### Dismissing a stale approval

When a gate run fails on head H, the controller lists the PR's reviews with the review App and dismisses each `APPROVED` review by the review App's bot user whose `commit_id` is H (`PUT /repos/{owner}/{repo}/pulls/{n}/reviews/{id}/dismissals`, covered by `pull_requests: write`).

Rejected: publishing the gate result as a required check run. It needs `checks: write` on the writer App, a check name that branch protection must require, and a second source of truth next to the approval the review App already posts. Dismissal reuses the existing permission and leaves branch protection unchanged.

### Credentials

The controller builds `github.Config` values in memory: `AppConfig` gains a `Key` field so the PEM never touches disk. The writer provider is created for checkout, plan, write, propose, gate and review; the review App Secret and the reviewer model key are read only after `propose`, for the review, approve and dismissal steps. Every key, token, webhook secret and model key is registered with one `github.Secrets` per run, which redacts logs, errors, events and status. Span attributes carry only repo names, SHAs, PR numbers and counts.

### Telemetry

| Metric                             | Type      | Attributes                              |
| ---------------------------------- | --------- | --------------------------------------- |
| `okf.controller.events.received`   | counter   | `okf.controller.event`                  |
| `okf.controller.events.dropped`    | counter   | `okf.controller.reason`                 |
| `okf.controller.runs.queued`       | counter   | `okf.controller.trigger`                |
| `okf.controller.run.duration`      | histogram | `okf.controller.mode`, `okf.controller.outcome` |
| `okf.controller.run.failures`      | counter   | `okf.controller.stage`, `okf.controller.mode`   |
| `okf.controller.merge_to_pr`       | histogram | none                                    |
| `okf.controller.prs.approved`      | counter   | none                                    |
| `okf.controller.cursor.staleness`  | gauge     | `okf.source`                            |

`merge_to_pr` is the time from the earliest source PR merge the webhook saw since the last proposal to the run that opened or updated the knowledge PR. SIMPLIFIED: kept in memory, so a restart loses pending merge times. `cursor.staleness` is the time since the last run that brought the cursor to the source HEAD.

Each run is one `controller run` span with the source, knowledge repo, mode, stage, PR and outcome; stage spans and metrics from the packages nest under it.

## Risks / Trade-offs

- A tarball honours `export-ignore`, so a knowledge repo that export-ignores files fails `propose`'s checkout verification. Documented; the upgrade path is a checkout built from the trees and blobs APIs.
- Both Apps' keys live in one Pod. See the SIMPLIFIED note above.
- An in-memory lock assumes one active replica; leader election provides it.
