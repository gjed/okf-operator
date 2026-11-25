# Controller

`okf-operator controller` runs the pipeline as a Kubernetes operator. It is level-triggered: a GitHub webhook only queues a `Source`, every run diffs from the cursor on `kb-state` to the source HEAD, and a periodic requeue covers lost events. There is no event bus and no workflow engine.

## Install

```bash
helm install okf-operator oci://ghcr.io/gjed/charts/okf-operator \
  --version 1.2.3 --namespace okf-operator --create-namespace -f values.yaml
```

The chart and the image are published under the same release version, and the chart runs the image of its own version unless `image.tag` says otherwise. Pin `--version` to a release. The chart installs the CRDs, a one-replica Deployment, the webhook Service, a ServiceAccount with its RBAC, and optionally an Ingress and a NetworkPolicy. [`deploy/samples`](../deploy/samples/knowledgebase.yaml) is a `KnowledgeBase`, a `Source` and the Secrets they reference.

The chart never creates a Secret and has no credential in its values. Create the Secrets your `KnowledgeBase`s reference, then list them under `secrets` so the controller may read them; the Secret rules below say how.

Helm applies the CRDs only on `helm install`, never on `helm upgrade`. After upgrading to a release that changes the CRDs, apply them yourself:

```bash
helm show crds oci://ghcr.io/gjed/charts/okf-operator --version 1.2.4 | kubectl apply --server-side -f -
```

Without Helm, create the namespace (`kubectl create namespace okf-operator`), then render the chart and apply the output; the CRDs come from `helm show crds` as above:

```bash
helm template okf-operator oci://ghcr.io/gjed/charts/okf-operator \
  --version 1.2.3 --namespace okf-operator -f values.yaml | kubectl apply -n okf-operator -f -
```

To move a cluster that applied the removed `deploy/controller` manifests by hand, delete their objects first, because `helm install` refuses to take over objects it did not create. Keep the namespace, the CRDs and your `KnowledgeBase`, `Source` and Secret resources; the chart's `crds/` skips CRDs that already exist.

```bash
kubectl -n okf-operator delete deployment/okf-operator service/okf-operator-webhook serviceaccount/okf-operator \
  role/okf-operator-leader-election rolebinding/okf-operator-leader-election \
  role/okf-operator-secrets rolebinding/okf-operator-secrets
kubectl delete clusterrole/okf-operator clusterrolebinding/okf-operator
```

Delete `role/okf-operator-secrets` and its binding in every other namespace where you repeated them, then install the chart and list those Secrets under `secrets`. The controller is down until the install finishes; runs resume from the cursors on `kb-state`.

The Deployment runs one replica with the `Recreate` strategy, a non-root user, a read-only root filesystem, no capabilities and the `RuntimeDefault` seccomp profile. None of this is a value: a change would break leader election or the hardening.

### Values

| Value                                                       | Default                         | Description                                                                          |
| ----------------------------------------------------------- | ------------------------------- | ------------------------------------------------------------------------------------ |
| `image.repository`                                          | `ghcr.io/gjed/okf-operator`     | Image                                                                                |
| `image.tag`                                                 | chart `appVersion`              | Image tag                                                                            |
| `image.pullPolicy`                                          | `IfNotPresent`                  |                                                                                      |
| `imagePullSecrets`                                          | `[]`                            | Pull Secrets, by name                                                                |
| `serviceAccount.create`                                     | `true`                          | Create the ServiceAccount; with `false`, `serviceAccount.name` names an existing one |
| `serviceAccount.name`                                       | release name                    |                                                                                      |
| `serviceAccount.annotations`                                | `{}`                            |                                                                                      |
| `secrets`                                                   | `[]`                            | `{namespace, names}` entries; the controller may `get` exactly these Secrets         |
| `otlpEndpoint`                                              | empty                           | Sets `OTEL_EXPORTER_OTLP_ENDPOINT`; empty exports nothing                            |
| `env`                                                       | `[]`                            | Extra environment variables, for example `GITHUB_API_URL`                            |
| `extraArgs`                                                 | `[]`                            | Arguments after `controller`, see the flags below                                    |
| `tmpSizeLimit`                                              | `1Gi`                           | Size limit of `/tmp`, where each run's checkout lives                                |
| `resources`                                                 | 50m CPU, 128Mi to 512Mi         |                                                                                      |
| `podAnnotations`, `nodeSelector`, `tolerations`, `affinity` | empty                           | Scheduling and metadata                                                              |
| `service.port`                                              | `80`                            | Port of the webhook Service                                                          |
| `ingress.enabled`                                           | `false`                         | Expose `/webhook`                                                                    |
| `ingress.className`, `ingress.annotations`                  | empty                           |                                                                                      |
| `ingress.host`                                              | empty                           | Required when the Ingress is enabled                                                 |
| `ingress.tlsSecretName`                                     | empty                           | Existing TLS Secret; empty renders no TLS block                                      |
| `networkPolicy.enabled`                                     | `false`                         | Create the NetworkPolicy, see below                                                  |
| `networkPolicy.ingress.from`                                | `[]`                            | Peers that may reach the webhook port; empty allows any source                       |
| `networkPolicy.egress.dns`                                  | `kube-dns` in `kube-system`     | Peers of the DNS server, port 53                                                     |
| `networkPolicy.egress.kubeApi`                              | `0.0.0.0/0`, ports 443 and 6443 | CIDRs and ports of the Kubernetes API server                                         |
| `networkPolicy.egress.https.cidrs`                          | `0.0.0.0/0`                     | CIDRs allowed on TCP 443: the GitHub API and the model endpoint                      |
| `networkPolicy.egress.extraRules`                           | `[]`                            | Raw egress rules, for example an in-cluster model endpoint or an OTLP collector      |

### NetworkPolicy

A NetworkPolicy cannot match host names, so the GitHub API and the model endpoint can only be limited by address. GitHub publishes the API ranges at `https://api.github.com/meta` and they change, so the chart cannot embed them: the default allows TCP 443 to any address, and the policy holds egress to DNS, the API server and HTTPS until you narrow `networkPolicy.egress.https.cidrs` to GitHub's ranges and your model provider's. Narrow `networkPolicy.egress.kubeApi.cidrs` to the API server's address at the same time, or leave it open. A model endpoint on another port, in the cluster or not, such as the `ollama.models:11434` of the examples below, needs a rule in `networkPolicy.egress.extraRules`. The policy needs a CNI that enforces NetworkPolicy.

| Flag                          | Default | Description                                                           |
| ----------------------------- | ------- | --------------------------------------------------------------------- |
| `--webhook-bind-address`      | `:8080` | Webhook receiver, `POST /webhook`                                     |
| `--health-probe-bind-address` | `:8081` | `/healthz` and `/readyz`                                              |
| `--leader-elect`              | `true`  | One active replica through a Lease                                    |
| `--leader-election-namespace` | Pod's   | Namespace of the Lease                                                |
| `--namespace`                 | all     | Watch only this namespace                                             |
| `--concurrency`               | `4`     | Most Sources run at once; Sources of one knowledge repo never overlap |

`GITHUB_API_URL` overrides the GitHub API base URL. Telemetry uses the `OTEL_*` variables, as for every command.

## Resources

Group `okf.dev`, version `v1alpha1`. Both kinds are namespaced; a `Source` refers to a `KnowledgeBase` in its own namespace.

```yaml
apiVersion: okf.dev/v1alpha1
kind: KnowledgeBase
metadata:
  name: knowledge
spec:
  repository: example-org/knowledge
  writer:
    appSecretRef: {name: knowledge-writer-app}
    model:
      baseURL: http://ollama.models:11434/v1
      name: qwen3:8b
      apiKeySecretRef: {name: knowledge-writer-model}   # optional
  reviewer:
    appSecretRef: {name: knowledge-review-app}
    model:
      baseURL: http://ollama.models:11434/v1
      name: qwen2.5:7b
  webhookSecretRef: {name: knowledge-webhook}
---
apiVersion: okf.dev/v1alpha1
kind: Source
metadata:
  name: api
spec:
  repository: example-org/api
  knowledgeBaseRef: {name: knowledge}
  requeueInterval: 1h       # default
  reconcileInterval: 168h   # default, weekly; 0s disables
```

Secrets are referenced by name only and hold fixed keys:

| Secret                  | Keys                    |
| ----------------------- | ----------------------- |
| `writer.appSecretRef`   | `app-id`, `private-key` |
| `reviewer.appSecretRef` | `app-id`, `private-key` |
| `model.apiKeySecretRef` | `api-key`               |
| `webhookSecretRef`      | `secret`                |

The controller reads Secrets with `get` only, uncached, so its Role can list them by `resourceNames`. List every Secret your `KnowledgeBase`s reference in the `secrets` value, in the namespace of the `KnowledgeBase`; the chart renders one Role and RoleBinding per namespace, and no `list` or `watch`:

```yaml
secrets:
  - namespace: team-a        # defaults to the release namespace
    names:
      - knowledge-writer-app
      - knowledge-writer-model
      - knowledge-webhook
```

A Secret that is not listed fails the run with `CredentialsFailed`. Name each namespace once: two entries for one namespace fail the install. An entry with no `names` fails the install, because an empty `resourceNames` would grant every Secret in the namespace.

### Source status

| Field               | Meaning                                                                                   |
| ------------------- | ----------------------------------------------------------------------------------------- |
| `conditions[Ready]` | `True` after a successful run; `False` with reason `<Stage>Failed` and the redacted error |
| `lastRunTime`       | When the last run finished                                                                |
| `lastRunMode`       | `plan` or `reconcile`                                                                     |
| `lastCursor`        | The commit the cursor held after the last run, as `propose` reported it                   |
| `lastPR`            | Number and URL of the bot PR                                                              |
| `lastError`         | The redacted error of the last run, empty after a success                                 |
| `lastReconcileTime` | When the last successful reconcile run started                                            |

`lastCursor` is informational: every run reads the cursor from `kb-state`. Every run also records a `RunSucceeded` or `RunFailed` event on the `Source`.

Failure stages: `knowledgebase`, `credentials`, `declined`, `checkout`, `routing`, `repair`, `plan`, `reconcile`, `write`, `propose`, `gate`, `review`, `approve`, `dismiss`.

## Webhooks

Point the writer App's webhook, or a repository webhook on each source repo, at the `<release>-webhook` Service's `/webhook` path through your ingress (`ingress.enabled` renders one), with the secret in the `webhookSecretRef` Secret and the **Push** and **Pull requests** events.

1. Bodies over 5 MiB answer 413.
1. `X-Hub-Signature-256` is checked with HMAC-SHA256, in constant time, against the webhook secret of every `KnowledgeBase`. A missing header or no match answers 401 and queues nothing. Several knowledge bases may share one secret.
1. An event from the knowledge repo of a matching knowledge base, or sent by the writer or review App's bot user, is dropped.
1. Only `push` to the default branch and `pull_request` `closed` with `merged: true` into the default branch queue anything, `ping` included.
1. The queued `Source`s are those of the event's repository whose knowledge base's secret matched. Anything that queues nothing answers 202.

The bot logins come from `GET /app` and are learned on each knowledge base's first run. The receiver runs on the leader only, and only the leader reports Ready, so the Service sends deliveries to it alone. A stopping leader releases its Lease at once. Keep the Deployment's `Recreate` strategy: under `RollingUpdate` a new Pod would wait for the old Pod's Lease and never become Ready.

## A run

1. Read the writer App Secret and the writer model key.
1. Record declined bot PRs, see [Operator state](state.md#declined-ranges).
1. Find the open bot PR and read its `mergeable_state`. A PR that is `behind` is updated with update-branch and a `dirty` one has its conflicting pages regenerated, see [Repairing a bot PR](state.md#repairing-a-bot-pr). Approvals of the review App on any other commit of the PR are dismissed. The checkout commit is the PR's head after the repair, else the default branch head.
1. Download that commit with `GET /repos/{owner}/{repo}/tarball/{sha}` into a temporary directory, removed after the run. The image has no git.
1. Read `.kb/routing.yaml` from the default branch head, as the README asks CLI users to pass with `--routing`: the bot branch's copy is as old as its PR.
1. `plan`, or `reconcile` when `reconcileInterval` has passed since the last reconcile run (or since the `Source` was created).
1. `write` against the checkout, then `propose` with the checkout commit as the knowledge base.
1. Only for a `plan` run whose `propose` opened or updated the PR: `gate` without the review. If it passes, read the review App Secret and the reviewer model key, run `review`, `gate` with the review result, and `approve` with the review App.
1. A run whose `propose` pushed nothing, while the bot PR head is still the one the controller last pushed, runs `gate` without the review on that head again, with the plan remembered from that push. It never reviews or approves again.

The deterministic gate runs first, so a PR that can never auto-merge costs no reviewer tokens. A reconcile PR always goes to a human, and a run whose `propose` pushed nothing does not gate. `review` reads GitHub as the writer App: the review App is installed on the knowledge repo only and is used to approve and dismiss.

The pipeline runs in the controller process. A Job per run is the upgrade path when each step needs its own Secrets.

### Serialisation

Each run holds an in-memory lock keyed by the lower-cased knowledge repo. A `Source` whose knowledge repo is busy is queued again after 15 seconds, so two `Source`s of one knowledge repo never run at once, even across two `KnowledgeBase`s naming the same repo. Leader election keeps one active replica, and the cursor compare-and-swap in `propose` still fails a run that loses a race.

### Stale approvals

Every run that pushes nothing gates the unchanged bot PR head again, so a policy or routing change on the base branch is seen without a new commit. When a gate run on head H fails, the controller dismisses, as the review App, every `APPROVED` review the review App posted on H (`PUT /repos/{owner}/{repo}/pulls/{n}/reviews/{id}/dismissals`, covered by **Pull requests** write). Branch protection then stops counting an approval of a head whose latest gate failed, which "Dismiss stale pull request approvals" does not do without a new commit.

Every run with an open bot PR also dismisses the review App's approvals of any commit that is not the PR's current head, right after a repair settles the head and before the new head is checked out, with a message that names the commit. A repair that moved the head and then failed is cleared by the next run. The head itself is not reviewed or approved again by that run.

A PR whose `mergeable_state` is `dirty` fails the `mergeable` gate rule, so the review App never approves a PR that cannot merge, and its approval of a head that has since gone `dirty` is dismissed the same way.

The plan behind the head is kept in memory: after a restart, a head is gated again only once a run pushes to it.

Publishing the gate result as a required check run was rejected: it needs **Checks** write on the writer App and a check that branch protection must require, next to the approval the review App already posts.

## Telemetry

| Metric                            | Type      | Unit | Attributes                                      |
| --------------------------------- | --------- | ---- | ----------------------------------------------- |
| `okf.controller.events.received`  | counter   |      | `okf.controller.event`                          |
| `okf.controller.events.dropped`   | counter   |      | `okf.controller.reason`                         |
| `okf.controller.runs.queued`      | counter   |      | `okf.controller.trigger` (`webhook`, `busy`)    |
| `okf.controller.run.duration`     | histogram | s    | `okf.controller.mode`, `okf.controller.outcome` |
| `okf.controller.run.failures`     | counter   |      | `okf.controller.stage`, `okf.controller.mode`   |
| `okf.controller.merge_to_pr`      | histogram | s    |                                                 |
| `okf.controller.prs.approved`     | counter   |      |                                                 |
| `okf.controller.cursor.staleness` | gauge     | s    | `okf.source.repo`                               |

`propose` adds `okf.propose.prs.repaired` (counter, `{pr}`) for every repair of an open bot PR, with `okf.source.repo` and `okf.propose.repair`: `update_branch` for a behind PR and `regenerate` for a conflicting one. It is not part of the per-run `okf.propose.prs.opened` and `okf.propose.prs.updated` counts.

Drop reasons: `signature`, `too_large`, `malformed`, `knowledge_repo`, `bot`, `ignored_event`, `no_source`.

`merge_to_pr` is the time from the earliest source PR merge the webhook saw for a `Source` to the run that opened or updated its knowledge PR. `cursor.staleness` is the time since a run last left the cursor at the source HEAD. Both are kept in memory and start over after a restart, and both are dropped when the `Source` is deleted, so a deleted `Source` stops reporting staleness. `cursor.staleness` carries the source repo only: two `Source`s on the same repo share one series.

Each run is one `controller run` trace with the source, knowledge repo, mode, outcome, PR and, on failure, the stage. Each stage is a child span named after it, carrying the same attributes as the matching CLI command. Keys, JWTs, installation tokens, webhook secrets and model keys are redacted from logs, events, status and spans.

## Limitations

- GitHub archives honour `export-ignore` and `export-subst`; a knowledge repo that uses them fails `propose`'s checkout check.
- Declined ranges are read from the PR body, so ranges dropped from a very long body are not recorded.
- The pending merge times and bot logins live in memory.
